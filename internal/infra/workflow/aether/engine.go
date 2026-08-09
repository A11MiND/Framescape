// Package aetherengine is the only package that constructs and wraps
// *aether.Engine itself (PRD §2.3 闸门三 / DEV_PLAN.md §5): business code
// (domain/application layers) must depend only on workflow.Engine, never on
// aether directly. Executors (internal/infra/executor/*) are the exception —
// they implement aether's executor.Plugin contract and legitimately import
// aether's leaf packages (model/executor/broker), since that IS the plugin
// wire format, not business logic.
package aetherengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BabySid/aether"
	"github.com/BabySid/aether/broker"
	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
	"github.com/BabySid/aether/store"

	"aigc-platform/internal/domain/workflow"
)

// Engine adapts *aether.Engine to workflow.Engine.
type Engine struct {
	eng *aether.Engine
}

// handlerSetter is implemented by brokers that need the engine's own
// OnTaskStarted/OnTaskCompleted callbacks wired in after construction
// (two-phase: the callbacks don't exist until aether.New returns). Both
// LocalBroker (W1, in-process) and AsynqBroker (W2, distributed via Redis —
// its consumer for the completions/starts queue calls these) implement it.
type handlerSetter interface {
	SetHandlers(onStart broker.StartHandler, onComplete broker.CompletionHandler)
}

