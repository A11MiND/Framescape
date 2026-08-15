// Package projection turns every Aether state change into (1) an upsert of
// the job_nodes read-model (PRD §9.1: "job_nodes 是投影表不是真相来源...
// 允许短暂不一致") and (2) a Redis Pub/Sub publish that the SSE endpoint
// fans out to browsers.
//
// The data source is internal/infra/workflow/aether.ChangeCallback, not
// hook.Notifier — see docs/aether-validation-report.md §六 for why: hooks
// are opt-in per workflow/task declaration and don't fire for ordinary phase
// transitions, so they can't be the projection's source of truth.
//
// Uses database/sql directly (not GORM's Raw().Scan()), for two reasons:
// consistency with store_mysql.go's "state transitions use hand-written SQL"
// rule (PRD §15.1/R11), and because GORM's Scan() is built for struct/map
// destinations — scanning a bare *[]byte through it misinterprets the
// column as a single byte, not a byte slice (hit this directly the first
// time this file was tested against the real distributed W2 stack).
package projection

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BabySid/aether/store"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/executor/minimax"
	aetherengine "aigc-platform/internal/infra/workflow/aether"
	"aigc-platform/internal/pkg/logger"
	"aigc-platform/internal/pkg/metrics"
)

// ChannelForRun is the Redis Pub/Sub channel a given workflow run's events
// are published on. The SSE handler subscribes to this after resolving a
// job's biz_id to its workflow_run_id (internal/interfaces/http/sse.go).
func ChannelForRun(workflowRunID string) string { return "sse:workflow:" + workflowRunID }

// Event is the JSON envelope published on ChannelForRun and forwarded
// verbatim as an SSE `data:` payload (PRD §13.5).
type Event struct {
	Type          string         `json:"type"` // "node_update" | "job_update"
	Node          string         `json:"node,omitempty"`
	LoopIndex     int            `json:"loop_index,omitempty"`
	Phase         string         `json:"phase"`
	Outputs       map[string]any `json:"outputs,omitempty"`
	ErrorMsg      string         `json:"error_msg,omitempty"`
	WorkflowRunID string         `json:"workflow_run_id"`
}

type Projector struct {
	db      *sql.DB
	redis   *redis.Client
	credits *creditsvc.Service
	// minimax/reader are only used by F8.3's maybeReviewAsset — nil-safe
	// (that hook just no-ops) so existing callers/tests that don't care
	// about post-hoc review don't need to change.
	minimax *minimax.Client
	reader  assetstore.Reader
}

func New(db *sql.DB, redisClient *redis.Client, credits *creditsvc.Service, minimaxClient *minimax.Client, reader assetstore.Reader) *Projector {
	return &Projector{db: db, redis: redisClient, credits: credits, minimax: minimaxClient, reader: reader}
}

// Callback returns the aetherengine.ChangeCallback wired to this projector,
// for cmd/scheduler to pass into MySQLStore.SetChangeCallback.
func (p *Projector) Callback() aetherengine.ChangeCallback {
	return aetherengine.ChangeCallback{
		OnTaskRun:     p.onTaskRun,
		OnWorkflowRun: p.onWorkflowRun,
	}
}

