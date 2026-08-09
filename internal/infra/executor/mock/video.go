package mock

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// VideoConfig is mock.video's declared input contract.
type VideoConfig struct {
	Prompt     string `json:"prompt"`
	DurationS  int    `json:"duration-s"`
	Resolution string `json:"resolution"` // 768P / 2K
	UserID     string `json:"user-id"`
}

// VideoPlugin fakes minimax.video: the real executor is asynchronous
// (submit → poll/callback → materialize, §10.3); the mock sleeps longer
// than the image mock to make that asymmetry visible in the DAG UI early,
// then materializes a placeholder video asset (no real bytes — W1 does not
// need a playable file, only a correctly-shaped asset row and outputs).
type VideoPlugin struct {
	sink  assetstore.Sink
	delay time.Duration
}

func NewVideoPlugin(sink assetstore.Sink) *VideoPlugin {
	return &VideoPlugin{sink: sink, delay: 5 * time.Second}
}

func (p *VideoPlugin) Type() string { return "mock.video" }

func (p *VideoPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[VideoConfig, executor.DynamicOutputs](
		"mock.video", "1.0", "Fake video generator for pre-MiniMax development",
	)
}

func (p *VideoPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg VideoConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind mock.video inputs: %w", err)
	}
	if cfg.DurationS <= 0 {
		cfg.DurationS = 5
	}
	if cfg.Resolution == "" {
		cfg.Resolution = "768P"
	}

	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	userID, err := strconv.ParseUint(cfg.UserID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("mock.video: invalid user-id %q: %w", cfg.UserID, err)
	}
	bizID, err := p.sink.Materialize(ctx, assetstore.NewAsset{
		UserID:        userID,
		Type:          "video",
		Source:        "generated",
		FromTaskRunID: req.TaskRunID,
		StorageKey:    fmt.Sprintf("mock/%s/video.mp4", req.TaskRunID),
		Mime:          "video/mp4",
		DurationMs:    cfg.DurationS * 1000,
		ResolutionTag: cfg.Resolution,
		Meta: map[string]any{
			"mock":   true,
			"prompt": cfg.Prompt,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("materialize mock video: %w", err)
	}

	return executor.OutputFrom(struct {
		AssetID       string `json:"asset-id"`
		OutputSeconds int    `json:"output-seconds"`
		Resolution    string `json:"resolution"`
	}{
		AssetID:       bizID,
		OutputSeconds: cfg.DurationS,
		Resolution:    cfg.Resolution,
	})
}
