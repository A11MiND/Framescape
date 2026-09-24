package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"aigc-platform/internal/domain/workflow"
)

func TestDAGResolvesInputsAndSucceeds(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "a", Executor: "t.ok", Inputs: map[string]workflow.Input{"cost": workflow.Lit(0.1)}},
		{Name: "b", Executor: "t.ok"},
		{Name: "c", Executor: "t.ok", Display: map[string]any{"result": true}, Inputs: map[string]workflow.Input{
			"from-a": workflow.From("a", "asset-id"),
			"all":    workflow.ListOf(workflow.From("a", "asset-id"), workflow.Lit(""), workflow.From("b", "asset-id"), workflow.Lit([]string{"x", "y"})),
		}},
	}})

	if st, _ := h.jobStatus(job); st != workflow.JobQueued {
		t.Fatalf("status after submit = %s, want queued", st)
	}
	h.drain(50)

	st, code := h.jobStatus(job)
	if st != workflow.JobSucceeded || code != "" {
		t.Fatalf("status = %s (%s), want succeeded", st, code)
	}
	c := h.node(job, "c")
	echo := c.Outputs["echo"].(map[string]any)
	if echo["from-a"] != "asset-a" {
		t.Fatalf("from-a = %v", echo["from-a"])
	}
	if got := echo["all"]; !reflect.DeepEqual(got, []any{"asset-a", "asset-b", "x", "y"}) {
		t.Fatalf("list input = %v", got)
	}
	var cover string
	var done, total int
	if err := h.db.QueryRow(`SELECT cover_asset_id, node_done, node_total FROM jobs WHERE id = ?`, job.ID).Scan(&cover, &done, &total); err != nil {
		t.Fatal(err)
	}
	if cover != "asset-c" || done != 3 || total != 3 {
		t.Fatalf("cover=%s done=%d total=%d", cover, done, total)
	}
	if h.bill.releases[job.ID] != 1 {
		t.Fatalf("release called %d times, want 1", h.bill.releases[job.ID])
	}
	types := h.pub.types(job.BizID)
	if types[0] != EventJobCreated || types[len(types)-1] != EventJobFinished {
		t.Fatalf("event order: %v", types)
	}
}

func TestRetryThenSucceedAndExhaustion(t *testing.T) {
	h := newHarness(t)
	ok := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "gen", Executor: "t.flaky", MaxAttempts: 3, Inputs: map[string]workflow.Input{"failures": workflow.Lit(2)}},
	}})
	h.drain(50)
	if st, _ := h.jobStatus(ok); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded after retries", st)
	}
	if n := h.node(ok, "gen"); n.Attempt != 3 {
		t.Fatalf("attempt = %d, want 3", n.Attempt)
	}

	bad := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "other", Executor: "t.ok"},
		{Name: "gen", Executor: "t.flaky", MaxAttempts: 2, Inputs: map[string]workflow.Input{"failures": workflow.Lit(5)}},
		{Name: "after", Executor: "t.ok", Deps: []string{"gen"}},
	}})
	h.drain(50)
	st, code := h.jobStatus(bad)
	if st != workflow.JobPartial || code != "executor_error" {
		t.Fatalf("status = %s (%s), want partial/executor_error", st, code)
	}
	if n := h.node(bad, "after"); n.Status != workflow.NodeSkipped {
		t.Fatalf("downstream status = %s, want skipped", n.Status)
	}
}

func TestBusinessFailureIsNotRetriedButIsBilled(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.reject", MaxAttempts: 3}}})
	h.drain(20)
	st, code := h.jobStatus(job)
	if st != workflow.JobFailed || code != "moderation" {
		t.Fatalf("status = %s (%s), want failed/moderation", st, code)
	}
	n := h.node(job, "gen")
	if n.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1 (no retry)", n.Attempt)
	}
	if h.bill.commits[job.BizID+"/gen/1"] != 0.5 {
		t.Fatalf("failed paid call not billed: %v", h.bill.commits)
	}
}

