// story_split.go is F5.4's auto-split: turns one story description into N
// comic-panel scene descriptions via MiniMax-M3. image.comic4 always chains
// panels now, so each panel's dependency chain is static and auto-split has
// to resolve before the DAG is even built — SplitStory is called
// synchronously from Go instead of running as a task. No DAG task wraps it,
// so there's no executor.Plugin here, just the plain function.
package minimax

import (
	"context"
	"fmt"
	"strings"
)

// PRD doesn't price F5.4's text model (added after §12.1's pricing table was
// written) — these are MiniMax-M3's standard-tier (<=512K context) rates
// from platform.minimaxi.com/docs/guides/pricing-paygo.
const (
	textModel          = "MiniMax-M3"
	textInputYuanPerM  = 2.10
	textOutputYuanPerM = 8.40
)

// SplitStory asks MiniMax-M3 to turn one story description into exactly
// count comic-panel scene descriptions. count is image.comic4's own
// user-chosen panel count (§07 gap: "漫畫可以自己選數量" — no longer fixed
// at 4). Returns nil panels (not an error) only when the API call itself
// returned zero choices; every other shape of a messy response is
// parsePanels' own job to salvage.
func SplitStory(ctx context.Context, client *Client, story string, count int) (panels []string, costYuan float64, err error) {
	if count < 1 {
		count = 4
	}
	if r := []rune(story); len(r) > 2000 {
		story = string(r[:2000])
	}

	instruction := fmt.Sprintf(
		"请把下面这段剧情拆成恰好 %d 个连续的漫画分镜画面描述。"+
			"只输出 %d 行画面描述，每行一格，不要编号、不要多余说明文字、不要空行。\n\n剧情：%s",
		count, count, story)

	resp, err := client.ChatCompletion(ctx, ChatCompletionRequest{
		Model:               textModel,
		Messages:            []ChatMessage{{Role: "user", Content: instruction}},
		Temperature:         0.7,
		MaxCompletionTokens: 200 * count,
		// This task wants a short structured list, not chain-of-thought — see
		// ThinkingConfig's doc for why this is required, not just an
		// optimization (without it, reasoning text leaks into the parsed
		// panels, confirmed by a real test call before this fix landed).
		Thinking: &ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Choices) == 0 {
		return nil, 0, nil
	}

	panels = parsePanels(resp.Choices[0].Message.Content, story, count)
	costYuan = float64(resp.Usage.PromptTokens)/1_000_000*textInputYuanPerM +
		float64(resp.Usage.CompletionTokens)/1_000_000*textOutputYuanPerM
	return panels, costYuan, nil
}

// parsePanels defensively extracts exactly count non-empty lines from the
// model's free-text response — this endpoint has no JSON-mode guarantee, so
// the model may still add numbering/bullets despite being asked not to.
// Strips common leading markers, pads with the original story text if fewer
// than count lines come back, and truncates if more do: a formatting slip
// should never fail the job when count non-empty prompts is all image.
// comic4 actually needs.
func parsePanels(content, fallback string, count int) []string {
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
	for len(lines) < count {
		lines = append(lines, fallback)
	}
	return lines[:count]
}
