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

// retryableNodes maps workflow_name to the one node (job_nodes.node_name)
// this package knows how to rebuild inputs for and resubmit as a standalone
// satellite Job (§the artifact's "現在還缺什麼" node-retry gap — Aether's
// own Engine port only exposes Submit/Get/Resume/Cancel, no way to
// re-trigger a single already-terminal task in place without reopening the
// vendored engine's own one-way Phase invariant, see engine.go's doc).
//
// Scoped to leaf `task` templates that all share one call-site name (this
// map is workflow_name -> a single node_name, not a pattern) whose inputs
// are fully reconstructible from the job's own persisted Spec alone.
// video.sequence's per-shot nodes were never eligible (each is its own
// nested DAG, not a leaf task). image.sequence and image.comic4 (both their
// own cross-shot/panel-chaining extensions, image_sequence.go's/
// image_comic4.go's own package docs) aren't eligible either: every
// shot/panel is now its own distinctly-named "shot-N"/"panel-N" DAG task
// instead of Loop iterations sharing one name + loop_index, and one that
// references an earlier shot/panel needs that shot/panel's already-
// materialized asset id, not just its own Spec entry — the same
// "genuinely doesn't fit this package's re-run-one-leaf-task model"
// reasoning video.sequence's own exclusion already documents.
var retryableNodes = map[string]string{
	"video.single": "gen",
}

// retryTemplateName maps workflow_name to the *definition file's own*
// template name for that node — usually identical to retryableNodes' entry,
// except video.single: its DAG call-site is named "gen" (matching
// job_nodes.node_name) but invokes a template declared as "gen-video".
// buildRetryWorkflow needs the template name to find the right `task` block
// in workflows/*.json; RetryNode needs the job_nodes name to look up the
// failed row — two different identifiers for the same node, so both maps
// exist rather than conflating them.
var retryTemplateName = map[string]string{
	"video.single": "gen-video",
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
	// already documented for this exact engine. Array-typed values (video.
	// single's reference-*-asset-ids) work the same way: json.Marshal turns
	// a Go []string into a literal JSON array on the template, no different
	// in kind from a literal string default — Aether's binder (bindOne,
	// third_party/aether/internal/binding/bind.go) doesn't care about type
	// when reading decl.Value directly.
	values := map[string]any{"user-id": strconv.FormatUint(userID, 10), "n": "1"}
	var text string
	var estimatedCredits int
	switch job.WorkflowName {
	case "video.single":
		if loopIndex != -1 {
			return nil, fmt.Errorf("video.single's %q node is not a loop iteration, loop_index must be -1, got %d", nodeName, loopIndex)
		}
		// Mirrors Create's video.single branch exactly (resolution
		// validation, duration clamp, F6.4 auto character-reference
		// fallback) — see jobsvc.go's own doc for why each check is there.
		// Deliberately NOT reproducing PromptEnhance even if the original
		// job had it on: that's a separate upstream node (enhance-prompt)
		// producing a rewritten prompt this package has no record of, and
		// chaining it back in would need a second dynamic task feeding
		// into "gen" — exactly the {{...}} interpolation complexity this
		// whole literal-values-only design avoids. Retry always uses the
		// original (or overridden) raw text, un-enhanced.
		if spec.Resolution != "" && spec.Resolution != "768P" && spec.Resolution != "2K" {
			return nil, fmt.Errorf("resolution must be 768P or 2K, got %q", spec.Resolution)
		}
		text = spec.Text
		if promptOverride != "" {
			text = promptOverride
		}
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: spec.Seed, MaxChars: 7000})
		values["prompt"] = compiled.Prompt
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > 15 {
			duration = 15
		}
		values["duration"] = strconv.Itoa(duration)
		resolution := spec.Resolution
		if resolution == "" {
			resolution = "768P"
		}
		values["resolution"] = resolution
		values["ratio"] = spec.Ratio
		values["first-frame-asset-id"] = spec.FirstFrameAssetID
		values["last-frame-asset-id"] = spec.LastFrameAssetID
		refImageIDs := spec.ReferenceImageAssetIDs
		if len(refImageIDs) == 0 && spec.FirstFrameAssetID == "" && spec.LastFrameAssetID == "" && len(spec.Characters) > 0 {
			autoRefs, err := s.resolveCharacterRefAssetIDs(ctx, userID, spec.Characters)
			if err != nil {
				return nil, err
			}
			refImageIDs = autoRefs
		}
		values["reference-image-asset-ids"] = nonNil(refImageIDs)
		values["reference-video-asset-ids"] = nonNil(spec.ReferenceVideoAssetIDs)
		values["reference-audio-asset-ids"] = nonNil(spec.ReferenceAudioAssetIDs)
		estimatedCredits = creditsvc.EstimateVideoCredits(duration, resolution)
	}

	defFile, ok := definitions[job.WorkflowName]
	if !ok {
		return nil, fmt.Errorf("no workflow definition file registered for %q", job.WorkflowName)
	}
	raw, err := workflowdefs.FS.ReadFile(defFile + ".json")
	if err != nil {
		return nil, fmt.Errorf("load workflow definition %q: %w", defFile, err)
	}
	satelliteJSON, err := buildRetryWorkflow(raw, retryTemplateName[job.WorkflowName], nodeName, values)
	if err != nil {
		return nil, err
	}

	bizID2 := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID2+":hold", "job", bizID2, estimatedCredits, "retry", job.WorkflowName); err != nil {
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
		BizID:         bizID2,
		UserID:        userID,
		WorkflowName:  job.WorkflowName,
		WorkflowRunID: string(runID),
		// No language-specific "重做:"/"Retry:" prefix baked into the stored
		// title — RetryOfJobID below is the real provenance signal, and the
		// frontend renders its own locale-aware retry indicator from that
		// instead of parsing a hardcoded-Chinese string prefix (see
		// handleListJobs/handleGetJob's retry_of_job_id field).
		Title:            truncate(job.Title, 128),
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

// buildRetryWorkflow extracts templateName's `task` template verbatim from
// an existing aether/v1 workflow document — identical executor/retry/
// timeout/phaseConditions to production, since it's the exact same JSON
// object, not a hand-rewritten copy — and wraps it in a fresh single-task
// Workflow document, with the wrapping DAG's own call-site named
// callSiteName (job_nodes' name for this node; may differ from
// templateName, see retryTemplateName's doc). Every parameter in values
// overwrites that parameter's literal `value` on the template itself; the
// wrapping DAG's task invocation passes no `arguments` at all, relying on
// Aether reading a template's own declared `value` as the default when the
// caller supplies none for that parameter — the same mechanism image_
// comic4.go's own genOnePanelTaskTemplate relies on for its literal
// "n":"1" default. This needs zero {{...}} interpolation anywhere.
func buildRetryWorkflow(defJSON []byte, templateName, callSiteName string, values map[string]any) ([]byte, error) {
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
		if name, _ := tk["name"].(string); name == templateName {
			task = tk
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("template %q is not a leaf task in this workflow definition", templateName)
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
		"metadata":   map[string]any{"name": "retry-" + callSiteName},
		"spec": map[string]any{
			"entrypoint": "main",
			"templates": []any{
				map[string]any{"dag": map[string]any{
					"name": "main",
					"tasks": []any{
						map[string]any{"name": callSiteName, "template": templateName},
					},
				}},
				map[string]any{"task": task},
			},
		},
	}
	return json.Marshal(satellite)
}
