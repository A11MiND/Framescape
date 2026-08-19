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
//
// # Narrative continuity (Spec.NarrativeContinuity)
//
// §07 gap: panel/shot-to-shot consistency across every generation flow in
// this codebase was too weak — same seed and character text, no real visual
// history. This is video.sequence's fix: instead of an r2va anchor shot
// referencing one static image, it can reference a real bundle built from
// shots already generated earlier in the *same* job (most-recent clips +
// their keyframes + the protagonist's own reference image), and that bundle
// — together with a running story outline — gets run through MiniMax's
// H3-Context-IR (minimax.prompt_enhance, already wired for video.single as
// F6.10) before the real r2va call, so the model actually sees what
// happened instead of being told about it in one static frame.
//
// This needed two things verified against the *live* MiniMax API before
// being built on, not assumed from stale doc comments:
//   - r2va's reference_video budget really is ≤3 clips totalling ≤15s (each
//     clip itself 2-15s) — this project's own shots run 4-15s each
//     (capability.VideoDurationMax), so at the realistic worst case (a user
//     running every shot at the full 15s) only one prior clip fits at all;
//     at a more typical 5s/shot, three fit. Both cases are handled by
//     selectBundleShots' own budget arithmetic below — no new video-trimming
//     infrastructure was needed, since every shot's *intended* duration is
//     already known at DAG-build time (one spec.DurationSeconds for the
//     whole job), not something only discoverable after generation.
//   - r2va's reference_image genuinely honors more than one item — verified
//     directly (a live 2-image r2va call's own usage accounting reported
//     input_image_count:2, task succeeded) — image_generation's
//     subject_reference is a different, unrelated endpoint capped at
//     exactly one (also verified directly: real call, real error 2013
//     "image_reference must be one"). The two endpoints' limits are not the
//     same thing and must never be conflated.
//
// Aether's valueFrom binds one parameter to exactly one
// tasks.X.outputs.parameters.Y path, so a bundle that draws from several
// different earlier shots' outputs can't be assembled as a single array
// argument directly — local.collect_refs (internal/infra/executor/local)
// is the intermediate collecting step this forces, mirroring
// local.ffmpeg.concat's own "several parallel dynamic inputs, one combined
// value" shape.
package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

const defaultRecalibrateEvery = 3 // §5.4: "镜头数 >4 时，每 3 段插入一次 r2va"

// countAnchorShots mirrors planShots' own isAnchor arithmetic exactly, but
// standalone — EstimateCredits/EstimateBreakdown are pure functions with no
// DB access (jobsvc.go's own doc on why), so they can't resolve whether an
// anchor actually gets a character/source reference (and so ends up r2va
// vs. falling back to t2va) the way planShots can. Every anchor is counted
// as if it will be Enhance=true regardless — the safe direction to round:
// a shot that degrades to t2va costs nothing extra here, so this can only
// ever over-estimate, never under-hold.
func countAnchorShots(shotCount, recalibrateEvery int) int {
	if recalibrateEvery <= 0 {
		recalibrateEvery = defaultRecalibrateEvery
	}
	n := 0
	for i := 0; i < shotCount; i++ {
		idx := i + 1
		if idx == 1 || (idx-1)%recalibrateEvery == 0 {
			n++
		}
	}
	return n
}

// referenceWindowSeconds/maxReferenceVideoClips mirror r2va's real
// reference_video budget: at most 3 clips, 15s combined.
const referenceWindowSeconds = 15
const maxReferenceVideoClips = 3

// maxReferenceImages is comfortably under MiniMax's documented ≤9 r2va
// reference_image cap — video.sequence's own narrative-continuity bundle
// only ever needs a handful of keyframes plus the protagonist's own
// reference image, not the full ceiling.
const maxReferenceImages = 5

// collectRefsImageSlots is local.collect_refs' own declared image-slot
// capacity — bigger than maxReferenceImages above because image.comic4
// reuses the same executor to gather however many panels the batch has (up
// to capability.ImageMaxN) into one array for its final compose-grid step.
const collectRefsImageSlots = 9

