package aetherengine

import (
	"context"
	"sync"
	"time"

	"github.com/BabySid/aether/store"
	"github.com/BabySid/aether/timeout"
)

// PollingTimeoutWatcher implements timeout.Watcher by periodically scanning
// the Store's active (non-terminal, deadline-bearing) runs — this is the
// piece that was missing all along W1-W3: without a Watcher wired via
// aether.WithTimeoutWatcher, a workflow.Task's declared `"timeout"` field is
// parsed and stored but never enforced (found by testing image-comic4
// against the real distributed stack: a hung executor sat "Running" well
// past its 1m timeout with nothing ever timing it out).
type PollingTimeoutWatcher struct {
	store    store.Store
	interval time.Duration

	events chan timeout.TimeoutEvent
	cancel context.CancelFunc
	once   sync.Once
}

func NewPollingTimeoutWatcher(st store.Store, interval time.Duration) *PollingTimeoutWatcher {
	return &PollingTimeoutWatcher{
		store:    st,
		interval: interval,
		events:   make(chan timeout.TimeoutEvent),
	}
}

func (w *PollingTimeoutWatcher) Start(ctx context.Context) error {
	w.once.Do(func() {
		innerCtx, cancel := context.WithCancel(ctx)
		w.cancel = cancel
		go w.run(innerCtx)
	})
	return nil
}

func (w *PollingTimeoutWatcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *PollingTimeoutWatcher) Events() <-chan timeout.TimeoutEvent { return w.events }

func (w *PollingTimeoutWatcher) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

func (w *PollingTimeoutWatcher) scan(ctx context.Context) {
	now := time.Now()

	if taskRuns, err := w.store.ListActiveTaskRuns(ctx); err == nil {
		for _, tr := range taskRuns {
			if tr.Deadline != nil && tr.Deadline.Before(now) {
				w.emit(ctx, timeout.TimeoutEvent{Kind: timeout.KindTask, RunID: tr.RunID})
			}
		}
	}
	if wfRuns, err := w.store.ListActiveWorkflowRuns(ctx); err == nil {
		for _, wr := range wfRuns {
			if wr.Deadline != nil && wr.Deadline.Before(now) {
				w.emit(ctx, timeout.TimeoutEvent{Kind: timeout.KindWorkflow, RunID: wr.RunID})
			}
		}
	}
}

func (w *PollingTimeoutWatcher) emit(ctx context.Context, ev timeout.TimeoutEvent) {
	select {
	case w.events <- ev:
	case <-ctx.Done():
	}
}

var _ timeout.Watcher = (*PollingTimeoutWatcher)(nil)
