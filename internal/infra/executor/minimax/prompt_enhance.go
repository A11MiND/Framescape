package minimax

import (
	"context"
	"fmt"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// PRD §3.4's per-token pricing for F6.10's optional prompt-enhancement node.
const (
	promptEnhanceInputYuanPerM  = 5.80
	promptEnhanceOutputYuanPerM = 23.00
)

// PromptEnhanceConfig is minimax.prompt_enhance's input contract (F6.10):
// the same multimodal reference shape minimax.video accepts, since
// H3-Context-IR reasons about exactly the content a subsequent video call
// would send. No Resolution field — this step never generates video.
type PromptEnhanceConfig struct {
	Prompt   string `json:"prompt"`
	Duration string `json:"duration"` // required by the endpoint even though it doesn't generate video
	Ratio    string `json:"ratio"`

	FirstFrameAssetID      string   `json:"first-frame-asset-id"`
	LastFrameAssetID       string   `json:"last-frame-asset-id"`
	ReferenceImageAssetIDs []string `json:"reference-image-asset-ids"`
	ReferenceVideoAssetIDs []string `json:"reference-video-asset-ids"`
	ReferenceAudioAssetIDs []string `json:"reference-audio-asset-ids"`
}

// PromptEnhancePlugin is minimax.prompt_enhance (F6.10, PRD §3.4): an
// optional DAG node placed before minimax.video/minimax.video.regen that
// asks MiniMax's H3-Context-IR model to turn the same multimodal content
// into a richer, structured prompt. Reuses videoBase's buildContent/refItem
// (asset-ID -> mm_file:// resolution) since the reference shape is
// identical to minimax.video's — only the submit/query/materialize tail
// differs (no video is produced, so there is no assetstore.Sink write).
type PromptEnhancePlugin struct {
	base *videoBase
}

func NewPromptEnhancePlugin(client *Client, reader assetstore.Reader, cache FileCache) *PromptEnhancePlugin {
	return &PromptEnhancePlugin{base: &videoBase{client: client, reader: reader, cache: cache}}
}

func (p *PromptEnhancePlugin) Type() string { return "minimax.prompt_enhance" }

func (p *PromptEnhancePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[PromptEnhanceConfig, executor.DynamicOutputs](
		"minimax.prompt_enhance", "1.0",
		"MiniMax H3-Context-IR optional prompt-enhancement node (F6.10/PRD §3.4): returns a richer prompt, does not generate video",
	)
}

func (p *PromptEnhancePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg PromptEnhanceConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.prompt_enhance inputs: %w", err)
	}

	duration := normalizeDuration(cfg.Duration)

	content, _, ratio, errOut := p.base.buildContent(ctx, videoRefs{
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

	created, err := p.base.client.CreateH3ContextIRTask(ctx, H3ContextIRRequest{
		Model: videoModel, Content: content, Duration: duration, Ratio: ratio,
	})
	if err != nil {
		return classifyVideoError(err), nil
	}

	task, waitErr := p.wait(ctx, created.TaskID)
	if waitErr != nil {
		return errOutputs(model.ExecCodeTimeout, "wait_timeout: "+waitErr.Error()), nil
	}

	switch task.Task.Status {
	case "failed":
		msg := "failed"
		if task.Task.Error != nil {
			msg = task.Task.Error.Code + ": " + task.Task.Error.Message
		}
		return errOutputs(model.ExecCodeFailed, msg), nil
	case "cancelled":
		return errOutputs(model.ExecCodeFailed, "cancelled_upstream"), nil
	case "succeeded":
		// continue below
	default:
		return errOutputs(model.ExecCodeError, "unexpected terminal status: "+task.Task.Status), nil
	}
	if task.Task.Content == nil || task.Task.Content.Prompt == "" {
		return errOutputs(model.ExecCodeError, "succeeded task has no content.prompt"), nil
	}

	costYuan := 0.0
	if task.Task.Usage != nil {
		costYuan = float64(task.Task.Usage.PromptTokens)/1_000_000*promptEnhanceInputYuanPerM +
			float64(task.Task.Usage.CompletionTokens)/1_000_000*promptEnhanceOutputYuanPerM
	}

	return executor.OutputFrom(struct {
		EnhancedPrompt string  `json:"enhanced-prompt"`
		CostYuan       float64 `json:"cost-yuan"`
		MinimaxTaskID  string  `json:"minimax-task-id"`
	}{
		EnhancedPrompt: task.Task.Content.Prompt,
		CostYuan:       costYuan,
		MinimaxTaskID:  created.TaskID,
	})
}

// wait polls QueryH3ContextIRTask on the same cadence/deadline as
// minimax.video's wait — H3-Context-IR shares the video task family's
// queueing behavior even though it's much faster in practice (text-only
// output, no rendering).
func (p *PromptEnhancePlugin) wait(ctx context.Context, taskID string) (*H3ContextIRTaskStatus, error) {
	deadline := time.Now().Add(videoMaxWait)
	ticker := time.NewTicker(videoPollInterval)
	defer ticker.Stop()

	for {
		if status, err := p.base.client.QueryH3ContextIRTask(ctx, taskID); err == nil && isTerminalVideoStatus(status.Task.Status) {
			return status, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("exceeded %s", videoMaxWait)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

var _ executor.Plugin = (*PromptEnhancePlugin)(nil)
