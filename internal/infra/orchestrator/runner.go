package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
)

// runnerState tracks executions in this process so cancel signals and
// shutdown can reach them.
type runnerState struct {
	mu           sync.Mutex
	active       map[uint64]context.CancelCauseFunc
	shuttingDown atomic.Bool
}

func newRunnerState() *runnerState {
	return &runnerState{active: map[uint64]context.CancelCauseFunc{}}
}

var (
	errUserCancel = errors.New("cancelled by request")
	errLeaseLost  = errors.New("lease lost")
	errShutdown   = errors.New("worker shutting down")
	errSkip       = errors.New("skip")
)

// CancelLocal interrupts executions of the given nodes running here.
func (o *Orchestrator) CancelLocal(nodeIDs []uint64) {
	o.runner.mu.Lock()
	defer o.runner.mu.Unlock()
	for _, id := range nodeIDs {
		if cancel, ok := o.runner.active[id]; ok {
			cancel(errUserCancel)
		}
	}
}

// Shutdown interrupts local executions; their nodes are handed back
// without consuming an attempt.
func (o *Orchestrator) Shutdown() {
	o.runner.shuttingDown.Store(true)
	o.runner.mu.Lock()
	defer o.runner.mu.Unlock()
	for _, cancel := range o.runner.active {
		cancel(errShutdown)
	}
}

func (o *Orchestrator) track(nodeID uint64, cancel context.CancelCauseFunc) func() {
	o.runner.mu.Lock()
	o.runner.active[nodeID] = cancel
	o.runner.mu.Unlock()
	return func() {
		o.runner.mu.Lock()
		delete(o.runner.active, nodeID)
		o.runner.mu.Unlock()
	}
}

// storedRef is what job_nodes.provider_ref holds.
type storedRef struct {
	executor.ProviderRef
	Polls      int `json:"polls,omitempty"`
	PollErrors int `json:"poll_errors,omitempty"`
}

