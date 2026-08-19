package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/persistence"
)

type createJobRequest struct {
	WorkflowName string      `json:"workflow_name" binding:"required"`
	Spec         jobsvc.Spec `json:"spec" binding:"required"`
	ProjectID    string      `json:"project_id,omitempty"`
}

func (s *Server) handleCreateJob(c *gin.Context) {
	var req createJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	var projectID *uint64
	if req.ProjectID != "" {
		resolved, ok := s.resolveProjectID(c.Request.Context(), userID(c), req.ProjectID)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		projectID = &resolved
	}

	// §11.5's HTTP Idempotency-Key defense: optional — a client that omits
	// it gets the old no-dedup behavior, but a retried request (network
	// timeout, double-click) that includes the same key gets back the
	// original job instead of holding credits or submitting twice. See
	// jobsvc.Service.Create's doc for the full mechanism.
	idemKey := c.GetHeader("Idempotency-Key")

	job, err := s.jobs.Create(c.Request.Context(), userID(c), req.WorkflowName, req.Spec, idemKey, projectID)
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
	// 204, not 200: see handleUpdateAsset's identical comment — a 200 with
	// no body makes the frontend's request() helper throw on resp.json(),
	// so the preview-gate resume looked like it failed even after succeeding.
	c.Status(http.StatusNoContent)
}

// handleListJobs is GET /api/v1/jobs?status=&cursor=&limit= (F7.1): the job
// list PRD §13.2 always specced but the frontend never had a backend for —
// jobsvc.Service.List's own doc covers the cursor shape.
func (s *Server) handleListJobs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)

	var projectID *uint64
	if pid := c.Query("project_id"); pid != "" {
		resolved, ok := s.resolveProjectID(c.Request.Context(), userID(c), pid)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		projectID = &resolved
	}

	rows, next, err := s.jobs.List(c.Request.Context(), userID(c), c.Query("status"), cursor, limit, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list jobs"))
		return
	}

	// Batch-resolve retry_of_job_id -> biz_id once for the whole page, same
	// reasoning as handleListAssets' project_id resolution — the frontend
	// renders its own locale-aware retry indicator from this rather than
	// parsing any hardcoded-language text (jobsvc.RetryNode's own doc).
	retryOfIDs := make([]uint64, 0)
	seen := map[uint64]bool{}
	for _, j := range rows {
		if j.RetryOfJobID != nil && !seen[*j.RetryOfJobID] {
			seen[*j.RetryOfJobID] = true
			retryOfIDs = append(retryOfIDs, *j.RetryOfJobID)
		}
	}
	retryOfBizByID := make(map[uint64]string, len(retryOfIDs))
	if len(retryOfIDs) > 0 {
		var srcJobs []persistence.Job
		_ = s.db.WithContext(c.Request.Context()).Where("id IN ?", retryOfIDs).Find(&srcJobs).Error
		for _, sj := range srcJobs {
			retryOfBizByID[sj.ID] = sj.BizID
		}
	}

	out := make([]gin.H, 0, len(rows))
	for _, j := range rows {
		retryOfBizID := ""
		if j.RetryOfJobID != nil {
			retryOfBizID = retryOfBizByID[*j.RetryOfJobID]
		}
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
			"retry_of_job_id":  retryOfBizID,
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
	items, credits, err := jobsvc.EstimateBreakdown(req.WorkflowName, req.Spec)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("estimate_failed", err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"credits_total": credits, "items": items})
}

// handleCancelJob is POST /api/v1/jobs/{bizID}/cancel (F7.4).
// jobsvc.Service.Cancel's own doc covers why this doesn't touch credits
// directly.
func (s *Server) handleCancelJob(c *gin.Context) {
	if err := s.jobs.Cancel(c.Request.Context(), userID(c), c.Param("bizID")); err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("cancel_failed", err.Error()))
		return
	}
	// 204, not 200: see handleUpdateAsset's identical comment.
	c.Status(http.StatusNoContent)
}

