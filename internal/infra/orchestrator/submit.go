package orchestrator

import (
	"aigc-platform/internal/pkg/apperr"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/pkg/id"
)

// ErrGateNotSuspended is returned by Resume when the gate is not waiting.
var ErrGateNotSuspended = apperr.New("not_awaiting_review", "the job is not waiting for a preview decision")

// ErrJobTerminal is returned when an operation needs a job that is still live.
var ErrJobTerminal = apperr.New("job_finished", "the job has already finished")

// Pending is post-commit work produced by a transactional call. Callers that
// own the transaction must invoke Flush after a successful commit.
type Pending struct {
	tasks  []Task
	events []Event
	cancel []uint64
}

func (p *Pending) addReady(n *Node) {
	p.tasks = append(p.tasks, Task{Kind: TaskRun, NodeID: n.ID, Seq: n.DispatchSeq, Queue: n.Queue, Timeout: n.Timeout})
}

// Flush dispatches and publishes after commit. Failures are recovered by the
// sweeper, so they are not reported.
func (o *Orchestrator) Flush(ctx context.Context, p *Pending) {
	if p == nil {
		return
	}
	for _, t := range p.tasks {
		if o.dispatch != nil {
			_ = o.dispatch.Enqueue(ctx, t)
		}
	}
	if len(p.cancel) > 0 && o.pub != nil {
		_ = o.pub.PublishCancel(ctx, p.cancel)
	}
	o.publish(ctx, p.events)
}

// SubmitTx persists a plan for a job row the caller has just inserted in tx.
func (o *Orchestrator) SubmitTx(ctx context.Context, tx *sql.Tx, job JobRef, plan *workflow.Plan) (*Pending, error) {
	if plan == nil || len(plan.Nodes) == 0 {
		return nil, fmt.Errorf("orchestrator: empty plan")
	}
	if err := workflow.Validate(plan.Nodes, nil); err != nil {
		return nil, fmt.Errorf("orchestrator: invalid plan: %w", err)
	}
	now := o.now()
	deadline := plan.Deadline
	if deadline <= 0 {
		deadline = o.cfg.DefaultDeadline
	}
	gateTTL := plan.GateTTL
	if gateTTL <= 0 {
		gateTTL = o.cfg.GateTTL
	}
	envelope, err := json.Marshal(planEnvelope{DeadlineS: int64(deadline / time.Second), GateTTLS: int64(gateTTL / time.Second), Meta: plan.Meta})
	if err != nil {
		return nil, fmt.Errorf("encode plan meta: %w", err)
	}

	p := &Pending{}
	buf := &eventBuffer{job: job}
	for _, spec := range plan.Nodes {
		deps := spec.Dependencies()
		status := workflow.NodePending
		switch {
		case len(deps) > 0:
		case spec.Executor == workflow.GateExecutor:
			status = workflow.NodeSuspended
		default:
			status = workflow.NodeReady
		}
		nodeID, err := insertNode(ctx, tx, job.ID, spec, len(deps), status, now, id.New())
		if err != nil {
			return nil, err
		}
		if status == workflow.NodeReady {
			p.addReady(&Node{ID: nodeID, Queue: QueueFor(spec.Executor), Timeout: spec.Timeout})
		}
	}

	initial := workflow.JobQueued
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET engine = 'v2', status = ?, plan_meta = ?, deadline_at = ?, node_total = ?, version = version + 1 WHERE id = ?`,
		initial, envelope, now.Add(deadline), len(plan.Nodes), job.ID); err != nil {
		return nil, fmt.Errorf("mark job v2: %w", err)
	}
	if err := buf.add(ctx, tx, now, EventJobCreated, map[string]any{"status": initial, "node_total": len(plan.Nodes)}); err != nil {
		return nil, err
	}
	p.events = buf.events
	return p, nil
}

// Resume completes a suspended gate with the user's decision, appends the
// nodes that decision requires and releases whatever now can run. hold, when
// non-nil, runs inside the same transaction (for example to reserve credits
// for the appended work); if it fails nothing changes.
func (o *Orchestrator) Resume(ctx context.Context, jobID uint64, gate string, decision map[string]any, patch []workflow.NodeSpec, hold func(ctx context.Context, tx *sql.Tx, job JobRef) error) error {
	p := &Pending{}
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if job.Engine != "v2" {
			return ErrJobNotFound
		}
		if workflow.JobTerminal(job.Status) || job.Status == workflow.JobCancelling {
			return ErrJobTerminal
		}
		nodes, err := loadJobNodes(ctx, tx, job.ID, true)
		if err != nil {
			return err
		}
		existing := make(map[string]bool, len(nodes))
		succeeded := map[string]bool{}
		var gateNode *Node
		for _, n := range nodes {
			existing[n.Name] = true
			if n.Status == workflow.NodeSucceeded {
				succeeded[n.Name] = true
			}
			if n.Name == gate {
				gateNode = n
			}
		}
		if gateNode == nil || gateNode.Executor != workflow.GateExecutor || gateNode.Status != workflow.NodeSuspended {
			return ErrGateNotSuspended
		}
		if err := workflow.Validate(patch, existing); err != nil {
			return fmt.Errorf("orchestrator: invalid patch: %w", err)
		}
		if hold != nil {
			if err := hold(ctx, tx, job.JobRef); err != nil {
				return err
			}
		}

		now := o.now()
		buf := &eventBuffer{job: job.JobRef}
		for _, spec := range patch {
			pending := 0
			for _, d := range spec.Dependencies() {
				if !succeeded[d] {
					pending++
				}
			}
			status := workflow.NodePending
			if pending == 0 {
				status = workflow.NodeReady
			}
			nodeID, err := insertNode(ctx, tx, job.ID, spec, pending, status, now, id.New())
			if err != nil {
				return err
			}
			n := &Node{ID: nodeID, JobID: job.ID, Name: spec.Name, Executor: spec.Executor, Status: status,
				Queue: QueueFor(spec.Executor), DepsPending: pending, Deps: spec.Dependencies(), Timeout: spec.Timeout}
			nodes = append(nodes, n)
			if status == workflow.NodeReady {
				p.addReady(n)
			}
		}

		outputs, err := json.Marshal(decision)
		if err != nil {
			return fmt.Errorf("encode decision: %w", err)
		}
		if err := setStatus(ctx, tx, gateNode, workflow.NodeSucceeded, "outputs_json = ?, finished_at = ?", outputs, now); err != nil {
			return err
		}
		released, err := advance(ctx, tx, nodes, gate, now)
		if err != nil {
			return err
		}
		for _, n := range released {
			if n.Status == workflow.NodeReady {
				p.addReady(n)
			}
		}
		if err := buf.add(ctx, tx, now, EventNodeStatus, map[string]any{"node": gate, "status": workflow.NodeSucceeded}); err != nil {
			return err
		}
		deadline := job.Deadline
		if deadline <= 0 {
			deadline = o.cfg.DefaultDeadline
		}
		if _, err := o.finalizeJob(ctx, tx, job, nodes, buf, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET deadline_at = ? WHERE id = ? AND finished_at IS NULL`, now.Add(deadline), job.ID); err != nil {
			return fmt.Errorf("reset deadline: %w", err)
		}
		p.events = buf.events
		return nil
	})
	if err != nil {
		return err
	}
	o.Flush(ctx, p)
	return nil
}

