package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/pkg/config"
)

// callbackDedupeTTL bounds how long a (task_id, status) pair is remembered
// for idempotency (§11.3: "同一 task_id 的同一状态重复推送要吃掉") — a status
// only transitions a handful of times over a task's life, so this only ever
// needs to outlive the video wait itself.
// handleMiniMaxCallback is POST /internal/callbacks/minimax. The callback
// URL carries a shared token (MINIMAX_CALLBACK_TOKEN) since MiniMax does not
// sign requests. The handshake must answer within 3 seconds, so the handler
// does no work beyond scheduling an immediate poll of the waiting node.
func (s *Server) handleMiniMaxCallback(c *gin.Context) {
	if expected := config.MiniMaxCallbackToken(); expected != "" && subtle.ConstantTimeCompare([]byte(c.Query("token")), []byte(expected)) != 1 {
		c.Status(http.StatusUnauthorized)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	var challenge struct {
		Challenge json.RawMessage `json:"challenge"`
	}
	if err := json.Unmarshal(raw, &challenge); err == nil && len(challenge.Challenge) > 0 {
		c.Data(http.StatusOK, "application/json", []byte(`{"challenge":`+string(challenge.Challenge)+`}`))
		return
	}

	var push struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &push); err != nil || push.Task.ID == "" {
		// Not a challenge, not a recognizable status push — ack anyway so
		// MiniMax doesn't retry-storm us over a shape we don't understand.
		c.Status(http.StatusOK)
		return
	}

	// The callback only wakes the waiting node's poll early; the poll itself
	// verifies the task status with the provider, so a forged or duplicate
	// callback cannot complete anything.
	if s.orch != nil {
		_ = s.orch.Nudge(c.Request.Context(), push.Task.ID)
	}
	c.Status(http.StatusOK)
}
