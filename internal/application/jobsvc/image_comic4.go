// image_comic4.go implements image.comic4 (F5.3): a Go-generated DAG where
// every panel after the first automatically chains to the immediately
// preceding panel's own output and gets an H3-Context-IR-enriched prompt
// aware of the story so far. There is only one mode — the independent-panel
// "quick mode" that used to coexist with this has been removed. Panel count
// is not fixed at 4: any count from minComic4Panels to capability.ImageMaxN
// is accepted, manual or auto-split.
//
// This needs a Go-generated DAG for the same reason image_sequence.go's
// cross-shot referencing does: panel 2's own subject_reference has to be
// panel 1's runtime-produced asset id, which Aether's Loop primitive can't
// express (only a flat itemsFrom list). image_generation's subject_
// reference is capped at exactly one reference image per call, so there is
// no equivalent to video.sequence's multi-image bundle here. MiniMax's
// H3-Context-IR (minimax.prompt_enhance) is used purely as a text-prompt
// writer — its input can still see the single chained reference image (via
// local.collect_refs, which turns a scalar fromTask value into a
// one-element array), so H3 sees the running story outline, this panel's
// own directed text, and that one reference image, and its output text
// becomes the real image_generation call's prompt.
//
// Auto-split (Spec.Story, no Panels) needs all N panel texts resolved
// before the DAG can be built, since a Loop distributing one array element
// per independent iteration has no equivalent for "panel 2 needs panel 1's
// own future output" chains — so this calls minimax.SplitStory
// synchronously in Go before building the workflow JSON.
package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// minComic4Panels is the low end of "漫畫可以自己選數量" — 1 panel isn't a
// comic (and wouldn't need compose-grid at all), so 2 is the real floor.
const minComic4Panels = 2

// Spec.ImageProvider's own doc (jobsvc.go) covers the "why" — these are just
// the two recognized values, "minimax" also being what an empty/unrecognized
// value normalizes to.
const (
	imageProviderMiniMax = "minimax"
	imageProviderGemini  = "gemini"
)

// normalizeImageProvider never fails: an unrecognized or empty value falls
// back to MiniMax (comic4's provider before this option existed), matching
// this file's general posture of soft-fallback over hard validation error
// for anything that isn't user-authored panel/story content.
func normalizeImageProvider(s string) string {
	if s == imageProviderGemini {
		return imageProviderGemini
	}
	return imageProviderMiniMax
}

type panelPlan struct {
	Index      int // 1-based, 1..N
	Prompt     string
	RawText    string
	Seed       string
	Dialogue   string // "" = no speech bubble this panel (minimax.PlanComic's per-panel suggestion)
	RefAssetID string // this panel's own build-time-known literal reference image under the job's reference strategy; "" = none. In "chain" mode only panel 1's value is meaningful — later panels reference the previous panel's own runtime output instead, see buildComic4Workflow.
}

// comic4PanelCount answers "how many panels will this job actually run" for
// EstimateCredits/EstimateBreakdown, which are pure functions with no
// DB/MiniMax access and so can't know an auto-split call's real returned
// count in advance — Spec.N (or a default of 4) is the best estimate
// available, erring toward over-estimating rather than under-holding.
func comic4PanelCount(spec Spec) (int, error) {
	if len(spec.Panels) >= minComic4Panels {
		if len(spec.Panels) > capability.ImageMaxN {
			return 0, fmt.Errorf("image.comic4 supports at most %d panels, got %d", capability.ImageMaxN, len(spec.Panels))
		}
		return len(spec.Panels), nil
	}
	if spec.Story != "" {
		n := spec.N
		if n < minComic4Panels {
			n = 4
		}
		if n > capability.ImageMaxN {
			n = capability.ImageMaxN
		}
		return n, nil
	}
	return 0, fmt.Errorf("image.comic4 requires at least %d panels, or a story to auto-split", minComic4Panels)
}