// shotPlan is the statically-decidable half of one shot's generation request
// — mode and prompt are pure functions of (shot index, characters, presets,
// recalibrateEvery, narrative-continuity settings), so both
// createVideoSequence (at Submit) and Resume (at Resume time, from the
// persisted Spec) recompute the identical plan rather than needing to store
// it separately. What ISN'T statically decidable is first-frame-asset-id
// for i2va shots (a runtime value — the previous shot's actual extracted
// last frame) and the resulting asset-id of each shot (only known once it
// actually runs); both are resolved separately from job_nodes/engine state
// where needed.
type shotPlan struct {
	Index  int // 1-based
	Prompt string
	Mode   string // "r2va" | "i2va" | "t2va" (t2va = i==1 with no character bound to anchor r2va)

	// Static single-reference anchor (Spec.SourceImageAssetID/
	// SourceVideoAssetID) — the "protagonist" identity anchor, set for every
	// r2va shot regardless of whether NarrativeContinuity is on.
	StaticRefImageAssetID string
	StaticRefVideoAssetID string

	// BundleShotIndexes: earlier shots (1-based, oldest first) whose own
	// clip + last-frame should also be referenced, on top of the static
	// anchor above. Always empty unless NarrativeContinuity is on for this
	// job; see selectBundleShots for how it's computed.
	BundleShotIndexes []int

	// Enhance is true for every r2va anchor shot once NarrativeContinuity
	// is on — even with an empty BundleShotIndexes (e.g. shot 1, nothing
	// earlier to reference yet), "protagonist image + this shot's own text"
	// alone still benefits from H3-Context-IR's structuring. Triggers
	// inserting a collect-shot-N + enhance-shot-N pair before the real
	// gen-shot call.
	Enhance bool
	// Outline is the running "story so far" text handed to the enhance
	// step alongside this shot's own Prompt — every earlier shot's own raw
	// (uncompiled) text, joined. Only meaningful when Enhance is true.
	Outline string
}

// planShots implements §5.4's mixed continuity strategy: shot 1 (and every
// recalibrateEvery-th shot after it) anchors on the character's reference
// image (r2va); every other shot continues from the previous shot's tail
// frame (i2va). If no character is bound, r2va has nothing to anchor on, so
// those shots fall back to t2va (plain text, requires an explicit ratio).
//
// bundlePicks is "smart" mode's precomputed answer (nil for every other
// mode, or when the advisory LLM call found nothing usable) — resolved once
// up front (smartSelectBundles) since it needs a real MiniMax call, and
// planShots itself must stay a pure function so createVideoSequence and
// Resume can both call it and get identical results from the same Spec.
func planShots(shots []string, characters []prompt.Character, presets []prompt.Preset, characterRefAssetID string, characterRefIsVideo bool, recalibrateEvery int, narrativeContinuity bool, selectionMode string, overrides []int, bundlePicks map[int][]int, duration int) []shotPlan {
	if recalibrateEvery <= 0 {
		recalibrateEvery = defaultRecalibrateEvery
	}
	rawTexts := shots
	plans := make([]shotPlan, len(shots))
	for i, text := range shots {
		idx := i + 1
		isAnchor := idx == 1 || (idx-1)%recalibrateEvery == 0
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, MaxChars: 7000})
		p := shotPlan{Index: idx, Prompt: compiled.Prompt}

		// A bundle needs computing *before* the mode decision below, not
		// after: without this, an anchor with no bound character and no
		// static SourceImageAssetID/SourceVideoAssetID always fell back to
		// t2va (no reference at all) even once earlier shots existed to
		// reference — NarrativeContinuity's whole point is that a shot's
		// *own already-generated predecessors* are a real reference on
		// their own, not merely an enrichment layered on top of a
		// pre-existing static anchor. Only shot 1 (or any anchor with
		// narrativeContinuity off) can have an empty bundle and no static
		// ref — that's the one case with genuinely nothing to anchor on.
		var bundleIndexes []int
		if isAnchor && narrativeContinuity {
			bundleIndexes = selectBundleShots(selectionMode, idx, duration, overrides, bundlePicks)
		}
		hasReference := characterRefAssetID != "" || len(bundleIndexes) > 0

		switch {
		case isAnchor && hasReference && characterRefAssetID != "" && characterRefIsVideo:
			p.Mode = "r2va"
			p.StaticRefVideoAssetID = characterRefAssetID
		case isAnchor && hasReference:
			p.Mode = "r2va"
			if characterRefAssetID != "" {
				p.StaticRefImageAssetID = characterRefAssetID
			}
			// else: r2va anchored purely on the dynamic bundle below, no
			// static literal at all — e.g. shot 4 referencing shots 1-3's
			// own already-generated clips with no protagonist image ever set.
		case isAnchor:
			p.Mode = "t2va"
		default:
			p.Mode = "i2va"
		}
		if p.Mode == "r2va" && narrativeContinuity {
			p.Enhance = true
			p.BundleShotIndexes = bundleIndexes
			p.Outline = buildOutline(rawTexts, idx)
		}
		plans[i] = p
	}
	return plans
}

