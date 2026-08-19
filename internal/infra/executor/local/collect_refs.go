// collect_refs.go is local.collect_refs: video.sequence's narrative-
// continuity bundle (Spec.NarrativeContinuity's own doc) needs one shot's
// r2va call to reference several *different* earlier shots' outputs at
// once — e.g. shot 7's reference-video-asset-ids might need shot 6's clip
// and shot 5's clip together. Aether's valueFrom binds one parameter to
// exactly one `tasks.X.outputs.parameters.Y` path; there is no way to build
// a single array argument whose elements come from several different
// tasks' outputs without an intermediate collecting step. This plugin is
// that step: several individually-optional, individually-sourced scalar
// inputs in, the non-empty ones combined into ordered arrays out. Pure Go,
// no external call — the same "several parallel dynamic inputs, one
// combined value" shape local.ffmpeg.concat's own ConcatConfig already
// uses for its keep/redone/upgraded shot arrays, just without ffmpeg.
package local

import (
	"context"
	"fmt"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// CollectRefsConfig has two callers with two different reasons for their
// slot counts. video.sequence's narrative-continuity bundle only ever needs
// a handful of keyframes plus the protagonist's own reference image (well
// under this struct's own image capacity) — its own real ceiling is the
// verified r2va reference_video budget (≤3 clips total, this project's own
// live-call verification), not this struct's slot count. image.comic4's
// Continuity Mode reuses the same executor for a different job — gathering
// however many panels the batch actually has (up to capability.ImageMaxN)
// into one array before the final compose-grid step — which is what
// actually sets the image slot count to 9, not video.sequence's own needs.
type CollectRefsConfig struct {
	Image1 string `json:"image-1"`
	Image2 string `json:"image-2"`
	Image3 string `json:"image-3"`
	Image4 string `json:"image-4"`
	Image5 string `json:"image-5"`
	Image6 string `json:"image-6"`
	Image7 string `json:"image-7"`
	Image8 string `json:"image-8"`
	Image9 string `json:"image-9"`
	Video1 string `json:"video-1"`
	Video2 string `json:"video-2"`
	Video3 string `json:"video-3"`
}

type CollectRefsPlugin struct{}

func NewCollectRefsPlugin() *CollectRefsPlugin { return &CollectRefsPlugin{} }

func (p *CollectRefsPlugin) Type() string { return "local.collect_refs" }

func (p *CollectRefsPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[CollectRefsConfig, executor.DynamicOutputs](
		"local.collect_refs", "1.0", "Combine several individually-sourced reference asset IDs into ordered arrays",
	)
}

func (p *CollectRefsPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg CollectRefsConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind local.collect_refs inputs: %w", err)
	}

	images := nonEmptyStrings(cfg.Image1, cfg.Image2, cfg.Image3, cfg.Image4, cfg.Image5, cfg.Image6, cfg.Image7, cfg.Image8, cfg.Image9)
	videos := nonEmptyStrings(cfg.Video1, cfg.Video2, cfg.Video3)

	return executor.OutputFrom(struct {
		ImageIDs []string `json:"image-ids"`
		VideoIDs []string `json:"video-ids"`
	}{ImageIDs: images, VideoIDs: videos})
}

func nonEmptyStrings(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

var _ executor.Plugin = (*CollectRefsPlugin)(nil)