// comic4StylizeRefCount is EstimateCredits/EstimateBreakdown's conservative
// upper bound for how many stylize-reference passes (buildComic4Workflow's
// own doc) a comic4 job might run: one per distinct bound character, or one
// for an ad hoc source image with no characters bound. These are pure
// functions with no DB/MiniMax access, so this can't know the AI planner's
// real reference_strategy in advance (e.g. it might land on "none" and skip
// stylizing entirely) — erring toward over-estimating is safe per the
// credits invariant (CLAUDE.md: balance+held == ledger sum), under-estimating
// is not.
func comic4StylizeRefCount(spec Spec) int {
	if len(spec.Characters) > 0 {
		return len(spec.Characters)
	}
	if spec.SourceImageAssetID != "" {
		return 1
	}
	return 0
}

func (s *Service) createImageComic4(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	// count/bounds-validation is comic4PanelCount's own job (also used by
	// EstimateCredits) — re-deriving the same min/max clamping here used to
	// drift from it under different local variable names (n vs count),
	// which desyncs the price shown to the user from what the job actually
	// runs. This only adds what comic4PanelCount can't do itself (no
	// DB/MiniMax access): actually calling SplitStory in auto-split mode.
	count, err := comic4PanelCount(spec)
	if err != nil {
		return nil, err
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	planCharInfos, slotRefAsset, err := s.resolveCharacterPlanInfo(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	// The AI comic-planner call (minimax.PlanComic) reads the user's story/
	// panels plus whichever bound characters actually have a real reference
	// image, and decides — instead of requiring the caller to pick a mode —
	// whether character consistency matters enough to anchor every panel on
	// a real image, whether more than one bound character needs its own
	// per-panel anchor, whether this is really a continuous single-take
	// scene that should chain panel-to-panel instead, what page layout fits
	// the story's pacing, and each panel's scene/action/expression/detail
	// breakdown plus an optional line of dialogue. An advisory call — see
	// its own doc — so it must never block job submission: a nil plan
	// (unavailable deployment, call failure, or unparseable response) falls
	// all the way back to this function's pre-planner behavior below
	// (legacy SplitStory auto-split + unconditional "chain" strategy +
	// equal-grid layout), which is exactly what shipped before this
	// feature existed.
	manualPanels := spec.Panels
	if len(manualPanels) < minComic4Panels {
		manualPanels = nil // Story mode — comic4PanelCount already confirmed spec.Story != "" in this branch
	}
	var plan *minimax.ComicPlan
	if s.minimax != nil {
		if p, _, planErr := minimax.PlanComic(ctx, s.minimax, minimax.PlanComicRequest{
			Story: spec.Story, Panels: manualPanels, Count: count,
			Characters: planCharInfos, HasSourceImage: spec.SourceImageAssetID != "",
		}); planErr == nil {
			plan = p
		}
	}

	var panelTexts []string
	splitCost := 0
	switch {
	case plan != nil:
		panelTexts = make([]string, len(plan.Panels))
		for i, p := range plan.Panels {
			panelTexts[i] = p.Text()
		}
	case len(manualPanels) >= minComic4Panels:
		panelTexts = manualPanels
	default:
		if s.minimax == nil {
			return nil, fmt.Errorf("story auto-split is unavailable in this deployment")
		}
		panelTexts, _, err = minimax.SplitStory(ctx, s.minimax, spec.Story, count)
		if err != nil {
			return nil, fmt.Errorf("split story into panels: %w", err)
		}
		if len(panelTexts) != count {
			return nil, fmt.Errorf("story split returned %d panels, want %d", len(panelTexts), count)
		}
	}
	// EstimateCredits/EstimateBreakdown now charge this bucket for every
	// comic4 job (their own doc) — the planner call above always attempts
	// to run, manual panels included, not just story-mode auto-split.
	splitCost = creditsvc.EstimateStorySplitCredits()

	referenceStrategy := minimax.RefStrategyChain
	layoutID := minimax.LayoutGridEqual
	if plan != nil {
		referenceStrategy = plan.ReferenceStrategy
		layoutID = plan.LayoutID
	}

	// anchorAssetID is the one real reference image available outside a
	// per-panel character assignment: an explicit ad hoc upload wins, else
	// the first bound character's own saved reference image (F3.1) — same
	// precedence video.single's F6.4 auto-reference already uses elsewhere
	// (jobsvc.go's own SourceImageAssetID doc).
	anchorAssetID := spec.SourceImageAssetID
	if anchorAssetID == "" && len(planCharInfos) > 0 {
		anchorAssetID = slotRefAsset[planCharInfos[0].Slot]
	}

	// subjectDescription is a text-only, species-agnostic backup consistency
	// anchor for anchorAssetID's own subject — see DescribeReferenceSubject's
	// own doc for why this matters specifically: MiniMax's subject_reference
	// mechanism is documented as tuned for human portraits, so a pet/animal
	// character (image.comic4's own motivating use case) can't lean on the
	// image channel alone the way a human character can. Advisory only:
	// any failure (no minimax client, asset lookup miss, vision-call error)
	// just leaves this empty, same posture as the AI planner itself.
	subjectDescription := ""
	if anchorAssetID != "" && s.minimax != nil {
		if url, err := s.resolveAssetPublicURL(ctx, anchorAssetID); err == nil && url != "" {
			subjectDescription, _ = minimax.DescribeReferenceSubject(ctx, s.minimax, url)
		}
	}

	// resolveSharedSeed's own doc covers why every panel needs the same
	// seed, including when neither spec.Seed nor a bound character set one.
	seed := resolveSharedSeed(characters, spec.Seed)
	seedStr := formatSeed(seed)

	// style is a whole-comic art-style phrase repeated into every panel's
	// own compiled prompt — found live off a job whose input asked for
	// "cute anime style" as a global preamble before its per-panel text:
	// with nothing carrying that intent past the story→panels split, every
	// panel came out photorealistic instead. minimax.PlanComic.Style is
	// already guaranteed non-empty (parseComicPlan defaults it); the
	// legacy (no-planner) fallback path needs its own default here since
	// there's no plan to read one from.
	style := minimax.DefaultComicStyle
	if plan != nil {
		style = plan.Style
	}

	plans := make([]panelPlan, len(panelTexts))
	for i, text := range panelTexts {
		styledText := text
		if subjectDescription != "" {
			styledText += "，角色具体外观（务必保持一致）：" + subjectDescription
		}
		if style != "" {
			styledText += "，" + style
		}
		compiled := prompt.Compile(prompt.Input{Text: styledText, Characters: characters, Presets: presets, Seed: seed})
		p := panelPlan{Index: i + 1, Prompt: compiled.Prompt, RawText: text, Seed: seedStr}

		switch referenceStrategy {
		case minimax.RefStrategyAnchor:
			p.RefAssetID = anchorAssetID
		case minimax.RefStrategyAnchorPerCharacter:
			slot := ""
			if plan != nil && i < len(plan.Panels) {
				slot = plan.Panels[i].CharacterSlot
			}
			if slot == "" && len(planCharInfos) > 0 {
				slot = planCharInfos[0].Slot
			}
			p.RefAssetID = slotRefAsset[slot]
			if p.RefAssetID == "" {
				p.RefAssetID = anchorAssetID // this panel's own character has no saved image — fall back rather than drop the reference entirely
			}
		case minimax.RefStrategyChain:
			if i == 0 {
				p.RefAssetID = anchorAssetID
			}
			// RefStrategyNone and every later chain-mode panel: leave
			// RefAssetID empty — chain mode resolves later panels' own
			// reference at DAG-build time in buildComic4Workflow instead.
		}
		if plan != nil && i < len(plan.Panels) {
			p.Dialogue = plan.Panels[i].Dialogue
		}
		plans[i] = p
	}

	imageProvider := normalizeImageProvider(spec.ImageProvider)
	wfJSON := buildComic4Workflow(plans, referenceStrategy, layoutID, style, imageProvider)

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	// One minimax.image call plus one minimax.prompt_enhance call per
	// panel, plus the split cost if auto-split ran.
	estimatedCredits := creditsvc.EstimatePerNodeImageCredits(len(plans)) + len(plans)*creditsvc.EstimatePromptEnhanceCredits() + splitCost
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "job", "image.comic4"); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}

	args := map[string]any{"user-id": strconv.FormatUint(userID, 10)}
	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: "image-comic4", JSON: wfJSON}, args)
	if err != nil {
		_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, estimatedCredits)
		return nil, fmt.Errorf("submit workflow: %w", err)
	}

	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
		ProjectID:       projectID,
		WorkflowName:    "image.comic4",
		WorkflowRunID:   string(runID),
		Title:           truncate(plans[0].RawText, 128),
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

