// video_sequence.go implements F6.7/F6.8 (PRD §5.4/§5.5, DEV_PLAN.md §10,
// the "core week" feature): a continuous multi-shot video with a 768P
// preview gate before committing to expensive 2K upgrades.
//
// Unlike every other workflow_name, video.sequence's aether/v1 document is
// generated in Go rather than loaded from workflows/*.json — the shot count
// N is different per job, and (docs/aether-validation-report.md §四) Aether's
// Loop primitive cannot chain "this iteration's input depends on the
// previous iteration's runtime output", which the draft phase needs (shot
// i's first_frame is shot i-1's just-extracted last frame). The draft phase
// is instead a linear chain of N individually-generated DAG task nodes.
//
// The overall skeleton still matches §5.5's original design:
//
//	draft (N-shot chain) -> gate (human.gate, suspends) -> [redo-loop | upgrade-loop] -> concat
//
// gate's Suspend/Resume mechanics and the empty-Loop handling this design
// depends on were both verified against the real engine before this file was
// written — see docs/aether-validation-report.md §六/§八.
package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

const defaultRecalibrateEvery = 3 // §5.4: "镜头数 >4 时，每 3 段插入一次 r2va"

// shotPlan is the statically-decidable half of one shot's generation request
// — mode and prompt are pure functions of (shot index, characters, presets,
// recalibrateEvery), so both createVideoSequence (at Submit) and Resume (at
// Resume time, from the persisted Spec) recompute the identical plan rather
// than needing to store it separately. What ISN'T statically decidable is
// first-frame-asset-id for i2va shots (a runtime value — the previous shot's
// actual extracted last frame) and the resulting asset-id of each shot
// (only known once it actually runs); both are resolved separately from
// job_nodes/engine state where needed.
type shotPlan struct {
	Index                 int // 1-based
	Prompt                string
	Mode                  string // "r2va" | "i2va" | "t2va" (t2va = i==1 with no character bound to anchor r2va)
	ReferenceImageAssetID string // set only for r2va
}

// planShots implements §5.4's mixed continuity strategy: shot 1 (and every
// recalibrateEvery-th shot after it) anchors on the character's reference
// image (r2va); every other shot continues from the previous shot's tail
// frame (i2va). If no character is bound, r2va has nothing to anchor on, so
// those shots fall back to t2va (plain text, requires an explicit ratio).
func planShots(shots []string, characters []prompt.Character, presets []prompt.Preset, characterRefAssetID string, recalibrateEvery int) []shotPlan {
	if recalibrateEvery <= 0 {
		recalibrateEvery = defaultRecalibrateEvery
	}
	plans := make([]shotPlan, len(shots))
	for i, text := range shots {
		idx := i + 1
		isAnchor := idx == 1 || (idx-1)%recalibrateEvery == 0
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, MaxChars: 7000})
		p := shotPlan{Index: idx, Prompt: compiled.Prompt}
		switch {
		case isAnchor && characterRefAssetID != "":
			p.Mode = "r2va"
			p.ReferenceImageAssetID = characterRefAssetID
		case isAnchor:
			p.Mode = "t2va"
		default:
			p.Mode = "i2va"
		}
		plans[i] = p
	}
	return plans
}

// characterRefAsset returns the first bound character's first reference
// asset (F3.2's ref_asset_ids), or "" if none is bound / none has a
// reference image — callers treat "" as "no r2va anchor available".
func (s *Service) characterRefAsset(ctx context.Context, userID uint64, slots []CharacterSlot) string {
	if len(slots) == 0 {
		return ""
	}
	var row persistence.Character
	err := s.db.WithContext(ctx).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", slots[0].CharacterID, userID).
		First(&row).Error
	if err != nil || len(row.RefAssetIDs) == 0 {
		return ""
	}
	var refIDs []string
	if err := json.Unmarshal(row.RefAssetIDs, &refIDs); err != nil || len(refIDs) == 0 {
		return ""
	}
	return refIDs[0]
}

