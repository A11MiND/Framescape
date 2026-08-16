package minimax

import (
	"context"
	"fmt"
	"strings"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// PRD doesn't price F5.4's text model (added after §12.1's pricing table was
// written) — these are MiniMax-M3's standard-tier (<=512K context) rates
// from platform.minimaxi.com/docs/guides/pricing-paygo.
const (
	textModel          = "MiniMax-M3"
	textInputYuanPerM  = 2.10
	textOutputYuanPerM = 8.40
)

// StorySplitConfig is minimax.text.split_story's input contract (F5.4): one
// story description in, exactly 4 panel prompts out.
type StorySplitConfig struct {
	Story  string `json:"story"`
	UserID string `json:"user-id"`
	// SourceImageAssetID rides along unchanged into every generated panel's
	// item map (same "carries no meaning to this executor, just needs to
	// reach minimax.image" reasoning as UserID above) — jobsvc.go's own doc
	// on Spec.SourceImageAssetID covers why every image.* mode now accepts
	// this the same way.
	SourceImageAssetID string `json:"source-image-asset-id"`
}

// StorySplitPlugin is minimax.text.split_story: asks MiniMax-M3 to turn one
// story description into 4 comic-panel scene descriptions, shaped as the
// same [{prompt,user-id}, ...] object array image-comic4.json's Loop already
// expects (jobsvc.go builds this identically for the manual-4-panels path;
// see image-comic4-auto.json's annotation for why the object-array shape is
// required). Deliberately does NOT run character/preset compilation on the
// output — prompt.Compile() is a Go-side synchronous step this executor has
// no access to, so the auto-split path trades character-consistency for not
// having to hand-write 4 panels; combining both is a further enhancement
// beyond F5.4's P1 scope.
type StorySplitPlugin struct {
	client *Client
}

func NewStorySplitPlugin(client *Client) *StorySplitPlugin {
	return &StorySplitPlugin{client: client}
}

func (p *StorySplitPlugin) Type() string { return "minimax.text.split_story" }

func (p *StorySplitPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[StorySplitConfig, executor.DynamicOutputs](
		"minimax.text.split_story", "1.0",
		"F5.4: MiniMax-M3 splits one story description into exactly 4 comic-panel prompts",
	)
}

func (p *StorySplitPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg StorySplitConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.text.split_story inputs: %w", err)
	}

	story := cfg.Story
	if r := []rune(story); len(r) > 2000 {
		story = string(r[:2000])
	}

	instruction := "请把下面这段剧情拆成恰好 4 个连续的漫画分镜画面描述。" +
		"只输出 4 行画面描述，每行一格，不要编号、不要多余说明文字、不要空行。\n\n剧情：" + story

	resp, err := p.client.ChatCompletion(ctx, ChatCompletionRequest{
		Model:               textModel,
		Messages:            []ChatMessage{{Role: "user", Content: instruction}},
		Temperature:         0.7,
		MaxCompletionTokens: 800,
		// This task wants a short structured list, not chain-of-thought — see
		// ThinkingConfig's doc for why this is required, not just an
		// optimization (without it, reasoning text leaks into the parsed
		// panels, confirmed by a real test call before this fix landed).
		Thinking: &ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return classifyVideoError(err), nil
	}
	if len(resp.Choices) == 0 {
		return errOutputs(model.ExecCodeError, "empty choices from chat completion"), nil
	}

	panels := parsePanels(resp.Choices[0].Message.Content, story)
	items := make([]map[string]string, len(panels))
	for i, panelText := range panels {
		items[i] = map[string]string{"prompt": panelText, "user-id": cfg.UserID, "source-image-asset-id": cfg.SourceImageAssetID}
	}

	costYuan := float64(resp.Usage.PromptTokens)/1_000_000*textInputYuanPerM +
		float64(resp.Usage.CompletionTokens)/1_000_000*textOutputYuanPerM

	return executor.OutputFrom(struct {
		Panels   []map[string]string `json:"panels"`
		CostYuan float64             `json:"cost-yuan"`
	}{Panels: items, CostYuan: costYuan})
}

// parsePanels defensively extracts exactly 4 non-empty lines from the
// model's free-text response — this endpoint has no JSON-mode guarantee, so
// the model may still add numbering/bullets despite being asked not to.
// Strips common leading markers, pads with the original story text if fewer
// than 4 lines come back, and truncates if more do: a formatting slip should
// never fail the task when 4 non-empty prompts is all image-comic4's Loop
// actually needs.
func parsePanels(content, fallback string) []string {
	// Backstop for Thinking{Type:"disabled"}: strip any <think>...</think>
	// block that still comes through (e.g. truncated by max_completion_tokens
	// before the closing tag), same belt-and-braces pattern as this
	// codebase's other backend guards behind a primary one.
	if start := strings.Index(content, "<think>"); start >= 0 {
		if end := strings.Index(content, "</think>"); end > start {
			content = content[:start] + content[end+len("</think>"):]
		} else {
			content = content[:start]
		}
	}
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimLeft(line, "0123456789.、-–—) ")
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	for len(lines) < 4 {
		lines = append(lines, fallback)
	}
	return lines[:4]
}

var _ executor.Plugin = (*StorySplitPlugin)(nil)
