// image_sequence.go implements F5.5's cross-shot referencing extension
// (§07 gap: a user asked to #-reference a sibling shot's about-to-be-
// generated image within the same batch, not just an existing library
// asset). Spec.ShotSourceRefs' own doc covers the shape; this file covers
// why it forced image.sequence off the static workflows/image-sequence.json
// + real Loop it used to run on: once shot i's own source-image-asset-id can
// depend on shot j's runtime-produced output, the shots are no longer
// independent, and Aether's Loop primitive has no way to express a per-item
// dependency on another item's own runtime output (only a flat itemsFrom
// list) — the exact same limitation docs/aether-validation-report.md §四
// already documents for video.sequence's i2va tail-frame chaining, which is
// why that workflow's own document is generated in Go too.
//
// Unlike video.sequence's linear N-shot chain, most image.sequence batches
// have no cross-shot references at all (F5.5's original, still-common case)
// — forcing every shot into one serial chain regardless would trade away
// the free parallelism the old Loop gave those batches for nothing. So this
// builds a proper DAG with per-shot dependencies instead: a shot with no
// reference has no dependency and still runs concurrently with every other
// independent shot; only a shot that references an earlier one waits on it.
// References are backward-only by construction (the frontend picker only
// ever offers earlier shots, and validateShotSourceRefs rejects anything
// else server-side), so the resulting dependency graph is cycle-free without
// needing a separate topological-sort/cycle-detection pass.
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
)

// imageShotPlan is one shot's fully-resolved generation request — mirrors
// video_sequence.go's shotPlan, same "statically-decidable half, computed
// once and reused" reasoning (createImageSequence and RetryNode both derive
// prompts from the same persisted Spec, though RetryNode never needed
// RefShotIndex — see retry.go's image.sequence case doc).
type imageShotPlan struct {
	Index              int // 1-based
	Prompt             string
	Seed               string
	SourceImageAssetID string // static batch-wide fallback, ignored when RefShotIndex > 0
	RefShotIndex       int    // 1-based index of an earlier shot in this batch to use as the dynamic source instead, 0 = none
}

// validateShotSourceRefs is the server-side backstop for the frontend's own
// "only offer earlier shots" restriction (§19.4.1's双保险 pattern, same
// reasoning as minimax/video.go's mode mutual-exclusion check) — rejects a
// forward or self reference outright rather than silently dropping or
// (worse) building a cyclic dependency graph Aether would then fail on in a
// far more confusing way.
func validateShotSourceRefs(refs []int, shotCount int) error {
	for i, r := range refs {
		if r == 0 {
			continue
		}
		if i >= shotCount {
			return fmt.Errorf("shot_source_refs has more entries than shots")
		}
		if r < 1 || r > i {
			return fmt.Errorf("shot %d's source reference must point at an earlier shot (1..%d), got %d", i+1, i, r)
		}
	}
	return nil
}

func (s *Service) createImageSequence(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	if len(spec.Shots) == 0 {
		return nil, fmt.Errorf("image.sequence requires at least 1 shot")
	}
	if err := validateShotSourceRefs(spec.ShotSourceRefs, len(spec.Shots)); err != nil {
		return nil, err
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	// Resolve the seed once (F5.5: "同 seed") — every shot embeds it, same
	// reasoning as Create's image.comic4 branch. resolveSharedSeed's own
	// doc covers the fallback for when neither spec.Seed nor a bound
	// character set one.
	seed := resolveSharedSeed(characters, spec.Seed)
	seedStr := formatSeed(seed)

	plans := make([]imageShotPlan, len(spec.Shots))
	for i, shotText := range spec.Shots {
		compiled := prompt.Compile(prompt.Input{Text: shotText, Characters: characters, Presets: presets, Seed: seed})
		refIdx := 0
		if i < len(spec.ShotSourceRefs) {
			refIdx = spec.ShotSourceRefs[i]
		}
		if refIdx == 0 && i > 0 && spec.ImageSequenceMode == "continuity" {
			// No manual override for this shot — Continuity Mode's own
			// default fills in "the immediately preceding shot" instead of
			// leaving it unreferenced (Spec.ImageSequenceMode's own doc).
			refIdx = i // 1-based index of shot i (0-based loop var i == shot i's own 1-based predecessor)
		}
		plans[i] = imageShotPlan{
			Index: i + 1, Prompt: compiled.Prompt, Seed: seedStr,
			SourceImageAssetID: spec.SourceImageAssetID, RefShotIndex: refIdx,
		}
	}

	wfJSON := buildImageSequenceWorkflow(plans)

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	// Per-node, same reasoning as image.comic4's Loop in Create().
	estimatedCredits := creditsvc.EstimatePerNodeImageCredits(len(plans))
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "job", "image.sequence"); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}

	args := map[string]any{"user-id": strconv.FormatUint(userID, 10)}
	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: "image-sequence", JSON: wfJSON}, args)
	if err != nil {
		_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, estimatedCredits)
		return nil, fmt.Errorf("submit workflow: %w", err)
	}

	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
		ProjectID:       projectID,
		WorkflowName:    "image.sequence",
		WorkflowRunID:   string(runID),
		Title:           truncate(spec.Shots[0], 128),
		Status:          "running",
		Spec:            specJSON,
		CreditEstimated: estimatedCredits,
		CreditHeld:      estimatedCredits,
		IdemKey:         nullableIdemKey(idemKey),
		StartedAt:       ptrTime(time.Now()),
	}
	if err := s.db.WithContext(ctx).Create(job).Error; err != nil {
		return s.handleDuplicateIdemKey(ctx, err, userID, idemKey, bizID, estimatedCredits)
	}
	return job, nil
}

