// Package mock provides fake MiniMax executors so the full Job → Aether →
// Executor → Asset chain can be built and tested before any real API key
// exists (PRD §10.1: "mock.image / mock.video, 第 1 周就写"; DEV_PLAN.md §5).
// Their output shape (asset-ids/success-count/failed-count) intentionally
// mirrors what the real minimax.image executor will return in W3, so
// phaseConditions and downstream compose/concat nodes can be developed and
// tested against the mock before MiniMax is wired in.
package mock

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strconv"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// ImageConfig is mock.image's declared input contract. Field names are
// kebab-case (DNS-1123) per docs/aether-validation-report.md §three.1.
//
// N is string, not int — every real workflow file declares "n" as a
// type:"string" parameter (matching minimax.image's own ImageConfig.N:
// workflow.parameters always arrive as JSON strings).
type ImageConfig struct {
	Prompt string `json:"prompt" desc:"structured prompt text"`
	N      string `json:"n" desc:"how many images to generate, 1..9, as a string"`
	UserID string `json:"user-id" desc:"owning user's numeric id, as a string"`
}

// ImagePlugin fakes minimax.image: sleeps briefly (simulating a real API
// round-trip) then materializes N solid-colour placeholder images.
type ImagePlugin struct {
	sink  assetstore.Sink
	delay time.Duration
}

func NewImagePlugin(sink assetstore.Sink) *ImagePlugin {
	return &ImagePlugin{sink: sink, delay: 2 * time.Second}
}

func (p *ImagePlugin) Type() string { return "mock.image" }

func (p *ImagePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ImageConfig, executor.DynamicOutputs](
		"mock.image", "1.0", "Fake image generator for pre-MiniMax development",
	)
}

func (p *ImagePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ImageConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind mock.image inputs: %w", err)
	}
	n, _ := strconv.Atoi(cfg.N)
	if n <= 0 {
		n = 1
	}

	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	userID, err := strconv.ParseUint(cfg.UserID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("mock.image: invalid user-id %q: %w", cfg.UserID, err)
	}

	assetIDs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hue := rand.Intn(360)
		bizID, err := p.sink.Materialize(ctx, assetstore.NewAsset{
			UserID:        userID,
			Type:          "image",
			Source:        "generated",
			FromTaskRunID: req.TaskRunID,
			StorageKey:    fmt.Sprintf("mock/%s/%d.png", req.TaskRunID, i),
			PublicURL:     placeholderPNGDataURI(hue),
			Mime:          "image/png",
			Width:         768,
			Height:        768,
			Meta: map[string]any{
				"mock":   true,
				"prompt": cfg.Prompt,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("materialize mock image %d: %w", i, err)
		}
		assetIDs = append(assetIDs, bizID)
	}

	// AssetID (singular) mirrors minimax.image's own convenience field —
	// needed so a single mock.image panel's output can chain into the next
	// panel's reference (image.comic4, image.sequence).
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

// placeholderPNGSize is deliberately tiny, not 768 (the asset's own
// declared Width/Height metadata): assets.public_url is a varchar(1024),
// which a solid-colour 768x768 PNG's base64 payload can exceed. Every
// consumer upscales anyway — a browser <img> stretches to its CSS box
// regardless of intrinsic size, and local.compose's own nearest-neighbor
// resize fills tileSize from whatever arrives — so a 16x16 solid square is
// visually identical to a 768x768 one at every call site.
const placeholderPNGSize = 16

// placeholderPNGDataURI builds a tiny solid-colour PNG so the frontend has a
// real, renderable image URL without needing any object storage. A raster
// format (not SVG) matters beyond the browser: local.compose decodes each
// panel through Go's stdlib image codecs to tile them, and Go has no SVG
// decoder registered.
func placeholderPNGDataURI(hue int) string {
	img := image.NewRGBA(image.Rect(0, 0, placeholderPNGSize, placeholderPNGSize))
	fill := hueToRGBA(hue)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img) // encoding a freshly-built in-memory RGBA can't fail
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// hueToRGBA converts an HSL(hue, 70%, 55%) colour (the original SVG
// placeholder's own palette) to RGBA, so the mock's placeholder colour
// scheme is unchanged by the SVG->PNG format swap above.
func hueToRGBA(hue int) color.RGBA {
	const s, l = 0.70, 0.55
	h := float64(((hue % 360) + 360) % 360)
	c := (1 - abs(2*l-1)) * s
	x := c * (1 - abs(mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func mod(a, b float64) float64 {
	m := a - float64(int(a/b))*b
	if m < 0 {
		m += b
	}
	return m
}
