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

type panelPlan struct {
	Index   int // 1-based, 1..N
	Prompt  string
	RawText string
	Seed    string
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

func (s *Service) createImageComic4(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	panelTexts := spec.Panels
	splitCost := 0
	switch {
	case len(panelTexts) >= minComic4Panels:
		if len(panelTexts) > capability.ImageMaxN {
			return nil, fmt.Errorf("image.comic4 supports at most %d panels, got %d", capability.ImageMaxN, len(panelTexts))
		}
	case spec.Story != "":
		if s.minimax == nil {
			return nil, fmt.Errorf("story auto-split is unavailable in this deployment")
		}
		count := spec.N
		if count < minComic4Panels {
			count = 4 // auto-split's default panel count
		}
		if count > capability.ImageMaxN {
			count = capability.ImageMaxN
		}
		var err error
		panelTexts, _, err = minimax.SplitStory(ctx, s.minimax, spec.Story, count)
		if err != nil {
			return nil, fmt.Errorf("split story into panels: %w", err)
		}
		if len(panelTexts) != count {
			return nil, fmt.Errorf("story split returned %d panels, want %d", len(panelTexts), count)
		}
		splitCost = creditsvc.EstimateStorySplitCredits()
	default:
		return nil, fmt.Errorf("image.comic4 requires at least %d panels, or a story to auto-split", minComic4Panels)
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	plans := make([]panelPlan, len(panelTexts))
	for i, text := range panelTexts {
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: spec.Seed})
		plans[i] = panelPlan{Index: i + 1, Prompt: compiled.Prompt, RawText: text, Seed: formatSeed(compiled.Seed)}
	}

	wfJSON := buildComic4Workflow(plans, spec.SourceImageAssetID)

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
		"retry":  map[string]any{"limit": 1}, "timeout": "5m",
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

// buildComic4Workflow chains panel 1 -> panel 2 -> ... -> panel N, each
// preceded by an enhance-panel-N node whose single reference image is the
// same one the real gen-one-panel-N call uses: staticSourceAssetID for
// panel 1 (may be empty), the immediately preceding panel's own asset-id
// for every later panel.
func buildComic4Workflow(plans []panelPlan, staticSourceAssetID string) []byte {
	mainTasks := make([]any, 0, len(plans)*3+2)

	for _, p := range plans {
		panelName := fmt.Sprintf("panel-%d", p.Index)
		enhanceName := fmt.Sprintf("enhance-panel-%d", p.Index)
		collectName := fmt.Sprintf("collect-panel-%d", p.Index)

		var refImageArg map[string]any
		var refImageIDsForEnhance map[string]any
		deps := []string{}
		switch {
		case p.Index == 1 && staticSourceAssetID != "":
			// A literal, known at build time — no collect step needed, same
			// as any other literal array elsewhere in this file.
			refImageArg = literal("source-image-asset-id", staticSourceAssetID)
			refImageIDsForEnhance = literal("reference-image-asset-ids", []string{staticSourceAssetID})
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
			outlineAndPrompt = "漫画剧情大纲（已发生的画格）：" + outline + "\n\n本格需要表现：" + p.Prompt
		}
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

		mainTasks = append(mainTasks, map[string]any{
			"name": panelName, "template": "gen-one-panel", "dependencies": append(deps, enhanceName),
			"arguments": map[string]any{"parameters": []any{
				fromTask("prompt", enhanceName, "enhanced-prompt"),
				refImageArg,
				fromWorkflow("user-id", "user-id"),
				literal("seed", p.Seed),
			}},
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
			literal("layout", ""), // compose.go always computes the real grid from len(asset-ids) now
			fromWorkflow("user-id", "user-id"),
		}},
	})

	templates := []any{
		map[string]any{"dag": map[string]any{"name": "main", "tasks": mainTasks}},
		map[string]any{"task": genOnePanelTaskTemplate()},
		map[string]any{"task": enhancePanelTaskTemplate()},
		map[string]any{"task": map[string]any{
			"name": "compose-grid", "executor": map[string]any{"type": "local.compose"},
			"inputs": map[string]any{"parameters": []any{
				map[string]any{"name": "asset-ids", "type": "array"},
				map[string]any{"name": "layout", "type": "string"},
				map[string]any{"name": "user-id", "type": "string"},
			}},
			"timeout": "1m",
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
