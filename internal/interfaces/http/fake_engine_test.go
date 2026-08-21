package httpapi

import (
	"context"
	"fmt"
	"sync"

	"aigc-platform/internal/domain/workflow"
)

// fakeEngine is a minimal in-memory stand-in for the real Aether-backed
// workflow.Engine (internal/infra/workflow/rpc.Client in production) —
// jobsvc.Service depends only on the workflow.Engine port (PRD §2.3 闸门三),
// so this package's HTTP-layer tests never need a real orchestration engine
// running to exercise job creation/lookup/cancel. Submit always succeeds
// with a single synthetic "gen" node in Running phase; a test that needs a
// specific outcome (terminal, failed, ...) calls setPhase directly rather
// than waiting on anything.
type fakeEngine struct {
	mu   sync.Mutex
	runs map[workflow.RunID]*workflow.Run
	next int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{runs: map[workflow.RunID]*workflow.Run{}}
}

func (f *fakeEngine) Submit(_ context.Context, _ *workflow.Definition, _ map[string]any) (workflow.RunID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := workflow.RunID(fmt.Sprintf("fake-run-%d", f.next))
	f.runs[id] = &workflow.Run{
		ID:    id,
		Phase: "Running",
		Nodes: []workflow.NodeState{{Name: "gen", Phase: "Running", LoopIndex: -1}},
	}
	return id, nil
}

func (f *fakeEngine) Get(_ context.Context, id workflow.RunID) (*workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return nil, fmt.Errorf("fakeEngine: run %q not found", id)
	}
	cp := *r
	cp.Nodes = append([]workflow.NodeState(nil), r.Nodes...)
	return &cp, nil
}

func (f *fakeEngine) Cancel(_ context.Context, id workflow.RunID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return fmt.Errorf("fakeEngine: run %q not found", id)
	}
	r.Phase = "Cancelled"
	return nil
}

func (f *fakeEngine) Resume(_ context.Context, id workflow.RunID, _ string, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.runs[id]; !ok {
		return fmt.Errorf("fakeEngine: run %q not found", id)
	}
	return nil
}

// workflowRunIDFrom just names the workflow.RunID(string) conversion so
// call sites read as intent ("this is a run ID") rather than a bare cast.
func workflowRunIDFrom(s string) workflow.RunID { return workflow.RunID(s) }

// setPhase lets a test move a run straight to a terminal phase without
// waiting on anything — jobsvc.Service.Get's own terminal-phase sync is
// what actually reflects this into jobs.status.
func (f *fakeEngine) setPhase(id workflow.RunID, phase string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[id]; ok {
		r.Phase = phase
	}
}
