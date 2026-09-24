// Package workflows builds executable plans for each generation workflow.
// Builders are pure: callers resolve characters, presets, references and
// any planner output first, so every plan shape is unit-testable. Node names
// match what API clients read results from (gen, compose, concat, shot-N,
// panel-N).
package workflows

import (
	"time"

	"aigc-platform/internal/domain/workflow"
)

// Per-executor execution policy.
type policy struct {
	attempts int
	timeout  time.Duration
}

var (
	imagePolicy      = policy{attempts: 3, timeout: 3 * time.Minute}
	openAIPolicy     = policy{attempts: 1, timeout: 6 * time.Minute}
	videoPolicy      = policy{attempts: 2, timeout: 30 * time.Minute}
	extractPolicy    = policy{attempts: 2, timeout: 2 * time.Minute}
	enhancePolicy    = policy{attempts: 2, timeout: 5 * time.Minute}
	panelEnhance     = policy{attempts: 3, timeout: 5 * time.Minute}
	composePolicy    = policy{attempts: 3, timeout: time.Minute}
	concatPolicy     = policy{attempts: 2, timeout: 10 * time.Minute}
	defaultDeadline  = 2 * time.Hour
	sequenceDeadline = 6 * time.Hour
)

func node(name, executor string, p policy, inputs map[string]workflow.Input) workflow.NodeSpec {
	return workflow.NodeSpec{Name: name, Executor: executor, Inputs: inputs, MaxAttempts: p.attempts, Timeout: p.timeout}
}

func withDisplay(n workflow.NodeSpec, kv ...any) workflow.NodeSpec {
	if n.Display == nil {
		n.Display = map[string]any{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		n.Display[kv[i].(string)] = kv[i+1]
	}
	return n
}

func lit(v any) workflow.Input { return workflow.Lit(v) }

func from(node, output string) workflow.Input { return workflow.From(node, output) }