// RunNode executes a ready node. Returning an error asks the queue to
// redeliver; that only happens for infrastructure failures before the node
// was claimed.
func (o *Orchestrator) RunNode(ctx context.Context, nodeID uint64, seq int) error {
	n, err := loadNode(ctx, o.db, nodeID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.Status != workflow.NodeReady || n.DispatchSeq != seq {
		return nil
	}
	if o.runner.shuttingDown.Load() {
		return fmt.Errorf("worker shutting down")
	}
	now := o.now()
	if n.NextRunAt != nil && n.NextRunAt.After(now.Add(time.Second)) {
		return o.enqueue(ctx, Task{Kind: TaskRun, NodeID: n.ID, Seq: n.DispatchSeq, Queue: n.Queue, ProcessAt: *n.NextRunAt, Timeout: n.Timeout})
	}
	if reason, err := o.admit(ctx, n); err != nil {
		return err
	} else if reason != "" {
		return o.deferNode(ctx, n, reason)
	}
	if ok, err := o.claim(ctx, n, workflow.NodeReady); err != nil || !ok {
		return err
	}

	plugin, found := o.plugins.Get(n.Executor)
	if !found {
		return o.finish(ctx, n, outcome{status: workflow.NodeFailed, code: "executor_unavailable", message: "no executor registered for " + n.Executor})
	}
	inputs, err := resolveInputs(ctx, o.db, n)
	if err != nil {
		return o.finish(ctx, n, outcome{status: workflow.NodeFailed, code: "input_unresolved", message: err.Error()})
	}
	req := o.request(n, inputs)

	execCtx, stop := o.execContext(n)
	defer stop()

	if async, ok := plugin.(executor.AsyncPlugin); ok {
		ref, out, err := async.Submit(execCtx, req)
		if out == nil && err == nil {
			if ref.SubmittedAt.IsZero() {
				ref.SubmittedAt = o.now()
			}
			return o.toWaiting(ctx, n, storedRef{ProviderRef: ref}, o.cfg.PollInterval)
		}
		return o.settle(ctx, execCtx, n, out, err, false)
	}
	out, err := plugin.Execute(execCtx, req)
	return o.settle(ctx, execCtx, n, out, err, false)
}

// PollNode checks the remote task of a waiting node.
func (o *Orchestrator) PollNode(ctx context.Context, nodeID uint64, seq int) error {
	n, err := loadNode(ctx, o.db, nodeID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.Status != workflow.NodeWaiting || n.DispatchSeq != seq {
		return nil
	}
	if o.runner.shuttingDown.Load() {
		return fmt.Errorf("worker shutting down")
	}
	plugin, found := o.plugins.Get(n.Executor)
	async, isAsync := plugin.(executor.AsyncPlugin)
	if !found || !isAsync {
		return nil
	}
	if ok, err := o.claim(ctx, n, workflow.NodeWaiting); err != nil || !ok {
		return err
	}
	var ref storedRef
	if err := json.Unmarshal(n.ProviderRef, &ref); err != nil {
		return o.finish(ctx, n, outcome{status: workflow.NodeFailed, code: "provider_ref_invalid", message: err.Error()})
	}
	if n.CancelRequested {
		if c, ok := plugin.(executor.Canceler); ok {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = c.Cancel(cctx, ref.ProviderRef)
			cancel()
		}
		return o.finish(ctx, n, outcome{status: workflow.NodeCancelled, code: "cancelled", message: "cancelled"})
	}
	if n.Timeout > 0 && !ref.SubmittedAt.IsZero() && o.now().After(ref.SubmittedAt.Add(n.Timeout)) {
		return o.finish(ctx, n, o.retryOrFail(n, "timeout", "remote task exceeded its deadline", nil))
	}
	inputs, err := resolveInputs(ctx, o.db, n)
	if err != nil {
		return o.finish(ctx, n, outcome{status: workflow.NodeFailed, code: "input_unresolved", message: err.Error()})
	}
	pollCtx, stop := o.execContext(&Node{ID: n.ID, Timeout: 5 * time.Minute})
	defer stop()
	res, err := async.Poll(pollCtx, o.request(n, inputs), ref.ProviderRef)
	if cause := context.Cause(pollCtx); cause == errShutdown || cause == errLeaseLost || cause == errUserCancel {
		return o.toWaiting(ctx, n, ref, time.Second)
	}
	if err != nil {
		ref.PollErrors++
		if ref.PollErrors >= o.cfg.MaxPollErrors {
			return o.finish(ctx, n, o.retryOrFail(n, "poll_failed", err.Error(), nil))
		}
		return o.toWaiting(ctx, n, ref, o.pollDelay(ref, 0))
	}
	if !res.Done {
		ref.PollErrors = 0
		ref.Polls++
		return o.toWaiting(ctx, n, ref, o.pollDelay(ref, res.After))
	}
	return o.settle(ctx, pollCtx, n, res.Outputs, nil, true)
}

func (o *Orchestrator) pollDelay(ref storedRef, suggested time.Duration) time.Duration {
	if suggested > 0 {
		return suggested
	}
	d := o.cfg.PollInterval * time.Duration(1+ref.Polls/6)
	if d > o.cfg.MaxPollInterval {
		d = o.cfg.MaxPollInterval
	}
	return d
}

func (o *Orchestrator) request(n *Node, inputs *model.Inputs) *executor.ExecuteRequest {
	return &executor.ExecuteRequest{
		TaskRunID: n.TaskRunID, WorkflowRunID: fmt.Sprintf("job-%d", n.JobID), TaskName: n.Name,
		TemplateName: n.Executor, Inputs: inputs, Timeout: n.Timeout.String(), RetryCount: n.Attempt - 1,
	}
}

// execContext bounds an execution by the node timeout, renews the lease and
// reacts to cancellation.
func (o *Orchestrator) execContext(n *Node) (context.Context, func()) {
	timeout := n.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	base, cancel := context.WithCancelCause(executor.WithAttribution(context.Background(), executor.Attribution{
		UserID: n.UserID, JobID: n.JobID, NodeID: n.ID, Attempt: n.Attempt,
	}))
	ctx, cancelTimeout := context.WithTimeout(base, timeout)
	untrack := o.track(n.ID, cancel)
	done := make(chan struct{})
	go o.heartbeat(ctx, n.ID, cancel, done)
	return ctx, func() {
		close(done)
		untrack()
		cancelTimeout()
		cancel(nil)
	}
}

func (o *Orchestrator) heartbeat(ctx context.Context, nodeID uint64, cancel context.CancelCauseFunc, done <-chan struct{}) {
	ticker := time.NewTicker(o.cfg.Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
			res, err := o.db.ExecContext(hctx, `UPDATE job_nodes SET lease_until = ? WHERE id = ? AND status = ? AND lease_owner = ?`,
				o.now().Add(o.cfg.Lease), nodeID, workflow.NodeRunning, o.cfg.WorkerID)
			var cancelRequested bool
			if err == nil {
				_ = o.db.QueryRowContext(hctx, `SELECT cancel_requested FROM job_nodes WHERE id = ?`, nodeID).Scan(&cancelRequested)
			}
			hcancel()
			if err != nil {
				continue
			}
			if affected, _ := res.RowsAffected(); affected == 0 {
				cancel(errLeaseLost)
				return
			}
			if cancelRequested {
				cancel(errUserCancel)
				return
			}
		}
	}
}

// claim moves a node to running under this worker's lease.
func (o *Orchestrator) claim(ctx context.Context, n *Node, from string) (bool, error) {
	var events []Event
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, n.JobID)
		if err != nil {
			return err
		}
		if workflow.JobTerminal(job.Status) {
			return errSkip
		}
		now := o.now()
		var res sql.Result
		if from == workflow.NodeReady {
			res, err = tx.ExecContext(ctx, `UPDATE job_nodes SET status = ?, phase = ?, attempt = attempt + 1, lease_owner = ?, lease_until = ?,
				started_at = COALESCE(started_at, ?), queue_reason = '', next_run_at = NULL, version = version + 1
				WHERE id = ? AND status = ? AND dispatch_seq = ?`,
				workflow.NodeRunning, workflow.LegacyPhase(workflow.NodeRunning), o.cfg.WorkerID, now.Add(o.cfg.Lease), now, n.ID, workflow.NodeReady, n.DispatchSeq)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE job_nodes SET status = ?, lease_owner = ?, lease_until = ?, version = version + 1
				WHERE id = ? AND status = ? AND dispatch_seq = ?`,
				workflow.NodeRunning, o.cfg.WorkerID, now.Add(o.cfg.Lease), n.ID, workflow.NodeWaiting, n.DispatchSeq)
		}
		if err != nil {
			return fmt.Errorf("claim node %d: %w", n.ID, err)
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			return errSkip
		}
		if from == workflow.NodeReady {
			n.Attempt++
			if n.StartedAt == nil {
				n.StartedAt = &now
			}
		}
		n.Status, n.LeaseOwner, n.UserID = workflow.NodeRunning, o.cfg.WorkerID, job.UserID
		buf := &eventBuffer{job: job.JobRef}
		if job.Status == workflow.JobQueued {
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, started_at = COALESCE(started_at, ?) WHERE id = ?`, workflow.JobRunning, now, job.ID); err != nil {
				return err
			}
			if err := buf.add(ctx, tx, now, EventJobStatus, map[string]any{"status": workflow.JobRunning}); err != nil {
				return err
			}
		}
		if from == workflow.NodeReady {
			if err := buf.add(ctx, tx, now, EventNodeStatus, map[string]any{"node": n.Name, "status": workflow.NodeRunning, "attempt": n.Attempt}); err != nil {
				return err
			}
		}
		events = buf.events
		return nil
	})
	if errors.Is(err, errSkip) || errors.Is(err, ErrJobNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	o.publish(ctx, events)
	return true, nil
}