func genOnePanelTaskTemplate() map[string]any {
	return map[string]any{
		"name": "gen-one-panel", "executor": map[string]any{"type": "minimax.image"},
		"inputs": map[string]any{"parameters": []any{
			map[string]any{"name": "prompt", "type": "string"},
			map[string]any{"name": "source-image-asset-id", "type": "string"},
			map[string]any{"name": "user-id", "type": "string"},
			map[string]any{"name": "n", "type": "string", "value": "1"},
			map[string]any{"name": "seed", "type": "string", "value": ""},
			// expected-style gates minimax.image's own automatic-reroll
			// check (image.go's checkIllustrationStyle): empty means "skip
			// the check" (every other minimax.image caller platform-wide),
			// non-empty asks the plugin to verify the result actually looks
			// illustrated rather than photorealistic and silently reroll
			// with a fresh seed (up to a small cap) if it doesn't — found
			// live that a correct, explicit style instruction in the prompt
			// still only converted ~3 of 4 panels away from photorealism on
			// its own when anchored on a real photo.
			map[string]any{"name": "expected-style", "type": "string", "value": ""},
			// expected-dialogue is expected-style's sibling gate, same
			// posture: empty skips the check (every caller except a panel
			// with dialogue), non-empty asks the plugin to actually read the
			// speech-bubble text and reroll if it's garbled or wrong —
			// dialogueInstruction's own prompt text asks the model to draw
			// it, this is the verify-and-retry half of that.
			map[string]any{"name": "expected-dialogue", "type": "string", "value": ""},
		}},
		"phaseConditions": map[string]any{
			"succeeded": `outputs.parameters["success-count"] == outputs.parameters["requested-n"]`,
			"failed":    `outputs.parameters["success-count"] < outputs.parameters["requested-n"]`,
		},
		"retry": map[string]any{"limit": 2}, "timeout": "3m",
	}
}

