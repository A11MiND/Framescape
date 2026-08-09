package aetherengine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"

	"github.com/BabySid/aether/broker"
	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// Queue naming (PRD §11.1's q:image/q:video/q:local/q:llm/q:critical
// partitioning). W2 only has mock executors, so most types fall through to
// the default queue — the mapping is filled in as W3/W5 add minimax.image,
// minimax.video, local.*, so cmd/worker's queue subscription list doesn't
// need to change shape later, only grow.
const (
	QueueControl  = "q:control" // scheduler-only: task-started/completed reports from workers
	QueueDefault  = "q:default"
	QueueImage    = "q:image"
	QueueVideo    = "q:video"
	QueueLocal    = "q:local"
	QueueLLM      = "q:llm"
	QueueCritical = "q:critical"

	taskTypeDispatch  = "aigc:dispatch"
	taskTypeStarted   = "aigc:started"
	taskTypeCompleted = "aigc:completed"
)

// QueueForExecutorType routes an executor type to its asynq queue.
func QueueForExecutorType(execType string) string {
	switch execType {
	case "mock.image", "minimax.image", "minimax.file.upload":
		return QueueImage
	case "mock.video", "minimax.video", "minimax.video.regen":
		return QueueVideo
	case "minimax.context_ir":
		return QueueLLM
	case "local.compose", "local.ffmpeg.extract", "local.ffmpeg.concat", "local.moderation":
		return QueueLocal
	default:
		return QueueDefault
	}
}

// AllQueues is the full set cmd/worker subscribes to, with the relative
// weights from PRD §11.1 ({critical:6, image:4, video:3, local:2, llm:2}).
func AllQueues() map[string]int {
	return map[string]int{
		QueueCritical: 6,
		QueueImage:    4,
		QueueVideo:    3,
		QueueLocal:    2,
		QueueLLM:      2,
		QueueDefault:  2,
	}
}

// startedReport / completedReport are the control-queue wire payloads.
type startedReport struct {
	TaskRunID string `json:"task_run_id"`
}

type completedReport struct {
	TaskRunID     string          `json:"task_run_id"`
	WorkflowRunID string          `json:"workflow_run_id"`
	ExecOutputs   json.RawMessage `json:"exec_outputs"`
}

// AsynqBroker is the scheduler-side broker.TaskBroker: Dispatch enqueues a
// Fat TaskAssignment to the appropriate worker queue; a background control
// consumer (RunControlConsumer) turns worker reports back into
// OnTaskStarted/OnTaskCompleted calls on the in-process Engine. This
// replaces W1's LocalBroker — cmd/worker is now a genuinely separate,
// independently-scalable process (DEV_PLAN.md §6/§11.2①).
type AsynqBroker struct {
	client    *asynq.Client
	inspector *asynq.Inspector
	redisOpt  asynq.RedisClientOpt

	mu         sync.Mutex
	onStart    broker.StartHandler
	onComplete broker.CompletionHandler
	asynqIDs   map[string]string // taskRunID -> asynq-assigned task ID, for Cancel
}

func NewAsynqBroker(redisOpt asynq.RedisClientOpt) *AsynqBroker {
	return &AsynqBroker{
		client:    asynq.NewClient(redisOpt),
		inspector: asynq.NewInspector(redisOpt),
		redisOpt:  redisOpt,
		asynqIDs:  make(map[string]string),
	}
}

func (b *AsynqBroker) SetHandlers(onStart broker.StartHandler, onComplete broker.CompletionHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onStart = onStart
	b.onComplete = onComplete
}

func (b *AsynqBroker) Dispatch(ctx context.Context, assignment *broker.TaskAssignment) error {
	payload, err := json.Marshal(assignment)
	if err != nil {
		return fmt.Errorf("marshal task assignment: %w", err)
	}
	task := asynq.NewTask(taskTypeDispatch, payload)

	opts := []asynq.Option{asynq.Queue(QueueForExecutorType(assignment.ExecutorType))}
	if assignment.Timeout != "" {
		if d, err := time.ParseDuration(assignment.Timeout); err == nil && d > 0 {
			// Small margin over the assignment timeout so aether's own
			// timeout watchdog (which races against the same deadline) is
			// the one that reports Timeout, not asynq silently dropping it.
			opts = append(opts, asynq.Timeout(d+30*time.Second))
		}
	}

	info, err := b.client.EnqueueContext(ctx, task, opts...)
	if err != nil {
		return fmt.Errorf("enqueue task %s: %w", assignment.TaskRunID, err)
	}

	b.mu.Lock()
	b.asynqIDs[assignment.TaskRunID] = info.ID
	b.mu.Unlock()
	return nil
}