// selectBundleShots implements Spec.ReferenceSelectionMode: for anchor shot
// anchorIdx (1-based), returns which earlier shots (1-based, oldest first)
// belong in its narrative-continuity bundle, sized to what the real r2va
// reference_video budget can actually hold.
func selectBundleShots(mode string, anchorIdx int, duration int, overrides []int, bundlePicks map[int][]int) []int {
	// A manual #-mention override always wins for this specific shot,
	// whatever the mode is set to.
	if anchorIdx-1 >= 0 && anchorIdx-1 < len(overrides) && overrides[anchorIdx-1] > 0 {
		return []int{overrides[anchorIdx-1]}
	}
	if mode == "manual" {
		return nil // no override set for this shot, and manual mode has no automatic fallback
	}
	if mode == "smart" {
		if picks, ok := bundlePicks[anchorIdx]; ok && len(picks) > 0 {
			return picks
		}
		// smartSelectBundles found nothing usable for this shot (LLM call
		// failed, or just didn't mention it) — fall through to the same
		// window arithmetic "window" mode uses, rather than leaving this
		// anchor with no bundle at all.
	}
	if duration <= 0 {
		duration = 5
	}
	maxClips := referenceWindowSeconds / duration
	if maxClips > maxReferenceVideoClips {
		maxClips = maxReferenceVideoClips
	}
	if maxClips < 1 {
		maxClips = 1
	}
	var out []int
	for i := anchorIdx - 1; i >= 1 && len(out) < maxClips; i-- {
		out = append([]int{i}, out...) // prepend so the result stays oldest-first
	}
	return out
}

// buildOutline joins every shot's own raw text strictly before anchorIdx —
// deliberately the uncompiled text (not prompt.Compile's character-expanded
// version): identity continuity is already the reference bundle's job
// (real images H3-Context-IR can actually see), so the outline's only job
// is plot/scene continuity in plain language, without bloating the H3 call
// with repeated character-description text it doesn't need.
func buildOutline(shots []string, anchorIdx int) string {
	if anchorIdx <= 1 {
		return ""
	}
	parts := make([]string, 0, anchorIdx-1)
	for i := 0; i < anchorIdx-1; i++ {
		if t := strings.TrimSpace(shots[i]); t != "" {
			parts = append(parts, fmt.Sprintf("第%d段：%s", i+1, t))
		}
	}
	return strings.Join(parts, "；")
}

