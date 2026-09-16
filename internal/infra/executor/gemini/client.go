// Package gemini implements image.comic4's optional second image-generation
// provider (alongside internal/infra/executor/minimax): Google's
// gemini-2.5-flash-image via Vertex AI. Added after live testing found
// MiniMax's subject_reference mechanism is documented and empirically
// confirmed as portrait-tuned (its own docs: "为获得最佳效果，请上传单人正面照片"),
// giving materially worse character-identity fidelity for non-human
// reference photos than for human ones — Gemini's character-consistency and
// multi-image-fusion recipes carry no such restriction. This package is
// deliberately narrow: only image.comic4's actual image-generation step
// (gen-one-panel/stylize-reference) is swappable; the AI planner
// (minimax.PlanComic), reference-subject description, and prompt enhancement
// all stay on MiniMax regardless of which provider renders the panels — see
// image_comic4.go's own doc on why splitting the swap there was the smaller,
// safer change.
package gemini

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/genai"
)

// Config is NewClient's construction input. HTTPClient/BaseURL exist purely
// for tests (image_test.go points a real client at an httptest.Server,
// bypassing Application Default Credentials entirely) — every real caller
// leaves both empty.
type Config struct {
	ProjectID  string
	Location   string
	Model      string
	HTTPClient *http.Client
	BaseURL    string
}

type Client struct {
	genai *genai.Client
	model string
}

// NewClient constructs a Vertex AI-backed client. With HTTPClient/BaseURL
// both empty (every real deployment), authentication is Application Default
// Credentials — a GOOGLE_APPLICATION_CREDENTIALS-pointed service account key
// file, gcloud user credentials, or GCE/Cloud Run attached metadata identity,
// resolved entirely inside the SDK; this package never reads a credential
// file itself.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gemini: project ID is required")
	}
	cc := &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  cfg.ProjectID,
		Location: cfg.Location,
	}
	if cfg.HTTPClient != nil {
		cc.HTTPClient = cfg.HTTPClient
	}
	if cfg.BaseURL != "" {
		cc.HTTPOptions = genai.HTTPOptions{BaseURL: cfg.BaseURL}
	}
	gc, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("gemini: create vertex ai client: %w", err)
	}
	model := cfg.Model
	if model == "" {
		model = "gemini-2.5-flash-image"
	}
	return &Client{genai: gc, model: model}, nil
}

// ImageGenerationRequest generates exactly one image per call — comic4's own
// Execute loop (image.go) calls this once per requested image, mirroring the
// per-attempt loop minimax.ImagePlugin already uses, rather than relying on
// CandidateCount for a batch (a candidate can legitimately contain zero image
// parts, e.g. a text-only refusal, which is simpler to detect and retry one
// call at a time than to reconcile against a requested batch size).
type ImageGenerationRequest struct {
	Prompt      string
	Seed        *int64
	AspectRatio string
	// ReferenceImages are prepended to the prompt as inline image parts —
	// Gemini's own multi-image-fusion recipe (up to a handful of images in
	// one call), used here for exactly one reference photo per comic4 panel
	// today (image_comic4.go's own one-reference-per-panel design predates
	// this provider and isn't being changed here), but the request shape
	// itself doesn't limit it to one.
	ReferenceImages []ReferenceImage
}

type ReferenceImage struct {
	Data     []byte
	MIMEType string
}

type ImageGenerationResponse struct {
	ImageData []byte
	MIMEType  string
}

// GenerateImage calls Gemini's generateContent with ResponseModalities
// including IMAGE (the documented way to get image output from this model —
// it is not Imagen's separate :predict endpoint) and extracts the first
// inline image part from the first candidate.
func (c *Client) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResponse, error) {
	parts := make([]*genai.Part, 0, len(req.ReferenceImages)+1)
	for _, ref := range req.ReferenceImages {
		parts = append(parts, &genai.Part{InlineData: &genai.Blob{Data: ref.Data, MIMEType: ref.MIMEType}})
	}
	// The trailing directive matters: found live that comic4's H3-enhanced
	// prompts (long, multi-paragraph shot descriptions written for a video
	// model, reused here as-is) sometimes make gemini-2.5-flash-image
	// respond with finish_reason STOP and no image part at all — it just
	// replies conversationally instead of drawing. An explicit "image only"
	// instruction at the very end (closest to the model's own response
	// decision) measurably reduces this without needing to rewrite comic4's
	// own prompt-building — GenerateContent has no dedicated "must return an
	// image" config flag, only ResponseModalities' hint above.
	parts = append(parts, genai.NewPartFromText(req.Prompt+
		"\n\nIMPORTANT: your response must consist of the generated image only — do not reply with a text description or commentary instead of drawing it."))

	cfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE", "TEXT"},
	}
	if req.Seed != nil {
		s := int32(*req.Seed)
		cfg.Seed = &s
	}
	if req.AspectRatio != "" {
		cfg.ImageConfig = &genai.ImageConfig{AspectRatio: req.AspectRatio}
	}

	resp, err := c.genai.Models.GenerateContent(ctx, c.model, []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}, cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini generateContent: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		reason := ""
		if resp.PromptFeedback != nil {
			reason = string(resp.PromptFeedback.BlockReason)
		}
		return nil, fmt.Errorf("gemini generateContent: no candidates returned (block_reason=%q)", reason)
	}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return &ImageGenerationResponse{ImageData: part.InlineData.Data, MIMEType: part.InlineData.MIMEType}, nil
		}
	}
	return nil, fmt.Errorf("gemini generateContent: no image part in response (finish_reason=%q)", resp.Candidates[0].FinishReason)
}