// genOnePanelGeminiTaskTemplate is genOnePanelTaskTemplate's Gemini
// counterpart (Spec.ImageProvider's own doc covers why this is a separate
// executor type rather than a branch inside minimax.image): no
// expected-style/expected-dialogue params, since gemini.ImagePlugin doesn't
// implement minimax.ImagePlugin's vision-check-and-reroll loop — this
// provider was added specifically to see how it performs on style/identity/
// dialogue fidelity zero-shot, without MiniMax's own mitigations muddying
// the comparison.
func genOnePanelGeminiTaskTemplate() map[string]any {
	return map[string]any{
		"name": "gen-one-panel-gemini", "executor": map[string]any{"type": "gemini.image"},
		"inputs": map[string]any{"parameters": []any{
			map[string]any{"name": "prompt", "type": "string"},
			map[string]any{"name": "source-image-asset-id", "type": "string"},
			map[string]any{"name": "user-id", "type": "string"},
			map[string]any{"name": "n", "type": "string", "value": "1"},
			map[string]any{"name": "seed", "type": "string", "value": ""},
		}},
		"phaseConditions": map[string]any{
			"succeeded": `outputs.parameters["success-count"] == outputs.parameters["requested-n"]`,
			"failed":    `outputs.parameters["success-count"] < outputs.parameters["requested-n"]`,
		},
		"retry": map[string]any{"limit": 2}, "timeout": "3m",
	}
}

func enhancePanelTaskTemplate() map[string]any {
	return map[string]any{
		"name": "enhance-panel", "executor": map[string]any{"type": "minimax.prompt_enhance"},
		"inputs": map[string]any{"parameters": enhanceInputDecl()},
		// retry.limit matches gen-one-panel's: this node sits in front of
		// every panel's own image call and is exposed to the same
		// provider-latency risk, so a single transient blip here shouldn't
		// get fewer retry chances than the node after it (was 1 — found live
		// off a job that failed outright on panel 1's enhance step with zero
		// images ever generated).
		"retry": map[string]any{"limit": 2}, "timeout": "5m",
	}
}

