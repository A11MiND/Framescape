// image_comic4.go implements image.comic4's "連貫模式" (Continuity Mode) —
// see Spec.Comic4Mode's own doc for why both modes coexist rather than one
// replacing the other ("兩套並存，就說兩種模式" was an explicit ask, and
// naming both was left to this implementation). "快速模式" (Quick Mode, the
// default, empty Comic4Mode) is completely unchanged from before this file
// existed: still the static workflows/image-comic4.json /
// image-comic4-auto.json Loop, still 4 fully independent panels — Create()
// only routes here when Comic4Mode is explicitly "continuity".
//
// Continuity Mode needs a Go-generated DAG for the same reason
// image_sequence.go's cross-shot referencing does: panel 2's own subject_
// reference needs to be panel 1's runtime-produced asset id, which
// Aether's Loop primitive can't express (only a flat itemsFrom list, same
// limitation video_sequence.go's own package doc covers). Unlike image.
// sequence's optional per-shot #-reference, every panel here automatically
// chains to the immediately preceding panel — matching the ask's own
// wording ("根據上一頁的內容...的一致性"). The real generation call
// (minimax.image) only ever gets one reference image regardless: image_
// generation's subject_reference is verified capped at exactly one per
// call (a real call, real error 2013 "image_reference must be one" —
// video_sequence.go's package doc covers the same verification for the
// *video* r2va endpoint's much higher, unrelated cap), so there is no
// equivalent to video.sequence's multi-image bundle at the image_
// generation layer. MiniMax's H3-Context-IR (minimax.prompt_enhance) is
// used purely as a text-prompt writer here — its own input can still see
// the single chained reference image (via local.collect_refs, the same
// intermediate step video.sequence needs whenever a scalar fromTask value
// has to become a one-element array), so H3 sees the running story
// outline, this panel's own directed text, and that one reference image,
// and its output text becomes the real image_generation call's prompt.
//
// Auto-split (Spec.Story, no Panels) needs all 4 panel texts resolved
// *before* the DAG can be built, unlike Quick Mode's image-comic4-auto.json
// (a Loop distributes one array element per independent iteration, which
// has no equivalent for "panel 2 needs panel 1's own future output" chains)
// — so Continuity Mode calls minimax.SplitStory synchronously in Go here,
// the same "resolve before building the DAG" reasoning video_sequence.go's
// own "smart" reference-selection mode already established.
package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

type panelPlan struct {
	Index   int // 1-based, 1..4
	Prompt  string
	RawText string
}

func (s *Service) createImageComic4Continuity(ctx context.Context, userID uint64, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	panelTexts := spec.Panels
	splitCost := 0
	if len(panelTexts) != 4 {
		if spec.Story == "" {
			return nil, fmt.Errorf("image.comic4 requires exactly 4 panels, or a story to auto-split")
		}
		if s.minimax == nil {
			return nil, fmt.Errorf("story auto-split is unavailable in this deployment")
		}
		var err error
		panelTexts, _, err = minimax.SplitStory(ctx, s.minimax, spec.Story)
		if err != nil {
			return nil, fmt.Errorf("split story into panels: %w", err)
		}
		if len(panelTexts) != 4 {
			return nil, fmt.Errorf("story split returned %d panels, want 4", len(panelTexts))
		}
		splitCost = creditsvc.EstimateStorySplitCredits()
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	plans := make([]panelPlan, 4)
	for i, text := range panelTexts {
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: spec.Seed})
		plans[i] = panelPlan{Index: i + 1, Prompt: compiled.Prompt, RawText: text}
	}

	wfJSON := buildComic4ContinuityWorkflow(plans, spec.SourceImageAssetID)

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	// Every panel gets enhanced in Continuity Mode (this file's own doc) —
	// 4 minimax.image calls plus 4 minimax.prompt_enhance calls, plus the
	// sync split cost if auto-split ran.
	estimatedCredits := creditsvc.EstimatePerNodeImageCredits(4) + 4*creditsvc.EstimatePromptEnhanceCredits() + splitCost
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "job", "image.comic4"); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}

	args := map[string]any{"user-id": strconv.FormatUint(userID, 10)}
	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: "image-comic4-continuity", JSON: wfJSON}, args)
	if err != nil {
		_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, estimatedCredits)
		return nil, fmt.Errorf("submit workflow: %w", err)
	}

	title := plans[0].RawText
	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
		ProjectID:       projectID,
		WorkflowName:    "image.comic4",
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