func (s *Service) createVideoSequence(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	if len(spec.Shots) == 0 {
		return nil, fmt.Errorf("video.sequence requires at least 1 shot")
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}
	characterRefAssetID := s.characterRefAsset(ctx, userID, spec.Characters)

	duration := spec.DurationSeconds
	if duration <= 0 {
		duration = 5
	}
	if duration > capability.VideoDurationMax {
		duration = 15 // mirrors video.go's normalizeDuration clamp, so the hold matches what actually runs
	}
	ratio := spec.Ratio
	if ratio == "" {
		ratio = "16:9" // only consumed by t2va-fallback shots (buildVideoSequenceWorkflow)
	}

	plans := planShots(spec.Shots, characters, presets, characterRefAssetID, spec.RecalibrateEvery)
	wfJSON := buildVideoSequenceWorkflow(plans, duration, ratio)

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	// §12.3's "预览门只预扣 768P 部分积分" — the draft phase is always 768P
	// regardless of what gets upgraded later at Resume time.
	estimatedCredits := creditsvc.EstimateVideoCredits(duration, "768P") * len(plans)
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "video.sequence draft"); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}

	args := map[string]any{"user-id": strconv.FormatUint(userID, 10)}
	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: "video-sequence", JSON: wfJSON}, args)
	if err != nil {
		_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, estimatedCredits)
		return nil, fmt.Errorf("submit workflow: %w", err)
	}

	title := spec.Shots[0]
	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
		ProjectID:       projectID,
		WorkflowName:    "video.sequence",
		WorkflowRunID:   string(runID),
		Title:           truncate(title, 128),
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

// --- Workflow JSON construction ---

// param builds a literal-value model.Parameter-shaped map — every value
// baked directly into the generated JSON at build time (no workflow.parameters
// indirection needed, since this document is generated fresh per job and
// already knows every shot's exact values).
func literal(name string, value any) map[string]any {
	return map[string]any{"name": name, "value": value}
}

func fromTask(name, taskName, path string) map[string]any {
	return map[string]any{"name": name, "valueFrom": map[string]any{"parameter": "tasks." + taskName + ".outputs.parameters." + path}}
}

func fromWorkflow(name, path string) map[string]any {
	return map[string]any{"name": name, "valueFrom": map[string]any{"parameter": "workflow.parameters." + path}}
}

// genShotInputDecl is gen-shot's declared inputs.parameters — shared by
// every shot-N DAG node and by redo-loop's body (minimax.video again, same
// executor, see the package doc's "redo = re-run the draft step" reasoning
// in DEV_PLAN.md §10).
func genShotInputDecl() []map[string]any {
	return []map[string]any{
		{"name": "prompt", "type": "string"},
		{"name": "duration", "type": "string"},
		{"name": "resolution", "type": "string"},
		{"name": "ratio", "type": "string"},
		{"name": "first-frame-asset-id", "type": "string"},
		{"name": "reference-image-asset-ids", "type": "array"},
		{"name": "user-id", "type": "string"},
		{"name": "shot-index", "type": "string"},
	}
}