// padCollectRefsArgs fills whatever image/video slots a caller didn't
// already build with empty literals, so every collect-refs call site
// supplies all collectRefsImageSlots+maxReferenceVideoClips declared
// inputs regardless of how many are actually meaningful — an omitted
// Aether argument's bound Value is zero-length bytes, which fails
// BindInputs' json.Unmarshal downstream. imageArgs must already be built
// as image-1, image-2, ... in that order (each caller here only ever fills
// a sequential prefix, never a gap).
func padCollectRefsArgs(imageArgs, videoArgs []any) []any {
	args := append([]any{}, imageArgs...)
	for i := len(imageArgs); i < collectRefsImageSlots; i++ {
		args = append(args, literal(fmt.Sprintf("image-%d", i+1), ""))
	}
	args = append(args, videoArgs...)
	for i := len(videoArgs); i < maxReferenceVideoClips; i++ {
		args = append(args, literal(fmt.Sprintf("video-%d", i+1), ""))
	}
	return args
}

// buildComic4Workflow builds each panel's enhance-panel-N + gen-one-panel-N
// pair according to referenceStrategy, decided by minimax.PlanComic (or the
// fallback default of RefStrategyChain when the planner didn't run/didn't
// return a usable plan):
//   - RefStrategyChain (the original, only behavior before the AI planner
//     existed): panel 1 anchors on plans[0].RefAssetID (may be empty), every
//     later panel chains onto the immediately preceding panel's own runtime
//     output — a real DAG dependency, so these panels run strictly in
//     sequence.
//   - RefStrategyAnchor / RefStrategyAnchorPerCharacter / RefStrategyNone:
//     every panel's reference image (if any) is plans[i].RefAssetID, a
//     literal already resolved at build time by createImageComic4 — no
//     panel depends on any other panel's output, so all N panels' own
//     enhance+gen chains run as independent parallel branches of the DAG,
//     converging only at collect-panels/compose. This also means the whole
//     job finishes faster and is less exposed to one slow panel dragging
//     down every panel after it (the same concern M0's HTTP-timeout fix
//     addresses at the transport level).
func buildComic4Workflow(plans []panelPlan, referenceStrategy, layoutID, style, imageProvider string) []byte {
	mainTasks := make([]any, 0, len(plans)*3+4)

	// panelTemplate is which of genOnePanelTaskTemplate/
	// genOnePanelGeminiTaskTemplate every panel and stylize-reference pass in
	// this job uses — the stylize pass deliberately shares imageProvider with
	// the real panels (rather than always running on MiniMax) so a
	// gemini-provider job's whole pipeline stays on one model: comparing
	// providers should compare the two end-to-end pipelines, not a
	// MiniMax-stylized reference feeding into a Gemini panel call.
	panelTemplate := "gen-one-panel"
	if imageProvider == imageProviderGemini {
		panelTemplate = "gen-one-panel-gemini"
	}
	// qualityGateArgs appends minimax.image's own expected-style/
	// expected-dialogue literals — meaningless (and undeclared) on the
	// Gemini template, so gemini-provider jobs omit them entirely rather
	// than pass args the template doesn't accept.
	qualityGateArgs := func(dialogue string) []any {
		if imageProvider != imageProviderMiniMax {
			return nil
		}
		return []any{literal("expected-style", style), literal("expected-dialogue", dialogue)}
	}

	// stylizeRef memoizes one "convert this raw reference photo into the
	// comic's art style" pass per distinct raw asset id, so every panel that
	// shares the same character only pays for it once. Found live that
	// asking for style compliance inside each panel's own already-crowded
	// prompt (scene + outline + dialogue + style all competing at once) only
	// converted about 1 of 4 panels away from photorealism when the
	// reference was a real photo — minimax.image's subject_reference
	// mechanism visibly biases toward preserving the reference's own
	// photographic quality. A dedicated node with nothing to do but that one
	// conversion, whose OUTPUT (not the raw upload) becomes every real
	// panel's own subject_reference from then on, converts far more
	// reliably since there's no competing content to dilute the instruction.
	type stylizedRef struct{ StylizeTask, CollectTask string }
	stylizeFor := make(map[string]stylizedRef)
	stylizeReference := func(rawAssetID string) stylizedRef {
		if ref, ok := stylizeFor[rawAssetID]; ok {
			return ref
		}
		idx := len(stylizeFor) + 1
		stylizeName := fmt.Sprintf("stylize-reference-%d", idx)
		collectName := fmt.Sprintf("collect-stylize-%d", idx)
		stylizeArgs := []any{
			literal("prompt", stylizeReferencePrompt(style)),
			literal("source-image-asset-id", rawAssetID),
			fromWorkflow("user-id", "user-id"),
			literal("n", "1"),
			literal("seed", ""),
		}
		stylizeArgs = append(stylizeArgs, qualityGateArgs("")...)
		mainTasks = append(mainTasks, map[string]any{
			"name": stylizeName, "template": panelTemplate, "dependencies": []string{},
			"arguments": map[string]any{"parameters": stylizeArgs},
		})
		// Same scalar-asset-id-to-array bridge every chain-mode panel below
		// already needs local.collect_refs for.
		mainTasks = append(mainTasks, map[string]any{
			"name": collectName, "template": "collect-refs", "dependencies": []string{stylizeName},
			"arguments": map[string]any{"parameters": padCollectRefsArgs(
				[]any{fromTask("image-1", stylizeName, "asset-id")}, nil,
			)},
		})
		ref := stylizedRef{StylizeTask: stylizeName, CollectTask: collectName}
		stylizeFor[rawAssetID] = ref
		return ref
	}

	for _, p := range plans {
		panelName := fmt.Sprintf("panel-%d", p.Index)
		enhanceName := fmt.Sprintf("enhance-panel-%d", p.Index)
		collectName := fmt.Sprintf("collect-panel-%d", p.Index)

		var refImageArg map[string]any
		var refImageIDsForEnhance map[string]any
		deps := []string{}
		switch {
		case referenceStrategy != minimax.RefStrategyChain && p.RefAssetID != "":
			// Build-time-known (once stylized) for every panel, independent
			// of every other panel — see this function's own doc on why
			// that means these panels can run in parallel. All panels
			// sharing the same raw asset id wait on the same one stylize
			// pass rather than each running their own.
			ref := stylizeReference(p.RefAssetID)
			refImageArg = fromTask("source-image-asset-id", ref.StylizeTask, "asset-id")
			refImageIDsForEnhance = fromTask("reference-image-asset-ids", ref.CollectTask, "image-ids")
			deps = append(deps, ref.StylizeTask, ref.CollectTask)
		case referenceStrategy != minimax.RefStrategyChain:
			refImageArg = literal("source-image-asset-id", "")
			refImageIDsForEnhance = literal("reference-image-asset-ids", []string{})
		case p.Index == 1 && p.RefAssetID != "":
			ref := stylizeReference(p.RefAssetID)
			refImageArg = fromTask("source-image-asset-id", ref.StylizeTask, "asset-id")
			refImageIDsForEnhance = fromTask("reference-image-asset-ids", ref.CollectTask, "image-ids")
			deps = append(deps, ref.StylizeTask, ref.CollectTask)
		case p.Index == 1:
			refImageArg = literal("source-image-asset-id", "")
			refImageIDsForEnhance = literal("reference-image-asset-ids", []string{})
		default:
			// panel-(N-1)'s own "asset-id" output is a scalar string, but
			// enhance-panel's reference-image-asset-ids is declared type
			// array — Aether has no scalar-to-array coercion, so
			// local.collect_refs bridges it into a real one-element array.
			prevPanel := fmt.Sprintf("panel-%d", p.Index-1)
			mainTasks = append(mainTasks, map[string]any{
				"name": collectName, "template": "collect-refs", "dependencies": []string{prevPanel},
				"arguments": map[string]any{"parameters": padCollectRefsArgs(
					[]any{fromTask("image-1", prevPanel, "asset-id")}, nil,
				)},
			})
			refImageArg = fromTask("source-image-asset-id", prevPanel, "asset-id")
			refImageIDsForEnhance = fromTask("reference-image-asset-ids", collectName, "image-ids")
			deps = append(deps, prevPanel, collectName)
		}

		outline := buildPanelOutline(plans, p.Index)
		outlineAndPrompt := p.Prompt
		if outline != "" {
			// The explicit "只画...不要把之前几格的画面也画进来" guard matters
			// specifically when there's no reference image (RefStrategyNone):
			// found live that H3-Context-IR, given a recap-plus-new-beat
			// prompt with no <Picture> to anchor on, falls back to its
			// native video-continuation behavior and emits a literal
			// "[Shot 1] ... [Shot 2] ..." multi-scene description — which
			// minimax.image then renders as one merged frame containing
			// both the earlier and current panel's content. A bound
			// reference image reliably keeps it to a single "reference
			// generation" scene on its own (confirmed across every anchor/
			// anchor_per_character test panel), so this guard is cheap
			// insurance there too, not just a none-mode-only fix.
			outlineAndPrompt = "漫画剧情大纲（已发生的画格，仅供你理解故事上下文，不需要画出来）：" + outline +
				"\n\n本格需要表现（这一格实际要画的唯一画面）：" + p.Prompt +
				"\n\n注意：只画“本格需要表现”里的这一个瞬间，不要把大纲里之前几格的画面内容也画进同一张图里。"
		}
		outlineAndPrompt += dialogueInstruction(p.Dialogue)
		outlineAndPrompt += styleInstruction(style)
		mainTasks = append(mainTasks, map[string]any{
			"name": enhanceName, "template": "enhance-panel", "dependencies": deps,
			"arguments": map[string]any{"parameters": []any{
				literal("prompt", outlineAndPrompt),
				literal("duration", "5"), // discarded — enhance-panel never produces video, see prompt_enhance.go's own doc
				literal("ratio", "1:1"),
				literal("first-frame-asset-id", ""),
				literal("last-frame-asset-id", ""),
				refImageIDsForEnhance,
				literal("reference-video-asset-ids", []string{}),
				literal("reference-audio-asset-ids", []string{}),
			}},
		})

		panelArgs := []any{
			fromTask("prompt", enhanceName, "enhanced-prompt"),
			refImageArg,
			fromWorkflow("user-id", "user-id"),
			literal("seed", p.Seed),
		}
		panelArgs = append(panelArgs, qualityGateArgs(p.Dialogue)...)
		mainTasks = append(mainTasks, map[string]any{
			"name": panelName, "template": panelTemplate, "dependencies": append(deps, enhanceName),
			"arguments": map[string]any{"parameters": panelArgs},
		})
	}

	// local.compose's ComposeConfig takes one "asset-ids" array parameter,
	// not N individually-named ones, so local.collect_refs gathers every
	// panel's own asset-id into an array before compose-grid runs.
	// collectRefsImageSlots (9, capability.ImageMaxN) is comic4's own real
	// panel-count ceiling, so this always fits.
	composeDeps := make([]string, len(plans))
	collectImageArgs := make([]any, len(plans))
	for i, p := range plans {
		panelName := fmt.Sprintf("panel-%d", p.Index)
		composeDeps[i] = panelName
		collectImageArgs[i] = fromTask(fmt.Sprintf("image-%d", i+1), panelName, "asset-id")
	}
	mainTasks = append(mainTasks, map[string]any{
		"name": "collect-panels", "template": "collect-refs", "dependencies": composeDeps,
		"arguments": map[string]any{"parameters": padCollectRefsArgs(collectImageArgs, nil)},
	})
	mainTasks = append(mainTasks, map[string]any{
		"name": "compose", "template": "compose-grid", "dependencies": []string{"collect-panels"},
		"arguments": map[string]any{"parameters": []any{
			fromTask("asset-ids", "collect-panels", "image-ids"),
			literal("layout", layoutID), // local.compose looks this up against its own named-template table, falling back to the equal-grid it always used to compute unconditionally
			fromWorkflow("user-id", "user-id"),
		}},
	})

	panelTaskTemplate := genOnePanelTaskTemplate()
	if imageProvider == imageProviderGemini {
		panelTaskTemplate = genOnePanelGeminiTaskTemplate()
	}
	templates := []any{
		map[string]any{"dag": map[string]any{"name": "main", "tasks": mainTasks}},
		map[string]any{"task": panelTaskTemplate},
		map[string]any{"task": enhancePanelTaskTemplate()},
		map[string]any{"task": map[string]any{
			"name": "compose-grid", "executor": map[string]any{"type": "local.compose"},
			"inputs": map[string]any{"parameters": []any{
				map[string]any{"name": "asset-ids", "type": "array"},
				map[string]any{"name": "layout", "type": "string"},
				map[string]any{"name": "user-id", "type": "string"},
			}},
			// Previously had no retry at all — a single transient blip here
			// discarded all N already-generated (and already-paid-for) panels
			// with zero recovery, found live off a real comic4 job whose
			// panels all succeeded but the job still failed.
			"retry": map[string]any{"limit": 2}, "timeout": "1m",
		}},
		map[string]any{"task": map[string]any{
			"name": "collect-refs", "executor": map[string]any{"type": "local.collect_refs"},
			"inputs": map[string]any{"parameters": collectRefsInputDecl()},
			"retry":  map[string]any{"limit": 1}, "timeout": "30s",
		}},
	}

	doc := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name": "image-comic4",
			"annotations": map[string]any{
				"description": "F5.3 四格漫画（数量可变，见 minComic4Panels/capability.ImageMaxN）: code-generated per job, see internal/application/jobsvc/image_comic4.go",
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
		panic(fmt.Sprintf("buildComic4Workflow: marshal: %v", err))
	}
	return out
}