func genOnePanelTaskTemplate() map[string]any {
	return map[string]any{
		"name": "gen-one-panel", "executor": map[string]any{"type": "minimax.image"},
		"inputs": map[string]any{"parameters": []any{
			map[string]any{"name": "prompt", "type": "string"},
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

func enhancePanelTaskTemplate() map[string]any {
	return map[string]any{
		"name": "enhance-panel", "executor": map[string]any{"type": "minimax.prompt_enhance"},
		"inputs": map[string]any{"parameters": enhanceInputDecl()},
		"retry":  map[string]any{"limit": 1}, "timeout": "5m",
	}
}

// buildComic4ContinuityWorkflow chains panel 1 -> panel 2 -> panel 3 ->
// panel 4, each (optionally, always in practice — every panel gets
// Enhance) preceded by an enhance-panel-N node whose single reference image
// is the same one the real gen-one-panel-N call uses: staticSourceAssetID
// for panel 1 (may be empty), the immediately preceding panel's own
// asset-id for panels 2-4.
func buildComic4ContinuityWorkflow(plans []panelPlan, staticSourceAssetID string) []byte {
	mainTasks := make([]any, 0, len(plans)*3+1)

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
			// array — Aether has no scalar-to-array coercion (a raw JSON
			// string bound into a []string field fails BindInputs' own
			// json.Unmarshal), so local.collect_refs bridges it into a real
			// one-element array, the same intermediate-collecting step
			// buildBundleTasks (video_sequence.go) already needed for a
			// multi-element version of the same problem.
			prevPanel := fmt.Sprintf("panel-%d", p.Index-1)
			mainTasks = append(mainTasks, map[string]any{
				"name": collectName, "template": "collect-refs", "dependencies": []string{prevPanel},
				"arguments": map[string]any{"parameters": []any{
					fromTask("image-1", prevPanel, "asset-id"),
					literal("image-2", ""), literal("image-3", ""), literal("image-4", ""), literal("image-5", ""),
					literal("video-1", ""), literal("video-2", ""), literal("video-3", ""),
				}},
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
			}},
		})
	}

	// local.compose's own ComposeConfig takes one "asset-ids" array
	// parameter (compose.go), not 4 individually-named ones — the exact
	// same scalar-to-array problem every per-panel enhance step above
	// already needed local.collect_refs for, so this reuses it once more
	// to gather all 4 panels' own asset-ids before compose-grid runs.
	composeDeps := make([]string, len(plans))
	collectArgs := make([]any, 0, 8)
	for i, p := range plans {
		panelName := fmt.Sprintf("panel-%d", p.Index)
		composeDeps[i] = panelName
		collectArgs = append(collectArgs, fromTask(fmt.Sprintf("image-%d", i+1), panelName, "asset-id"))
	}
	for i := len(plans); i < 5; i++ {
		collectArgs = append(collectArgs, literal(fmt.Sprintf("image-%d", i+1), ""))
	}
	collectArgs = append(collectArgs, literal("video-1", ""), literal("video-2", ""), literal("video-3", ""))
	mainTasks = append(mainTasks, map[string]any{
		"name": "collect-panels", "template": "collect-refs", "dependencies": composeDeps,
		"arguments": map[string]any{"parameters": collectArgs},
	})
	mainTasks = append(mainTasks, map[string]any{
		"name": "compose", "template": "compose-grid", "dependencies": []string{"collect-panels"},
		"arguments": map[string]any{"parameters": []any{
			fromTask("asset-ids", "collect-panels", "image-ids"),
			literal("layout", "2x2"),
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
				map[string]any{"name": "layout", "type": "string", "value": "2x2"},
				map[string]any{"name": "user-id", "type": "string"},
			}},
			"timeout": "1m",
		}},
	}
	templates = append(templates, map[string]any{"task": map[string]any{
		"name": "collect-refs", "executor": map[string]any{"type": "local.collect_refs"},
		"inputs": map[string]any{"parameters": collectRefsInputDecl()},
		"retry":  map[string]any{"limit": 1}, "timeout": "30s",
	}})

	doc := map[string]any{
		"apiVersion": "aether/v1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name": "image-comic4-continuity",
			"annotations": map[string]any{
				"description": "F5.3 四格漫画连贯模式: code-generated per job, see internal/application/jobsvc/image_comic4.go",
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
		panic(fmt.Sprintf("buildComic4ContinuityWorkflow: marshal: %v", err))
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
