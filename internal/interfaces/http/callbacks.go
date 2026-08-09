package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/pkg/config"
)

// callbackDedupeTTL bounds how long a (task_id, status) pair is remembered
// for idempotency (§11.3: "同一 task_id 的同一状态重复推送要吃掉") — a status
// only transitions a handful of times over a task's life, so this only ever
// needs to outlive the video wait itself.
const callbackDedupeTTL = 30 * time.Minute

// handleMiniMaxCallback is POST /internal/callbacks/minimax (§3.3/§11.3).
// MiniMax's own docs don't specify a request-signing scheme in what this
// project has seen, so "来源校验" here is a shared secret baked into the
// callback_url itself (?token=...) rather than a header signature — set via
// MINIMAX_CALLBACK_TOKEN; a request with the wrong (or, if configured, a
// missing) token is rejected before any parsing happens.
//
// Everything else is deliberately trivial: no DB reads, no business logic —
// §11.3/§3.3 require the challenge handshake to complete within 3 seconds,
// and status pushes just publish to Redis for whichever minimax.video
// Execute() is waiting (video.go's wait()); that wait loop polls
// independently regardless, so a lost or malformed callback here can never
// stall a job — it only ever misses the fast-path wakeup.
func (s *Server) handleMiniMaxCallback(c *gin.Context) {
	if expected := config.MiniMaxCallbackToken(); expected != "" && c.Query("token") != expected {
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

	ctx := c.Request.Context()
	dedupeKey := "mmcb:" + push.Task.ID + ":" + push.Task.Status
	if s.redis != nil {
		ok, err := s.redis.SetNX(ctx, dedupeKey, 1, callbackDedupeTTL).Result()
		if err == nil && ok {
			// Channel name shared with minimax.VideoPlugin.wait()'s subscribe
			// side (videoCallbackChannel) — publish is best-effort, the wait
			// loop's own polling ticker is the source of truth regardless.
			s.redis.Publish(ctx, "minimax:video:"+push.Task.ID, push.Task.Status)
		}
	}
	c.Status(http.StatusOK)
}