func TestNoCapacityDefersWithoutConsumingAttempt(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.nocap", MaxAttempts: 1}}})
	h.drain(20)
	if st, _ := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded", st)
	}
	if n := h.node(job, "gen"); n.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", n.Attempt)
	}
}

func TestCheckAllRequested(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.partial", Check: workflow.CheckAllRequested}}})
	h.drain(10)
	st, code := h.jobStatus(job)
	if st != workflow.JobPartial || code != "incomplete_output" {
		t.Fatalf("status = %s (%s), want partial/incomplete_output", st, code)
	}
}

func TestDuplicateDeliveryRunsOnce(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok"}}})
	n := h.node(job, "gen")
	for i := 0; i < 3; i++ {
		if err := h.orch.RunNode(h.ctx, n.ID, n.DispatchSeq); err != nil {
			t.Fatal(err)
		}
	}
	if calls := h.fx.callsOf(n.TaskRunID); calls != 1 {
		t.Fatalf("executor ran %d times, want 1", calls)
	}
}

func TestAsyncNodeSurvivesWorkerCrashWithoutResubmitting(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "shot-1", Executor: "t.async", MaxAttempts: 2, Timeout: time.Hour, Inputs: map[string]workflow.Input{"polls": workflow.Lit(3)}},
		{Name: "concat", Executor: "t.ok", Inputs: map[string]workflow.Input{"clip": workflow.From("shot-1", "asset-id")}},
	}})
	n := h.node(job, "shot-1")
	if err := h.orch.RunNode(h.ctx, n.ID, n.DispatchSeq); err != nil {
		t.Fatal(err)
	}
	if n = h.node(job, "shot-1"); n.Status != workflow.NodeWaiting {
		t.Fatalf("after submit status = %s, want waiting", n.Status)
	}

	// A worker claims the poll and dies before finishing it.
	if ok, err := h.orch.claim(h.ctx, n, workflow.NodeWaiting); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	h.disp.drop.Store(true)
	h.clock.advance(time.Minute)
	h.disp.drop.Store(false)
	if _, err := h.orch.Sweep(h.ctx); err != nil {
		t.Fatal(err)
	}
	if n = h.node(job, "shot-1"); n.Status != workflow.NodeWaiting {
		t.Fatalf("after lease expiry status = %s, want waiting again", n.Status)
	}
	h.drain(50)

	if st, _ := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded", st)
	}
	if got := h.fx.submits.Load(); got != 1 {
		t.Fatalf("remote task submitted %d times, want exactly 1", got)
	}
	if h.bill.commits[job.BizID+"/shot-1/1"] != 2.5 {
		t.Fatalf("async cost not settled: %v", h.bill.commits)
	}
	if c := h.node(job, "concat"); c.Outputs["echo"].(map[string]any)["clip"] != "video-shot-1" {
		t.Fatalf("concat input = %v", c.Outputs["echo"])
	}
}

func TestSyncWorkerCrashIsRetriedBySweeper(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok", MaxAttempts: 2}}})
	n := h.node(job, "gen")
	h.disp.pop(h.clock.now())
	if ok, err := h.orch.claim(h.ctx, n, workflow.NodeReady); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	h.clock.advance(2 * time.Minute)
	stats, err := h.orch.Sweep(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LeasesExpired != 1 {
		t.Fatalf("expired leases = %d, want 1", stats.LeasesExpired)
	}
	h.drain(20)
	if st, _ := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded", st)
	}
	if n = h.node(job, "gen"); n.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", n.Attempt)
	}
}

func TestLostDispatchIsResent(t *testing.T) {
	h := newHarness(t)
	h.disp.drop.Store(true)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok"}}})
	h.disp.drop.Store(false)
	h.drain(5)
	if st, _ := h.jobStatus(job); st != workflow.JobQueued {
		t.Fatalf("status = %s, want still queued", st)
	}
	h.clock.advance(31 * time.Second)
	if stats, err := h.orch.Sweep(h.ctx); err != nil || stats.Redispatched == 0 {
		t.Fatalf("sweep: %+v %v", stats, err)
	}
	h.drain(10)
	if st, _ := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded", st)
	}
}