func buildVideoSequenceWorkflow(plans []shotPlan, duration int, ratio string) []byte {
	n := len(plans)
	durationStr := strconv.Itoa(duration)

	mainTasks := make([]any, 0, n*2+4)
	templates := make([]any, 0, n*2+8)

	// Draft: shot-1 -> shot-1-extract -> shot-2 -> shot-2-extract -> ... -> shot-N.
	// (docs/aether-validation-report.md §四: must be a linear DAG chain, not a
	// Loop — shot i's first_frame is shot i-1's runtime-produced last frame.)
	prevExtract := "" // name of the previous shot's extract task, "" for shot 1
	for _, p := range plans {
		shotName := fmt.Sprintf("shot-%d", p.Index)
		// Every field gen-shot declares must get a value, even a logically
		// "unused" one (empty string / empty array) — Aether's Binder leaves
		// an omitted argument's bound Value as zero-length bytes (not JSON
		// "" or null), which then fails json.Unmarshal inside BindInputs
		// ("unexpected end of JSON input"). Confirmed directly against the
		// real engine (docs/aether-validation-report.md §八's empty-loop
		// investigation surfaced the same failure mode for a different
		// path); omitting an argument here is not an option, only "supply a
		// deliberately-empty value" is.
		args := []any{
			literal("prompt", p.Prompt),
			literal("duration", durationStr),
			literal("resolution", "768P"),
			literal("ratio", ""),
			literal("first-frame-asset-id", ""),
			literal("reference-image-asset-ids", []string{}),
			// user-id: the one genuinely dynamic value in this document
			// (which user submitted the job) — carried through
			// workflow.parameters rather than baked in as a literal.
			fromWorkflow("user-id", "user-id"),
			literal("shot-index", strconv.Itoa(p.Index)),
		}
		switch p.Mode {
		case "r2va":
			args[5] = literal("reference-image-asset-ids", []string{p.ReferenceImageAssetID})
		case "t2va":
			args[3] = literal("ratio", ratio)
		case "i2va":
			args[4] = fromTask("first-frame-asset-id", prevExtract, "last-frame-asset-id")
		}

		deps := []string{}
		if prevExtract != "" {
			deps = []string{prevExtract}
		}
		mainTasks = append(mainTasks, map[string]any{
			"name": shotName, "template": "gen-shot", "dependencies": deps,
			"arguments": map[string]any{"parameters": args},
		})

		if p.Index < n {
			extractName := fmt.Sprintf("shot-%d-extract", p.Index)
			mainTasks = append(mainTasks, map[string]any{
				"name": extractName, "template": "extract-shot", "dependencies": []string{shotName},
				"arguments": map[string]any{"parameters": []any{
					fromTask("video-asset-id", shotName, "asset-id"),
					fromWorkflow("user-id", "user-id"),
				}},
			})
			prevExtract = extractName
		}
	}

	lastShot := fmt.Sprintf("shot-%d", n)
	// redo and upgrade are chained sequentially (upgrade depends on redo, not
	// on gate directly) rather than run in parallel off gate — deliberately,
	// not for lack of independence (they operate on disjoint shot sets).
	// docs/aether-validation-report.md §八's empty-loop patch calls
	// advanceScope recursively from inside a zero-iteration Loop's
	// activation, which itself can run *inside* createEligibleTasks's own
	// toExecute loop; if two sibling Loop tasks both become eligible in the
	// same batch (both depending only on gate) and either is zero-iteration,
	// that recursive call can discover and activate the second sibling
	// before the outer loop's own iteration reaches it, causing a
	// double-activation. Confirmed directly: a real 2-shot run with both
	// buckets empty ("keep everything") deadlocked the workflow into a
	// corrupted Error state. Chaining them so at most one Loop task per DAG
	// task ever becomes newly-eligible in a single batch removes the
	// precondition for the reentrant double-activation entirely — cheaper
	// than a deeper engine patch, at the cost of redo/upgrade no longer
	// running concurrently (both are provider-call-time-dominated anyway).
	mainTasks = append(mainTasks,
		map[string]any{"name": "gate", "template": "preview-gate", "dependencies": []string{lastShot}},
		map[string]any{
			"name": "redo", "template": "redo-loop", "dependencies": []string{"gate"},
			"arguments": map[string]any{"parameters": []any{fromTask("redo-list", "gate", "redo-items")}},
		},
		map[string]any{
			"name": "upgrade", "template": "upgrade-loop", "dependencies": []string{"redo"},
			"arguments": map[string]any{"parameters": []any{fromTask("upgrade-list", "gate", "upgrade-items")}},
		},
		map[string]any{
			"name": "concat", "template": "concat-video", "dependencies": []string{"upgrade"},
			"arguments": map[string]any{"parameters": []any{
				fromTask("keep-shot-index", "gate", "keep-shot-index"),
				fromTask("keep-asset-id", "gate", "keep-asset-id"),
				fromTask("redone-shot-index", "redo", "shot-index"),
				fromTask("redone-asset-id", "redo", "asset-id"),
				fromTask("upgraded-shot-index", "upgrade", "shot-index"),
				fromTask("upgraded-asset-id", "upgrade", "asset-id"),
				literal("total-shots", strconv.Itoa(n)),
				fromWorkflow("user-id", "user-id"),
			}},
		},
	)

	templates = append(templates,
		map[string]any{"dag": map[string]any{"name": "main", "tasks": mainTasks}},
		map[string]any{"task": map[string]any{
			"name": "gen-shot", "executor": map[string]any{"type": "minimax.video"},
			"inputs": map[string]any{"parameters": genShotInputDecl()},
			"retry":  map[string]any{"limit": 1}, "timeout": "30m",
		}},
		map[string]any{"task": map[string]any{
			"name": "extract-shot", "executor": map[string]any{"type": "local.ffmpeg.extract"},
			"inputs": map[string]any{"parameters": []any{
				map[string]any{"name": "video-asset-id", "type": "string"},
				map[string]any{"name": "user-id", "type": "string"},
			}},
			"retry": map[string]any{"limit": 1}, "timeout": "2m",
		}},
		map[string]any{"task": map[string]any{
			"name": "preview-gate", "executor": map[string]any{"type": "human.gate"},
		}},
		map[string]any{"loop": map[string]any{
			"name":      "redo-loop",
			"inputs":    map[string]any{"parameters": []any{map[string]any{"name": "redo-list", "type": "array"}}},
			"itemsFrom": "{{inputs.parameters.redo-list}}", "body": "redo-shot",
			"aggregate": map[string]any{"strategy": "list", "parameters": []string{"asset-id", "shot-index"}},
			"outputs": map[string]any{"parameters": []any{
				map[string]any{"name": "asset-id", "type": "array"},
				map[string]any{"name": "shot-index", "type": "array"},
			}},
		}},
		map[string]any{"task": map[string]any{
			"name": "redo-shot", "executor": map[string]any{"type": "minimax.video"},
			"inputs": map[string]any{"parameters": genShotInputDecl()},
			"retry":  map[string]any{"limit": 1}, "timeout": "30m",
		}},
		map[string]any{"loop": map[string]any{
			"name":      "upgrade-loop",
			"inputs":    map[string]any{"parameters": []any{map[string]any{"name": "upgrade-list", "type": "array"}}},
			"itemsFrom": "{{inputs.parameters.upgrade-list}}", "body": "upgrade-shot",
			"aggregate": map[string]any{"strategy": "list", "parameters": []string{"asset-id", "shot-index"}},
			"outputs": map[string]any{"parameters": []any{
				map[string]any{"name": "asset-id", "type": "array"},
				map[string]any{"name": "shot-index", "type": "array"},
			}},
		}},
		map[string]any{"task": map[string]any{
			"name": "upgrade-shot", "executor": map[string]any{"type": "minimax.video.regen"},
			"inputs": map[string]any{"parameters": []any{
				map[string]any{"name": "prompt", "type": "string"},
				map[string]any{"name": "duration", "type": "string"},
				map[string]any{"name": "ratio", "type": "string"},
				map[string]any{"name": "first-frame-asset-id", "type": "string"},
				map[string]any{"name": "reference-image-asset-ids", "type": "array"},
				map[string]any{"name": "base-video-asset-id", "type": "string"},
				map[string]any{"name": "user-id", "type": "string"},
				map[string]any{"name": "shot-index", "type": "string"},
			}},
			"retry": map[string]any{"limit": 1}, "timeout": "30m",
		}},
		map[string]any{"task": map[string]any{
			"name": "concat-video", "executor": map[string]any{"type": "local.ffmpeg.concat"},
			"inputs": map[string]any{"parameters": []any{
				map[string]any{"name": "keep-shot-index", "type": "array"},
				map[string]any{"name": "keep-asset-id", "type": "array"},
				map[string]any{"name": "redone-shot-index", "type": "array"},
				map[string]any{"name": "redone-asset-id", "type": "array"},
				map[string]any{"name": "upgraded-shot-index", "type": "array"},
				map[string]any{"name": "upgraded-asset-id", "type": "array"},
				map[string]any{"name": "total-shots", "type": "string"},
				map[string]any{"name": "user-id", "type": "string"},
			}},
			"retry": map[string]any{"limit": 1}, "timeout": "10m",
		}},
	)

	doc := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name": "video-sequence",
			"annotations": map[string]any{
				"description": "F6.7/F6.8 连续影片：code-generated per job, see internal/application/jobsvc/video_sequence.go",
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
		// this shape cannot fail; a panic here means a real programming bug
		// (e.g. a NaN float, a channel), not a runtime/input condition.
		panic(fmt.Sprintf("buildVideoSequenceWorkflow: marshal: %v", err))
	}
	return out
}

