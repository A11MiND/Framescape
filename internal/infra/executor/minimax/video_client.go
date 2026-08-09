package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// VideoContentItem is one element of a video generation request's `content`
// array (PRD §3.2). Exactly one of Text/ImageURL/VideoURL/AudioURL is set
// per item; Role only applies to the image/video/audio variants.
type VideoContentItem struct {
	Type     string  `json:"type"` // "text" | "image_url" | "video_url" | "audio_url"
	Text     string  `json:"text,omitempty"`
	ImageURL *URLRef `json:"image_url,omitempty"`
	VideoURL *URLRef `json:"video_url,omitempty"`
	AudioURL *URLRef `json:"audio_url,omitempty"`
	// Role's real accepted values (verified against MiniMax's own API docs,
	// see video_regen.go's VideoRegenConfig doc — the PRD's role list
	// included a "base_video" that does not exist): first_frame/last_frame/
	// reference_image for image_url, reference_video for video_url,
	// reference_audio for audio_url.
	Role string `json:"role,omitempty"`
}

// URLRef wraps a reference: either a real URL or mm_file://{file_id}
// (PRD §3.2: "同一个角色参考图在 N 段视频里复用时，只上传一次，后续传 file_id").
type URLRef struct {
	URL string `json:"url"`
}

type VideoGenerationRequest struct {
	Model         string             `json:"model"`
	Content       []VideoContentItem `json:"content"`
	Resolution    string             `json:"resolution"` // 768P | 2K
	Duration      int                `json:"duration"`   // 4..15 seconds
	Ratio         string             `json:"ratio,omitempty"`
	CallbackURL   string             `json:"callback_url,omitempty"`
	AigcWatermark bool               `json:"aigc_watermark,omitempty"`
}

type CreateVideoTaskResponse struct {
	TaskID   string   `json:"task_id"`
	BaseResp BaseResp `json:"base_resp"`
}

// HTTPStatusError preserves the real HTTP status code of a video-endpoint
// error response (§3.2/§10.4: unlike image_generation, video errors use
// actual HTTP status codes, not a body-embedded base_resp.status_code) so
// callers can classify per §10.4's table instead of pattern-matching a
// formatted string.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *Client) CreateVideoTask(ctx context.Context, req VideoGenerationRequest) (*CreateVideoTaskResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal video generation request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/video_generation", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call video_generation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	// PRD §3.2: video errors DO use real HTTP status codes (unlike image).
	if resp.StatusCode >= 400 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	var out CreateVideoTaskResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}

type VideoTaskUsage struct {
	TotalSeconds    int `json:"total_seconds"`
	InputSeconds    int `json:"input_seconds"`
	OutputSeconds   int `json:"output_seconds"`
	InputImageCount int `json:"input_image_count"`
}

type VideoTaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type VideoTaskStatus struct {
	Task struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"` // queued | running | succeeded | failed | cancelled
		Content *struct {
			URL string `json:"url"`
		} `json:"content"`
		Resolution string          `json:"resolution"`
		Duration   int             `json:"duration"`
		Usage      *VideoTaskUsage `json:"usage"`
		Ratio      string          `json:"ratio"`
		Error      *VideoTaskError `json:"error"`
	} `json:"task"`
}

func (c *Client) QueryVideoTask(ctx context.Context, taskID string) (*VideoTaskStatus, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/query/video_generation/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call query/video_generation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	var out VideoTaskStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}

// DownloadVideo fetches the finished video's bytes from its (temporary)
// MiniMax URL — same immediate-materialize requirement as images (PRD R3).
func (c *Client) DownloadVideo(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download video: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download video: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