func (b *AsynqBroker) Cancel(_ context.Context, taskRunID string) error {
	b.mu.Lock()
	id, ok := b.asynqIDs[taskRunID]
	b.mu.Unlock()
	if !ok {
		return nil // never dispatched, or already completed and forgotten
	}
	// Best-effort per asynq's own docs: signals a running handler's context
	// to cancel; has no effect if the task already finished.
	return b.inspector.CancelProcessing(id)
}

func (b *AsynqBroker) FetchTask(_ context.Context, _ string) (*broker.TaskAssignment, error) {
	return nil, fmt.Errorf("asynq broker: FetchTask not used — cmd/worker runs its own asynq.Server/ServeMux")
}

func (b *AsynqBroker) StartTask(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("asynq broker: StartTask not used — workers report via the control queue")
}

func (b *AsynqBroker) CompleteTask(_ context.Context, _ *broker.TaskResult) error {
	return fmt.Errorf("asynq broker: CompleteTask not used — workers report via the control queue")
}

func (b *AsynqBroker) Close() error {
	b.inspector.Close()
	return b.client.Close()
}

// RunControlConsumer runs the scheduler-side asynq server that consumes
// QueueControl and turns worker reports into direct, in-process calls to the
// Engine's OnTaskStarted/OnTaskCompleted (set via SetHandlers). Blocks until
// ctx is cancelled; call it in its own goroutine from cmd/scheduler.
//
// Concurrency is deliberately 1, not the usual "pick a number and move on":
// a worker enqueues "started" then "completed" for the same task in strict
// sequence, but OnTaskCompleted's guard requires the task to already be
// Running (set by processing "started" first). Fast local executors
// (local.compose finishes in well under a second) enqueue both messages
// close enough together that a concurrent consumer can process "completed"
// before "started", silently dropping it — the task then sits Running
// forever until the timeout watchdog eventually kills it. Found by testing
// image.comic4 against the real stack: minimax.image calls take 15-20s, so
// "started" always wins that race by a wide margin and this never surfaced
// there — it only showed up on local.compose's sub-second runtime. A single
// consumer processes q:control strictly in enqueue order, which removes the
// race entirely; these are tiny bookkeeping messages, not the actual
// generation work (that runs on q:image/q:video/q:local), so serializing
// them costs nothing.
func (b *AsynqBroker) RunControlConsumer(ctx context.Context) error {
	srv := asynq.NewServer(b.redisOpt, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{QueueControl: 1},
		BaseContext: func() context.Context { return ctx },
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(taskTypeStarted, func(hctx context.Context, t *asynq.Task) error {
		var r startedReport
		if err := json.Unmarshal(t.Payload(), &r); err != nil {
			return fmt.Errorf("decode started report: %w", err)
		}
		b.mu.Lock()
		onStart := b.onStart
		b.mu.Unlock()
		if onStart != nil {
			onStart(hctx, r.TaskRunID)
		}
		return nil
	})
	mux.HandleFunc(taskTypeCompleted, func(hctx context.Context, t *asynq.Task) error {
		var r completedReport
		if err := json.Unmarshal(t.Payload(), &r); err != nil {
			return fmt.Errorf("decode completed report: %w", err)
		}
		result := &broker.TaskResult{TaskRunID: r.TaskRunID, WorkflowRunID: r.WorkflowRunID}
		if len(r.ExecOutputs) > 0 {
			if err := json.Unmarshal(r.ExecOutputs, &result.ExecOutputs); err != nil {
				return fmt.Errorf("decode exec outputs: %w", err)
			}
		}
		b.mu.Lock()
		onComplete := b.onComplete
		b.mu.Unlock()
		if onComplete != nil {
			onComplete(hctx, result)
		}
		return nil
	})

	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()
	return srv.Run(mux)
}

var _ broker.TaskBroker = (*AsynqBroker)(nil)

// --- worker-side reporting (imported by cmd/worker) ---

// WorkerReporter lets cmd/worker report task lifecycle events back to
// cmd/scheduler's control queue without holding an Engine instance.
type WorkerReporter struct {
	client *asynq.Client
}

func NewWorkerReporter(redisOpt asynq.RedisClientOpt) *WorkerReporter {
	return &WorkerReporter{client: asynq.NewClient(redisOpt)}
}

func (w *WorkerReporter) Close() error { return w.client.Close() }

func (w *WorkerReporter) ReportStarted(ctx context.Context, taskRunID string) error {
	payload, err := json.Marshal(startedReport{TaskRunID: taskRunID})
	if err != nil {
		return fmt.Errorf("marshal started report: %w", err)
	}
	_, err = w.client.EnqueueContext(ctx, asynq.NewTask(taskTypeStarted, payload), asynq.Queue(QueueControl))
	return err
}

func (w *WorkerReporter) ReportCompleted(ctx context.Context, result *broker.TaskResult) error {
	var outJSON json.RawMessage
	if result.ExecOutputs != nil {
		b, err := json.Marshal(result.ExecOutputs)
		if err != nil {
			return fmt.Errorf("marshal exec outputs: %w", err)
		}
		outJSON = b
	}
	payload, err := json.Marshal(completedReport{
		TaskRunID: result.TaskRunID, WorkflowRunID: result.WorkflowRunID, ExecOutputs: outJSON,
	})
	if err != nil {
		return fmt.Errorf("marshal completed report: %w", err)
	}
	_, err = w.client.EnqueueContext(ctx, asynq.NewTask(taskTypeCompleted, payload), asynq.Queue(QueueControl))
	return err
}

// RunWorker is cmd/worker's entire consumption loop: subscribes to
// AllQueues(), and for each dispatched task, resolves the executor from
// registry, runs it, and reports start/completion back to the scheduler's
// control queue via reporter. Blocks until ctx is cancelled.
//
// This lives here (not in cmd/worker) because it, the AsynqBroker, and the
// WorkerReporter must agree on the exact wire format (taskTypeDispatch,
// queue names) — keeping producer and consumer of that format in one file
// means changing it can't silently desync the two processes.
func RunWorker(ctx context.Context, redisOpt asynq.RedisClientOpt, registry *executor.Registry, concurrency int) error {
	reporter := NewWorkerReporter(redisOpt)
	defer reporter.Close()

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: concurrency,
		Queues:      AllQueues(),
		BaseContext: func() context.Context { return ctx },
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(taskTypeDispatch, func(hctx context.Context, t *asynq.Task) error {
		var assignment broker.TaskAssignment
		if err := json.Unmarshal(t.Payload(), &assignment); err != nil {
			return fmt.Errorf("decode task assignment: %w", err)
		}

		if err := reporter.ReportStarted(hctx, assignment.TaskRunID); err != nil {
			// Non-fatal: the projection will just show "Ready" a bit longer
			// than reality. Proceed with execution regardless.
			_ = err
		}

		plugin, ok := registry.Get(assignment.ExecutorType)
		var execOutputs *model.ExecOutputs
		if !ok {
			execOutputs = &model.ExecOutputs{
				Code: model.ExecCodeError, Message: fmt.Sprintf("no executor registered for type %q", assignment.ExecutorType),
			}
		} else {
			out, err := plugin.Execute(hctx, &executor.ExecuteRequest{
				TaskRunID: assignment.TaskRunID, WorkflowRunID: assignment.WorkflowRunID,
				TaskName: assignment.TaskName, TemplateName: assignment.TemplateName,
				Inputs: assignment.Inputs, Resources: assignment.Resources,
				Timeout: assignment.Timeout, RetryCount: assignment.RetryCount,
			})
			switch {
			case hctx.Err() != nil:
				execOutputs = &model.ExecOutputs{Code: model.ExecCodeTimeout, Message: "task exceeded its deadline"}
			case err != nil:
				execOutputs = &model.ExecOutputs{Code: model.ExecCodeError, Message: err.Error()}
			case out == nil:
				execOutputs = &model.ExecOutputs{Code: model.ExecCodeSucceeded}
			default:
				execOutputs = out
			}
		}

		return reporter.ReportCompleted(context.Background(), &broker.TaskResult{
			TaskRunID: assignment.TaskRunID, WorkflowRunID: assignment.WorkflowRunID, ExecOutputs: execOutputs,
		})
	})

	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()
	return srv.Run(mux)
}