// genOneShotTaskTemplate is the gen-one-shot leaf task's shape — identical
// to (and replaces) the old workflows/image-sequence.json's own "gen-one-shot"
// task template. Shared by buildImageSequenceWorkflow (production) and
// genOneShotDefJSON (retry.go's RetryNode, which needs the exact same task
// shape to build a single-node satellite workflow, but no longer has a
// static file to read it from).
func genOneShotTaskTemplate() map[string]any {
	return map[string]any{
		"name": "gen-one-shot", "executor": map[string]any{"type": "minimax.image"},
		"inputs": map[string]any{"parameters": []any{
			map[string]any{"name": "prompt", "type": "string"},
			map[string]any{"name": "seed", "type": "string"},
			map[string]any{"name": "source-image-asset-id", "type": "string"},
			map[string]any{"name": "user-id", "type": "string"},
			map[string]any{"name": "n", "type": "string", "value": "1"},
		}},
		"phaseConditions": map[string]any{
			"succeeded": `outputs.parameters["success-count"] == outputs.parameters["requested-n"]`,
			"failed":    `outputs.parameters["success-count"] < outputs.parameters["requested-n"]`,
		},
		"retry": map[string]any{"limit": 2}, "timeout": "3m",
	}
}

// genOneShotDefJSON wraps genOneShotTaskTemplate in a minimal aether/v1
// document shaped exactly like the definitions-file-loaded documents
// buildRetryWorkflow otherwise reads via workflowdefs.FS — see retry.go's
// image.sequence case, which is the only caller. Kept separate from
// buildImageSequenceWorkflow's own doc-building below since RetryNode's
// buildRetryWorkflow does its own extraction/wrapping and only needs
// something to extract "gen-one-shot" out of, not a full multi-task document.
func genOneShotDefJSON() []byte {
	doc := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"spec": map[string]any{
			"templates": []any{
				map[string]any{"task": genOneShotTaskTemplate()},
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(fmt.Sprintf("genOneShotDefJSON: marshal: %v", err))
	}
	return b
}

// buildImageSequenceWorkflow generates one job's aether/v1 document: every
// shot is its own "shot-N" DAG task (video_sequence.go's own "shot-%d"
// naming convention, reused so job_nodes stays consistent across both
// code-generated workflows) — independent shots get no dependencies and run
// concurrently exactly like the old Loop did; a shot with RefShotIndex > 0
// depends on that earlier shot's task and pulls source-image-asset-id from
// its runtime asset-id output instead of a literal.
//
// Every field gen-one-shot declares must get a value, even a logically
// "unused" one (empty string) — same Aether Binder quirk
// buildVideoSequenceWorkflow's own doc already covers (an omitted argument's
// bound Value is zero-length bytes, which fails json.Unmarshal downstream),
// verified against the real engine, not merely assumed.
func buildImageSequenceWorkflow(plans []imageShotPlan) []byte {
	mainTasks := make([]any, 0, len(plans))
	for _, p := range plans {
		deps := []string{}
		sourceArg := literal("source-image-asset-id", p.SourceImageAssetID)
		if p.RefShotIndex > 0 {
			refName := fmt.Sprintf("shot-%d", p.RefShotIndex)
			deps = []string{refName}
			sourceArg = fromTask("source-image-asset-id", refName, "asset-id")
		}
		args := []any{
			literal("prompt", p.Prompt),
			literal("seed", p.Seed),
			sourceArg,
			fromWorkflow("user-id", "user-id"),
			literal("n", "1"),
		}
		mainTasks = append(mainTasks, map[string]any{
			"name": fmt.Sprintf("shot-%d", p.Index), "template": "gen-one-shot",
			"dependencies": deps,
			"arguments":    map[string]any{"parameters": args},
		})
	}

	templates := []any{
		map[string]any{"dag": map[string]any{"name": "main", "tasks": mainTasks}},
		map[string]any{"task": genOneShotTaskTemplate()},
	}

	doc := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name": "image-sequence",
			"annotations": map[string]any{
				"description": "F5.5 连续生成: code-generated per job, see internal/application/jobsvc/image_sequence.go",
			},
		},
		"spec": map[string]any{
			"entrypoint": "main",
			"arguments":  map[string]any{"parameters": []any{map[string]any{"name": "user-id", "type": "string"}}},
			"templates":  templates,
		},
	}
	out, err := json.Marshal(doc)
	if err != nil {
		// Every value fed in is a plain string/int/slice — json.Marshal on
		// this shape cannot fail; a panic here means a real programming bug,
		// not a runtime/input condition (video_sequence.go's own doc, same
		// reasoning).
		panic(fmt.Sprintf("buildImageSequenceWorkflow: marshal: %v", err))
	}
	return out
}
