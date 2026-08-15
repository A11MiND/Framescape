package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/jobsvc"
)

type createJobRequest struct {
	WorkflowName string      `json:"workflow_name" binding:"required"`
	Spec         jobsvc.Spec `json:"spec" binding:"required"`
}

func (s *Server) handleCreateJob(c *gin.Context) {
	var req createJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	// §11.5's HTTP Idempotency-Key defense: optional — a client that omits
	// it gets the old no-dedup behavior, but a retried request (network
	// timeout, double-click) that includes the same key gets back the
	// original job instead of holding credits or submitting twice. See
	// jobsvc.Service.Create's doc for the full mechanism.
	idemKey := c.GetHeader("Idempotency-Key")

	job, err := s.jobs.Create(c.Request.Context(), userID(c), req.WorkflowName, req.Spec, idemKey)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("submit_failed", err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
	})
}

// handleResumeJob is POST /api/v1/jobs/{bizID}/resume (§13.4): the preview
// gate's decision for a video.sequence job. Only meaningful while the job's
// `gate` task is Suspended — jobsvc.Resume/Aether's own Resume() are no-ops
// (not errors) if it already moved on, matching Resume's documented
// "no-op if the task is no longer in PhaseRunning" semantics.
func (s *Server) handleResumeJob(c *gin.Context) {
	var req jobsvc.ResumeVideoSequenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if err := s.jobs.Resume(c.Request.Context(), userID(c), c.Param("bizID"), req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("resume_failed", err.Error()))
		return
	}
	c.Status(http.StatusOK)
}

// handleListJobs is GET /api/v1/jobs?status=&cursor=&limit= (F7.1): the job
// list PRD §13.2 always specced but the frontend never had a backend for —
// jobsvc.Service.List's own doc covers the cursor shape.
func (s *Server) handleListJobs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)

	rows, next, err := s.jobs.List(c.Request.Context(), userID(c), c.Query("status"), cursor, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list jobs"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, j := range rows {
		out = append(out, gin.H{
			"biz_id":           j.BizID,
			"workflow_name":    j.WorkflowName,
			"title":            j.Title,
			"status":           j.Status,
			"node_total":       j.NodeTotal,
			"node_done":        j.NodeDone,
			"node_failed":      j.NodeFailed,
			"credit_estimated": j.CreditEstimated,
			"credit_held":      j.CreditHeld,
			"credit_settled":   j.CreditSettled,
			"created_at":       j.CreatedAt,
			"finished_at":      j.FinishedAt,
		})
	}
	resp := gin.H{"jobs": out}
	if next > 0 {
		resp["next_cursor"] = strconv.FormatUint(next, 10)
	}
	c.JSON(http.StatusOK, resp)
}

// handleEstimateJob is POST /api/v1/jobs/estimate (§13.3): quotes the same
// credit figure Create would hold, without holding it or submitting
// anything — jobsvc.EstimateCredits's own doc covers why this is a separate
// function from Create rather than a shared call site.
func (s *Server) handleEstimateJob(c *gin.Context) {
	var req createJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	credits, err := jobsvc.EstimateCredits(req.WorkflowName, req.Spec)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("estimate_failed", err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"credits_total": credits})
}

// handleCancelJob is POST /api/v1/jobs/{bizID}/cancel (F7.4).
// jobsvc.Service.Cancel's own doc covers why this doesn't touch credits
// directly.
func (s *Server) handleCancelJob(c *gin.Context) {
	if err := s.jobs.Cancel(c.Request.Context(), userID(c), c.Param("bizID")); err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("cancel_failed", err.Error()))
		return
	}
	c.Status(http.StatusOK)
}

func (s *Server) handleGetJob(c *gin.Context) {
	job, run, err := s.jobs.Get(c.Request.Context(), userID(c), c.Param("bizID"))
	if err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", err.Error()))
		return
	}

	nodes := make([]gin.H, 0)
	if run != nil {
		for _, n := range run.Nodes {
			nodes = append(nodes, gin.H{
				"name":    n.Name,
				"phase":   n.Phase,
				"outputs": n.Outputs,
				"error":   n.ErrorMsg,
				// loop_index (-1 outside a loop) was already resolved
				// correctly by the engine (workflow.NodeState.LoopIndex,
				// itself real scope-tree data — see LoopIndexFromScope's
				// doc, not a stub) but never made it into this response.
				// Without it every image.comic4/image.sequence iteration
				// shares one name ("gen-one-panel" ×4), so the frontend's
				// DAG view could only ever show one aggregated "3/4 done"
				// blob instead of each panel's own status — see
				// jobGraph.ts's buildJobGraph for the consumer this unlocks.
				"loop_index": n.LoopIndex,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"workflow_name":   job.WorkflowName,
		"title":           job.Title,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
		"nodes":           nodes,
		// json.RawMessage so job.Spec's already-valid JSON bytes embed
		// directly rather than being marshaled as a base64 string. Needed so
		// /jobs/{bizID} (F7.2's DAG view) is fully reconstructible from the
		// URL alone — e.g. the embedded preview gate needs the original
		// duration_seconds back to estimate the 2K-upgrade cost, and that's
		// only ever been persisted in Spec, never echoed elsewhere.
		"spec": json.RawMessage(job.Spec),
	})
}