// outcome is the orchestrator's reading of one execution.
type outcome struct {
	status   string // succeeded | failed | cancelled | ready (retry)
	code     string
	message  string
	outputs  map[string]any
	cost     float64
	retryIn  time.Duration
	reason   string // queue_reason when status is ready
	refunded bool   // retry does not consume the attempt
}

// settle interprets an execution result, including why its context ended.
// A finished poll is always recorded: re-running it would start a second
// paid remote task.
func (o *Orchestrator) settle(ctx, execCtx context.Context, n *Node, out *model.ExecOutputs, err error, polled bool) error {
	cause := context.Cause(execCtx)
	if polled {
		cause = nil
	}
	outputs := map[string]any{}
	if out != nil {
		outputs = model.ParamsToMap(out.Parameters)
	}
	cost, _ := outputs["cost-yuan"].(float64)
	switch {
	case cause == errLeaseLost:
		return nil
	case cause == errShutdown:
		return o.finish(ctx, n, outcome{status: workflow.NodeReady, retryIn: time.Second, reason: "requeued", refunded: true, outputs: outputs, cost: cost})
	case cause == errUserCancel:
		return o.finish(ctx, n, outcome{status: workflow.NodeCancelled, code: "cancelled", message: "cancelled", outputs: outputs, cost: cost})
	}
	if nc, ok := executor.AsNoCapacity(err); ok {
		delay := nc.RetryAfter
		if delay <= 0 {
			delay = 5 * time.Second
		}
		return o.finish(ctx, n, outcome{status: workflow.NodeReady, retryIn: delay, reason: nc.Reason, refunded: true})
	}
	if err != nil {
		code := "executor_error"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		oc := o.retryOrFail(n, code, err.Error(), outputs)
		oc.cost = cost
		return o.finish(ctx, n, oc)
	}
	if out == nil {
		return o.finish(ctx, n, o.retryOrFail(n, "executor_error", "executor returned no result", outputs))
	}
	oc := outcome{outputs: outputs, cost: cost, message: out.Message}
	switch out.Code {
	case model.ExecCodeSucceeded:
		if n.Check == workflow.CheckAllRequested && !allRequested(outputs) {
			oc.status, oc.code = workflow.NodeFailed, "incomplete_output"
			if oc.message == "" {
				oc.message = "fewer outputs than requested"
			}
		} else {
			oc.status = workflow.NodeSucceeded
		}
	case model.ExecCodeFailed:
		oc.status, oc.code = workflow.NodeFailed, failureCode(out.Message)
	case model.ExecCodeError, model.ExecCodeTimeout:
		code := "executor_error"
		if out.Code == model.ExecCodeTimeout {
			code = "timeout"
		}
		r := o.retryOrFail(n, code, out.Message, outputs)
		r.cost = cost
		oc = r
	default:
		oc.status, oc.code = workflow.NodeFailed, "unexpected_result"
	}
	return o.finish(ctx, n, oc)
}

