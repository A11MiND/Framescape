package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ChatMessage/ChatCompletionRequest/ChatCompletionResponse are MiniMax's
// OpenAI-compatible chat completions surface (verified against
// platform.minimaxi.com/docs/api-reference/text-openai-api — same
// api.minimaxi.com base this whole client already uses). Unlike the native
// image_generation/video_generation endpoints, this surface uses real HTTP
// status codes for errors (no body-embedded base_resp), same as the video
// endpoints — classifyVideoError is reused for both for that reason.
// Content is `any` rather than `string` because the OpenAI-compatible
// surface accepts either a plain string (F5.4's story-split usage) or an
// array of typed content parts for multimodal/vision input (F8.3's
// post-hoc asset review, e.g. [{"type":"image_url","image_url":{"url":...}},
// {"type":"text","text":...}]) — both marshal correctly through the same
// field since json.Marshal handles a string or a []map[string]any identically
// whether the static type holding it is `any` or the concrete type itself.
type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ThinkingConfig lets a caller turn off MiniMax-M3's default reasoning mode
// (verified against the docs: M3 supports `{"type":"disabled"}`; older M2.x
// models cannot disable it at all). Needed for any use case that wants a
// short structured answer, not chain-of-thought — without this, M3's
// <think>...</think> reasoning preamble ends up mixed into message.content
// alongside (or instead of, if truncated by max_completion_tokens) the
// actual answer.
type ThinkingConfig struct {
	Type string `json:"type"` // "disabled"
}

type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	Temperature         float64         `json:"temperature,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Thinking            *ThinkingConfig `json:"thinking,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ChatCompletion calls POST /v1/chat/completions (F5.4's MiniMax-M3 story
// splitter is currently its only caller).
func (c *Client) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call chat/completions: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var out ChatCompletionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}
