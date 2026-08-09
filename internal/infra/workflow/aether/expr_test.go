package aetherengine

import (
	"context"
	"testing"
)

// TestExprEvaluator_HyphenatedBracketAccess proves the exact scenario
// documented in docs/aether-validation-report.md §three.2: a phaseConditions
// expression referencing a hyphenated task name AND a hyphenated parameter
// name must use bracket notation, and the adapter's unflatten step must make
// that resolve correctly against Aether's flat, dotted-key EvalVars.
func TestExprEvaluator_HyphenatedBracketAccess(t *testing.T) {
	ev := NewExprEvaluator()
	env := map[string]any{
		"code":                  0,
		"tasks.gen-image.phase": "Succeeded",
		"tasks.gen-image.outputs.parameters.success-count": float64(3),
		"tasks.gen-image.outputs.parameters.requested-n":   float64(4),
	}

	got, err := ev.Eval(context.Background(), `code == 0 && tasks["gen-image"].outputs.parameters["success-count"] < tasks["gen-image"].outputs.parameters["requested-n"]`, env)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if got != true {
		t.Fatalf("expected true (partial success detected), got %v", got)
	}

	// Sanity: the naive dot-chain form must NOT silently succeed with the
	// right answer — if it does, our documented hazard is wrong.
	_, err = ev.Eval(context.Background(), `tasks.gen-image.phase == "Succeeded"`, env)
	if err == nil {
		t.Fatalf("expected dot-chain over a hyphenated identifier to fail to compile, but it succeeded")
	}
}
