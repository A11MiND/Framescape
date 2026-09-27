package orchestrator

import (
	"os"
	"sync"
	"testing"
	"time"

	"aigc-platform/internal/domain/workflow"
)

// This opt-in test exercises the local medium-scale target without making the
// normal race suite depend on a long database run. It uses the same CAS,
// dispatcher and billing paths as production, with the deterministic fixture
// executor so no paid provider is contacted.
func TestPhase7FiveHundredQueuedJobs(t *testing.T) {
	if os.Getenv("FRAMESCAPE_LOAD_TEST") != "1" {
		t.Skip("set FRAMESCAPE_LOAD_TEST=1 to run the local 500-job rehearsal")
	}
	h := newHarness(t)
	const jobs = 500
	refs := make([]JobRef, 0, jobs)
	for i := 0; i < jobs; i++ {
		refs = append(refs, h.submit(&workflow.Plan{Nodes: []workflow.NodeSpec{{Name: "gen", Executor: "t.ok"}}}))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idle := 0
			for idle < 100 {
				task, ok, next := h.disp.pop(h.clock.now())
				if !ok {
					if !next.IsZero() {
						h.clock.set(next)
					}
					idle++
					time.Sleep(time.Millisecond)
					continue
				}
				idle = 0
				if !h.owns(task.NodeID) {
					continue
				}
				if err := h.orch.RunNode(h.ctx, task.NodeID, task.Seq); err != nil {
					errs <- err
					return
				}
				// A duplicate delivery must be harmless after the CAS has completed.
				if err := h.orch.RunNode(h.ctx, task.NodeID, task.Seq); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, job := range refs {
		if status, code := h.jobStatus(job); status != workflow.JobSucceeded || code != "" {
			t.Fatalf("job %d status = %s (%s), want succeeded", job.ID, status, code)
		}
	}
}
