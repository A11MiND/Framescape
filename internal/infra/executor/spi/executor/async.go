package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aigc-platform/internal/infra/executor/spi/model"
)

// AsyncPlugin is an executor whose provider runs the work as a remote task.
// Submit starts it and returns a reference; the orchestrator persists the
// reference, frees the worker slot and calls Poll later (on a timer or when
// the provider's callback arrives). A worker restart therefore never loses
// or repeats a paid remote task.
type AsyncPlugin interface {
	Plugin
	// Submit starts the remote task. A non-nil outputs value is an immediate
	// terminal result (for example input validation failed) and no task was
	// started.
	Submit(ctx context.Context, req *ExecuteRequest) (ProviderRef, *model.ExecOutputs, error)
	// Poll checks the remote task and, once it has finished, materializes
	// its result.
	Poll(ctx context.Context, req *ExecuteRequest, ref ProviderRef) (PollResult, error)
}

// Canceler is implemented by async plugins whose provider can stop a task.
type Canceler interface {
	Cancel(ctx context.Context, ref ProviderRef) error
}

// ProviderRef identifies a remote task.
type ProviderRef struct {
	Provider    string          `json:"provider"`
	TaskID      string          `json:"task_id"`
	SubmittedAt time.Time       `json:"submitted_at"`
	Extra       json.RawMessage `json:"extra,omitempty"`
}

// PollResult is one observation of a remote task.
type PollResult struct {
	Done    bool
	Outputs *model.ExecOutputs
	// After suggests when to poll next; zero means the orchestrator default.
	After time.Duration
}

// NoCapacityError means the step could not start because a provider or user
// concurrency/rate limit is exhausted. The orchestrator defers the node
// without consuming an attempt instead of failing it.
type NoCapacityError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *NoCapacityError) Error() string {
	return fmt.Sprintf("no capacity (%s), retry after %s", e.Reason, e.RetryAfter)
}

// AsNoCapacity reports whether err signals exhausted capacity.
func AsNoCapacity(err error) (*NoCapacityError, bool) {
	var e *NoCapacityError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
