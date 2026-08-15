// Package mock provides fake MiniMax executors so the full Job → Aether →
// Executor → Asset chain can be built and tested before any real API key
// exists (PRD §10.1: "mock.image / mock.video, 第 1 周就写"; DEV_PLAN.md §5).
// Their output shape (asset-ids/success-count/failed-count) intentionally
// mirrors what the real minimax.image executor will return in W3, so
// phaseConditions and downstream compose/concat nodes can be developed and
// tested against the mock before MiniMax is wired in.
package mock

import (
	"context"
	"fmt"
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
// type:"string" parameter (matching minimax.image's own ImageConfig.N,
// see its doc for why: workflow.parameters always arrive as JSON strings).
// Loop-body invocations (image.comic4/image.sequence's normal path) never
// actually exercise this: their per-iteration item never supplies "n", and
// this plugin's own n<=0 fallback below masks a mismatched type silently.
// A plain DAG-task invocation that relies on the template's own declared
// literal `value` as its default (Aether's bindOne priority 3 — see
// third_party/aether/internal/binding/bind.go) does exercise it, e.g.
// jobsvc.RetryNode's satellite workflows — that's how this got caught.
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
			StorageKey:    fmt.Sprintf("mock/%s/%d.svg", req.TaskRunID, i),
			PublicURL:     placeholderSVGDataURI(hue, cfg.Prompt),
			Mime:          "image/svg+xml",
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

	return executor.OutputFrom(struct {
		AssetIDs     []string `json:"asset-ids"`
		SuccessCount int      `json:"success-count"`
		FailedCount  int      `json:"failed-count"`
		RequestedN   int      `json:"requested-n"`
	}{
		AssetIDs:     assetIDs,
		SuccessCount: len(assetIDs),
		FailedCount:  n - len(assetIDs),
		RequestedN:   n,
	})
}

// placeholderSVGDataURI builds a tiny inline SVG so the frontend has a real,
// renderable image URL without needing any object storage in W1 (MinIO
// wiring + real materialize-to-S3 is a W3 concern per DEV_PLAN.md §7).
func placeholderSVGDataURI(hue int, prompt string) string {
	label := prompt
	if len(label) > 40 {
		label = label[:40] + "…"
	}
	svg := fmt.Sprintf(
		`<svg xmlns='http://www.w3.org/2000/svg' width='768' height='768'>`+
			`<rect width='100%%' height='100%%' fill='hsl(%d,70%%,55%%)'/>`+
			`<text x='24' y='700' font-family='sans-serif' font-size='28' fill='white'>%s</text>`+
			`</svg>`, hue, escapeXML(label))
	return "data:image/svg+xml;utf8," + svg
}

func escapeXML(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '&':
			out = append(out, []rune("&amp;")...)
		case '<':
			out = append(out, []rune("&lt;")...)
		case '>':
			out = append(out, []rune("&gt;")...)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