func TestGateResumeWithPatch(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "shot-1", Executor: "t.ok"},
		{Name: "gate", Executor: workflow.GateExecutor, Deps: []string{"shot-1"}},
	}})
	h.drain(20)
	if st, _ := h.jobStatus(job); st != workflow.JobAwaitingReview {
		t.Fatalf("status = %s, want awaiting_review", st)
	}
	if !slices.Contains(h.pub.types(job.BizID), EventNeedsReview) {
		t.Fatal("no needs_review event")
	}

	held := false
	patch := []workflow.NodeSpec{
		{Name: "redo-1", Executor: "t.ok", Deps: []string{"gate"}},
		{Name: "concat", Executor: "t.ok", Display: map[string]any{"result": true}, Inputs: map[string]workflow.Input{
			"clips": workflow.ListOf(workflow.From("shot-1", "asset-id"), workflow.From("redo-1", "asset-id")),
		}},
	}
	err := h.orch.Resume(h.ctx, job.ID, "gate", map[string]any{"redo": []int{1}}, patch, func(_ context.Context, _ *sql.Tx, _ JobRef) error {
		held = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("hold callback not invoked")
	}
	if err := h.orch.Resume(h.ctx, job.ID, "gate", nil, nil, nil); !errors.Is(err, ErrGateNotSuspended) {
		t.Fatalf("second resume err = %v, want ErrGateNotSuspended", err)
	}
	h.drain(20)
	if st, _ := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s, want succeeded", st)
	}
	echo := h.node(job, "concat").Outputs["echo"].(map[string]any)
	if !reflect.DeepEqual(echo["clips"], []any{"asset-shot-1", "asset-redo-1"}) {
		t.Fatalf("concat clips = %v", echo["clips"])
	}
	if gate := h.node(job, "gate"); gate.Outputs["redo"] == nil {
		t.Fatalf("gate decision not stored: %v", gate.Outputs)
	}
}

