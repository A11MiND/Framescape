package aetherengine

import (
	"context"
	"strings"

	"github.com/expr-lang/expr"

	aetherexpr "github.com/BabySid/aether/expr"
)

// ExprEvaluator implements aether's expr.Evaluator using expr-lang/expr.
//
// Aether hands us a FLAT map whose keys are dotted strings, e.g.
// "outputs.parameters.success-count" or "tasks.gen-image.phase"
// (internal/binding/env.go's EvalVars). expr-lang, like any real expression
// language, parses `a.b.c` as three chained accesses — not as a lookup of
// the literal string "a.b.c" — and a bare `-` inside an identifier is always
// the subtraction operator, never a name character.
//
// Since every Aether-protocol name (task names, parameter names) is
// DNS-1123 kebab-case (docs/aether-validation-report.md §three.1), almost
// every identifier a workflow author writes can legally contain a hyphen.
// So we do two things here, not one:
//
//  1. Unflatten the dotted keys into a real nested map[string]any before
//     handing it to expr-lang, so dot-chains over the FIXED protocol
//     keywords (outputs, parameters, tasks, phase, code, ...) keep working.
//  2. Require workflow authors to bracket any hyphenated path SEGMENT —
//     both task names and parameter names — e.g.
//     tasks["gen-image"].outputs.parameters["success-count"]
//     not
//     tasks.gen-image.outputs.parameters.success-count   (parses as subtraction)
//
// This is enforced by CI (workflows/*.json linter, DEV_PLAN.md §12), not by
// this adapter — by the time an expression reaches here it must already be
// correct.
type ExprEvaluator struct{}

func NewExprEvaluator() ExprEvaluator { return ExprEvaluator{} }

func (ExprEvaluator) Eval(_ context.Context, exprStr string, env map[string]any) (any, error) {
	nested := unflatten(env)
	program, err := expr.Compile(exprStr, expr.Env(nested), expr.AllowUndefinedVariables())
	if err != nil {
		return nil, err
	}
	return expr.Run(program, nested)
}

// unflatten turns {"outputs.parameters.success-count": 3, "code": 0} into
// {"outputs": {"parameters": {"success-count": 3}}, "code": 0}.
func unflatten(flat map[string]any) map[string]any {
	root := make(map[string]any, len(flat))
	for k, v := range flat {
		parts := strings.Split(k, ".")
		cur := root
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = v
				continue
			}
			next, ok := cur[part].(map[string]any)
			if !ok {
				next = make(map[string]any)
				cur[part] = next
			}
			cur = next
		}
	}
	return root
}

var _ aetherexpr.Evaluator = ExprEvaluator{}