func (p *Projector) onTaskRun(ctx context.Context, tr *store.TaskRun) {
	log := logger.From(ctx)

	jobID, err := p.jobIDForRun(ctx, tr.WorkflowRunID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Expected, benign race: jobsvc.Create() calls Engine.Submit()
		// before it has a workflow_run_id to insert the jobs row with, so
		// the earliest transitions (Created/Ready, sometimes Running) can
		// fire before that INSERT lands — typically a few milliseconds,
		// negligible next to any real generation taking seconds. job_nodes
		// tolerates this (PRD §9.1); later transitions self-heal it via
		// ON DUPLICATE KEY UPDATE.
		log.Debug("projection: jobs row not yet visible, skipping job_nodes upsert this time", zap.String("workflow_run_id", tr.WorkflowRunID))
	case err != nil:
		log.Error("projection: look up job id failed", zap.String("workflow_run_id", tr.WorkflowRunID), zap.Error(err))
	default:
		if err := p.upsertJobNode(ctx, jobID, tr); err != nil {
			log.Error("projection: upsert job_nodes failed", zap.String("task_run_id", tr.RunID), zap.Error(err))
		}
		p.maybeCommitCredits(ctx, jobID, tr)
		p.maybeRecordModeration(ctx, jobID, tr)
		p.maybeReviewAsset(ctx, jobID, tr)
	}

	phase := ""
	if tr.Status != nil {
		phase = string(*tr.Status)
	}
	if isTerminalPhase(phase) {
		executorType := ""
		if wf, err := p.workflowJSON(ctx, tr.WorkflowRunID); err == nil {
			executorType = aetherengine.ResolveExecutorType(wf, tr.TemplateName)
		}
		metrics.TaskRunsTotal.WithLabelValues(executorType, phase).Inc()
	}
	var outputs map[string]any
	errMsg := ""
	if tr.Outputs != nil {
		outputs = aetherengine.ParamsToMap(tr.Outputs.Parameters)
		errMsg = tr.Outputs.Message
	}
	p.publish(ctx, tr.WorkflowRunID, Event{
		Type: "node_update", Node: tr.TaskName, LoopIndex: aetherengine.LoopIndexFromScope(tr.Scope),
		Phase: phase, Outputs: outputs, ErrorMsg: errMsg, WorkflowRunID: tr.WorkflowRunID,
	})
}

func (p *Projector) onWorkflowRun(ctx context.Context, wr *store.WorkflowRun) {
	phase := ""
	if wr.Status != nil {
		phase = string(*wr.Status)
	}
	var outputs map[string]any
	if wr.Outputs != nil {
		outputs = aetherengine.ParamsToMap(wr.Outputs.Parameters)
	}
	if isTerminalPhase(phase) {
		p.maybeRefundCredits(ctx, wr.RunID)
		var workflowName string
		if err := p.db.QueryRowContext(ctx, `SELECT workflow_name FROM jobs WHERE workflow_run_id = ?`, wr.RunID).Scan(&workflowName); err == nil {
			metrics.JobsTotal.WithLabelValues(workflowName, phase).Inc()
		}
	}
	p.publish(ctx, wr.RunID, Event{
		Type: "job_update", Phase: phase, Outputs: outputs, WorkflowRunID: wr.RunID,
	})
}

func isTerminalPhase(phase string) bool {
	switch phase {
	case "Succeeded", "Failed", "Error", "Timeout", "Cancelled":
		return true
	default:
		return false
	}
}

// maybeCommitCredits implements §12.3's per-node settlement: a task that
// just succeeded with a "cost-yuan" output (every minimax.* executor emits
// this) converts to credits and moves out of held permanently. Every other
// transition (Running, Failed, a task with no cost-yuan output at all —
// local.*/human.gate) is a no-op here, not an error; most task transitions
// simply have nothing to commit.
func (p *Projector) maybeCommitCredits(ctx context.Context, jobID uint64, tr *store.TaskRun) {
	if tr.Status == nil || string(*tr.Status) != "Succeeded" || tr.Outputs == nil {
		return
	}
	outputs := aetherengine.ParamsToMap(tr.Outputs.Parameters)
	raw, ok := outputs["cost-yuan"]
	if !ok {
		return
	}
	costYuan, ok := raw.(float64)
	if !ok || costYuan <= 0 {
		return
	}

	log := logger.From(ctx)
	var userID uint64
	if err := p.db.QueryRowContext(ctx, `SELECT user_id FROM jobs WHERE id = ?`, jobID).Scan(&userID); err != nil {
		log.Error("projection: look up job user_id for credit commit failed", zap.Uint64("job_id", jobID), zap.Error(err))
		return
	}

	idemKey := "task_run:" + tr.RunID + ":commit"
	actual, err := p.credits.Commit(ctx, userID, idemKey, tr.RunID, costYuan)
	if err != nil {
		log.Error("projection: commit credits failed", zap.String("task_run_id", tr.RunID), zap.Error(err))
		return
	}
	// Use the amount Commit() actually deducted from held, not
	// creditsvc.CreditsFromYuan(costYuan) recomputed here — they can
	// legitimately differ (Commit's own doc explains why), and crediting
	// jobs.credit_settled with the wrong one is exactly what let
	// maybeRefundCredits's held-settled arithmetic drift from reality.
	if _, err := p.db.ExecContext(ctx, `UPDATE jobs SET credit_settled = credit_settled + ? WHERE id = ?`, actual, jobID); err != nil {
		log.Error("projection: update jobs.credit_settled failed", zap.Uint64("job_id", jobID), zap.Error(err))
	}
	// job_nodes.credit_cost existed on the table since the first migration
	// but nothing ever wrote it — §19.4.6's node detail drawer wants
	// per-node "消耗积分", not just the job-level total this same `actual`
	// already feeds into jobs.credit_settled above. Same non-transactional
	// best-effort write as that line (this whole function already treats a
	// failed secondary write as log-and-continue, never as a reason to
	// retry or fail the commit itself — the credits side of this already
	// succeeded by this point).
	if _, err := p.db.ExecContext(ctx, `UPDATE job_nodes SET credit_cost = credit_cost + ? WHERE task_run_id = ?`, actual, tr.RunID); err != nil {
		log.Error("projection: update job_nodes.credit_cost failed", zap.String("task_run_id", tr.RunID), zap.Error(err))
	}
	executorType := ""
	if wf, err := p.workflowJSON(ctx, tr.WorkflowRunID); err == nil {
		executorType = aetherengine.ResolveExecutorType(wf, tr.TemplateName)
	}
	metrics.CreditsCommittedYuan.WithLabelValues(executorType).Add(costYuan)
}