// --- Resume ---

// ResumeVideoSequenceRequest is POST /jobs/{bizID}/resume's body (§13.4).
// Shot indices are 1-based, matching shotPlan.Index.
type ResumeVideoSequenceRequest struct {
	SelectedShots       []int          `json:"selected_shots"`        // upgrade to 2K
	RedoShots           []int          `json:"redo_shots"`            // regenerate at 768P
	RedoPromptOverrides map[int]string `json:"redo_prompt_overrides"` // shot index -> replacement prompt text (optional; default reuses the original)
}

// Resume implements the preview gate's decision (§13.4/§5.5): buckets every
// shot into keep/redo/upgrade, resolves each bucket's genuinely-runtime
// values (previous shot's last frame, the shot's own original asset) from
// the live engine state, and hands the whole thing to gate as its Resume
// payload — gate just echoes it back as outputs for redo-loop/upgrade-loop/
// concat to consume (see this file's package doc and
// docs/aether-validation-report.md §六 for why gate itself stays this dumb).
func (s *Service) Resume(ctx context.Context, userID uint64, bizID string, req ResumeVideoSequenceRequest) error {
	var job persistence.Job
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ?", bizID, userID).First(&job).Error; err != nil {
		return fmt.Errorf("job %q not found: %w", bizID, err)
	}
	if job.WorkflowName != "video.sequence" {
		return fmt.Errorf("job %q is not a video.sequence job", bizID)
	}

	var spec Spec
	if err := json.Unmarshal(job.Spec, &spec); err != nil {
		return fmt.Errorf("unmarshal job spec: %w", err)
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return err
	}
	characterRefAssetID := s.characterRefAsset(ctx, userID, spec.Characters)
	plans := planShots(spec.Shots, characters, presets, characterRefAssetID, spec.RecalibrateEvery)

	duration := spec.DurationSeconds
	if duration <= 0 {
		duration = 5
	}
	durationStr := strconv.Itoa(duration)
	ratio := spec.Ratio
	if ratio == "" {
		ratio = "16:9"
	}
	userIDStr := strconv.FormatUint(userID, 10)

	run, err := s.eng.Get(ctx, workflow.RunID(job.WorkflowRunID))
	if err != nil {
		return fmt.Errorf("get workflow run: %w", err)
	}
	nodeOutputs := map[string]map[string]any{}
	for _, node := range run.Nodes {
		nodeOutputs[node.Name] = node.Outputs
	}
	strOut := func(taskName, field string) string {
		if v, ok := nodeOutputs[taskName][field]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	upgradeSet := toSet(req.SelectedShots)
	redoSet := toSet(req.RedoShots)

	redoItems := make([]map[string]any, 0, len(req.RedoShots))
	upgradeItems := make([]map[string]any, 0, len(req.SelectedShots))
	keepShotIndex := make([]string, 0, len(plans))
	keepAssetID := make([]string, 0, len(plans))

	for _, p := range plans {
		shotName := fmt.Sprintf("shot-%d", p.Index)
		extractName := fmt.Sprintf("shot-%d-extract", p.Index-1)
		firstFrame := ""
		if p.Mode == "i2va" {
			firstFrame = strOut(extractName, "last-frame-asset-id")
		}
		refImages := []string{}
		if p.Mode == "r2va" {
			refImages = []string{p.ReferenceImageAssetID}
		}
		originalAssetID := strOut(shotName, "asset-id")

		switch {
		case redoSet[p.Index]:
			promptText := p.Prompt
			if override, ok := req.RedoPromptOverrides[p.Index]; ok && override != "" {
				promptText = override
			}
			redoItems = append(redoItems, map[string]any{
				"prompt": promptText, "duration": durationStr, "resolution": "768P", "ratio": ratio,
				"first-frame-asset-id": firstFrame, "reference-image-asset-ids": refImages,
				"user-id": userIDStr, "shot-index": strconv.Itoa(p.Index),
			})
		case upgradeSet[p.Index]:
			upgradeItems = append(upgradeItems, map[string]any{
				"prompt": p.Prompt, "duration": durationStr, "ratio": ratio,
				"first-frame-asset-id": firstFrame, "reference-image-asset-ids": refImages,
				"base-video-asset-id": originalAssetID,
				"user-id":             userIDStr, "shot-index": strconv.Itoa(p.Index),
			})
		default:
			keepShotIndex = append(keepShotIndex, strconv.Itoa(p.Index))
			keepAssetID = append(keepAssetID, originalAssetID)
		}
	}

	// §12.3's second hold: "用户勾选升 2K 的段后，Resume 前再做一次预扣" — the
	// draft-time hold only ever covered 768P, so upgrading needs its own
	// hold for the delta up to full 2K cost. redo doesn't need one: it's
	// still 768P, already covered by the original estimate.
	if len(upgradeItems) > 0 {
		upgradeCredits := creditsvc.EstimateVideoCredits(duration, "2K") * len(upgradeItems)
		if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold:upgrade", "job", bizID, upgradeCredits, "video.sequence upgrade to 2K"); err != nil {
			return fmt.Errorf("hold upgrade credits: %w", err)
		}
		if err := s.db.WithContext(ctx).Model(&persistence.Job{}).Where("id = ?", job.ID).
			UpdateColumn("credit_held", gorm.Expr("credit_held + ?", upgradeCredits)).Error; err != nil {
			return fmt.Errorf("record upgrade hold: %w", err)
		}
	}

	payload := map[string]any{
		"decision":        "made",
		"redo-items":      redoItems,
		"upgrade-items":   upgradeItems,
		"keep-shot-index": keepShotIndex,
		"keep-asset-id":   keepAssetID,
	}
	return s.eng.Resume(ctx, workflow.RunID(job.WorkflowRunID), "gate", payload)
}

func toSet(indexes []int) map[int]bool {
	out := make(map[int]bool, len(indexes))
	for _, i := range indexes {
		out[i] = true
	}
	return out
}
