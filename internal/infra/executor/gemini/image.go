package gemini

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/infra/executor/assetstore"
)

// ImageConfig deliberately mirrors only the subset of minimax.ImageConfig's
// json tags either comic4 or image.single actually needs from this provider
// — no model/style-weight/watermark fields, since none of those are
// meaningful for this provider. Kept in its own struct rather than sharing
// minimax's so each provider's input contract can evolve independently —
// see client.go's package doc on why only the image-generation step is
// swappable at all.
type ImageConfig struct {
	Prompt string `json:"prompt"`
	N      string `json:"n"`
	Seed   string `json:"seed"`
	UserID string `json:"user-id"`
	// AspectRatio is image.single-gemini only for now (comic4's own template
	// never sets it, so it's always "" there — Execute's own default applies).
	AspectRatio string `json:"aspect-ratio"`
	// SourceImageAssetID is comic4's own single-reference field (its DAG
	// only ever anchors one panel on one raw/stylized asset at a time — see
	// image_comic4.go's own doc on why). ReferenceImageAssetIDs is
	// image.single-gemini's plural counterpart, added specifically to
	// exercise Gemini's own multi-image-fusion recipe (Google's own
	// "Recipe 7") for a scene needing more than one bound character at
	// once — a hard ceiling MiniMax's subject_reference can't cross (exactly
	// one reference image per call). When ReferenceImageAssetIDs is
	// non-empty it wins outright; SourceImageAssetID is only consulted when
	// it's empty, so no caller needs to populate both.
	SourceImageAssetID     string   `json:"source-image-asset-id"`
	ReferenceImageAssetIDs []string `json:"reference-image-asset-ids"`
}

// maxReferenceImages bounds how many reference images one GenerateContent
// call embeds — Google's own multi-image recipes demonstrate a handful at
// once; capped here mainly so a caller passing an unexpectedly long list
// can't balloon one request's payload size without limit.
const maxReferenceImages = 3

type ImagePlugin struct {
	client  *Client
	sink    assetstore.Sink
	reader  assetstore.Reader
	limiter *Limiter
}

// limiter may be nil (ratelimit.go's own doc: unbounded concurrency, the
// only behavior that existed before Limiter did).
func NewImagePlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader, limiter *Limiter) *ImagePlugin {
	return &ImagePlugin{client: client, sink: sink, reader: reader, limiter: limiter}
}

func (p *ImagePlugin) Type() string { return "gemini.image" }

func (p *ImagePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ImageConfig, executor.DynamicOutputs](
		"gemini.image", "1.0", "Google gemini-2.5-flash-image (Vertex AI) synchronous image generation",
	)
}

