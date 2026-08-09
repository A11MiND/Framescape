package httpapi

import (
	"encoding/json"
	"net/http"

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

func (s *Server) handleGetJob(c *gin.Context) {
	job, run, err := s.jobs.Get(c.Request.Context(), c.Param("bizID"))
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
