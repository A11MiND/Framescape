package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"aigc-platform/internal/domain/workflow"
)

// ErrStale means a compare-and-set lost: the row changed since it was read.
var ErrStale = errors.New("orchestrator: stale node state")

// ErrJobNotFound is returned when a job row does not exist or is not v2.
var ErrJobNotFound = errors.New("orchestrator: job not found")

// Node is a job_nodes row as the orchestrator sees it.
type Node struct {
	ID              uint64
	JobID           uint64
	Name            string
	TaskRunID       string
	Executor        string
	Status          string
	Queue           string
	DepsPending     int
	Deps            []string
	Inputs          map[string]workflow.Input
	Outputs         map[string]any
	Check           string
	Attempt         int
	MaxAttempts     int
	Timeout         time.Duration
	DispatchSeq     int
	DispatchedAt    *time.Time
	NextRunAt       *time.Time
	QueueReason     string
	LeaseOwner      string
	LeaseUntil      *time.Time
	CancelRequested bool
	ProviderRef     json.RawMessage
	ReservedCredits int
	CreditCost      int
	CostYuan        float64
	ErrorCode       string
	ErrorMsg        string
	Display         map[string]any
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

const nodeColumns = `id, job_id, node_name, task_run_id, executor_type, status, queue, deps_pending, deps, inputs_json,
	outputs_json, check_rule, attempt, max_attempts, timeout_ms, dispatch_seq, dispatched_at, next_run_at, queue_reason,
	lease_owner, lease_until, cancel_requested, provider_ref, reserved_credits, credit_cost, cost_yuan, error_code,
	error_msg, display, started_at, finished_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(r rowScanner) (*Node, error) {
	var (
		n                             Node
		deps, inputs, outputs, disp   []byte
		providerRef                   []byte
		timeoutMS                     int64
		dispatchedAt, nextRun, leaseU sql.NullTime
		startedAt, finishedAt         sql.NullTime
		cancelReq                     bool
	)
	if err := r.Scan(&n.ID, &n.JobID, &n.Name, &n.TaskRunID, &n.Executor, &n.Status, &n.Queue, &n.DepsPending, &deps, &inputs,
		&outputs, &n.Check, &n.Attempt, &n.MaxAttempts, &timeoutMS, &n.DispatchSeq, &dispatchedAt, &nextRun, &n.QueueReason,
		&n.LeaseOwner, &leaseU, &cancelReq, &providerRef, &n.ReservedCredits, &n.CreditCost, &n.CostYuan, &n.ErrorCode,
		&n.ErrorMsg, &disp, &startedAt, &finishedAt); err != nil {
		return nil, err
	}
	n.Timeout = time.Duration(timeoutMS) * time.Millisecond
	n.CancelRequested = cancelReq
	n.DispatchedAt = nullTime(dispatchedAt)
	n.NextRunAt = nullTime(nextRun)
	n.LeaseUntil = nullTime(leaseU)
	n.StartedAt = nullTime(startedAt)
	n.FinishedAt = nullTime(finishedAt)
	if len(providerRef) > 0 && string(providerRef) != "null" {
		n.ProviderRef = providerRef
	}
	if len(deps) > 0 {
		if err := json.Unmarshal(deps, &n.Deps); err != nil {
			return nil, fmt.Errorf("decode deps of node %d: %w", n.ID, err)
		}
	}
	if len(inputs) > 0 {
		if err := json.Unmarshal(inputs, &n.Inputs); err != nil {
			return nil, fmt.Errorf("decode inputs of node %d: %w", n.ID, err)
		}
	}
	if len(outputs) > 0 {
		if err := json.Unmarshal(outputs, &n.Outputs); err != nil {
			return nil, fmt.Errorf("decode outputs of node %d: %w", n.ID, err)
		}
	}
	if len(disp) > 0 {
		_ = json.Unmarshal(disp, &n.Display)
	}
	return &n, nil
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadNode(ctx context.Context, q querier, nodeID uint64, forUpdate bool) (*Node, error) {
	query := `SELECT ` + nodeColumns + ` FROM job_nodes WHERE id = ?`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanNode(q.QueryRowContext(ctx, query, nodeID))
}

func loadJobNodes(ctx context.Context, q querier, jobID uint64, forUpdate bool) ([]*Node, error) {
	query := `SELECT ` + nodeColumns + ` FROM job_nodes WHERE job_id = ? ORDER BY id`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	rows, err := q.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("query job nodes: %w", err)
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// jobRow is the subset of jobs the orchestrator reads under lock.
type jobRow struct {
	JobRef
	Status   string
	Engine   string
	Deadline time.Duration
	GateTTL  time.Duration
	// ErrorCode/ErrorMsg carry an abort reason while the job is cancelling.
	ErrorCode string
	ErrorMsg  string
}

// planEnvelope is what jobs.plan_meta stores.
type planEnvelope struct {
	DeadlineS int64           `json:"deadline_s"`
	GateTTLS  int64           `json:"gate_ttl_s"`
	Meta      json.RawMessage `json:"meta,omitempty"`
}

func lockJob(ctx context.Context, tx *sql.Tx, jobID uint64) (*jobRow, error) {
	var (
		j    jobRow
		meta []byte
	)
	err := tx.QueryRowContext(ctx, `SELECT id, biz_id, user_id, status, engine, plan_meta, error_code, error_msg FROM jobs WHERE id = ? FOR UPDATE`, jobID).
		Scan(&j.ID, &j.BizID, &j.UserID, &j.Status, &j.Engine, &meta, &j.ErrorCode, &j.ErrorMsg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock job %d: %w", jobID, err)
	}
	if len(meta) > 0 {
		var env planEnvelope
		if json.Unmarshal(meta, &env) == nil {
			j.Deadline = time.Duration(env.DeadlineS) * time.Second
			j.GateTTL = time.Duration(env.GateTTLS) * time.Second
		}
	}
	return &j, nil
}

// PlanMeta returns the builder-owned metadata stored with a v2 job.
func (o *Orchestrator) PlanMeta(ctx context.Context, jobID uint64) (json.RawMessage, error) {
	var meta []byte
	if err := o.db.QueryRowContext(ctx, `SELECT plan_meta FROM jobs WHERE id = ?`, jobID).Scan(&meta); err != nil {
		return nil, err
	}
	if len(meta) == 0 {
		return nil, nil
	}
	var env planEnvelope
	if err := json.Unmarshal(meta, &env); err != nil {
		return nil, err
	}
	return env.Meta, nil
}

// Nodes returns all nodes of a job in creation order.
func (o *Orchestrator) Nodes(ctx context.Context, jobID uint64) ([]*Node, error) {
	return loadJobNodes(ctx, o.db, jobID, false)
}

func insertNode(ctx context.Context, tx *sql.Tx, jobID uint64, spec workflow.NodeSpec, depsPending int, status string, now time.Time, taskRunID string) (uint64, error) {
	deps := spec.Dependencies()
	depsJSON, _ := json.Marshal(deps)
	inputsJSON, err := json.Marshal(spec.Inputs)
	if err != nil {
		return 0, fmt.Errorf("encode inputs of %q: %w", spec.Name, err)
	}
	var display []byte
	if len(spec.Display) > 0 {
		display, _ = json.Marshal(spec.Display)
	}
	maxAttempts := spec.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var dispatchedAt, startedAt any
	if status == workflow.NodeReady {
		dispatchedAt = now
	}
	if status == workflow.NodeSuspended {
		startedAt = now
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO job_nodes
		(job_id, task_run_id, node_name, loop_index, parent_scope, executor_type, phase, status, queue, deps_pending, deps,
		 inputs_json, check_rule, max_attempts, timeout_ms, dispatched_at, reserved_credits, display, started_at, created_at)
		VALUES (?, ?, ?, -1, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, taskRunID, spec.Name, spec.Executor, workflow.LegacyPhase(status), status, QueueFor(spec.Executor), depsPending, depsJSON,
		inputsJSON, spec.Check, maxAttempts, spec.Timeout.Milliseconds(), dispatchedAt, spec.ReservedCredits, display, startedAt, now)
	if err != nil {
		return 0, fmt.Errorf("insert node %q: %w", spec.Name, err)
	}
	nodeID, _ := res.LastInsertId()
	return uint64(nodeID), nil
}

// setStatus is the single place node status changes are written.
func setStatus(ctx context.Context, tx *sql.Tx, n *Node, status string, extra string, args ...any) error {
	query := `UPDATE job_nodes SET status = ?, phase = ?, version = version + 1`
	if extra != "" {
		query += ", " + extra
	}
	query += ` WHERE id = ? AND status = ?`
	full := append([]any{status, workflow.LegacyPhase(status)}, args...)
	full = append(full, n.ID, n.Status)
	res, err := tx.ExecContext(ctx, query, full...)
	if err != nil {
		return fmt.Errorf("update node %d to %s: %w", n.ID, status, err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return ErrStale
	}
	n.Status = status
	return nil
}

// advance releases dependents of a node that just succeeded and returns the
// nodes that became ready or suspended.
func advance(ctx context.Context, tx *sql.Tx, nodes []*Node, done string, now time.Time) ([]*Node, error) {
	var released []*Node
	for _, n := range nodes {
		if n.Status != workflow.NodePending || !slices.Contains(n.Deps, done) {
			continue
		}
		n.DepsPending--
		switch {
		case n.DepsPending > 0:
			if _, err := tx.ExecContext(ctx, `UPDATE job_nodes SET deps_pending = ? WHERE id = ?`, n.DepsPending, n.ID); err != nil {
				return nil, fmt.Errorf("decrement deps of %q: %w", n.Name, err)
			}
		case n.Executor == workflow.GateExecutor:
			if err := setStatus(ctx, tx, n, workflow.NodeSuspended, "deps_pending = 0, started_at = ?", now); err != nil {
				return nil, err
			}
			released = append(released, n)
		default:
			if err := setStatus(ctx, tx, n, workflow.NodeReady, "deps_pending = 0, dispatched_at = ?, next_run_at = NULL", now); err != nil {
				return nil, err
			}
			released = append(released, n)
		}
	}
	return released, nil
}

// skipDownstream marks every pending node that can no longer run because
// `from` failed or was cancelled.
func skipDownstream(ctx context.Context, tx *sql.Tx, nodes []*Node, from string, now time.Time) error {
	blocked := map[string]bool{from: true}
	for changed := true; changed; {
		changed = false
		for _, n := range nodes {
			if n.Status != workflow.NodePending {
				continue
			}
			for _, d := range n.Deps {
				if blocked[d] {
					if err := setStatus(ctx, tx, n, workflow.NodeSkipped, "finished_at = ?", now); err != nil {
						return err
					}
					blocked[n.Name] = true
					changed = true
					break
				}
			}
		}
	}
	return nil
}

// jobOutcome derives the job status and summary fields from its nodes.
type jobOutcome struct {
	Status     string
	Total      int
	Done       int
	Failed     int
	ErrorCode  string
	ErrorMsg   string
	CoverAsset string
	Terminal   bool
}

func summarize(current string, nodes []*Node) jobOutcome {
	out := jobOutcome{Total: len(nodes)}
	var active, suspended, waitingToRun, started, cancelled int
	var anyAssets bool
	for _, n := range nodes {
		switch n.Status {
		case workflow.NodeRunning, workflow.NodeWaiting:
			active++
		case workflow.NodeSuspended:
			suspended++
		case workflow.NodeReady, workflow.NodePending:
			waitingToRun++
		case workflow.NodeSucceeded:
			out.Done++
			if a := firstAsset(n.Outputs); a != "" {
				anyAssets = true
				if isResult(n) || out.CoverAsset == "" {
					out.CoverAsset = a
				}
			}
		case workflow.NodeFailed:
			out.Failed++
			if a := firstAsset(n.Outputs); a != "" {
				anyAssets = true
				if out.CoverAsset == "" {
					out.CoverAsset = a
				}
			}
			if out.ErrorCode == "" && out.ErrorMsg == "" {
				out.ErrorCode, out.ErrorMsg = n.ErrorCode, n.ErrorMsg
			}
		case workflow.NodeCancelled:
			cancelled++
		}
		if n.StartedAt != nil && n.Executor != workflow.GateExecutor {
			started++
		}
	}
	switch {
	case current == workflow.JobCancelling && active == 0 && waitingToRun == 0 && suspended == 0:
		out.Status, out.Terminal = workflow.JobCancelled, true
	case current == workflow.JobCancelling:
		out.Status = workflow.JobCancelling
	case active > 0:
		out.Status = workflow.JobRunning
	case suspended > 0:
		out.Status = workflow.JobAwaitingReview
	case waitingToRun > 0 && started == 0:
		out.Status = workflow.JobQueued
	case waitingToRun > 0:
		out.Status = workflow.JobRunning
	case out.Failed > 0 && anyAssets:
		out.Status, out.Terminal = workflow.JobPartial, true
	case out.Failed > 0:
		out.Status, out.Terminal = workflow.JobFailed, true
	case cancelled > 0:
		out.Status, out.Terminal = workflow.JobCancelled, true
	default:
		out.Status, out.Terminal = workflow.JobSucceeded, true
	}
	return out
}

func isResult(n *Node) bool {
	v, _ := n.Display["result"].(bool)
	return v
}

func firstAsset(outputs map[string]any) string {
	if s, ok := outputs["asset-id"].(string); ok && s != "" {
		return s
	}
	if list, ok := outputs["asset-ids"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// finalizeJob recomputes the job row from its nodes; on the transition to a
// terminal status it releases the remaining reservation.
func (o *Orchestrator) finalizeJob(ctx context.Context, tx *sql.Tx, job *jobRow, nodes []*Node, buf *eventBuffer, now time.Time) (jobOutcome, error) {
	out := summarize(job.Status, nodes)
	if job.Status == workflow.JobCancelling && job.ErrorCode != "" {
		out.ErrorCode, out.ErrorMsg = job.ErrorCode, job.ErrorMsg
		if out.Terminal {
			out.Status = workflow.JobFailed
		}
	}
	sets := []string{"status = ?", "node_total = ?", "node_done = ?", "node_failed = ?", "error_code = ?", "error_msg = ?", "cover_asset_id = ?", "version = version + 1"}
	args := []any{out.Status, out.Total, out.Done, out.Failed, truncateRunes(out.ErrorCode, 64), truncateRunes(out.ErrorMsg, 512), out.CoverAsset}
	switch {
	case out.Terminal:
		sets = append(sets, "finished_at = ?", "deadline_at = NULL")
		args = append(args, now)
	case out.Status == workflow.JobAwaitingReview:
		sets = append(sets, "deadline_at = NULL")
	}
	args = append(args, job.ID)
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return out, fmt.Errorf("update job %d: %w", job.ID, err)
	}
	if out.Status != job.Status {
		if err := buf.add(ctx, tx, now, EventJobStatus, map[string]any{"status": out.Status, "node_done": out.Done, "node_total": out.Total}); err != nil {
			return out, err
		}
		if out.Status == workflow.JobAwaitingReview {
			if err := buf.add(ctx, tx, now, EventNeedsReview, map[string]any{}); err != nil {
				return out, err
			}
		}
	}
	if out.Terminal && !workflow.JobTerminal(job.Status) {
		if o.billing != nil {
			if released, err := o.billing.ReleaseTx(ctx, tx, job.JobRef); err != nil {
				return out, fmt.Errorf("release reservation: %w", err)
			} else if released > 0 {
				if err := buf.add(ctx, tx, now, EventCreditChange, map[string]any{"released": released}); err != nil {
					return out, err
				}
			}
		}
		if err := buf.add(ctx, tx, now, EventJobFinished, map[string]any{
			"status": out.Status, "error_code": out.ErrorCode, "cover_asset_id": out.CoverAsset,
		}); err != nil {
			return out, err
		}
		if o.hooks != nil {
			if err := o.hooks.JobFinishedTx(ctx, tx, job.JobRef, out.Status); err != nil {
				return out, err
			}
		}
	}
	job.Status = out.Status
	return out, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func withTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