// handleDeleteJob is DELETE /api/v1/jobs/{bizID} — soft-deletes one job
// record out of the user's own 作业 history. jobsvc.Service.Delete's own
// doc covers why this is soft (deleted_at) and why only terminal jobs
// qualify.
func (s *Server) handleDeleteJob(c *gin.Context) {
	if err := s.jobs.Delete(c.Request.Context(), userID(c), c.Param("bizID")); err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("delete_failed", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

type retryNodeRequest struct {
	LoopIndex      int    `json:"loop_index"`
	PromptOverride string `json:"prompt_override,omitempty"`
}

// handleRetryNode is POST /api/v1/jobs/{bizID}/nodes/{nodeName}/retry — the
// exact path PRD §13.2/DEV_PLAN's node-retry gap always referenced:
// resubmits one Failed/Error/Timeout leaf task as a standalone satellite
// Job — jobsvc.Service.RetryNode's own doc covers why this doesn't try to
// patch the original run in place. Returns the new satellite job the same
// shape handleCreateJob does, so the frontend can navigate straight to it.
func (s *Server) handleRetryNode(c *gin.Context) {
	var req retryNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	job, err := s.jobs.RetryNode(c.Request.Context(), userID(c), c.Param("bizID"), c.Param("nodeName"), req.LoopIndex, req.PromptOverride)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("retry_failed", err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
	})
}

func (s *Server) handleGetJob(c *gin.Context) {
	job, run, err := s.jobs.Get(c.Request.Context(), userID(c), c.Param("bizID"))
	if err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", err.Error()))
		return
	}

	// job_nodes is the projection table (populated by
	// internal/application/projection), not the engine's own live state —
	// it's the only place credit_cost/started_at/finished_at exist at all
	// (the workflow.Engine port has no notion of credits, and only tracks
	// UpdatedAt, not a first-seen-Running timestamp). Keyed by
	// name|loop_index to match run.Nodes below, the same identity pair
	// job_nodes' own uk constraint uses.
	var projRows []persistence.JobNode
	_ = s.db.WithContext(c.Request.Context()).Where("job_id = ?", job.ID).Find(&projRows).Error
	projByKey := make(map[string]persistence.JobNode, len(projRows))
	for _, r := range projRows {
		projByKey[fmt.Sprintf("%s|%d", r.NodeName, r.LoopIndex)] = r
	}

	nodes := make([]gin.H, 0)
	if run != nil {
		for _, n := range run.Nodes {
			row := gin.H{
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
			}
			if pr, ok := projByKey[fmt.Sprintf("%s|%d", n.Name, n.LoopIndex)]; ok {
				row["credit_cost"] = pr.CreditCost
				row["started_at"] = pr.StartedAt
				row["finished_at"] = pr.FinishedAt
			}
			nodes = append(nodes, row)
		}
	}

	retryOfBizID := ""
	if job.RetryOfJobID != nil {
		_ = s.db.WithContext(c.Request.Context()).Model(&persistence.Job{}).
			Select("biz_id").Where("id = ?", *job.RetryOfJobID).Scan(&retryOfBizID).Error
	}

	projectBizID := ""
	if job.ProjectID != nil {
		_ = s.db.WithContext(c.Request.Context()).Model(&persistence.Project{}).
			Select("biz_id").Where("id = ?", *job.ProjectID).Scan(&projectBizID).Error
	}

	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"workflow_name":   job.WorkflowName,
		"title":           job.Title,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
		"retry_of_job_id": retryOfBizID,
		"project_id":      projectBizID,
		// credit_estimated/held/settled were already tracked on this row
		// (W7) but never echoed to the detail response — the job detail
		// page's "已消耗 / 預估" comparison bar (§19.4.3) needs both numbers
		// at once rather than requiring a second trip to /credits/ledger.
		"credit_estimated": job.CreditEstimated,
		"credit_held":      job.CreditHeld,
		"credit_settled":   job.CreditSettled,
		"nodes":            nodes,
		// json.RawMessage so job.Spec's already-valid JSON bytes embed
		// directly rather than being marshaled as a base64 string. Needed so
		// /jobs/{bizID} (F7.2's DAG view) is fully reconstructible from the
		// URL alone — e.g. the embedded preview gate needs the original
		// duration_seconds back to estimate the 2K-upgrade cost, and that's
		// only ever been persisted in Spec, never echoed elsewhere.
		"spec": json.RawMessage(job.Spec),
	})
}