// maybeRecordModeration implements F8.4's audit trail: any task that fails
// with the "sensitive_content:" prefix (minimax.image/minimax.video's own
// §10.4 error classification, both already using this exact prefix) gets a
// moderation_records row. uk_task_run makes this idempotent the same way
// everything else here is — a duplicate OnTaskRun delivery for an
// already-recorded rejection is a harmless no-op insert.
func (p *Projector) maybeRecordModeration(ctx context.Context, jobID uint64, tr *store.TaskRun) {
	if tr.Status == nil || string(*tr.Status) != "Failed" || tr.Outputs == nil {
		return
	}
	const prefix = "sensitive_content:"
	msg := tr.Outputs.Message
	if len(msg) < len(prefix) || msg[:len(prefix)] != prefix {
		return
	}

	log := logger.From(ctx)
	var userID uint64
	if err := p.db.QueryRowContext(ctx, `SELECT user_id FROM jobs WHERE id = ?`, jobID).Scan(&userID); err != nil {
		log.Error("projection: look up job user_id for moderation record failed", zap.Uint64("job_id", jobID), zap.Error(err))
		return
	}
	executorType := ""
	if wf, err := p.workflowJSON(ctx, tr.WorkflowRunID); err == nil {
		executorType = aetherengine.ResolveExecutorType(wf, tr.TemplateName)
	}

	if _, err := p.db.ExecContext(ctx, `
		INSERT INTO moderation_records (task_run_id, job_id, user_id, executor_type, provider_message)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE task_run_id = task_run_id`,
		tr.RunID, jobID, userID, executorType, msg); err != nil {
		log.Error("projection: insert moderation_records failed", zap.String("task_run_id", tr.RunID), zap.Error(err))
	}
}

