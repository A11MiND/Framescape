package jobsvc

import (
	"encoding/json"
	"testing"

	workflowdefs "aigc-platform/workflows"
)

// TestBuildRetryWorkflow exercises the pure JSON transform against the real
// embedded workflow files — no DB, no engine — so a change to
// workflows/image-comic4.json or image-sequence.json that breaks retry's
// assumptions (task template shape, parameter names) fails here instead of
// only being discovered live.
func TestBuildRetryWorkflow(t *testing.T) {
	for _, tc := range []struct {
		defFile  string
		nodeName string
		values   map[string]string
	}{
		{"image-comic4", "gen-one-panel", map[string]string{"prompt": "a corrected panel", "user-id": "7", "n": "1"}},
		{"image-sequence", "gen-one-shot", map[string]string{"prompt": "a corrected shot", "user-id": "7", "n": "1", "seed": "42"}},
	} {
		t.Run(tc.defFile, func(t *testing.T) {
			raw, err := workflowdefs.FS.ReadFile(tc.defFile + ".json")
			if err != nil {
				t.Fatalf("read %s: %v", tc.defFile, err)
			}
			out, err := buildRetryWorkflow(raw, tc.nodeName, tc.values)
			if err != nil {
				t.Fatalf("buildRetryWorkflow: %v", err)
			}

			var doc map[string]any
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			if doc["apiVersion"] != "aether/v1" || doc["kind"] != "Workflow" {
				t.Fatalf("unexpected envelope: %v", doc)
			}
			spec, _ := doc["spec"].(map[string]any)
			if spec == nil || spec["entrypoint"] != "main" {
				t.Fatalf("bad spec.entrypoint: %v", spec)
			}
			templates, _ := spec["templates"].([]any)
			if len(templates) != 2 {
				t.Fatalf("expected exactly 2 templates (main dag + leaf task), got %d", len(templates))
			}

			mainTmpl, _ := templates[0].(map[string]any)
			dag, _ := mainTmpl["dag"].(map[string]any)
			if dag == nil || dag["name"] != "main" {
				t.Fatalf("templates[0] is not the main dag: %v", mainTmpl)
			}
			tasks, _ := dag["tasks"].([]any)
			if len(tasks) != 1 {
				t.Fatalf("main dag should have exactly 1 task, got %d", len(tasks))
			}
			callSite, _ := tasks[0].(map[string]any)
			if callSite["name"] != tc.nodeName || callSite["template"] != tc.nodeName {
				t.Fatalf("main dag's task should call %q with no arguments, got %v", tc.nodeName, callSite)
			}
			if _, hasArgs := callSite["arguments"]; hasArgs {
				t.Fatalf("main dag's task must not pass arguments — every value must be a literal default on the leaf template, got %v", callSite)
			}

			leafTmpl, _ := templates[1].(map[string]any)
			task, _ := leafTmpl["task"].(map[string]any)
			if task == nil || task["name"] != tc.nodeName {
				t.Fatalf("templates[1] is not the %q leaf task: %v", tc.nodeName, leafTmpl)
			}
			// executor/retry/timeout/phaseConditions must survive untouched
			// (identical to production) — buildRetryWorkflow only mutates
			// inputs.parameters[].value.
			if _, ok := task["executor"]; !ok {
				t.Fatalf("leaf task lost its executor block: %v", task)
			}

			inputs, _ := task["inputs"].(map[string]any)
			params, _ := inputs["parameters"].([]any)
			seen := map[string]string{}
			for _, p := range params {
				pm, _ := p.(map[string]any)
				name, _ := pm["name"].(string)
				if v, ok := pm["value"]; ok {
					seen[name] = v.(string)
				}
			}
			for name, want := range tc.values {
				got, ok := seen[name]
				if !ok {
					t.Errorf("parameter %q has no literal value in output, want %q", name, want)
					continue
				}
				if got != want {
					t.Errorf("parameter %q literal value = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestBuildRetryWorkflowUnknownNode(t *testing.T) {
	raw, err := workflowdefs.FS.ReadFile("image-comic4.json")
	if err != nil {
		t.Fatalf("read image-comic4.json: %v", err)
	}
	if _, err := buildRetryWorkflow(raw, "no-such-task", nil); err == nil {
		t.Fatal("expected an error for a node name that isn't a leaf task in the definition, got nil")
	}
}