func TestCancelStopsRunningAndPendingWork(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
		{Name: "slow", Executor: "t.block", Timeout: time.Minute},
		{Name: "after", Executor: "t.ok", Deps: []string{"slow"}},
	}})
	n := h.node(job, "slow")
	h.disp.pop(h.clock.now())
	done := make(chan error, 1)
	go func() { done <- h.orch.RunNode(h.ctx, n.ID, n.DispatchSeq) }()
	deadline := time.Now().Add(5 * time.Second)
	for h.node(job, "slow").Status != workflow.NodeRunning {
		if time.Now().After(deadline) {
			t.Fatal("node never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for h.fx.callsOf(h.node(job, "slow").TaskRunID) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.orch.Cancel(h.ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("running node did not stop")
	}
	if st, _ := h.jobStatus(job); st != workflow.JobCancelled {
		t.Fatalf("status = %s, want cancelled", st)
	}
	if s := h.node(job, "after").Status; s != workflow.NodeCancelled {
		t.Fatalf("pending node = %s, want cancelled", s)
	}
	if h.bill.releases[job.ID] != 1 {
		t.Fatalf("release called %d times, want 1", h.bill.releases[job.ID])
	}
}

func TestDeadlineAbortsJob(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{Deadline: time.Minute, Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok"}}})
	h.disp.drop.Store(true)
	h.clock.advance(2 * time.Minute)
	if _, err := h.orch.Sweep(h.ctx); err != nil {
		t.Fatal(err)
	}
	st, code := h.jobStatus(job)
	if st != workflow.JobFailed || code != "deadline_exceeded" {
		t.Fatalf("status = %s (%s), want failed/deadline_exceeded", st, code)
	}
}

func TestGateTTLExpiresJob(t *testing.T) {
	h := newHarness(t)
	job := h.submit(&workflow.Plan{GateTTL: time.Hour, Nodes: []workflow.NodeSpec{{Name: "gate", Executor: workflow.GateExecutor}}})
	if st, _ := h.jobStatus(job); st != workflow.JobQueued {
		t.Logf("initial status %s", st)
	}
	h.clock.advance(2 * time.Hour)
	if _, err := h.orch.Sweep(h.ctx); err != nil {
		t.Fatal(err)
	}
	st, code := h.jobStatus(job)
	if st != workflow.JobFailed || code != "gate_expired" {
		t.Fatalf("status = %s (%s), want failed/gate_expired", st, code)
	}
}

func TestUnpublishedEventsAreResent(t *testing.T) {
	h := newHarness(t)
	h.pub.fail.Store(true)
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok"}}})
	h.drain(10)
	h.pub.fail.Store(false)
	if len(h.pub.types(job.BizID)) != 0 {
		t.Fatal("events published while publisher was failing")
	}
	h.clock.advance(10 * time.Second)
	if _, err := h.orch.Sweep(h.ctx); err != nil {
		t.Fatal(err)
	}
	types := h.pub.types(job.BizID)
	if !slices.Contains(types, EventJobFinished) {
		t.Fatalf("finished event not re-published: %v", types)
	}
	events, err := h.orch.EventsSince(h.ctx, job.UserID, 0, 100)
	if err != nil || len(events) != len(types) {
		t.Fatalf("EventsSince = %d events (%v), published %d", len(events), err, len(types))
	}
}

func TestConcurrentWorkersWithDuplicateDelivery(t *testing.T) {
	h := newHarness(t)
	second := New(Options{
		DB: h.db, Dispatcher: h.disp, Publisher: h.pub, Billing: h.bill, Plugins: h.reg,
		Config: Config{WorkerID: "test-worker-b", Lease: 30 * time.Second, RetryBase: time.Millisecond},
		Now:    h.clock.now,
	})
	workers := []*Orchestrator{h.orch, second}

	const jobs = 30
	var refs []JobRef
	for i := 0; i < jobs; i++ {
		refs = append(refs, h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{
			{Name: "a", Executor: "t.ok"},
			{Name: "b", Executor: "t.ok", Inputs: map[string]workflow.Input{"x": workflow.From("a", "asset-id")}},
			{Name: "c", Executor: "t.flaky", MaxAttempts: 3, Inputs: map[string]workflow.Input{"x": workflow.From("a", "asset-id"), "failures": workflow.Lit(1)}},
			{Name: "d", Executor: "t.ok", Inputs: map[string]workflow.Input{"all": workflow.ListOf(workflow.From("b", "asset-id"), workflow.From("c", "asset-id"))}},
		}}))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			orch := workers[w%2]
			idle := 0
			for idle < 200 {
				task, ok, next := h.disp.pop(h.clock.now())
				if !ok {
					if !next.IsZero() {
						h.clock.set(next)
					}
					idle++
					time.Sleep(2 * time.Millisecond)
					continue
				}
				idle = 0
				for i := 0; i < 2; i++ { // every message is delivered twice
					var err error
					if task.Kind == TaskRun {
						err = orch.RunNode(h.ctx, task.NodeID, task.Seq)
					} else {
						err = orch.PollNode(h.ctx, task.NodeID, task.Seq)
					}
					if err != nil {
						errs <- err
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("worker error: %v", err)
	}
	for _, job := range refs {
		if st, code := h.jobStatus(job); st != workflow.JobSucceeded {
			t.Fatalf("job %d status = %s (%s)", job.ID, st, code)
		}
		for _, name := range []string{"a", "b", "d"} {
			if calls := h.fx.callsOf(h.node(job, name).TaskRunID); calls != 1 {
				t.Fatalf("job %d node %s ran %d times", job.ID, name, calls)
			}
		}
		if calls := h.fx.callsOf(h.node(job, "c").TaskRunID); calls != 2 {
			t.Fatalf("job %d flaky node ran %d times, want 2", job.ID, calls)
		}
		if h.bill.releases[job.ID] != 1 {
			t.Fatalf("job %d released %d times", job.ID, h.bill.releases[job.ID])
		}
	}
}