// Cancel stops a job at the user's request.
func (o *Orchestrator) Cancel(ctx context.Context, jobID uint64) error {
	return o.Abort(ctx, jobID, "", "")
}

// Abort stops a job. With an empty code the job ends as cancelled; with a
// code (deadline_exceeded, gate_expired) it ends as failed with that code.
// Work already paid for is still settled; the rest of the reservation is
// released once no node is running.
func (o *Orchestrator) Abort(ctx context.Context, jobID uint64, code, message string) error {
	p := &Pending{}
	err := withTx(ctx, o.db, func(tx *sql.Tx) error {
		job, err := lockJob(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if job.Engine != "v2" {
			return ErrJobNotFound
		}
		if workflow.JobTerminal(job.Status) {
			return nil
		}
		nodes, err := loadJobNodes(ctx, tx, job.ID, true)
		if err != nil {
			return err
		}
		now := o.now()
		buf := &eventBuffer{job: job.JobRef}
		for _, n := range nodes {
			switch n.Status {
			case workflow.NodePending, workflow.NodeReady, workflow.NodeSuspended:
				if err := setStatus(ctx, tx, n, workflow.NodeCancelled, "finished_at = ?, lease_owner = '', lease_until = NULL", now); err != nil {
					return err
				}
			case workflow.NodeRunning, workflow.NodeWaiting:
				if _, err := tx.ExecContext(ctx, `UPDATE job_nodes SET cancel_requested = 1 WHERE id = ?`, n.ID); err != nil {
					return fmt.Errorf("flag cancel on node %d: %w", n.ID, err)
				}
				n.CancelRequested = true
				p.cancel = append(p.cancel, n.ID)
				if n.Status == workflow.NodeWaiting {
					p.tasks = append(p.tasks, Task{Kind: TaskPoll, NodeID: n.ID, Seq: n.DispatchSeq, Queue: n.Queue, Nudge: true})
				}
			}
		}
		job.Status, job.ErrorCode, job.ErrorMsg = workflow.JobCancelling, code, message
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ?`, workflow.JobCancelling, job.ID); err != nil {
			return err
		}
		if _, err := o.finalizeJob(ctx, tx, job, nodes, buf, now); err != nil {
			return err
		}
		p.events = buf.events
		return nil
	})
	if err != nil {
		return err
	}
	o.Flush(ctx, p)
	return nil
}
