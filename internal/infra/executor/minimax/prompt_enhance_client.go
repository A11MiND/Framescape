package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// H3ContextIRRequest is POST /v2/h3_context_ir's request shape (PRD §3.4,
// F6.10): the exact same multimodal `content` array a video_generation call
// would use (verified against platform.minimaxi.com/docs/api-reference/
// video-generation-v2-h3-context-ir — the China-domain docs, matching this
// client's api.minimaxi.com base; the international platform.minimax.io
// mirror agrees on every field here). `duration`/`ratio` are required even
// though this call never generates video — H3-Context-IR reasons about the
// same constraints the eventual video call would have.
type H3ContextIRRequest struct {
	Model    string             `json:"model"`
	Content  []VideoContentItem `json:"content"`
	Duration int                `json:"duration"`
	Ratio    string             `json:"ratio,omitempty"`
}

type CreateH3ContextIRResponse struct {
	TaskID   string   `json:"task_id"`
	BaseResp BaseResp `json:"base_resp"`
}

// H3ContextIRTaskStatus mirrors VideoTaskStatus's shape but for
// task_type=h3_context_ir: `content` holds `prompt` (not `url`), and `usage`
// is token-based (not seconds-based) since billing is per §3.4's ¥5.80/M
// input + ¥23.00/M output tokens, not per output second.
type H3ContextIRTaskStatus struct {
	Task struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"` // queued | running | succeeded | failed | cancelled — same enum as video tasks
		Content *struct {
			Prompt string `json:"prompt"`
		} `json:"content"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *VideoTaskError `json:"error"`
	} `json:"task"`
}

// CreateH3ContextIRTask submits an F6.10 prompt-enhancement task. Confirmed
// against the real API to share MiniMax's H3 task family query/list/cancel
// surface (PRD §3.4: "与其他 H3 任务共用查询/列表/取消接口") — only task
// creation has its own path, so QueryVideoTask's endpoint is reused unchanged
// for polling (see waitH3ContextIR below), just decoded into a different
// response shape.
func (c *Client) CreateH3ContextIRTask(ctx context.Context, req H3ContextIRRequest) (*CreateH3ContextIRResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal h3_context_ir request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/h3_context_ir", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call h3_context_ir: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var out CreateH3ContextIRResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}

// QueryH3ContextIRTask polls the same /v2/query/video_generation/{task_id}
// endpoint VideoClient.QueryVideoTask uses (see doc above) but decodes into
// H3ContextIRTaskStatus's token-usage/prompt-content shape instead.
func (c *Client) QueryH3ContextIRTask(ctx context.Context, taskID string) (*H3ContextIRTaskStatus, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/query/video_generation/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call query/video_generation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	var out H3ContextIRTaskStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	return &out, nil
}