func (o *Orchestrator) retryOrFail(n *Node, code, message string, outputs map[string]any) outcome {
	if n.Attempt < n.MaxAttempts {
		backoff := o.cfg.RetryBase * time.Duration(1<<min(n.Attempt-1, 6))
		backoff += time.Duration(rand.Int64N(int64(backoff/2) + 1))
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		return outcome{status: workflow.NodeReady, code: code, message: message, outputs: outputs, retryIn: backoff, reason: "backoff"}
	}
	return outcome{status: workflow.NodeFailed, code: code, message: message, outputs: outputs}
}

func failureCode(message string) string {
	if strings.HasPrefix(message, "sensitive_content:") {
		return "moderation"
	}
	return "provider_rejected"
}

func allRequested(outputs map[string]any) bool {
	got, ok1 := toFloat(outputs["success-count"])
	want, ok2 := toFloat(outputs["requested-n"])
	if !ok1 || !ok2 {
		return true
	}
	return got >= want
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		var f float64
		_, err := fmt.Sscan(x, &f)
		return f, err == nil
	}
	return 0, false
}

// toWaiting hands a node back to the provider: no worker holds it until the
// next poll.
func (o *Orchestrator) toWaiting(ctx context.Context, n *Node, ref storedRef, delay time.Duration) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	now := o.now()
	next := now.Add(delay)
	var events []Event
	err = withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, n.JobID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE job_nodes SET status = ?, provider_ref = ?, provider_task_id = ?, next_run_at = ?,
			dispatch_seq = dispatch_seq + 1, dispatched_at = ?, lease_owner = '', lease_until = NULL, version = version + 1
			WHERE id = ? AND status = ? AND lease_owner = ?`,
			workflow.NodeWaiting, raw, ref.TaskID, next, now, n.ID, workflow.NodeRunning, o.cfg.WorkerID)
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			return errSkip
		}
		if len(n.ProviderRef) == 0 {
			buf := &eventBuffer{job: job.JobRef}
			if err := buf.add(ctx, tx, now, EventNodeStatus, map[string]any{"node": n.Name, "status": workflow.NodeWaiting}); err != nil {
				return err
			}
			events = buf.events
		}
		return nil
	})
	if errors.Is(err, errSkip) {
		return nil
	}
	if err != nil {
		return err
	}
	o.publish(ctx, events)
	return o.enqueue(ctx, Task{Kind: TaskPoll, NodeID: n.ID, Seq: n.DispatchSeq + 1, Queue: n.Queue, ProcessAt: next})
}

// finish records an execution outcome and advances the job, all in one
// transaction, then dispatches whatever became runnable.
func (o *Orchestrator) finish(ctx context.Context, n *Node, oc outcome) error {
	p := &Pending{}
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, n.JobID)
		if err != nil {
			return err
		}
		nodes, err := loadJobNodes(ctx, tx, job.ID, true)
		if err != nil {
			return err
		}
		var cur *Node
		for _, x := range nodes {
			if x.ID == n.ID {
				cur = x
			}
		}
		if cur == nil || cur.Status != workflow.NodeRunning || cur.Attempt != n.Attempt || cur.LeaseOwner != o.cfg.WorkerID {
			return errSkip
		}
		now := o.now()
		buf := &eventBuffer{job: job.JobRef}

		credits := 0
		if oc.cost > 0 && o.billing != nil {
			if credits, err = o.billing.CommitTx(ctx, tx, job.JobRef, cur.Name, cur.Attempt, oc.cost); err != nil {
				return fmt.Errorf("settle node %q: %w", cur.Name, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET credit_settled = credit_settled + ? WHERE id = ?`, credits, job.ID); err != nil {
				return err
			}
			if credits > 0 {
				if err := buf.add(ctx, tx, now, EventCreditChange, map[string]any{"charged": credits, "node": cur.Name}); err != nil {
					return err
				}
			}
		}
		if oc.cost > 0 {
			if err := attributeCost(ctx, tx, job, cur, oc.cost, now); err != nil {
				return err
			}
		}
		outputsJSON, err := json.Marshal(oc.outputs)
		if err != nil {
			return fmt.Errorf("encode outputs: %w", err)
		}
		assetIDs := assetList(oc.outputs)
		cur.Outputs, cur.ErrorCode, cur.ErrorMsg = oc.outputs, oc.code, oc.message
		common := "outputs_json = ?, asset_ids = ?, cost_yuan = cost_yuan + ?, credit_cost = credit_cost + ?, error_code = ?, error_msg = ?, lease_owner = '', lease_until = NULL"
		args := []any{outputsJSON, assetIDs, oc.cost, credits, truncateRunes(oc.code, 64), truncateRunes(oc.message, 512)}

		switch oc.status {
		case workflow.NodeSucceeded:
			if err := setStatus(ctx, tx, cur, workflow.NodeSucceeded, common+", finished_at = ?", append(args, now)...); err != nil {
				return err
			}
			released, err := advance(ctx, tx, nodes, cur.Name, now)
			if err != nil {
				return err
			}
			for _, r := range released {
				if r.Status == workflow.NodeReady {
					p.addReady(r)
				}
			}
		case workflow.NodeFailed, workflow.NodeCancelled:
			if err := setStatus(ctx, tx, cur, oc.status, common+", finished_at = ?", append(args, now)...); err != nil {
				return err
			}
			if err := skipDownstream(ctx, tx, nodes, cur.Name, now); err != nil {
				return err
			}
		case workflow.NodeReady:
			next := now.Add(oc.retryIn)
			attemptAdj := 0
			if oc.refunded {
				attemptAdj = 1
			}
			if err := setStatus(ctx, tx, cur, workflow.NodeReady, common+`, attempt = attempt - ?, next_run_at = ?, dispatch_seq = dispatch_seq + 1,
				dispatched_at = ?, queue_reason = ?, provider_ref = NULL, provider_task_id = ''`,
				append(args, attemptAdj, next, now, oc.reason)...); err != nil {
				return err
			}
			p.tasks = append(p.tasks, Task{Kind: TaskRun, NodeID: cur.ID, Seq: cur.DispatchSeq + 1, Queue: cur.Queue, ProcessAt: next, Timeout: cur.Timeout})
		default:
			return fmt.Errorf("unknown outcome %q", oc.status)
		}

		event := map[string]any{"node": cur.Name, "status": oc.status, "attempt": cur.Attempt}
		if oc.code != "" {
			event["error_code"] = oc.code
		}
		if oc.reason != "" {
			event["queue_reason"] = oc.reason
		}
		if err := buf.add(ctx, tx, now, EventNodeStatus, event); err != nil {
			return err
		}
		if o.hooks != nil && workflow.NodeTerminal(oc.status) {
			follow, err := o.hooks.NodeFinishedTx(ctx, tx, job.JobRef, FinishedNode{
				ID: cur.ID, Name: cur.Name, TaskRunID: cur.TaskRunID, Executor: cur.Executor, Status: oc.status,
				Message: oc.message, Outputs: oc.outputs, Attempt: cur.Attempt,
			})
			if err != nil {
				return err
			}
			p.tasks = append(p.tasks, follow...)
		}
		if _, err := o.finalizeJob(ctx, tx, job, nodes, buf, now); err != nil {
			return err
		}
		p.events = buf.events
		return nil
	})
	if errors.Is(err, errSkip) || errors.Is(err, ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	o.Flush(ctx, p)
	return nil
}

