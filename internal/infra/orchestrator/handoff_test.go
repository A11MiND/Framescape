package orchestrator

import (
	"context"
	"testing"
	"time"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
	"aigc-platform/internal/pkg/id"
)

// gatedPlugin is registered on every worker but only configured on some.
type gatedPlugin struct {
	fnPlugin
	configured bool
}

func (p gatedPlugin) Configured() bool { return p.configured }

func gated(t *testing.T, configured bool) gatedPlugin {
	return gatedPlugin{fnPlugin{"t.gated", func(context.Context, *executor.ExecuteRequest) (*model.ExecOutputs, error) {
		if !configured {
			t.Error("an unconfigured plugin was executed")
		}
		return executor.OutputFrom(struct {
			AssetID string `json:"asset-id"`
		}{AssetID: "gated"})
	}}, configured}
}

func TestUnconfiguredWorkerHandsStepToAnother(t *testing.T) {
	h := newHarness(t)
	if err := h.reg.Register(gated(t, false)); err != nil {
		t.Fatal(err)
	}
	job := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.gated", MaxAttempts: 1}}})
	task, ok, _ := h.disp.pop(h.clock.now())
	if !ok {
		t.Fatal("nothing dispatched")
	}
	if err := h.orch.RunNode(h.ctx, task.NodeID, task.Seq); err != nil {
		t.Fatal(err)
	}
	if n := h.node(job, "gen"); n.Status != workflow.NodeReady || n.QueueReason != "handoff" || n.Attempt != 0 {
		t.Fatalf("after handoff: status=%s reason=%q attempt=%d", n.Status, n.QueueReason, n.Attempt)
	}

	// A second worker that has the provider settings picks the step up.
	reg := executor.NewRegistry()
	if err := reg.Register(gated(t, true)); err != nil {
		t.Fatal(err)
	}
	h.orch = New(Options{
		DB: h.db, Dispatcher: h.disp, Publisher: h.pub, Billing: h.bill, Plugins: reg,
		Config: Config{WorkerID: "test-other-" + id.New()[20:], Lease: 30 * time.Second, RetryBase: time.Second},
		Now:    h.clock.now,
	})
	h.pub.orch = h.orch
	h.drain(20)
	if st, code := h.jobStatus(job); st != workflow.JobSucceeded {
		t.Fatalf("status = %s (%s), want succeeded", st, code)
	}
	if n := h.node(job, "gen"); n.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", n.Attempt)
	}
}

func TestStepNoWorkerCanRunFailsAfterHandoffWindow(t *testing.T) {
	h := newHarness(t)
	if err := h.reg.Register(gated(t, false)); err != nil {
		t.Fatal(err)
	}
	unconfigured := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.gated", MaxAttempts: 1}}})
	unregistered := h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.absent", MaxAttempts: 1}}})
	h.drain(200)
	if st, code := h.jobStatus(unconfigured); st != workflow.JobFailed || code != CodeProviderAccount {
		t.Fatalf("unconfigured: %s (%s), want failed (%s)", st, code, CodeProviderAccount)
	}
	if st, code := h.jobStatus(unregistered); st != workflow.JobFailed || code != CodeExecutorUnavailable {
		t.Fatalf("unregistered: %s (%s), want failed (%s)", st, code, CodeExecutorUnavailable)
	}
}
