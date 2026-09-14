// Package minimax implements the real provider executors (PRD §3/§10.1),
// replacing the mock.* plugins wired in W1-W2. Live-verified against
// https://api.minimaxi.com (not api.minimax.chat / api.minimaxi.chat — both
// of those either don't resolve to this account or silently drop requests;
// api.minimaxi.com is confirmed correct both for chat/completions and
// /v1/image_generation).
//
// NOTE: MiniMax's own JSON wire format (snake_case: aspect_ratio,
// response_format, image_urls, status_code, ...) is a completely different
// namespace from Aether's protocol-level naming (kebab-case, DNS-1123 —
// docs/aether-validation-report.md §three.1). Never confuse the two: the
// structs in this file use MiniMax's own convention because that's what
// their API actually expects on the wire; the executor.Plugin's declared
// Inputs/Outputs (image.go) use Aether's kebab-case convention because
// that's a different, unrelated protocol boundary.
package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		// This one client is shared by every call shape this package makes:
		// image_generation (~20s observed, PRD §3.1), files/upload, and the
		// H3-Context-IR/video task-creation calls prompt_enhance.go and
		// video.go issue from inside an asynq-executed task. Those tasks
		// already get a correctly-scoped per-call deadline from the caller's
		// own ctx (broker_asynq.go sets asynq.Timeout(assignment.Timeout+30s)
		// per Aether task — 5m for enhance-panel, 3m for gen-one-panel, up to
		// 30m for video), so this field must never be shorter than the
		// longest of those or it silently overrides them at the transport
		// level regardless of what the task declared (found live: an
		// enhance-panel node with a declared 5m timeout still died at 90s
		// with "Client.Timeout exceeded while awaiting headers" on a slow
		// h3_context_ir call). 6 minutes is comfortably above every declared
		// Aether task timeout in this codebase's workflows while still
		// failing fast for the handful of synchronous, request-bound callers
		// with no task-derived ctx deadline of their own (SplitStory,
		// prompt_rewrite.go, trial.go) — those still return in low seconds
		// in the normal case; this only changes their worst-case ceiling.
		http: &http.Client{Timeout: 6 * time.Minute},
	}
}

// BaseResp is embedded in every MiniMax response (PRD §3.1): errors never
// use the HTTP status code, they live here with HTTP always 200.
type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type ImageStyle struct {
	StyleType   string  `json:"style_type,omitempty"`
	StyleWeight float64 `json:"style_weight,omitempty"`
}

// SubjectReferenceItem is F5.8's image-to-image mechanism (verified against
// platform.minimaxi.com's image generation guide): a single reference image
// whose subject the output preserves while following the text prompt.
// MiniMax's image_generation only documents "character" as a type and only
// supports one reference image per request.
type SubjectReferenceItem struct {
	Type      string `json:"type"`       // "character"
	ImageFile string `json:"image_file"` // URL or data URI
}

type ImageGenerationRequest struct {
	Model            string                 `json:"model"`
	Prompt           string                 `json:"prompt"`
	N                int                    `json:"n,omitempty"`
	AspectRatio      string                 `json:"aspect_ratio,omitempty"`
	Width            int                    `json:"width,omitempty"`
	Height           int                    `json:"height,omitempty"`
	Seed             *int64                 `json:"seed,omitempty"`
	Style            *ImageStyle            `json:"style,omitempty"`
	ResponseFormat   string                 `json:"response_format,omitempty"`
	PromptOptimizer  *bool                  `json:"prompt_optimizer,omitempty"`
	AigcWatermark    *bool                  `json:"aigc_watermark,omitempty"`
	SubjectReference []SubjectReferenceItem `json:"subject_reference,omitempty"`
}

type ImageGenerationResponse struct {
	ID   string `json:"id"`
	Data struct {
		ImageURLs   []string `json:"image_urls"`
		ImageBase64 []string `json:"image_base64"`
	} `json:"data"`
	Metadata struct {
		SuccessCount string `json:"success_count"` // MiniMax returns these as quoted strings, not numbers
		FailedCount  string `json:"failed_count"`
	} `json:"metadata"`
	BaseResp BaseResp `json:"base_resp"`
}

func (c *Client) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal image generation request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/image_generation", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call image_generation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	// PRD §3.1: HTTP status is always 200 even on business errors — but
	// guard anyway in case a proxy/gateway returns a real HTTP-level error
	// (e.g. 502) with a non-JSON body.
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("image_generation returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var out ImageGenerationResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}

// UploadFileResponse is POST /v1/files/upload's response shape.
type UploadFileResponse struct {
	File struct {
		FileID   int64  `json:"file_id"`
		Bytes    int64  `json:"bytes"`
		Filename string `json:"filename"`
		Purpose  string `json:"purpose"`
	} `json:"file"`
	BaseResp BaseResp `json:"base_resp"`
}

// UploadFile uploads bytes so they can be referenced as mm_file://{file_id}
// in later calls (e.g. minimax.video's reference_image, W5+) without
// re-sending the bytes. purpose is one of MiniMax's fixed enum values —
// "video_generation_input" for the images/video/audio this platform uses
// (7-day expiry, PRD-adjacent finding not in the original doc excerpt: see
// docs/aether-validation-report.md's W4 addendum).
func (c *Client) UploadFile(ctx context.Context, purpose string, filename string, data []byte) (*UploadFileResponse, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("purpose", purpose); err != nil {
		return nil, fmt.Errorf("write purpose field: %w", err)
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return nil, fmt.Errorf("write file bytes: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/files/upload", &body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call files/upload: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("files/upload returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var out UploadFileResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}

// DownloadImage fetches image bytes from a (temporary, 24h-valid) MiniMax
// URL. Callers must re-upload these immediately (PRD R3) — this function
// does not cache or persist anything itself.
func (c *Client) DownloadImage(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download image: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read image body: %w", err)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return data, contentType, nil
}