func assetList(outputs map[string]any) []byte {
	var ids []string
	if list, ok := outputs["asset-ids"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
	}
	if len(ids) == 0 {
		if s, ok := outputs["asset-id"].(string); ok && s != "" {
			ids = []string{s}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(ids)
	return raw
}

// Enqueue dispatches a follow-up task outside any job transition.
func (o *Orchestrator) Enqueue(ctx context.Context, t Task) error { return o.enqueue(ctx, t) }

func (o *Orchestrator) enqueue(ctx context.Context, t Task) error {
	if o.dispatch == nil {
		return nil
	}
	return o.dispatch.Enqueue(ctx, t)
}

// attributeCost puts a node attempt's cost on its last recorded provider
// call, or records a call when the provider's transport is not recorded.
func attributeCost(ctx context.Context, tx *sql.Tx, job *jobRow, n *Node, cost float64, now time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE provider_calls SET cost_yuan = cost_yuan + ? WHERE node_id = ? AND attempt = ? ORDER BY id DESC LIMIT 1`, cost, n.ID, n.Attempt)
	if err != nil {
		return fmt.Errorf("attribute cost: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		return nil
	}
	provider, _, _ := strings.Cut(n.Executor, ".")
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_calls (provider, operation, user_id, job_id, node_id, attempt, status, cost_yuan, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, 'succeeded', ?, COALESCE(?, ?), ?)`,
		provider, n.Executor, job.UserID, job.ID, n.ID, n.Attempt, cost, n.StartedAt, now, now)
	if err != nil {
		return fmt.Errorf("record cost: %w", err)
	}
	return nil
}

// admit returns a non-empty queue reason when the node must wait for
// capacity. Local steps and gates are never limited.
func (o *Orchestrator) admit(ctx context.Context, n *Node) (string, error) {
	lim := o.cfg.Limits
	if strings.HasPrefix(n.Executor, "local.") || n.Executor == workflow.GateExecutor {
		return "", nil
	}
	if max := lim.Executor[n.Executor]; max > 0 {
		var running int
		if err := o.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_nodes WHERE status = ? AND executor_type = ?`, workflow.NodeRunning, n.Executor).Scan(&running); err != nil {
			return "", err
		}
		if running >= max {
			return "provider_capacity", nil
		}
	}
	if lim.PerUserActive > 0 {
		var active int
		if err := o.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_nodes n JOIN jobs j ON j.id = n.job_id
			WHERE j.user_id = (SELECT user_id FROM jobs WHERE id = ?) AND j.status IN (?, ?, ?, ?)
			AND n.status IN (?, ?) AND n.executor_type NOT LIKE 'local.%'`,
			n.JobID, workflow.JobQueued, workflow.JobRunning, workflow.JobAwaitingReview, workflow.JobCancelling,
			workflow.NodeRunning, workflow.NodeWaiting).Scan(&active); err != nil {
			return "", err
		}
		if active >= lim.PerUserActive {
			return "user_limit", nil
		}
	}
	return "", nil
}

