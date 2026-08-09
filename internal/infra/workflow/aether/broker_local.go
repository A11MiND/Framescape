package aetherengine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BabySid/aether/broker"
	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// LocalBroker is an in-process broker.TaskBroker: Dispatch runs the executor
// directly in a goroutine and calls back into the engine synchronously (via
// the StartHandler/CompletionHandler set through SetHandlers), matching the
// "local" mode broker.go describes in its own doc comments. This is what W1
// runs on; W2 replaces it with an asynq-backed broker so cmd/worker can be a
// separate, independently-scaled process (DEV_PLAN.md §6) — cmd/api and
// cmd/scheduler code should depend only on workflow.Engine and never notice
// the swap.
//
// FetchTask/StartTask/CompleteTask are the "worker pulls work" side of the
// interface, used by distributed brokers where a separate process calls
// them. LocalBroker doesn't need them (Dispatch already ran the executor
// in-process), so they return an explicit unsupported error rather than
// silently doing nothing.
type LocalBroker struct {
	registry *executor.Registry

	mu         sync.Mutex
	onStart    broker.StartHandler
	onComplete broker.CompletionHandler
	cancels    map[string]context.CancelFunc
}

func NewLocalBroker(registry *executor.Registry) *LocalBroker {
	return &LocalBroker{
		registry: registry,
		cancels:  make(map[string]context.CancelFunc),
	}
}

// SetHandlers wires the engine's OnTaskStarted/OnTaskCompleted callbacks.
// Called once, after the Engine has been constructed (the Engine needs the
// broker to exist first, and the broker needs the Engine's methods — this
// two-phase wiring breaks that cycle).
func (b *LocalBroker) SetHandlers(onStart broker.StartHandler, onComplete broker.CompletionHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onStart = onStart
	b.onComplete = onComplete
}

func (b *LocalBroker) Dispatch(_ context.Context, assignment *broker.TaskAssignment) error {
	plugin, ok := b.registry.Get(assignment.ExecutorType)
	if !ok {
		return fmt.Errorf("local broker: no executor registered for type %q", assignment.ExecutorType)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	if assignment.Timeout != "" {
		if d, err := time.ParseDuration(assignment.Timeout); err == nil && d > 0 {
			var timeoutCancel context.CancelFunc
			taskCtx, timeoutCancel = context.WithTimeout(taskCtx, d)
			prevCancel := cancel
			cancel = func() { timeoutCancel(); prevCancel() }
		}
	}

	b.mu.Lock()
	b.cancels[assignment.TaskRunID] = cancel
	onStart := b.onStart
	onComplete := b.onComplete
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.cancels, assignment.TaskRunID)
			b.mu.Unlock()
			cancel()
		}()

		if onStart != nil {
			onStart(taskCtx, assignment.TaskRunID)
		}

		out, err := plugin.Execute(taskCtx, &executor.ExecuteRequest{
			TaskRunID:     assignment.TaskRunID,
			WorkflowRunID: assignment.WorkflowRunID,
			TaskName:      assignment.TaskName,
			TemplateName:  assignment.TemplateName,
			Inputs:        assignment.Inputs,
			Resources:     assignment.Resources,
			Timeout:       assignment.Timeout,
			RetryCount:    assignment.RetryCount,
		})

		var execOutputs *model.ExecOutputs
		switch {
		case taskCtx.Err() == context.DeadlineExceeded:
			execOutputs = &model.ExecOutputs{Code: model.ExecCodeTimeout, Message: "task exceeded its deadline"}
		case err != nil:
			execOutputs = &model.ExecOutputs{Code: model.ExecCodeError, Message: err.Error()}
		case out == nil:
			execOutputs = &model.ExecOutputs{Code: model.ExecCodeSucceeded}
		default:
			execOutputs = out
		}

		if onComplete != nil {
			onComplete(context.Background(), &broker.TaskResult{
				TaskRunID:     assignment.TaskRunID,
				WorkflowRunID: assignment.WorkflowRunID,
				ExecOutputs:   execOutputs,
			})
		}
	}()

	return nil
}

func (b *LocalBroker) Cancel(_ context.Context, taskRunID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.cancels[taskRunID]; ok {
		cancel()
		delete(b.cancels, taskRunID)
	}
	return nil
}

func (b *LocalBroker) FetchTask(_ context.Context, _ string) (*broker.TaskAssignment, error) {
	return nil, fmt.Errorf("local broker: FetchTask not supported (Dispatch runs executors in-process)")
}

func (b *LocalBroker) StartTask(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("local broker: StartTask not supported (Dispatch runs executors in-process)")
}

func (b *LocalBroker) CompleteTask(_ context.Context, _ *broker.TaskResult) error {
	return fmt.Errorf("local broker: CompleteTask not supported (Dispatch runs executors in-process)")
}

func (b *LocalBroker) Close() error { return nil }

var _ broker.TaskBroker = (*LocalBroker)(nil)