// New builds the engine from an injected Store and TaskBroker. cmd/scheduler
// chooses which to inject — in-memory + LocalBroker for tests/W1, MySQL +
// AsynqBroker in production (DEV_PLAN.md §6) — every other caller in this
// codebase depends on workflow.Engine and never notices the swap.
func New(st store.Store, brk broker.TaskBroker, registry *executor.Registry) (*Engine, error) {
	eng, err := aether.New(
		aether.WithStore(st),
		aether.WithIDGenerator(ULIDGenerator{}),
		aether.WithExprEvaluator(NewExprEvaluator()),
		aether.WithTaskBroker(brk),
		aether.WithExecutorRegistry(registry),
		// Without this, task/workflow "timeout" fields are parsed and
		// persisted as Deadline but nothing ever enforces them — found by
		// testing image-comic4 against the real distributed stack, where a
		// hung executor sat "Running" indefinitely past its declared 1m
		// timeout (docs/aether-validation-report.md's W3 addendum).
		aether.WithTimeoutWatcher(NewPollingTimeoutWatcher(st, 5*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("construct aether engine: %w", err)
	}

	if hs, ok := brk.(handlerSetter); ok {
		hs.SetHandlers(eng.OnTaskStarted, eng.OnTaskCompleted)
	}

	return &Engine{eng: eng}, nil
}

// NewInMemory wires the W1 skeleton setup (in-memory Store, in-process
// LocalBroker) — used by tests and available as a fallback; production
// (cmd/scheduler) calls New directly with the MySQL/asynq implementations.
func NewInMemory(registry *executor.Registry) (*Engine, error) {
	return New(NewMemoryStore(), NewLocalBroker(registry), registry)
}

// Start runs the engine's background watchdogs (timeout detection, etc.).
// Must be called once at process startup (cmd/scheduler).
func (e *Engine) Start(ctx context.Context) error {
	return e.eng.Start(ctx)
}

func (e *Engine) Stop() {
	e.eng.Stop()
}

func (e *Engine) Submit(ctx context.Context, def *workflow.Definition, args map[string]any) (workflow.RunID, error) {
	var wf model.Workflow
	if err := json.Unmarshal(def.JSON, &wf); err != nil {
		return "", fmt.Errorf("parse workflow definition %q: %w", def.Name, err)
	}
	if len(args) > 0 {
		if wf.Spec.Arguments == nil {
			wf.Spec.Arguments = &model.Arguments{}
		}
		for k, v := range args {
			raw, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("marshal argument %q: %w", k, err)
			}
			wf.Spec.Arguments.Parameters = append(wf.Spec.Arguments.Parameters, model.Parameter{
				Name: k, Type: paramType(v), Value: raw,
			})
		}
	}

	runID, err := e.eng.Submit(ctx, &wf)
	if err != nil {
		return "", fmt.Errorf("submit workflow %q: %w", def.Name, err)
	}
	return workflow.RunID(runID), nil
}

func (e *Engine) Get(ctx context.Context, id workflow.RunID) (*workflow.Run, error) {
	exec, err := e.eng.Get(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("get workflow run %q: %w", id, err)
	}

	run := &workflow.Run{
		ID:    id,
		Phase: string(exec.Status),
		Nodes: make([]workflow.NodeState, 0, len(exec.Tasks)),
	}
	for _, t := range exec.Tasks {
		ns := workflow.NodeState{
			TaskRunID:    t.RunID,
			Name:         t.TaskName,
			LoopIndex:    LoopIndexFromScope(t.Scope),
			ParentScope:  t.Scope,
			ExecutorType: t.TemplateType,
			Phase:        string(t.Status),
			ErrorMsg:     t.Message,
		}
		if t.Outputs != nil {
			ns.Outputs = ParamsToMap(t.Outputs.Parameters)
			if t.Outputs.Code != 0 || len(t.Outputs.Parameters) > 0 {
				code := t.Outputs.Code
				ns.ExecCode = &code
			}
		}
		run.Nodes = append(run.Nodes, ns)
	}
	return run, nil
}

func (e *Engine) Cancel(ctx context.Context, id workflow.RunID) error {
	if err := e.eng.Cancel(ctx, string(id)); err != nil {
		return fmt.Errorf("cancel workflow run %q: %w", id, err)
	}
	return nil
}

// Resume takes a human-readable task name (workflow.Engine's port is
// name-based — callers like jobsvc shouldn't need to know Aether's internal
// TaskRunID concept), but aether.Engine.Resume itself keys strictly off
// TaskRunID (it does store.GetTaskRun(ctx, taskID) internally). So this
// resolves name -> current TaskRunID via Get() first. Only correct for a
// task name that appears at most once in the run (true for human.gate,
// which is a plain DAG task, never inside a Loop where the same name repeats
// once per iteration under different scopes).
func (e *Engine) Resume(ctx context.Context, id workflow.RunID, taskName string, inputs map[string]any) error {
	exec, err := e.eng.Get(ctx, string(id))
	if err != nil {
		return fmt.Errorf("resume: get workflow run %q: %w", id, err)
	}
	taskRunID := ""
	for _, t := range exec.Tasks {
		if t.TaskName == taskName {
			taskRunID = t.RunID
			break
		}
	}
	if taskRunID == "" {
		return fmt.Errorf("resume: task %q not found in workflow run %q", taskName, id)
	}

	if err := e.eng.Resume(ctx, string(id), taskRunID, inputs); err != nil {
		return fmt.Errorf("resume workflow run %q task %q: %w", id, taskName, err)
	}
	return nil
}

// paramType is a best-effort documentation hint on the submitted
// model.Parameter — Aether's own binding code resolves purely off Value,
// not Type, so this is cosmetic, not load-bearing.
func paramType(v any) string {
	switch v.(type) {
	case []string, []any:
		return "array"
	case int, int64, float64:
		return "number"
	case bool:
		return "bool"
	default:
		return "string"
	}
}

// ParamsToMap decodes a slice of model.Parameter (raw JSON values) into a
// plain map, for handing outputs to non-aether code (SSE payloads, the
// job_nodes projection, the HTTP layer).
func ParamsToMap(params []model.Parameter) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for _, p := range params {
		var v any
		if len(p.Value) > 0 {
			_ = json.Unmarshal(p.Value, &v)
		}
		out[p.Name] = v
	}
	return out
}

// LoopIndexFromScope is a best-effort UI convenience: Aether encodes the
// iteration index inside the Scope string (e.g. "shot-loop.loop[2]/", see
// engine_loop.go's iterScope construction) rather than as a separate field.
// Returns -1 when the task is not inside a loop iteration.
func LoopIndexFromScope(scope string) int {
	open := -1
	for i := len(scope) - 1; i >= 0; i-- {
		if scope[i] == ']' {
			continue
		}
		if scope[i] == '[' {
			open = i
			break
		}
	}
	if open < 0 {
		return -1
	}
	closeAt := -1
	for i := open; i < len(scope); i++ {
		if scope[i] == ']' {
			closeAt = i
			break
		}
	}
	if closeAt < 0 {
		return -1
	}
	var idx int
	if _, err := fmt.Sscanf(scope[open+1:closeAt], "%d", &idx); err != nil {
		return -1
	}
	return idx
}

var _ workflow.Engine = (*Engine)(nil)
