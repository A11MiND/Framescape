package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"aigc-platform/internal/domain/workflow"
)

// SweepStats reports what one sweep repaired.
type SweepStats struct {
	Redispatched  int
	LeasesExpired int
	PollsResent   int
	EventsResent  int
	JobsAborted   int
	JobsRepaired  int
}

// Sweep repairs everything a crash or a lost queue message can leave
// behind. It is idempotent and safe to run concurrently in every worker:
// candidate rows are claimed with SKIP LOCKED and every change is a CAS.
func (o *Orchestrator) Sweep(ctx context.Context) (SweepStats, error) {
	var st SweepStats
	var err error
	if st.Redispatched, err = o.sweepStaleReady(ctx); err != nil {
		return st, err
	}
	if st.LeasesExpired, err = o.sweepExpiredLeases(ctx); err != nil {
		return st, err
	}
	if st.PollsResent, err = o.sweepOverduePolls(ctx); err != nil {
		return st, err
	}
	if st.EventsResent, err = o.sweepUnpublished(ctx); err != nil {
		return st, err
	}
	if st.JobsAborted, err = o.sweepDeadlines(ctx); err != nil {
		return st, err
	}
	if st.JobsRepaired, err = o.sweepStuckJobs(ctx); err != nil {
		return st, err
	}
	return st, nil
}

// sweepStaleReady re-sends ready nodes whose dispatch message may be lost.
// The TaskID is unchanged, so a message that still exists is not duplicated.
func (o *Orchestrator) sweepStaleReady(ctx context.Context) (int, error) {
	now := o.now()
	var tasks []Task
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, dispatch_seq, queue, timeout_ms FROM job_nodes
			WHERE status = ? AND (next_run_at IS NULL OR next_run_at <= ?) AND (dispatched_at IS NULL OR dispatched_at < ?)
			ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`,
			workflow.NodeReady, now, now.Add(-o.cfg.ReadyStaleAfter), o.cfg.SweepBatch)
		if err != nil {
			return err
		}
		var ids []any
		for rows.Next() {
			var t Task
			var timeoutMS int64
			if err := rows.Scan(&t.NodeID, &t.Seq, &t.Queue, &timeoutMS); err != nil {
				rows.Close()
				return err
			}
			t.Kind, t.Timeout = TaskRun, time.Duration(timeoutMS)*time.Millisecond
			tasks = append(tasks, t)
			ids = append(ids, t.NodeID)
		}
		rows.Close()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE job_nodes SET dispatched_at = ? WHERE id = ?`, now, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sweep stale ready: %w", err)
	}
	for _, t := range tasks {
		_ = o.enqueue(ctx, t)
	}
	return len(tasks), nil
}

