package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
	workflowdefs "aigc-platform/workflows"
)

// retryableNodes maps workflow_name to the one loop-body node name this
// package knows how to rebuild inputs for and resubmit as a standalone
// satellite Job (§the artifact's "現在還缺什麼" node-retry gap — Aether's
// own Engine port only exposes Submit/Get/Resume/Cancel, no way to
// re-trigger a single already-terminal task in place without reopening the
// vendored engine's own one-way Phase invariant, see engine.go's doc).
//
// Deliberately scoped to these two only: both loop bodies are single leaf
// `task` templates (not nested DAGs) with purely string-typed inputs
// (prompt/seed/user-id/n) — video.single's "gen" has array-typed
// reference-*-asset-ids inputs whose behavior as a literal template
// default is unproven in this engine, and video.sequence's per-shot nodes
// are each their own nested DAG (gen+extract), not a leaf task, so neither
// fits the "re-run exactly one leaf task" model this package implements.
var retryableNodes = map[string]string{
	"image.comic4":   "gen-one-panel",
	"image.sequence": "gen-one-shot",
}

// RetryNode resubmits exactly one Failed/Error/Timeout leaf task from an
// existing job as a brand new, ordinary one-task Job — from the engine's
// perspective indistinguishable from any other Job, so it rides the entire
// existing pipeline (credit hold/commit/refund, SSE, job_nodes projection)
// for free. RetryOfJobID/RetryOfNodeName/RetryOfLoopIndex are the only new
// state (persistence.Job's own doc), letting the frontend link the new
// asset back to the node it corrects.
//
// This is deliberately NOT an in-place fix: the original job and its
// Failed node are left exactly as they were — a historically accurate
// record — and this produces a fresh, standalone corrected asset instead
// of trying to splice a corrected result back into the original run's own
// (already-terminal) DAG, which Aether has no mechanism for anyway.
func (s *Service) RetryNode(ctx context.Context, userID uint64, bizID, nodeName string, loopIndex int, promptOverride string) (*persistence.Job, error) {
	job, _, err := s.Get(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	taskName, ok := retryableNodes[job.WorkflowName]
	if !ok || taskName != nodeName {
		return nil, fmt.Errorf("node %q is not retryable for workflow %q", nodeName, job.WorkflowName)
	}

	var node persistence.JobNode
	if err := s.db.WithContext(ctx).
		Where("job_id = ? AND node_name = ? AND loop_index = ?", job.ID, nodeName, loopIndex).
		First(&node).Error; err != nil {
		return nil, fmt.Errorf("node %q[%d] not found: %w", nodeName, loopIndex, err)
	}
	switch node.Phase {
	case "Failed", "Error", "Timeout":
		// retryable
	default:
		return nil, fmt.Errorf("node %q[%d] is %s, not a failed terminal state — nothing to retry", nodeName, loopIndex, node.Phase)
	}

	var spec Spec
	if err := json.Unmarshal(job.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode job spec: %w", err)
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	// Every value here ends up as a literal `value` on the satellite
	// workflow's single task template (buildRetryWorkflow), never a
	// {{...}} interpolation — deliberately sidesteps the array/expression
	// interpolation fragility docs/aether-validation-report.md §四 W4
	// already documented for this exact engine.
	values := map[string]string{"user-id": strconv.FormatUint(userID, 10), "n": "1"}
	var text string
	switch job.WorkflowName {
	case "image.comic4":
		if loopIndex < 0 || loopIndex >= len(spec.Panels) {
			return nil, fmt.Errorf("panel index %d out of range for %d panels", loopIndex, len(spec.Panels))
		}
		text = spec.Panels[loopIndex]
		if promptOverride != "" {
			text = promptOverride
		}
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: spec.Seed})
		values["prompt"] = compiled.Prompt
	case "image.sequence":
		if loopIndex < 0 || loopIndex >= len(spec.Shots) {
			return nil, fmt.Errorf("shot index %d out of range for %d shots", loopIndex, len(spec.Shots))
		}
		text = spec.Shots[loopIndex]
		if promptOverride != "" {
			text = promptOverride
		}
		// Same seed resolution as Create's image.sequence branch: resolve
		// once from an empty-text compile, so every shot (including a
		// retried one) keeps the job's one shared seed (F5.5's "同 seed").
		seed := prompt.Compile(prompt.Input{Characters: characters, Seed: spec.Seed}).Seed
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: seed})
		values["prompt"] = compiled.Prompt
		if seed != nil {
			values["seed"] = strconv.FormatInt(*seed, 10)
		}
	}

	defFile, ok := definitions[job.WorkflowName]
	if !ok {
		return nil, fmt.Errorf("no workflow definition file registered for %q", job.WorkflowName)
	}
	raw, err := workflowdefs.FS.ReadFile(defFile + ".json")
	if err != nil {
		return nil, fmt.Errorf("load workflow definition %q: %w", defFile, err)
	}
	satelliteJSON, err := buildRetryWorkflow(raw, nodeName, values)
	if err != nil {
		return nil, err
	}

	estimatedCredits := creditsvc.EstimatePerNodeImageCredits(1)
	bizID2 := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID2+":hold", "job", bizID2, estimatedCredits, job.WorkflowName+" retry"); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}
	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: "retry-" + nodeName, JSON: satelliteJSON}, nil)
	if err != nil {
		_ = s.credits.Refund(ctx, userID, "job:"+bizID2+":refund", bizID2, estimatedCredits)
		return nil, fmt.Errorf("submit retry workflow: %w", err)
	}

	specJSON, err := json.Marshal(Spec{Text: text})
	if err != nil {
		return nil, fmt.Errorf("marshal retry spec: %w", err)
	}
	retryJob := &persistence.Job{
		BizID:            bizID2,
		UserID:           userID,
		WorkflowName:     job.WorkflowName,
		WorkflowRunID:    string(runID),
		Title:            truncate("重做: "+job.Title, 128),
		Status:           "running",
		Spec:             specJSON,
		CreditEstimated:  estimatedCredits,
		CreditHeld:       estimatedCredits,
		StartedAt:        ptrTime(time.Now()),
		RetryOfJobID:     &job.ID,
		RetryOfNodeName:  &nodeName,
		RetryOfLoopIndex: &loopIndex,
	}
	if err := s.db.WithContext(ctx).Create(retryJob).Error; err != nil {
		return nil, fmt.Errorf("persist retry job: %w", err)
	}
	return retryJob, nil
}

