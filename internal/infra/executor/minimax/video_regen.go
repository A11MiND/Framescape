package minimax

import (
	"context"
	"fmt"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/infra/executor/assetstore"
)

// VideoRegenConfig is minimax.video.regen's input contract (F6.6). PRD §3.5
// describes this as "resubmit the original 768P content + an extra
// type=video_url,role=base_video item" — verified directly against
// MiniMax's real API docs (platform.minimaxi.com/docs/api-reference/
// video-generation-v2-create) and this is not real: video_url's only valid
// role is reference_video (multimodal-reference scenarios), and there is no
// role anywhere for resubmitting/upscaling a previously generated video.
// MiniMax has no "upscale" primitive at all — confirmed the hard way, a real
// call with role=base_video was rejected with a 400 (bad_params,
// `content[2].role="base_video" invalid for type="video_url"`, error code
// 2013). §12.1's "RegenCostPerSecondYuan: 0.30" (cheaper than a fresh 2K
// generation) is downstream of this same false premise — there is no
// discounted "regen" pricing tier because there is no regen operation;
// getting a 2K version costs the full 2K rate because it IS a fresh 2K
// generation. BaseVideoAssetID is kept as a field (still supplied by
// jobsvc.Resume) purely for Meta traceability on the resulting asset — it is
// never sent to MiniMax.
type VideoRegenConfig struct {
	Prompt   string `json:"prompt"`
	Duration string `json:"duration"`
	Ratio    string `json:"ratio"`

	FirstFrameAssetID string `json:"first-frame-asset-id"`
	LastFrameAssetID  string `json:"last-frame-asset-id"`

	ReferenceImageAssetIDs []string `json:"reference-image-asset-ids"`
	ReferenceVideoAssetIDs []string `json:"reference-video-asset-ids"`
	ReferenceAudioAssetIDs []string `json:"reference-audio-asset-ids"`

	BaseVideoAssetID string `json:"base-video-asset-id"` // traceability only, see doc above

	AigcWatermark *bool  `json:"aigc-watermark"`
	UserID        string `json:"user-id"`
	// ShotIndex: see VideoConfig.ShotIndex's doc — same pass-through purpose
	// for the upgrade-to-2K Loop.
	ShotIndex string `json:"shot-index,omitempty"`
}

// VideoRegenPlugin is minimax.video.regen: a fresh MiniMax-H3 generation at
// 2K using the shot's original content unchanged (see VideoRegenConfig's doc
// for why this is NOT the "resubmit + base_video" design the PRD describes —
// that mechanism doesn't exist in the real API). Kept as its own executor
// type (rather than just reusing minimax.video with resolution=2K, which is
// all this does internally) for workflow-shape clarity in video.sequence's
// upgrade-loop and so the resulting asset's Meta records what it was
// upgraded from.
type VideoRegenPlugin struct {
	base *videoBase
}

func NewVideoRegenPlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader, cache FileCache, redisClient *redis.Client, callbackURL string, limiter *VideoLimiter, orphans OrphanTaskStore) *VideoRegenPlugin {
	return &VideoRegenPlugin{base: &videoBase{
		client: client, sink: sink, reader: reader, cache: cache,
		redis: redisClient, callbackURL: callbackURL, limiter: limiter, orphans: orphans,
	}}
}

func (p *VideoRegenPlugin) Type() string { return "minimax.video.regen" }

func (p *VideoRegenPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[VideoRegenConfig, executor.DynamicOutputs](
		"minimax.video.regen", "1.0", "MiniMax-H3 2K generation using a shot's original content (see VideoRegenConfig doc: not a real 'upscale')",
	)
}

func (p *VideoRegenPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg VideoRegenConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.video.regen inputs: %w", err)
	}

	duration := normalizeDuration(cfg.Duration)

	content, mode, ratio, errOut := p.base.buildContent(ctx, videoRefs{
		Prompt:                 cfg.Prompt,
		Ratio:                  cfg.Ratio,
		FirstFrameAssetID:      cfg.FirstFrameAssetID,
		LastFrameAssetID:       cfg.LastFrameAssetID,
		ReferenceImageAssetIDs: cfg.ReferenceImageAssetIDs,
		ReferenceVideoAssetIDs: cfg.ReferenceVideoAssetIDs,
		ReferenceAudioAssetIDs: cfg.ReferenceAudioAssetIDs,
	})
	if errOut != nil {
		return errOut, nil
	}

	return p.base.submitWaitMaterialize(ctx, req, submitParams{
		Content:       content,
		Resolution:    "2K",
		Duration:      duration,
		Ratio:         ratio,
		Mode:          mode,
		Prompt:        cfg.Prompt,
		UserID:        cfg.UserID,
		AigcWatermark: cfg.AigcWatermark,
		CostPerSecond: costPerSecondYuan["2K"], // no discounted "regen" rate — this is a fresh 2K generation, see doc above
		RegenOf:       cfg.BaseVideoAssetID,
		ShotIndex:     cfg.ShotIndex,
	})
}

var _ executor.Plugin = (*VideoRegenPlugin)(nil)