// maybeReviewAsset implements F8.3's post-generation review: F8.1/F8.2 only
// catch what MiniMax's own generation-time filter flags (calibrated for
// their content policy, not for downstream concerns like R14's "真人肖像、
// 二次元IP" — real portraits, anime/manga IP), so a successful minimax.image
// output still gets a second, independent look. Runs MiniMax-M3's vision
// input against the finished asset and logs to the same moderation_records
// table F8.4 uses (distinguished by the "post_review:" message prefix,
// following this codebase's existing convention of prefix-tagging
// synthesized messages rather than adding a new column for one more
// variant) — but only when flagged: an all-clear result logs nothing, same
// as F8.4 only logging actual rejections, not every successful generation.
// This is advisory only — never blocks or fails the job — so it runs
// detached from ctx in a goroutine: MiniMax's own generation-time check
// already gates whether the asset exists at all, and reviewing a
// still-cheap ~1-3s vision call in the hot state-transition path would add
// user-visible latency to every single successful generation for no
// product benefit.
func (p *Projector) maybeReviewAsset(ctx context.Context, jobID uint64, tr *store.TaskRun) {
	if p.minimax == nil || p.reader == nil || tr.Status == nil || string(*tr.Status) != "Succeeded" || tr.Outputs == nil {
		return
	}
	wf, err := p.workflowJSON(ctx, tr.WorkflowRunID)
	if err != nil {
		return
	}
	executorType := aetherengine.ResolveExecutorType(wf, tr.TemplateName)
	if executorType != "minimax.image" {
		// Scoped to images for now — video review would need frame
		// extraction or MiniMax-M3's video input, a further enhancement
		// beyond this P1 feature's scope.
		return
	}
	outputs := aetherengine.ParamsToMap(tr.Outputs.Parameters)
	assetID, _ := outputs["asset-id"].(string)
	if assetID == "" {
		return
	}

	log := logger.From(ctx)
	var userID uint64
	if err := p.db.QueryRowContext(ctx, `SELECT user_id FROM jobs WHERE id = ?`, jobID).Scan(&userID); err != nil {
		log.Error("projection: look up job user_id for asset review failed", zap.Uint64("job_id", jobID), zap.Error(err))
		return
	}

	go func() {
		reviewCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flagged, reason, err := p.reviewAsset(reviewCtx, assetID)
		if err != nil {
			log.Error("projection: post-hoc asset review failed", zap.String("asset_id", assetID), zap.Error(err))
			return
		}
		if !flagged {
			return
		}
		if _, err := p.db.ExecContext(reviewCtx, `
			INSERT INTO moderation_records (task_run_id, job_id, user_id, executor_type, provider_message)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE task_run_id = task_run_id`,
			tr.RunID, jobID, userID, executorType, "post_review: "+reason); err != nil {
			log.Error("projection: insert post-review moderation_records failed", zap.String("task_run_id", tr.RunID), zap.Error(err))
		}
	}()
}

// reviewAsset asks MiniMax-M3 (vision input) a strict yes/no content-safety
// question about one image asset. Returns flagged=true only when the
// model's first line is exactly "FLAG" — any other response (including a
// malformed one) is treated as a pass, since this check is advisory and a
// false negative here is far cheaper than a false positive silently
// flagging normal content.
func (p *Projector) reviewAsset(ctx context.Context, assetBizID string) (flagged bool, reason string, err error) {
	url, err := p.reader.PublicURL(ctx, assetBizID)
	if err != nil {
		return false, "", fmt.Errorf("look up asset %s: %w", assetBizID, err)
	}
	// Same constraint as everywhere else this codebase touches MiniMax: our
	// object storage isn't internet-reachable, so a raw URL back to our own
	// MinIO gets rejected ("disallowed url", confirmed by a real 400 before
	// this fix). Inlining the bytes as a data URI sidesteps it, same fix as
	// image.go's buildSubjectReferenceDataURI for F5.8.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", fmt.Errorf("build download request: %w", err)
	}
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return false, "", fmt.Errorf("download asset %s: %w", assetBizID, err)
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return false, "", fmt.Errorf("read asset %s: %w", assetBizID, err)
	}
	dataURI := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)

	resp, err := p.minimax.ChatCompletion(ctx, minimax.ChatCompletionRequest{
		Model: "MiniMax-M3",
		Messages: []minimax.ChatMessage{{
			Role: "user",
			Content: []map[string]any{
				{"type": "image_url", "image_url": map[string]string{"url": dataURI}},
				{"type": "text", "text": "You are a content-safety reviewer for an AI image generation product (R14: watch for real identifiable people's likenesses and well-known copyrighted characters, alongside standard NSFW/violence concerns). Look at the image. If it clearly contains any of these concerns, reply with exactly \"FLAG: <one short reason>\" as the first line. Otherwise reply with exactly \"OK\" and nothing else."},
			},
		}},
		Temperature:         0,
		MaxCompletionTokens: 60,
		Thinking:            &minimax.ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return false, "", err
	}
	if len(resp.Choices) == 0 {
		return false, "", nil
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if !strings.HasPrefix(content, "FLAG") {
		return false, "", nil
	}
	return true, strings.TrimSpace(strings.TrimPrefix(content, "FLAG:")), nil
}

// maybeRefundCredits implements §12.3's terminal-state settlement: whatever
// was held for this job beyond what actually got committed goes back to the
// user's spendable balance. Safe to call on every terminal transition this
// callback ever sees for a given run — idempotent via credit_ledger.uk_idem
// (the "job:{bizID}:refund" key), so a duplicate OnWorkflowRun delivery for
// an already-settled job is a harmless no-op, not a double refund.
func (p *Projector) maybeRefundCredits(ctx context.Context, workflowRunID string) {
	log := logger.From(ctx)
	var bizID string
	var userID uint64
	var held, settled int
	err := p.db.QueryRowContext(ctx, `SELECT biz_id, user_id, credit_held, credit_settled FROM jobs WHERE workflow_run_id = ?`, workflowRunID).
		Scan(&bizID, &userID, &held, &settled)
	if errors.Is(err, sql.ErrNoRows) {
		return // same benign race as onTaskRun's jobIDForRun — jobs row not visible yet
	}
	if err != nil {
		log.Error("projection: look up job for credit refund failed", zap.String("workflow_run_id", workflowRunID), zap.Error(err))
		return
	}
	refund := held - settled
	if refund <= 0 {
		return
	}
	if err := p.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, refund); err != nil {
		log.Error("projection: refund credits failed", zap.String("biz_id", bizID), zap.Error(err))
	}
}