// buildPanelOutline joins every earlier panel's own raw text — deliberately
// uncompiled, same reasoning as video_sequence.go's own buildOutline.
func buildPanelOutline(plans []panelPlan, index int) string {
	if index <= 1 {
		return ""
	}
	parts := make([]string, 0, index-1)
	for i := 0; i < index-1; i++ {
		if t := strings.TrimSpace(plans[i].RawText); t != "" {
			parts = append(parts, fmt.Sprintf("第%d格：%s", i+1, t))
		}
	}
	return strings.Join(parts, "；")
}

// dialogueInstruction appends a comic-speech-bubble rendering instruction to
// a panel's enhance-panel prompt when the AI planner (or a hand-typed 台词)
// decided this panel has a line of dialogue. Returns "" (no-op) for an empty
// dialogue. This is "plan A" of M6's two-way dialogue-accuracy experiment —
// relies entirely on MiniMax's own image model rendering the quoted text
// correctly, which is unverified going in (industry-wide, legible in-image
// text is a known weak spot for most diffusion models, CJK especially); a
// deterministic post-processing overlay is the fallback plan if this proves
// unreliable in practice.
func dialogueInstruction(dialogue string) string {
	if dialogue = strings.TrimSpace(dialogue); dialogue == "" {
		return ""
	}
	return "\n\n画面中加入一个漫画对话框（气泡），气泡内文字必须精准显示为：「" + dialogue + "」，字体清晰可辨、完整排布在气泡内，不得出现其他文字或乱码。"
}