func (p *ImagePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ImageConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind gemini.image inputs: %w", err)
	}
	n, _ := strconv.Atoi(cfg.N)
	if n <= 0 {
		n = 1
	}
	if n > capability.ImageMaxN {
		n = capability.ImageMaxN
	}
	if r := []rune(cfg.Prompt); len(r) > capability.ImageMaxPromptChars {
		cfg.Prompt = string(r[:capability.ImageMaxPromptChars])
	}

	var seed *int64
	if cfg.Seed != "" {
		if s, err := strconv.ParseInt(cfg.Seed, 10, 64); err == nil {
			seed = &s
		}
	}

	var refImages []ReferenceImage
	switch {
	case len(cfg.ReferenceImageAssetIDs) > 0:
		ids := cfg.ReferenceImageAssetIDs
		if len(ids) > maxReferenceImages {
			ids = ids[:maxReferenceImages]
		}
		for _, assetID := range ids {
			data, mime, err := p.downloadReferenceImage(ctx, assetID)
			if err != nil {
				return errOutputs(model.ExecCodeError, "reference_image: "+err.Error()), nil
			}
			refImages = append(refImages, ReferenceImage{Data: data, MIMEType: mime})
		}
	case cfg.SourceImageAssetID != "":
		data, mime, err := p.downloadReferenceImage(ctx, cfg.SourceImageAssetID)
		if err != nil {
			return errOutputs(model.ExecCodeError, "source_image: "+err.Error()), nil
		}
		refImages = append(refImages, ReferenceImage{Data: data, MIMEType: mime})
	}

	aspectRatio := cfg.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}

	assetIDs := make([]string, 0, n)
	var lastErr error
	for i := 0; i < n; i++ {
		imgReq := ImageGenerationRequest{
			Prompt:          cfg.Prompt,
			AspectRatio:     aspectRatio,
			ReferenceImages: refImages,
		}
		if seed != nil {
			s := *seed + int64(i) // distinct seeds across n>1 — a shared seed would repeat the same image n times
			imgReq.Seed = &s
		}
		release, ok, err := p.limiter.Acquire(ctx, req.TaskRunID)
		if err != nil {
			return nil, fmt.Errorf("gemini concurrency limiter: %w", err)
		}
		if !ok {
			// No slot free — found live that comic4's anchor-mode DAG (every
			// panel's generation is an independent parallel branch, by
			// design) reliably tripped a fresh GCP project's default Vertex
			// AI quota this way. This is a systemic condition, not a
			// per-image failure, so it ends the whole node here rather than
			// counting against success-count: Aether's own retry/backoff
			// (the same posture minimax.VideoLimiter's own doc describes)
			// gives the burst time to clear before the next attempt.
			return errOutputs(model.ExecCodeError, "concurrency_limited: no gemini vertex ai slot available, see GEMINI_VERTEX_CONCURRENCY"), nil
		}
		resp, err := p.client.GenerateImage(ctx, imgReq)
		release()
		if err != nil {
			// A single failed generation shouldn't fail the whole batch —
			// same posture as minimax.ImagePlugin's own per-image download
			// loop; phaseConditions (success-count vs requested-n) catches
			// it. lastErr is still kept: found live that comic4 (n=1 per
			// panel) turned a real GenerateContent failure into a
			// content-free "0 of 1 succeeded" node with no error message
			// anywhere to debug from — surfaced below once every attempt is
			// known to have failed.
			lastErr = err
			continue
		}
		width, height := imageDimensions(resp.ImageData)
		bizID, err := p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
			UserID:        parseUserID(cfg.UserID),
			Type:          "image",
			Source:        "generated",
			FromTaskRunID: req.TaskRunID,
			Body:          bytesReader(resp.ImageData),
			SizeBytes:     int64(len(resp.ImageData)),
			Ext:           extForContentType(resp.MIMEType),
			Mime:          resp.MIMEType,
			Width:         width,
			Height:        height,
			Meta: map[string]any{
				"model":  p.client.model,
				"prompt": cfg.Prompt,
				"seed":   cfg.Seed,
				"index":  i,
			},
		})
		if err != nil {
			continue
		}
		assetIDs = append(assetIDs, bizID)
	}

	if len(assetIDs) == 0 && lastErr != nil {
		// Every attempt failed outright (not just a materialize hiccup after
		// a successful generation) — surface the real reason instead of a
		// bare "0 of n succeeded" the phaseConditions failure alone can't
		// explain. Aether's own retry.limit still gets a chance at this
		// first, same as any other ExecCodeError.
		return errOutputs(model.ExecCodeError, lastErr.Error()), nil
	}

	firstAssetID := ""
	if len(assetIDs) > 0 {
		firstAssetID = assetIDs[0]
	}

	return executor.OutputFrom(struct {
		AssetID      string   `json:"asset-id"`
		AssetIDs     []string `json:"asset-ids"`
		SuccessCount int      `json:"success-count"`
		FailedCount  int      `json:"failed-count"`
		RequestedN   int      `json:"requested-n"`
	}{
		AssetID:      firstAssetID,
		AssetIDs:     assetIDs,
		SuccessCount: len(assetIDs),
		FailedCount:  n - len(assetIDs),
		RequestedN:   n,
	})
}

// downloadReferenceImage resolves a local asset to raw bytes + MIME type —
// same PublicURL-then-fetch path minimax.ImagePlugin's
// buildSubjectReferenceDataURI uses, just returning bytes directly since
// Gemini's Blob wants raw data, not a data URI string.
func (p *ImagePlugin) downloadReferenceImage(ctx context.Context, assetBizID string) (data []byte, mime string, err error) {
	url, err := p.reader.PublicURL(ctx, assetBizID)
	if err != nil {
		return nil, "", fmt.Errorf("look up asset %s: %w", assetBizID, err)
	}
	data, err = downloadBytes(ctx, url)
	if err != nil {
		return nil, "", fmt.Errorf("download asset %s: %w", assetBizID, err)
	}
	return data, http.DetectContentType(data), nil
}

func errOutputs(code int, msg string) *model.ExecOutputs {
	return &model.ExecOutputs{Code: code, Message: msg}
}