// sweepExpiredLeases recovers nodes whose worker died mid-execution. A node
// with a remote task goes back to waiting (the task is re-attached, never
// re-submitted); otherwise the attempt is retried or failed.
func (o *Orchestrator) sweepExpiredLeases(ctx context.Context) (int, error) {
	now := o.now()
	rows, err := o.db.QueryContext(ctx, `SELECT id FROM job_nodes WHERE status = ? AND lease_until < ? ORDER BY id LIMIT ?`,
		workflow.NodeRunning, now, o.cfg.SweepBatch)
	if err != nil {
		return 0, fmt.Errorf("query expired leases: %w", err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	recovered := 0
	for _, id := range ids {
		n, err := loadNode(ctx, o.db, id, false)
		if err != nil || n.Status != workflow.NodeRunning || n.LeaseUntil == nil || !n.LeaseUntil.Before(now) {
			continue
		}
		owner := n.LeaseOwner
		// Take the lease over under our identity so finish/toWaiting's CAS
		// applies to exactly this expired attempt.
		res, err := o.db.ExecContext(ctx, `UPDATE job_nodes SET lease_owner = ?, lease_until = ? WHERE id = ? AND status = ? AND lease_owner = ? AND attempt = ?`,
			o.cfg.WorkerID, now.Add(o.cfg.Lease), n.ID, workflow.NodeRunning, owner, n.Attempt)
		if err != nil {
			return recovered, err
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			continue
		}
		n.LeaseOwner = o.cfg.WorkerID
		if len(n.ProviderRef) > 0 {
			var ref storedRef
			if json.Unmarshal(n.ProviderRef, &ref) == nil && ref.TaskID != "" {
				if err := o.toWaiting(ctx, n, ref, time.Second); err != nil {
					return recovered, err
				}
				recovered++
				continue
			}
		}
		if n.CancelRequested {
			err = o.finish(ctx, n, outcome{status: workflow.NodeCancelled, code: "cancelled", message: "cancelled"})
		} else {
			err = o.finish(ctx, n, o.retryOrFail(n, "worker_lost", "the worker running this step stopped responding", nil))
		}
		if err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

// sweepOverduePolls re-sends polls whose delayed message was lost.
func (o *Orchestrator) sweepOverduePolls(ctx context.Context) (int, error) {
	now := o.now()
	rows, err := o.db.QueryContext(ctx, `SELECT id, dispatch_seq, queue FROM job_nodes WHERE status = ? AND next_run_at < ? ORDER BY id LIMIT ?`,
		workflow.NodeWaiting, now.Add(-o.cfg.ReadyStaleAfter), o.cfg.SweepBatch)
	if err != nil {
		return 0, fmt.Errorf("query overdue polls: %w", err)
	}
	var tasks []Task
	for rows.Next() {
		t := Task{Kind: TaskPoll}
		if err := rows.Scan(&t.NodeID, &t.Seq, &t.Queue); err != nil {
			rows.Close()
			return 0, err
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	for _, t := range tasks {
		_ = o.enqueue(ctx, t)
	}
	return len(tasks), nil
}

// sweepUnpublished re-publishes events whose publish failed or was lost.
func (o *Orchestrator) sweepUnpublished(ctx context.Context) (int, error) {
	now := o.now()
	rows, err := o.db.QueryContext(ctx, `SELECT id, user_id, job_biz_id, type, payload, created_at FROM job_events
		WHERE published_at IS NULL AND created_at < ? ORDER BY id LIMIT ?`, now.Add(-5*time.Second), o.cfg.SweepBatch*5)
	if err != nil {
		return 0, fmt.Errorf("query unpublished events: %w", err)
	}
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.UserID, &e.JobBizID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, e)
	}
	rows.Close()
	o.publish(ctx, events)
	return len(events), nil
}

// sweepDeadlines aborts jobs that ran past their deadline and gates that
// waited past their TTL.
func (o *Orchestrator) sweepDeadlines(ctx context.Context) (int, error) {
	now := o.now()
	aborted := 0
	rows, err := o.db.QueryContext(ctx, `SELECT id FROM jobs WHERE engine = 'v2' AND status IN (?, ?) AND deadline_at < ? LIMIT ?`,
		workflow.JobQueued, workflow.JobRunning, now, o.cfg.SweepBatch)
	if err != nil {
		return 0, fmt.Errorf("query deadlines: %w", err)
	}
	var overdue []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		overdue = append(overdue, id)
	}
	rows.Close()
	for _, id := range overdue {
		if err := o.Abort(ctx, id, "deadline_exceeded", "the job ran longer than its time limit"); err == nil {
			aborted++
		}
	}

	rows, err = o.db.QueryContext(ctx, `SELECT n.job_id, j.plan_meta FROM job_nodes n JOIN jobs j ON j.id = n.job_id
		WHERE n.status = ? AND j.engine = 'v2' AND n.started_at < ? LIMIT ?`,
		workflow.NodeSuspended, now.Add(-o.minGateTTL()), o.cfg.SweepBatch)
	if err != nil {
		return aborted, fmt.Errorf("query expired gates: %w", err)
	}
	type gate struct {
		jobID uint64
		meta  []byte
	}
	var gates []gate
	for rows.Next() {
		var g gate
		if err := rows.Scan(&g.jobID, &g.meta); err != nil {
			rows.Close()
			return aborted, err
		}
		gates = append(gates, g)
	}
	rows.Close()
	for _, g := range gates {
		if !o.gateExpired(ctx, g.jobID, g.meta, now) {
			continue
		}
		if err := o.Abort(ctx, g.jobID, "gate_expired", "the preview was not confirmed in time"); err == nil {
			aborted++
		}
	}
	return aborted, nil
}

// minGateTTL is the shortest TTL any job can have; per-job TTLs are checked
// after the coarse query.
func (o *Orchestrator) minGateTTL() time.Duration {
	return time.Minute
}

func (o *Orchestrator) gateExpired(ctx context.Context, jobID uint64, meta []byte, now time.Time) bool {
	ttl := o.cfg.GateTTL
	var env planEnvelope
	if json.Unmarshal(meta, &env) == nil && env.GateTTLS > 0 {
		ttl = time.Duration(env.GateTTLS) * time.Second
	}
	var since sql.NullTime
	if err := o.db.QueryRowContext(ctx, `SELECT MIN(started_at) FROM job_nodes WHERE job_id = ? AND status = ?`, jobID, workflow.NodeSuspended).Scan(&since); err != nil || !since.Valid {
		return false
	}
	return since.Time.Add(ttl).Before(now)
}

// sweepStuckJobs finalizes jobs whose nodes are all terminal but whose row
// was not updated, a state only an interrupted transaction outside the
// orchestrator could produce.
func (o *Orchestrator) sweepStuckJobs(ctx context.Context) (int, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT j.id FROM jobs j WHERE j.engine = 'v2'
		AND j.status NOT IN (?, ?, ?, ?)
		AND NOT EXISTS (SELECT 1 FROM job_nodes n WHERE n.job_id = j.id AND n.status NOT IN (?, ?, ?, ?))
		AND EXISTS (SELECT 1 FROM job_nodes n WHERE n.job_id = j.id)
		LIMIT ?`,
		workflow.JobSucceeded, workflow.JobPartial, workflow.JobFailed, workflow.JobCancelled,
		workflow.NodeSucceeded, workflow.NodeFailed, workflow.NodeCancelled, workflow.NodeSkipped, o.cfg.SweepBatch)
	if err != nil {
		return 0, fmt.Errorf("query stuck jobs: %w", err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	repaired := 0
	for _, id := range ids {
		p := &Pending{}
		err := withTx(ctx, o.db, func(tx *sql.Tx) error {
			job, err := lockJob(ctx, tx, id)
			if err != nil {
				return err
			}
			nodes, err := loadJobNodes(ctx, tx, id, true)
			if err != nil {
				return err
			}
			buf := &eventBuffer{job: job.JobRef}
			if _, err := o.finalizeJob(ctx, tx, job, nodes, buf, o.now()); err != nil {
				return err
			}
			p.events = buf.events
			return nil
		})
		if err == nil {
			o.Flush(ctx, p)
			repaired++
		}
	}
	return repaired, nil
}

// PurgeEvents deletes delivered events older than the retention window.
func (o *Orchestrator) PurgeEvents(ctx context.Context) (int64, error) {
	res, err := o.db.ExecContext(ctx, `DELETE FROM job_events WHERE created_at < ? LIMIT 10000`, o.now().Add(-o.cfg.EventRetention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Nudge schedules an immediate poll of the node waiting on a provider task,
// used when the provider's completion callback arrives.
func (o *Orchestrator) Nudge(ctx context.Context, providerTaskID string) error {
	if providerTaskID == "" {
		return nil
	}
	var t Task
	err := o.db.QueryRowContext(ctx, `SELECT id, dispatch_seq, queue FROM job_nodes WHERE provider_task_id = ? AND status = ? LIMIT 1`,
		providerTaskID, workflow.NodeWaiting).Scan(&t.NodeID, &t.Seq, &t.Queue)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	t.Kind, t.Nudge = TaskPoll, true
	return o.enqueue(ctx, t)
}