// styleInstruction appends an explicit, forceful art-style directive to every
// panel's enhance-panel prompt — a separate, prominent append rather than
// relying solely on style being blended into the compiled scene text
// (prompt.Compile already does that too, in createImageComic4). Found live
// that a mild style phrase buried inside a longer scene description isn't
// enough to override minimax.image's own pull toward photorealism when the
// panel's subject_reference is a real uploaded photo — H3-Context-IR's own
// "subject_definitions" output kept describing the subject as "realistic"
// despite the compiled prompt already containing a cute-illustration style
// phrase. A standalone, unambiguous "must be illustrated, never photographic,
// even with a real photo reference" instruction is the fix, mirroring how
// dialogueInstruction's own bubble-text directive needed to be explicit
// rather than folded into prose.
func styleInstruction(style string) string {
	if style = strings.TrimSpace(style); style == "" {
		return ""
	}
	return "\n\n整体画面风格（强制要求，优先级高于参考图本身的质感）：" + style +
		"。这是一格漫画画面，绝对不能画成写实照片质感，即使角色参考图是真实照片，也只借用其长相/花色/五官特征，" +
		"必须彻底转换成上述漫画画法重新演绎。"
}

// stylizeReferencePrompt is stylize-reference's own prompt (buildComic4Workflow's
// own doc covers why this runs as an isolated node rather than folding style
// compliance into each panel's own already-crowded prompt): one focused ask,
// nothing else competing for the model's attention.
func stylizeReferencePrompt(style string) string {
	if style = strings.TrimSpace(style); style == "" {
		style = minimax.DefaultComicStyle
	}
	return "参考图里的角色，" + style + "。请保留角色的外观特征（毛色/花纹/五官/体型等辨识度特征），" +
		"重新绘制成一张干净的漫画角色定妆照，人物居中、姿势自然、背景简单。" +
		"绝对不能保留照片本身的写实质感、真实光影或噪点，输出必须是彻底的漫画插画画法，不是照片。"
}