// smartSelectBundles implements ReferenceSelectionMode "smart": one
// MiniMax-M3 call, given every shot's own text, asks which earlier shot(s)
// each anchor shot should reference for narrative/visual continuity,
// instead of selectBundleShots' own "most recent" assumption. Resolved
// once, synchronously, before the DAG is built — Aether's fromTask needs to
// know exactly which task to reference at JSON-construction time, so this
// can't be a runtime DAG decision the way the rest of this file's choices
// are (see the package doc). Returns nil on any error or empty/unusable
// response — a failed advisory call must never block job submission;
// selectBundleShots' own window arithmetic is the fallback.
func (s *Service) smartSelectBundles(ctx context.Context, shots []string, anchorIndexes []int) map[int][]int {
	if s.minimax == nil || len(anchorIndexes) == 0 {
		return nil
	}
	var b strings.Builder
	for i, t := range shots {
		fmt.Fprintf(&b, "第%d段：%s\n", i+1, t)
	}
	anchorList := make([]string, len(anchorIndexes))
	for i, a := range anchorIndexes {
		anchorList[i] = strconv.Itoa(a)
	}
	instruction := fmt.Sprintf(
		"下面是一段连续视频的分镜文字描述，共 %d 段：\n\n%s\n"+
			"请只针对这些锚点镜头（第 %s 段）分别判断：为了画面和情节连贯，这一段最需要参考前面哪 1-2 段（只能是编号更小的段）。"+
			"只输出一个 JSON 对象，key 是锚点镜头编号（字符串），value 是建议参考的更早镜头编号数组（最多 2 个，可以是空数组），不要输出任何其他文字。",
		len(shots), b.String(), strings.Join(anchorList, "、"))

	resp, err := s.minimax.ChatCompletion(ctx, minimax.ChatCompletionRequest{
		Model:               "MiniMax-M3",
		Messages:            []minimax.ChatMessage{{Role: "user", Content: instruction}},
		Temperature:         0.3,
		MaxCompletionTokens: 500,
		Thinking:            &minimax.ThinkingConfig{Type: "disabled"},
	})
	if err != nil || len(resp.Choices) == 0 {
		return nil
	}
	return parseSmartPicks(resp.Choices[0].Message.Content, anchorIndexes)
}

// parseSmartPicks extracts the {"idx": [a,b], ...} object the smart-mode
// instruction asked for, tolerating a model that wraps it in prose or a
// ```json fence despite being asked not to — same defensive parsing
// reasoning as story_split.go's own parsePanels, applied to a different
// shape. Silently drops anything that isn't a real, backward, in-range
// reference (validateShotReferenceOverrides' own rule) rather than letting
// a hallucinated index reach buildVideoSequenceWorkflow.
func parseSmartPicks(raw string, anchorIndexes []int) map[int][]int {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return nil
	}
	var parsed map[string][]int
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return nil
	}
	validAnchor := make(map[int]bool, len(anchorIndexes))
	for _, a := range anchorIndexes {
		validAnchor[a] = true
	}
	out := make(map[int][]int, len(parsed))
	for key, picks := range parsed {
		anchor, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || !validAnchor[anchor] {
			continue
		}
		valid := make([]int, 0, len(picks))
		for _, p := range picks {
			if p >= 1 && p < anchor {
				valid = append(valid, p)
			}
			if len(valid) == 2 {
				break
			}
		}
		if len(valid) > 0 {
			out[anchor] = valid
		}
	}
	return out
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

// resolveCharacterRef is createVideoSequence's and Resume's shared
// characterRefAssetID/characterRefIsVideo resolution — factored out so the
// two stay identical by construction rather than by careful copy-pasting.
func (s *Service) resolveCharacterRef(ctx context.Context, userID uint64, spec Spec) (assetID string, isVideo bool) {
	assetID = spec.SourceImageAssetID
	switch {
	case assetID != "":
		// image wins, nothing to do
	case spec.SourceVideoAssetID != "":
		assetID = spec.SourceVideoAssetID
		isVideo = true
	default:
		assetID = s.characterRefAsset(ctx, userID, spec.Characters)
	}
	return assetID, isVideo
}