// buildRetryWorkflow extracts nodeName's `task` template verbatim from an
// existing aether/v1 workflow document — identical executor/retry/timeout/
// phaseConditions to production, since it's the exact same JSON object,
// not a hand-rewritten copy — and wraps it in a fresh single-task Workflow
// document. Every parameter in values overwrites that parameter's literal
// `value` on the template itself; the wrapping DAG's task invocation
// passes no `arguments` at all, relying on Aether reading a template's own
// declared `value` as the default when the caller supplies none for that
// parameter — the exact mechanism image-comic4.json's own compose-grid
// task already relies on for its literal "layout":"2x2" default. This
// needs zero {{...}} interpolation anywhere.
func buildRetryWorkflow(defJSON []byte, nodeName string, values map[string]string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(defJSON, &doc); err != nil {
		return nil, fmt.Errorf("decode workflow definition: %w", err)
	}
	spec, _ := doc["spec"].(map[string]any)
	templates, _ := spec["templates"].([]any)

	var task map[string]any
	for _, t := range templates {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		tk, ok := tm["task"].(map[string]any)
		if !ok {
			continue
		}
		if name, _ := tk["name"].(string); name == nodeName {
			task = tk
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("node %q is not a leaf task in this workflow definition", nodeName)
	}

	if inputs, ok := task["inputs"].(map[string]any); ok {
		if params, ok := inputs["parameters"].([]any); ok {
			for _, p := range params {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				name, _ := pm["name"].(string)
				if v, ok := values[name]; ok {
					pm["value"] = v
				}
			}
		}
	}

	satellite := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"metadata":   map[string]any{"name": "retry-" + nodeName},
		"spec": map[string]any{
			"entrypoint": "main",
			"templates": []any{
				map[string]any{"dag": map[string]any{
					"name": "main",
					"tasks": []any{
						map[string]any{"name": nodeName, "template": nodeName},
					},
				}},
				map[string]any{"task": task},
			},
		},
	}
	return json.Marshal(satellite)
}
