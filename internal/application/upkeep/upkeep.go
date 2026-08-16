// Package upkeep implements two of §11.4's Scheduler-process background
// duties: suspended-job timeout cleanup and daily credit reconciliation.
// (The other three — poll-fallback scan, orphan-task reconciliation against
// MiniMax's own task list, and provider_files expiry cleanup — are
// deliberately not built yet; see DEV_PLAN.md's W7 section for why each is
// deferred rather than half-implemented.)
//
// Both duties here run on a plain time.Ticker rather than true wall-clock
// cron scheduling (§11.4 says suspended cleanup is hourly and financial
// reconciliation is specifically 03:00) — a POC-scale simplification: what
// matters for correctness is that both run periodically and are idempotent
// against re-running, not that reconciliation fires at exactly 3am.
package upkeep

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/pkg/logger"
)

// SuspendedTimeout is §11.4's "Suspended 超 7 天未 Resume → 自动取消并退积分".
const SuspendedTimeout = 7 * 24 * time.Hour

const (
	suspendedCheckInterval      = 1 * time.Hour
	reconciliationCheckInterval = 6 * time.Hour
	// skipPreviewCheckInterval is much shorter than suspendedCheckInterval
	// on purpose — Spec.SkipPreview's whole point is generation that feels
	// automatic, not a decision sitting Suspended for up to an hour before
	// anyone/anything looks at it.
	skipPreviewCheckInterval = 15 * time.Second
)

type Runner struct {
	db   *sql.DB
	eng  workflow.Engine
	jobs *jobsvc.Service

	// suspendedTimeout is a field (not the SuspendedTimeout constant
	// directly) so tests/manual verification can inject a short threshold
	// without waiting 7 real days for a Suspended row to qualify.
	suspendedTimeout time.Duration
}

func New(db *sql.DB, eng workflow.Engine, jobs *jobsvc.Service) *Runner {
	return &Runner{db: db, eng: eng, jobs: jobs, suspendedTimeout: SuspendedTimeout}
}

// WithSuspendedTimeout overrides the default 7-day threshold — used for
// manual/automated verification so a Suspended row can qualify for cleanup
// without waiting a full week.
func (r *Runner) WithSuspendedTimeout(d time.Duration) *Runner {
	r.suspendedTimeout = d
	return r
}

// Start launches both duties as background goroutines. Returns immediately;
// both loops run until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	go r.loop(ctx, suspendedCheckInterval, r.cleanupSuspended)
	go r.loop(ctx, reconciliationCheckInterval, r.checkReconciliation)
	go r.loop(ctx, skipPreviewCheckInterval, r.autoResumeSkipPreview)
}

func (r *Runner) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fn(ctx) // run once immediately, don't wait a full interval for the first pass
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

// cleanupSuspended implements §11.4's "挂起超时清理": any workflow with a
// task that's been Suspended longer than suspendedTimeout gets cancelled.
// The credit refund isn't done here directly — Engine.Cancel transitions
// the workflow to Cancelled, which fires the exact same OnWorkflowRun path
// (internal/application/projection's maybeRefundCredits) every other
// terminal transition already goes through, so cancellation and refund stay
// on one code path instead of two.
func (r *Runner) cleanupSuspended(ctx context.Context) {
	log := logger.From(ctx)
	cutoff := time.Now().Add(-r.suspendedTimeout)

	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT workflow_run_id FROM aether_task_runs
		WHERE status = 'Suspended' AND updated_at < ?`, cutoff)
	if err != nil {
		log.Error("upkeep: query suspended task runs failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var runIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			log.Error("upkeep: scan suspended workflow_run_id failed", zap.Error(err))
			continue
		}
		runIDs = append(runIDs, id)
	}

	for _, runID := range runIDs {
		if err := r.eng.Cancel(ctx, workflow.RunID(runID)); err != nil {
			log.Error("upkeep: cancel timed-out suspended workflow failed", zap.String("workflow_run_id", runID), zap.Error(err))
			continue
		}
		log.Info("upkeep: cancelled workflow suspended past timeout", zap.String("workflow_run_id", runID), zap.Duration("timeout", r.suspendedTimeout))
	}
}

// autoResumeSkipPreview implements Spec.SkipPreview (jobsvc.go's own doc):
// a video.sequence job that opted out of the 768P preview gate still goes
// through the exact same gate/Suspend mechanics as every other one (its
// draft just already ran at 2K, per video_sequence.go's own doc) — this is
// what stands in for the human decision an ordinary PreviewGate submission
// makes, calling Resume with every field empty (no shots picked for redo or
// upgrade), which jobsvc.Service.Resume's own doc confirms buckets every
// shot into "keep" and hands that straight to concat.
func (r *Runner) autoResumeSkipPreview(ctx context.Context) {
	log := logger.From(ctx)

	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT j.biz_id, j.user_id FROM aether_task_runs t
		JOIN jobs j ON j.workflow_run_id = t.workflow_run_id
		WHERE t.task_name = 'gate' AND t.status = 'Suspended'
		  AND j.workflow_name = 'video.sequence'
		  AND JSON_EXTRACT(j.spec, '$.skip_preview') = true`)
	if err != nil {
		log.Error("upkeep: query skip-preview suspended gates failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type pending struct {
		bizID  string
		userID uint64
	}
	var jobs []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.bizID, &p.userID); err != nil {
			log.Error("upkeep: scan skip-preview job failed", zap.Error(err))
			continue
		}
		jobs = append(jobs, p)
	}

	for _, p := range jobs {
		if err := r.jobs.Resume(ctx, p.userID, p.bizID, jobsvc.ResumeVideoSequenceRequest{}); err != nil {
			// Not necessarily a real failure — a slower prior tick's Resume
			// call can still be landing in the engine when this one fires,
			// so "gate already resumed" is an expected, harmless race here,
			// same as cleanupSuspended's own error handling below: log and
			// let the next tick's query (which won't find this row anymore
			// once the resume actually lands) settle it.
			log.Warn("upkeep: auto-resume skip-preview gate failed", zap.String("biz_id", p.bizID), zap.Error(err))
			continue
		}
		log.Info("upkeep: auto-resumed skip-preview gate", zap.String("biz_id", p.bizID))
	}
}

// checkReconciliation runs §12.3's invariant check and logs (not silently
// swallows) any user whose balance+held has drifted from their ledger sum —
// this POC has no alerting pipeline, so "logged at Error level" is the
// deliverable, not a paging integration.
func (r *Runner) checkReconciliation(ctx context.Context) {
	log := logger.From(ctx)
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.user_id, a.balance, a.held,
		       (SELECT COALESCE(SUM(amount),0) FROM credit_ledger l WHERE l.user_id = a.user_id) AS ledger_sum
		FROM credit_accounts a
		WHERE a.balance + a.held <>
		      (SELECT COALESCE(SUM(amount),0) FROM credit_ledger l WHERE l.user_id = a.user_id)`)
	if err != nil {
		log.Error("upkeep: reconciliation query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	mismatches := 0
	for rows.Next() {
		var userID uint64
		var balance, held, ledgerSum int
		if err := rows.Scan(&userID, &balance, &held, &ledgerSum); err != nil {
			log.Error("upkeep: scan reconciliation row failed", zap.Error(err))
			continue
		}
		mismatches++
		log.Error("upkeep: credit reconciliation mismatch",
			zap.Uint64("user_id", userID), zap.Int("balance", balance), zap.Int("held", held), zap.Int("ledger_sum", ledgerSum))
	}
	if mismatches == 0 {
		log.Debug("upkeep: credit reconciliation passed, no mismatches")
	}
}