// planVideoSequence is createVideoSequence's and Resume's shared plan
// derivation — resolves the character anchor, runs "smart" mode's advisory
// LLM call if requested, and calls planShots. Both callers must derive the
// identical plan from the same persisted Spec.
func (s *Service) planVideoSequence(ctx context.Context, userID uint64, spec Spec, characters []prompt.Character, presets []prompt.Preset) []shotPlan {
	characterRefAssetID, characterRefIsVideo := s.resolveCharacterRef(ctx, userID, spec)

	duration := spec.DurationSeconds
	if duration <= 0 {
		duration = 5
	}
	if duration > capability.VideoDurationMax {
		duration = 15
	}

	var bundlePicks map[int][]int
	if spec.NarrativeContinuity && spec.ReferenceSelectionMode == "smart" {
		recalibrateEvery := spec.RecalibrateEvery
		if recalibrateEvery <= 0 {
			recalibrateEvery = defaultRecalibrateEvery
		}
		var anchors []int
		for i := range spec.Shots {
			idx := i + 1
			if idx == 1 || (idx-1)%recalibrateEvery == 0 {
				anchors = append(anchors, idx)
			}
		}
		bundlePicks = s.smartSelectBundles(ctx, spec.Shots, anchors)
	}

	return planShots(spec.Shots, characters, presets, characterRefAssetID, characterRefIsVideo,
		spec.RecalibrateEvery, spec.NarrativeContinuity, spec.ReferenceSelectionMode, spec.ShotReferenceOverrides, bundlePicks, duration)
}

// validateShotReferenceOverrides mirrors image.sequence's
// validateShotSourceRefs exactly (image_sequence.go) — same backward-only
// rule, same reasoning: the frontend picker only ever offers earlier
// shots, this is the server-side backstop for any caller that bypasses it.
func validateShotReferenceOverrides(overrides []int, shotCount int) error {
	for i, r := range overrides {
		if r == 0 {
			continue
		}
		if i >= shotCount {
			return fmt.Errorf("shot_reference_overrides has more entries than shots")
		}
		if r < 1 || r > i {
			return fmt.Errorf("shot %d's reference override must point at an earlier shot (1..%d), got %d", i+1, i, r)
		}
	}
	return nil
}

func (s *Service) createVideoSequence(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	if len(spec.Shots) == 0 {
		return nil, fmt.Errorf("video.sequence requires at least 1 shot")
	}
	if err := validateShotReferenceOverrides(spec.ShotReferenceOverrides, len(spec.Shots)); err != nil {
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

	plans := s.planVideoSequence(ctx, userID, spec, characters, presets)
	// §12.3's "预览门只预扣 768P 部分积分" — the draft phase is normally
	// always 768P regardless of what gets upgraded later at Resume time.
	// SkipPreview (Spec's own doc) trades that cost protection away
	// deliberately: the draft generates directly at 2K, and the full 2K
	// cost is held upfront to match — there's no cheaper "preview" step
	// to under-hold against.
	draftResolution := "768P"
	if spec.SkipPreview {
		draftResolution = "2K"
	}
	wfJSON := buildVideoSequenceWorkflow(plans, duration, ratio, draftResolution)

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	estimatedCredits := creditsvc.EstimateVideoCredits(duration, draftResolution) * len(plans)
	estimatedCredits += estimateNarrativeEnhanceCredits(plans)
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "draft", "video.sequence"); err != nil {
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
		{"name": "reference-video-asset-ids", "type": "array"},
	}
}

// collectRefsInputDecl mirrors local.collect_refs.CollectRefsConfig's own
// slots exactly (collectRefsImageSlots image slots, maxReferenceVideoClips
// video slots).
func collectRefsInputDecl() []map[string]any {
	decl := make([]map[string]any, 0, collectRefsImageSlots+maxReferenceVideoClips)
	for i := 1; i <= collectRefsImageSlots; i++ {
		decl = append(decl, map[string]any{"name": fmt.Sprintf("image-%d", i), "type": "string"})
	}
	for i := 1; i <= maxReferenceVideoClips; i++ {
		decl = append(decl, map[string]any{"name": fmt.Sprintf("video-%d", i), "type": "string"})
	}
	return decl
}

