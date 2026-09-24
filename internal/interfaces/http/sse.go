package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/orchestrator"
)

const sseHeartbeat = 15 * time.Second

func (s *Server) sseStart(c *gin.Context) (http.Flusher, bool) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, errBody("internal", "streaming unsupported"))
		return nil, false
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	fmt.Fprintf(c.Writer, ": connected\n\n")
	flusher.Flush()
	return flusher, true
}

// handleStream is GET /api/v1/stream: every event of the signed-in user.
// A client reconnecting with Last-Event-ID (header or last_event_id query)
// first receives what it missed from job_events, then live events.
func (s *Server) handleStream(c *gin.Context) {
	if s.hub == nil || s.orch == nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "event stream unavailable"))
		return
	}
	ctx := c.Request.Context()
	uid := userID(c)
	live, stop, err := s.hub.Subscribe(ctx, uid)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "event stream unavailable"))
		return
	}
	defer stop()

	lastID := parseLastEventID(c)
	if lastID == 0 && c.GetHeader("Last-Event-ID") == "" && c.Query("last_event_id") == "" {
		if latest, err := s.orch.LatestEventID(ctx, uid); err == nil {
			lastID = latest
		}
	}
	flusher, ok := s.sseStart(c)
	if !ok {
		return
	}
	write := func(e orchestrator.Event) {
		if e.ID <= lastID {
			return
		}
		raw, _ := json.Marshal(e)
		fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, raw)
		flusher.Flush()
		lastID = e.ID
	}
	for {
		missed, err := s.orch.EventsSince(ctx, uid, lastID, 500)
		if err != nil {
			return
		}
		for _, e := range missed {
			write(e)
		}
		if len(missed) < 500 {
			break
		}
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		case e := <-live:
			write(e)
		}
	}
}

func parseLastEventID(c *gin.Context) uint64 {
	raw := c.GetHeader("Last-Event-ID")
	if raw == "" {
		raw = c.Query("last_event_id")
	}
	id, _ := strconv.ParseUint(raw, 10, 64)
	return id
}

// handleJobEvents is GET /api/v1/jobs/{bizID}/events: one job's events in
// the node_update/job_update/done shape existing clients consume.
func (s *Server) handleJobEvents(c *gin.Context) {
	ctx := c.Request.Context()
	job, _, err := s.jobs.Get(ctx, userID(c), c.Param("bizID"))
	if err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", err.Error()))
		return
	}
	if s.hub == nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "event stream unavailable"))
		return
	}
	live, stop, err := s.hub.Subscribe(ctx, job.UserID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "event stream unavailable"))
		return
	}
	defer stop()
	flusher, ok := s.sseStart(c)
	if !ok {
		return
	}
	if workflow.JobTerminal(job.Status) {
		fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		case e := <-live:
			if e.JobBizID != job.BizID {
				continue
			}
			var p struct {
				Node      string         `json:"node"`
				Status    string         `json:"status"`
				ErrorCode string         `json:"error_code"`
				Outputs   map[string]any `json:"outputs"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			switch e.Type {
			case orchestrator.EventNodeStatus:
				data, _ := json.Marshal(map[string]any{"type": "node_update", "node": p.Node, "phase": workflow.LegacyPhase(p.Status), "error_msg": p.ErrorCode})
				fmt.Fprintf(c.Writer, "event: node_update\ndata: %s\n\n", data)
			case orchestrator.EventJobStatus, orchestrator.EventJobFinished:
				data, _ := json.Marshal(map[string]any{"type": "job_update", "phase": p.Status})
				fmt.Fprintf(c.Writer, "event: job_update\ndata: %s\n\n", data)
				if workflow.JobTerminal(p.Status) {
					fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
					flusher.Flush()
					return
				}
			default:
				continue
			}
			flusher.Flush()
		}
	}
}
