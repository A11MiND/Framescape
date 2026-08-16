package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/executor/minimax"
)

// handleRewritePrompt is the composer's ✨ "AI 改写" button: a synchronous,
// job-free rewrite of whatever the caller has typed so far, distinct from
// F6.10's prompt_enhance (jobsvc.go's own doc) — that one runs as a paid
// workflow node baked into a specific generation job's DAG and only exists
// for video.single; this is a lightweight utility meant to be clicked
// repeatedly while still drafting, before any workflow is submitted at all,
// the same "cmd/api talks to MiniMax directly" exception trial.go already
// carved out for the same reason (no job/credit ceremony fits a
// not-yet-submitted draft). Reuses story_split.go's MiniMax-M3 chat model
// rather than adding a third LLM integration.
func (s *Server) handleRewritePrompt(c *gin.Context) {
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "text required"))
		return
	}
	if r := []rune(req.Text); len(r) > 1500 {
		req.Text = string(r[:1500])
	}

	instruction := "你是一个 AI 绘画/视频生成的提示词专家。请把下面这段用户写的粗略描述，改写成更生动、具体、有画面感的生成提示词：" +
		"补充光线、构图、氛围、细节等有助于生成效果的描述，但不要改变原本的核心主体和意图，不要无中生有加入用户没提到的人物或场景。" +
		"只输出改写后的一段提示词，不要编号、不要解释、不要引号。\n\n原始描述：" + req.Text

	resp, err := s.minimax.ChatCompletion(c.Request.Context(), minimax.ChatCompletionRequest{
		Model:               "MiniMax-M3",
		Messages:            []minimax.ChatMessage{{Role: "user", Content: instruction}},
		Temperature:         0.7,
		MaxCompletionTokens: 800,
		Thinking:            &minimax.ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, errBody("upstream_error", "改写失败，请重试"))
		return
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		c.JSON(http.StatusUnprocessableEntity, errBody("generation_failed", "改写失败，请重试"))
		return
	}

	rewritten := strings.TrimSpace(resp.Choices[0].Message.Content)
	rewritten = strings.Trim(rewritten, "\"“”")
	c.JSON(http.StatusOK, gin.H{"text": rewritten})
}
