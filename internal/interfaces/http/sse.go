package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/projection"
)

// handleJobEvents is GET /api/v1/jobs/{bizID}/events (F7.3, PRD §13.5):
// subscribes to the job's Redis Pub/Sub channel and forwards every
// projection.Event as an SSE frame. Nginx must run with
// `proxy_buffering off; proxy_read_timeout 600s;` (R15, DEV_PLAN.md §6) or
// this silently never reaches the browser — noted in deploy docs, not
// enforceable from here.
func (s *Server) handleJobEvents(c *gin.Context) {
	job, _, err := s.jobs.Get(c.Request.Context(), userID(c), c.Param("bizID"))
	if err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", err.Error()))
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // belt-and-braces alongside the Nginx config note above

	sub := s.redis.Subscribe(c.Request.Context(), projection.ChannelForRun(job.WorkflowRunID))
	defer sub.Close()
	ch := sub.Channel()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, errBody("internal", "streaming unsupported"))
		return
	}

	// Heartbeat so intermediate proxies/load balancers don't idle-timeout
	// the connection during long gaps between state changes (e.g. a 20-30s
	// video generation with no intermediate progress).
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	fmt.Fprintf(c.Writer, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var ev struct {
				Type  string `json:"type"`
				Phase string `json:"phase"`
			}
			_ = json.Unmarshal([]byte(msg.Payload), &ev)
			eventName := "node_update"
			if ev.Type == "job_update" {
				eventName = "job_update"
			}

			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventName, msg.Payload)
			flusher.Flush()

			if eventName == "job_update" && isTerminalPhase(ev.Phase) {
				fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func isTerminalPhase(phase string) bool {
	switch phase {
	case "Succeeded", "Failed", "Error", "Timeout", "Cancelled":
		return true
	default:
		return false
	}
}