// deferNode re-schedules a ready node that has to wait for capacity. It
// keeps its attempt count; the queue reason is shown to the user.
func (o *Orchestrator) deferNode(ctx context.Context, n *Node, reason string) error {
	now := o.now()
	next := now.Add(o.cfg.Limits.Defer)
	var events []Event
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, n.JobID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE job_nodes SET next_run_at = ?, queue_reason = ?, dispatch_seq = dispatch_seq + 1, dispatched_at = ?
			WHERE id = ? AND status = ? AND dispatch_seq = ?`, next, reason, now, n.ID, workflow.NodeReady, n.DispatchSeq)
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			return errSkip
		}
		if n.QueueReason != reason {
			buf := &eventBuffer{job: job.JobRef}
			if err := buf.add(ctx, tx, now, EventNodeStatus, map[string]any{"node": n.Name, "status": workflow.NodeReady, "queue_reason": reason}); err != nil {
				return err
			}
			events = buf.events
		}
		return nil
	})
	if errors.Is(err, errSkip) || errors.Is(err, ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	o.publish(ctx, events)
	return o.enqueue(ctx, Task{Kind: TaskRun, NodeID: n.ID, Seq: n.DispatchSeq + 1, Queue: n.Queue, ProcessAt: next, Timeout: n.Timeout})
}