func (p *Projector) publish(ctx context.Context, workflowRunID string, ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		logger.From(ctx).Error("projection: marshal event failed", zap.Error(err))
		return
	}
	if err := p.redis.Publish(ctx, ChannelForRun(workflowRunID), b).Err(); err != nil {
		logger.From(ctx).Warn("projection: redis publish failed", zap.Error(err))
	}
}

func (p *Projector) jobIDForRun(ctx context.Context, workflowRunID string) (uint64, error) {
	var jobID uint64
	err := p.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE workflow_run_id = ? LIMIT 1`, workflowRunID).Scan(&jobID)
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

func (p *Projector) upsertJobNode(ctx context.Context, jobID uint64, tr *store.TaskRun) error {
	status := ""
	if tr.Status != nil {
		status = string(*tr.Status)
	}
	var execCode *int8
	var assetIDsJSON []byte
	errMsg := ""
	if tr.Outputs != nil {
		c := int8(tr.Outputs.Code)
		execCode = &c
		errMsg = tr.Outputs.Message
		if raw, ok := aetherengine.ParamsToMap(tr.Outputs.Parameters)["asset-ids"]; ok {
			if b, err := json.Marshal(raw); err == nil {
				assetIDsJSON = b
			}
		}
	}

	// executor_type only resolves once the WorkflowRun row (with the raw
	// workflow JSON) exists; harmless to leave blank on the very first
	// Created write and fill it in on the next transition.
	executorType := ""
	if wf, err := p.workflowJSON(ctx, tr.WorkflowRunID); err == nil {
		executorType = aetherengine.ResolveExecutorType(wf, tr.TemplateName)
	}

	// started_at/finished_at existed on job_nodes since the first migration
	// but nothing ever wrote them either (same gap as credit_cost above) —
	// §19.4.6's node detail drawer wants "耗时". IF(?, NOW(3), NULL) sets
	// each only on the delivery where it first becomes true (Running /
	// any terminal phase); COALESCE on the UPDATE side means a later
	// duplicate or out-of-order delivery for the same task_run_id can never
	// clobber an already-recorded timestamp with a later one.
	isRunning := status == "Running"
	isTerminal := isTerminalPhase(status)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO job_nodes
			(job_id, task_run_id, node_name, loop_index, parent_scope, executor_type, phase, exec_code, asset_ids, error_msg, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, IF(?, NOW(3), NULL), IF(?, NOW(3), NULL))
		ON DUPLICATE KEY UPDATE
			phase = VALUES(phase), exec_code = VALUES(exec_code), asset_ids = VALUES(asset_ids),
			error_msg = VALUES(error_msg), executor_type = VALUES(executor_type),
			started_at = COALESCE(started_at, VALUES(started_at)),
			finished_at = COALESCE(finished_at, VALUES(finished_at))`,
		jobID, tr.RunID, tr.TaskName, aetherengine.LoopIndexFromScope(tr.Scope), tr.Scope, executorType,
		status, execCode, assetIDsJSON, errMsg, isRunning, isTerminal,
	)
	if err != nil {
		return fmt.Errorf("upsert job_nodes: %w", err)
	}
	return nil
}

func (p *Projector) workflowJSON(ctx context.Context, workflowRunID string) ([]byte, error) {
	var raw []byte
	err := p.db.QueryRowContext(ctx, `SELECT workflow_json FROM aether_workflow_runs WHERE run_id = ?`, workflowRunID).Scan(&raw)
	return raw, err
}
