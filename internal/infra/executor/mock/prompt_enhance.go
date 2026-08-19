package mock

import (
	"context"
	"fmt"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// PromptEnhanceConfig mirrors minimax.prompt_enhance.PromptEnhanceConfig's
// own input contract field-for-field, so this fake can stand in wherever a
// DAG builder references the real executor type without any other change.
type PromptEnhanceConfig struct {
	Prompt   string `json:"prompt"`
	Duration string `json:"duration"`
	Ratio    string `json:"ratio"`

	FirstFrameAssetID      string   `json:"first-frame-asset-id"`
	LastFrameAssetID       string   `json:"last-frame-asset-id"`
	ReferenceImageAssetIDs []string `json:"reference-image-asset-ids"`
	ReferenceVideoAssetIDs []string `json:"reference-video-asset-ids"`
	ReferenceAudioAssetIDs []string `json:"reference-audio-asset-ids"`
}

// PromptEnhancePlugin fakes minimax.prompt_enhance (H3-Context-IR): sleeps
// briefly, then echoes the input prompt back with a marker so downstream
// gen nodes still get a real, non-empty enhanced-prompt string to chain
// off of.
type PromptEnhancePlugin struct {
	delay time.Duration
}

func NewPromptEnhancePlugin() *PromptEnhancePlugin {
	return &PromptEnhancePlugin{delay: 1 * time.Second}
}

func (p *PromptEnhancePlugin) Type() string { return "mock.prompt_enhance" }

func (p *PromptEnhancePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[PromptEnhanceConfig, executor.DynamicOutputs](
		"mock.prompt_enhance", "1.0", "Fake H3-Context-IR prompt enhancer for pre-MiniMax development",
	)
}

func (p *PromptEnhancePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg PromptEnhanceConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind mock.prompt_enhance inputs: %w", err)
	}

	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return executor.OutputFrom(struct {
		EnhancedPrompt string  `json:"enhanced-prompt"`
		CostYuan       float64 `json:"cost-yuan"`
		MinimaxTaskID  string  `json:"minimax-task-id"`
	}{
		EnhancedPrompt: fmt.Sprintf("[mock-enhanced] %s", cfg.Prompt),
		CostYuan:       0,
		MinimaxTaskID:  "mock-task",
	})
}

var _ executor.Plugin = (*PromptEnhancePlugin)(nil)