// enhanceInputDecl mirrors minimax.prompt_enhance.PromptEnhanceConfig's own
// fields — the same multimodal reference shape genShotInputDecl's r2va
// fields use, since H3-Context-IR reasons about exactly the content the
// subsequent real gen-shot call sends.
func enhanceInputDecl() []map[string]any {
	return []map[string]any{
		{"name": "prompt", "type": "string"},
		{"name": "duration", "type": "string"},
		{"name": "ratio", "type": "string"},
		{"name": "first-frame-asset-id", "type": "string"},
		{"name": "last-frame-asset-id", "type": "string"},
		{"name": "reference-image-asset-ids", "type": "array"},
		{"name": "reference-video-asset-ids", "type": "array"},
		{"name": "reference-audio-asset-ids", "type": "array"},
	}
}

// buildBundleTasks appends collect-shot-N and enhance-shot-N call-sites for
// an r2va anchor shot that opted into NarrativeContinuity, and returns the
// two dynamic args (source-image/video-asset-ids as a *reference bundle*,
// not the static single anchor) shot-N's own real gen-shot call should use,
// plus the enhanced-prompt fromTask reference and the full dependency list
// shot-N needs. Kept as its own function since this same shape is needed by
// exactly one shot at a time but pulls together several unrelated pieces
// (collect's fixed 8 slots, enhance's multimodal shape, dependency
// bookkeeping) that would otherwise clutter buildVideoSequenceWorkflow's
// own per-shot loop.
func buildBundleTasks(p shotPlan, shotDurationStr, ratio string) (mainTasks []any, templateRefs struct {
	refImageIDsArg, refVideoIDsArg, promptArg map[string]any
	deps                                      []string
}) {
	collectName := fmt.Sprintf("collect-shot-%d", p.Index)
	enhanceName := fmt.Sprintf("enhance-shot-%d", p.Index)

	// Sized to collect-refs' own full declared capacity (collectRefsImageSlots),
	// not maxReferenceImages — every declared template input needs some
	// value, or Aether's Binder fails. useImage below is only ever called
	// once for the static ref plus once per BundleShotIndexes entry (itself
	// capped at maxReferenceVideoClips), so video.sequence's own bundle
	// naturally never approaches this many slots regardless of the array's
	// full size.
	imageSlots := make([]any, collectRefsImageSlots)
	for i := range imageSlots {
		imageSlots[i] = literal(fmt.Sprintf("image-%d", i+1), "")
	}
	videoSlots := make([]any, maxReferenceVideoClips)
	for i := range videoSlots {
		videoSlots[i] = literal(fmt.Sprintf("video-%d", i+1), "")
	}
	deps := map[string]bool{}
	nextImage, nextVideo := 0, 0
	useImage := func(arg map[string]any) {
		if nextImage < len(imageSlots) {
			imageSlots[nextImage] = arg
			nextImage++
		}
	}
	useVideo := func(arg map[string]any) {
		if nextVideo < len(videoSlots) {
			videoSlots[nextVideo] = arg
			nextVideo++
		}
	}
	if p.StaticRefImageAssetID != "" {
		useImage(literal(fmt.Sprintf("image-%d", nextImage+1), p.StaticRefImageAssetID))
	}
	if p.StaticRefVideoAssetID != "" {
		useVideo(literal(fmt.Sprintf("video-%d", nextVideo+1), p.StaticRefVideoAssetID))
	}
	for _, bi := range p.BundleShotIndexes {
		bShot := fmt.Sprintf("shot-%d", bi)
		bExtract := fmt.Sprintf("shot-%d-extract", bi)
		useVideo(fromTask(fmt.Sprintf("video-%d", nextVideo+1), bShot, "asset-id"))
		useImage(fromTask(fmt.Sprintf("image-%d", nextImage+1), bExtract, "last-frame-asset-id"))
		deps[bShot] = true
		deps[bExtract] = true
	}

	mainTasks = append(mainTasks, map[string]any{
		"name": collectName, "template": "collect-refs", "dependencies": sortedKeys(deps),
		"arguments": map[string]any{"parameters": append(imageSlots, videoSlots...)},
	})

	outlineAndPrompt := p.Prompt
	if p.Outline != "" {
		outlineAndPrompt = "剧情大纲（已发生的镜头）：" + p.Outline + "\n\n本镜头需要表现：" + p.Prompt
	}
	mainTasks = append(mainTasks, map[string]any{
		"name": enhanceName, "template": "enhance-shot", "dependencies": []string{collectName},
		"arguments": map[string]any{"parameters": []any{
			literal("prompt", outlineAndPrompt),
			literal("duration", shotDurationStr),
			literal("ratio", "adaptive"),
			literal("first-frame-asset-id", ""),
			literal("last-frame-asset-id", ""),
			fromTask("reference-image-asset-ids", collectName, "image-ids"),
			fromTask("reference-video-asset-ids", collectName, "video-ids"),
			literal("reference-audio-asset-ids", []string{}),
		}},
	})

	templateRefs.refImageIDsArg = fromTask("reference-image-asset-ids", collectName, "image-ids")
	templateRefs.refVideoIDsArg = fromTask("reference-video-asset-ids", collectName, "video-ids")
	templateRefs.promptArg = fromTask("prompt", enhanceName, "enhanced-prompt")
	templateRefs.deps = []string{enhanceName}
	return mainTasks, templateRefs
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic output matters for buildVideoSequenceWorkflow's own
	// generated-JSON stability (easier to diff/debug) — a plain sort is
	// cheap enough at this size (at most maxReferenceVideoClips*2 entries).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func buildVideoSequenceWorkflow(plans []shotPlan, duration int, ratio string, draftResolution string) []byte {
	n := len(plans)
	durationStr := strconv.Itoa(duration)

	mainTasks := make([]any, 0, n*3+4)
	templates := make([]any, 0, n*2+10)
	usesEnhance := false

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
			literal("resolution", draftResolution),
			literal("ratio", ""),
			literal("first-frame-asset-id", ""),
			literal("reference-image-asset-ids", []string{}),
			// user-id: the one genuinely dynamic value in this document
			// (which user submitted the job) — carried through
			// workflow.parameters rather than baked in as a literal.
			fromWorkflow("user-id", "user-id"),
			literal("shot-index", strconv.Itoa(p.Index)),
			literal("reference-video-asset-ids", []string{}),
		}
		deps := []string{}
		if prevExtract != "" {
			deps = []string{prevExtract}
		}

		switch p.Mode {
		case "r2va":
			if p.Enhance {
				usesEnhance = true
				bundleTasks, refs := buildBundleTasks(p, durationStr, ratio)
				mainTasks = append(mainTasks, bundleTasks...)
				args[0] = refs.promptArg
				args[5] = refs.refImageIDsArg
				args[8] = refs.refVideoIDsArg
				deps = append(deps, refs.deps...)
			} else if p.StaticRefVideoAssetID != "" {
				args[8] = literal("reference-video-asset-ids", []string{p.StaticRefVideoAssetID})
			} else {
				args[5] = literal("reference-image-asset-ids", []string{p.StaticRefImageAssetID})
			}
		case "t2va":
			args[3] = literal("ratio", ratio)
		case "i2va":
			args[4] = fromTask("first-frame-asset-id", prevExtract, "last-frame-asset-id")
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
				map[string]any{"name": "reference-video-asset-ids", "type": "array"},
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

	if usesEnhance {
		templates = append(templates,
			map[string]any{"task": map[string]any{
				"name": "collect-refs", "executor": map[string]any{"type": "local.collect_refs"},
				"inputs": map[string]any{"parameters": collectRefsInputDecl()},
				"retry":  map[string]any{"limit": 1}, "timeout": "30s",
			}},
			map[string]any{"task": map[string]any{
				"name": "enhance-shot", "executor": map[string]any{"type": "minimax.prompt_enhance"},
				"inputs": map[string]any{"parameters": enhanceInputDecl()},
				"retry":  map[string]any{"limit": 1}, "timeout": "5m",
			}},
		)
	}

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

// estimateNarrativeEnhanceCredits mirrors EstimateBreakdown's own per-flow
// discipline (jobsvc.go's doc on EstimateCredits: every branch's math must
// mirror the actual Hold exactly) — a rough per-call estimate for however
// many shots will actually run through minimax.prompt_enhance, using
// video.single's own PromptEnhance rate table (creditsvc.go) as the same
// H3-Context-IR call, just invoked more than once here.
func estimateNarrativeEnhanceCredits(plans []shotPlan) int {
	n := 0
	for _, p := range plans {
		if p.Enhance {
			n++
		}
	}
	return n * creditsvc.EstimatePromptEnhanceCredits()
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
//
// redo/upgrade items reuse each shot's own already-planned
// BundleShotIndexes (resolved by NOW-completed shots, since Resume only
// ever runs after every draft shot has finished) but pass the bundle's
// asset IDs as *literal* values here rather than another collect-refs/
// enhance-shot DAG detour: redo-loop/upgrade-loop bodies are Loop items
// (plain object arrays — §5.5's own "loop.arguments {{...}} interpolation
// doesn't work" constraint, docs/aether-validation-report.md §四 W4), and
// every value this function produces is already resolved from
// live engine state at Resume-call time — there is no DAG left to build a
// dynamic fromTask reference into.
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
	plans := s.planVideoSequence(ctx, userID, spec, characters, presets)

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
		refVideos := []string{}
		promptText := p.Prompt
		if p.Mode == "r2va" {
			if p.Enhance {
				// Every bundle member is a completed draft shot by now (redo/
				// upgrade only ever run after the whole draft chain and the
				// gate have finished) — resolve straight from live node
				// outputs instead of another fromTask detour (this func's
				// own doc).
				for _, bi := range p.BundleShotIndexes {
					if aid := strOut(fmt.Sprintf("shot-%d", bi), "asset-id"); aid != "" && len(refVideos) < maxReferenceVideoClips {
						refVideos = append(refVideos, aid)
					}
					if kf := strOut(fmt.Sprintf("shot-%d-extract", bi), "last-frame-asset-id"); kf != "" && len(refImages) < maxReferenceImages {
						refImages = append(refImages, kf)
					}
				}
				if p.StaticRefImageAssetID != "" && len(refImages) < maxReferenceImages {
					refImages = append(refImages, p.StaticRefImageAssetID)
				}
				if p.StaticRefVideoAssetID != "" && len(refVideos) < maxReferenceVideoClips {
					refVideos = append(refVideos, p.StaticRefVideoAssetID)
				}
			} else if p.StaticRefVideoAssetID != "" {
				refVideos = []string{p.StaticRefVideoAssetID}
			} else {
				refImages = []string{p.StaticRefImageAssetID}
			}
		}
		originalAssetID := strOut(shotName, "asset-id")

		switch {
		case redoSet[p.Index]:
			if override, ok := req.RedoPromptOverrides[p.Index]; ok && override != "" {
				promptText = override
			}
			redoItems = append(redoItems, map[string]any{
				"prompt": promptText, "duration": durationStr, "resolution": "768P", "ratio": ratio,
				"first-frame-asset-id": firstFrame, "reference-image-asset-ids": refImages,
				"user-id": userIDStr, "shot-index": strconv.Itoa(p.Index),
				"reference-video-asset-ids": refVideos,
			})
		case upgradeSet[p.Index]:
			upgradeItems = append(upgradeItems, map[string]any{
				"prompt": promptText, "duration": durationStr, "ratio": ratio,
				"first-frame-asset-id": firstFrame, "reference-image-asset-ids": refImages,
				"base-video-asset-id": originalAssetID,
				"user-id":             userIDStr, "shot-index": strconv.Itoa(p.Index),
				"reference-video-asset-ids": refVideos,
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
		if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold:upgrade", "job", bizID, upgradeCredits, "upgrade", "video.sequence"); err != nil {
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
