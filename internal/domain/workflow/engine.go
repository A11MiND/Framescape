// Package workflow defines the orchestration-engine port that business code
// depends on. This is PRD §2.3 闸门三: business code must never import aether
// directly — only internal/infra/workflow/aether may do that. Swapping the
// orchestration engine (Aether → something else) should touch exactly one
// package: internal/infra/workflow/aether.
package workflow

import "context"

// RunID identifies one orchestration-engine execution of a Definition.
type RunID string

// Definition is a workflow document. In this codebase it is always an
// aether/v1 JSON document (see workflows/*.json and dynamically-generated
// DAGs such as video.sequence's shot chain — DEV_PLAN.md §10), but the
// Engine port treats it as an opaque blob so callers never need to know the
// underlying protocol.
type Definition struct {
	// Name is the aether/v1 metadata.name (kebab-case, DNS-1123 — see
	// docs/aether-validation-report.md §three.1). It is NOT the same as the
	// business-layer jobs.workflow_name (e.g. "image.single"), which may use
	// dots and is stored separately.
	Name string
	// JSON is the raw aether/v1 Workflow document.
	JSON []byte
}

// NodeState is one task/DAG/loop node's current execution state, projected
// from the engine's internal representation for business consumption (feeds
// job_nodes and SSE — PRD §9.1, §13.5).
type NodeState struct {
	TaskRunID    string
	Name         string
	LoopIndex    int // -1 when not inside a loop iteration
	ParentScope  string
	ExecutorType string
	Phase        string // Created/Ready/Running/Succeeded/Failed/Error/Timeout/Skipped/Cancelled/Suspended
	ExecCode     *int
	Outputs      map[string]any
	ErrorMsg     string
}

// Run is a snapshot of one workflow execution.
type Run struct {
	ID    RunID
	Phase string // workflow-level phase, same enum as NodeState.Phase
	Nodes []NodeState
}

// Engine is the only orchestration surface business code may depend on.
type Engine interface {
	// Submit starts a new workflow run from def, with the given top-level
	// workflow.parameters. Values are JSON-marshaled as-is, so a string
	// arg becomes a JSON string and a []string arg becomes a JSON array —
	// e.g. image.comic4's four panel prompts need an array, not four
	// separate string args. Non-blocking: returns as soon as the run is
	// persisted and its initially-ready tasks are dispatched.
	Submit(ctx context.Context, def *Definition, args map[string]any) (RunID, error)

	// Get returns the current state of a run, including all task nodes
	// (DAG/Loop expanded) known so far.
	Get(ctx context.Context, id RunID) (*Run, error)

	// Cancel stops a run. Already-dispatched tasks are signalled to stop;
	// their in-flight provider calls should be cancelled where possible
	// (F7.4 — cancelling must reach MiniMax, not just stop locally).
	Cancel(ctx context.Context, id RunID) error

	// Resume continues a Suspended task (the preview gate, F6.8/F7.6) by
	// merging inputs into it and re-dispatching.
	Resume(ctx context.Context, id RunID, taskName string, inputs map[string]any) error
}
