package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
)

type createJobRequest struct {
	WorkflowName string      `json:"workflow_name" binding:"required"`
	Spec         jobsvc.Spec `json:"spec" binding:"required"`
	ProjectID    string      `json:"project_id,omitempty"`
	// QuoteTotal is the reservation the user confirmed; when present and the
	// current price differs, the job is refused instead of charging more.
	QuoteTotal *int `json:"quote_total,omitempty"`
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
	if req.QuoteTotal != nil {
		if current, err := jobsvc.EstimateCredits(req.WorkflowName, req.Spec); err == nil && current != *req.QuoteTotal {
			c.JSON(http.StatusConflict, gin.H{"code": "price_changed", "message": "the price changed since it was quoted", "credits_total": current})
			return
		}
	}

	job, err := s.jobs.Create(c.Request.Context(), userID(c), req.WorkflowName, req.Spec, idemKey, projectID)
	if err != nil {
		writeError(c, err, http.StatusUnprocessableEntity, "submit_failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
	})
}

// handleResumeJob is POST /api/v1/jobs/{bizID}/resume: applies the preview
// gate decision of a video.sequence job, reserving the quoted amount.
func (s *Server) handleResumeJob(c *gin.Context) {
	var req jobsvc.ResumeVideoSequenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if err := s.jobs.Resume(c.Request.Context(), userID(c), c.Param("bizID"), req); err != nil {
		writeResumeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// handleQuoteResume is POST /api/v1/jobs/{bizID}/resume/quote: what a gate
// decision would reserve, itemized, plus the all-upgrade comparison.
func (s *Server) handleQuoteResume(c *gin.Context) {
	var req jobsvc.ResumeVideoSequenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	q, err := s.jobs.QuoteResume(c.Request.Context(), userID(c), c.Param("bizID"), req)
	if err != nil {
		writeResumeError(c, err)
		return
	}
	c.JSON(http.StatusOK, q)
}

func writeResumeError(c *gin.Context, err error) {
	if errors.Is(err, orchestrator.ErrJobTerminal) {
		err = orchestrator.ErrGateNotSuspended
	}
	writeError(c, err, http.StatusUnprocessableEntity, "resume_failed")
}

// handleListJobs is GET /api/v1/jobs: the user's jobs newest first, or
// oldest first with order=oldest. Filters: bucket
// (needs_review|active|succeeded|failed|cancelled) or an exact status,
// exclude_status (comma separated), workflow, q (title contains),
// project_id; paged by cursor.
func (s *Server) handleListJobs(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)
	f := jobsvc.ListFilter{Status: c.Query("status"), Bucket: c.Query("bucket"), Workflow: c.Query("workflow"),
		Query: c.Query("q"), Cursor: cursor, Limit: limit, Oldest: c.Query("order") == "oldest"}
	if ex := c.Query("exclude_status"); ex != "" {
		f.Exclude = strings.Split(ex, ",")
	}
	if pid := c.Query("project_id"); pid != "" {
		resolved, ok := s.resolveProjectID(ctx, userID(c), pid)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		f.ProjectID = &resolved
	}
	rows, next, err := s.jobs.List(ctx, userID(c), f)
	if err != nil {
		writeError(c, err, http.StatusBadRequest, "bad_request")
		return
	}

	retryOf, projects, covers := map[uint64]string{}, map[uint64]string{}, map[string]persistence.Asset{}
	var retryIDs, projectIDs []uint64
	var coverIDs []string
	for _, j := range rows {
		if j.RetryOfJobID != nil {
			retryIDs = append(retryIDs, *j.RetryOfJobID)
		}
		if j.ProjectID != nil {
			projectIDs = append(projectIDs, *j.ProjectID)
		}
		if j.CoverAssetID != "" {
			coverIDs = append(coverIDs, j.CoverAssetID)
		}
	}
	if len(retryIDs) > 0 {
		var src []persistence.Job
		s.db.WithContext(ctx).Select("id", "biz_id").Where("id IN ?", retryIDs).Find(&src)
		for _, j := range src {
			retryOf[j.ID] = j.BizID
		}
	}
	if len(projectIDs) > 0 {
		var ps []persistence.Project
		s.db.WithContext(ctx).Select("id", "biz_id").Where("id IN ? AND deleted_at IS NULL", projectIDs).Find(&ps)
		for _, p := range ps {
			projects[p.ID] = p.BizID
		}
	}
	if len(coverIDs) > 0 {
		var as []persistence.Asset
		s.db.WithContext(ctx).Select("biz_id", "type", "public_url", "thumb_url").Where("biz_id IN ? AND deleted_at IS NULL", coverIDs).Find(&as)
		for _, a := range as {
			covers[a.BizID] = a
		}
	}

	credits, err := s.jobs.Credits(ctx, rows)
	if err != nil {
		writeError(c, err, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, j := range rows {
		row := gin.H{
			"credits": credits[j.ID],
			"biz_id":  j.BizID, "workflow_name": j.WorkflowName, "title": j.Title, "status": j.Status,
			"node_total": j.NodeTotal, "node_done": j.NodeDone, "node_failed": j.NodeFailed,
			"credit_estimated": j.CreditEstimated, "credit_held": j.CreditHeld, "credit_settled": j.CreditSettled,
			"error_code": j.ErrorCode, "error_msg": j.ErrorMsg,
			"created_at": j.CreatedAt, "started_at": j.StartedAt, "finished_at": j.FinishedAt,
			"retry_of_job_id": "", "project_id": "", "cover_asset_id": "", "cover_url": "", "cover_type": "",
		}
		if j.RetryOfJobID != nil {
			row["retry_of_job_id"] = retryOf[*j.RetryOfJobID]
		}
		if j.ProjectID != nil {
			row["project_id"] = projects[*j.ProjectID]
		}
		if a, ok := covers[j.CoverAssetID]; ok {
			row["cover_asset_id"], row["cover_url"], row["cover_type"] = a.BizID, thumbOrOriginal(a), a.Type
		}
		out = append(out, row)
	}
	resp := gin.H{"jobs": out}
	if next > 0 {
		resp["next_cursor"] = strconv.FormatUint(next, 10)
	}
	c.JSON(http.StatusOK, resp)
}

// handleJobsSummary is GET /api/v1/jobs/summary: job counts per bucket,
// optionally within one project.
func (s *Server) handleJobsSummary(c *gin.Context) {
	ctx := c.Request.Context()
	var projectID *uint64
	if pid := c.Query("project_id"); pid != "" {
		resolved, ok := s.resolveProjectID(ctx, userID(c), pid)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		projectID = &resolved
	}
	counts, err := s.jobs.Summary(ctx, userID(c), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "summarize jobs"))
		return
	}
	resp := gin.H{"statuses": counts.Statuses}
	for bucket, n := range counts.Buckets {
		resp[bucket] = n
	}
	c.JSON(http.StatusOK, resp)
}

// handleUpdateJob is PATCH /api/v1/jobs/{bizID}: rename a job.
func (s *Server) handleUpdateJob(c *gin.Context) {
	var req struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if err := s.jobs.Rename(c.Request.Context(), userID(c), c.Param("bizID"), req.Title); err != nil {
		writeError(c, err, http.StatusInternalServerError, "internal")
		return
	}
	c.Status(http.StatusNoContent)
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
		writeError(c, err, http.StatusUnprocessableEntity, "estimate_failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"credits_total": credits, "items": items})
}

// handlePreviewJob is POST /api/v1/jobs/preview: the prompt and references
// a single-call image request would send (characters and presets
// expanded), without submitting or reserving anything.
func (s *Server) handlePreviewJob(c *gin.Context) {
	var req createJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	p, err := s.jobs.PreviewRequest(c.Request.Context(), userID(c), req.WorkflowName, req.Spec)
	if err != nil {
		writeError(c, err, http.StatusUnprocessableEntity, "estimate_failed")
		return
	}
	c.JSON(http.StatusOK, p)
}

// handleCancelJob is POST /api/v1/jobs/{bizID}/cancel (F7.4).
// jobsvc.Service.Cancel's own doc covers why this doesn't touch credits
// directly.
func (s *Server) handleCancelJob(c *gin.Context) {
	if err := s.jobs.Cancel(c.Request.Context(), userID(c), c.Param("bizID")); err != nil {
		writeError(c, err, http.StatusUnprocessableEntity, "cancel_failed")
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
		writeError(c, err, http.StatusUnprocessableEntity, "delete_failed")
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
		writeError(c, err, http.StatusUnprocessableEntity, "retry_failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"biz_id":          job.BizID,
		"status":          job.Status,
		"workflow_run_id": job.WorkflowRunID,
	})
}

func (s *Server) handleGetJob(c *gin.Context) {
	ctx := c.Request.Context()
	job, run, err := s.jobs.Get(ctx, userID(c), c.Param("bizID"))
	if err != nil {
		writeError(c, err, http.StatusInternalServerError, "internal")
		return
	}
	credits, err := s.jobs.Credits(ctx, []persistence.Job{*job})
	if err != nil {
		writeError(c, err, http.StatusInternalServerError, "internal")
		return
	}
	var reviewDeadline *time.Time
	if run != nil {
		reviewDeadline = run.ReviewDeadline
	}
	nodes := make([]gin.H, 0)
	if run != nil {
		for _, n := range run.Nodes {
			nodes = append(nodes, gin.H{
				"name": n.Name, "phase": n.Phase, "status": n.Status, "executor": n.Executor,
				"outputs": n.Outputs, "error": n.ErrorMsg, "error_code": n.ErrorCode, "loop_index": n.LoopIndex,
				"attempt": n.Attempt, "queue_reason": n.QueueReason, "credit_cost": n.CreditCost,
				"started_at": n.StartedAt, "finished_at": n.FinishedAt, "display": n.Display,
			})
		}
	}
	retryOfBizID := ""
	if job.RetryOfJobID != nil {
		_ = s.db.WithContext(ctx).Model(&persistence.Job{}).Select("biz_id").Where("id = ?", *job.RetryOfJobID).Scan(&retryOfBizID).Error
	}
	projectBizID := ""
	if job.ProjectID != nil {
		_ = s.db.WithContext(ctx).Model(&persistence.Project{}).Select("biz_id").Where("id = ?", *job.ProjectID).Scan(&projectBizID).Error
	}
	c.JSON(http.StatusOK, gin.H{
		"biz_id": job.BizID, "workflow_name": job.WorkflowName, "title": job.Title, "status": job.Status,
		"workflow_run_id": job.WorkflowRunID, "retry_of_job_id": retryOfBizID, "project_id": projectBizID,
		"credit_estimated": job.CreditEstimated, "credit_held": job.CreditHeld, "credit_settled": job.CreditSettled,
		"credits": credits[job.ID], "review_deadline": reviewDeadline,
		"error_code": job.ErrorCode, "error_msg": job.ErrorMsg, "cover_asset_id": job.CoverAssetID,
		"created_at": job.CreatedAt, "started_at": job.StartedAt, "finished_at": job.FinishedAt,
		"nodes": nodes,
		// The raw spec lets a client rebuild the request (for example the
		// preview gate's shot duration).
		"spec": json.RawMessage(job.Spec),
	})
}
