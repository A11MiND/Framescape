package minimax

import (
	"context"
	"fmt"

	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"

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

func NewVideoRegenPlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader, cache FileCache, callbackURL string, limiter *VideoLimiter) *VideoRegenPlugin {
	return &VideoRegenPlugin{base: &videoBase{client: client, sink: sink, reader: reader, cache: cache, callbackURL: callbackURL, limiter: limiter}}
}

func (p *VideoRegenPlugin) Type() string { return "minimax.video.regen" }

func (p *VideoRegenPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[VideoRegenConfig, executor.DynamicOutputs](
		"minimax.video.regen", "1.0", "MiniMax-H3 2K generation using a shot's original content (see VideoRegenConfig doc: not a real 'upscale')",
	)
}

func (p *VideoRegenPlugin) params(req *executor.ExecuteRequest) (submitParams, error) {
	var cfg VideoRegenConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return submitParams{}, fmt.Errorf("bind minimax.video.regen inputs: %w", err)
	}
	// A 2K version is a fresh 2K generation of the same content at the full
	// 2K rate; MiniMax has no upscale operation.
	return submitParams{
		Resolution: "2K", Duration: normalizeDuration(cfg.Duration), Prompt: cfg.Prompt, UserID: cfg.UserID,
		AigcWatermark: cfg.AigcWatermark, CostPerSecond: costPerSecondYuan["2K"], RegenOf: cfg.BaseVideoAssetID, ShotIndex: cfg.ShotIndex,
		refs: videoRefs{
			Prompt: cfg.Prompt, Ratio: cfg.Ratio, FirstFrameAssetID: cfg.FirstFrameAssetID, LastFrameAssetID: cfg.LastFrameAssetID,
			ReferenceImageAssetIDs: cfg.ReferenceImageAssetIDs, ReferenceVideoAssetIDs: cfg.ReferenceVideoAssetIDs, ReferenceAudioAssetIDs: cfg.ReferenceAudioAssetIDs,
		},
	}, nil
}

func (p *VideoRegenPlugin) Submit(ctx context.Context, req *executor.ExecuteRequest) (executor.ProviderRef, *model.ExecOutputs, error) {
	params, err := p.params(req)
	if err != nil {
		return executor.ProviderRef{}, nil, err
	}
	return p.base.submit(ctx, req, params)
}

func (p *VideoRegenPlugin) Poll(ctx context.Context, req *executor.ExecuteRequest, ref executor.ProviderRef) (executor.PollResult, error) {
	params, err := p.params(req)
	if err != nil {
		return executor.PollResult{}, err
	}
	return p.base.poll(ctx, req, ref, params)
}

func (p *VideoRegenPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	return runToCompletion(ctx, p, req)
}

var _ executor.AsyncPlugin = (*VideoRegenPlugin)(nil)
