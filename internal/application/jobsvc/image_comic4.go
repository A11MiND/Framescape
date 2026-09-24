package jobsvc

import (
	"context"
	"fmt"
	"strings"

	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
)

// minComic4Panels: a single panel is not a comic.
const minComic4Panels = 2

const (
	imageProviderMiniMax = workflows.ProviderMiniMax
	imageProviderGemini  = workflows.ProviderGemini
)

// normalizeImageProvider falls back to MiniMax for empty or unknown values
// so a stale client never blocks submission.
func normalizeImageProvider(s string) string {
	if s == imageProviderGemini {
		return imageProviderGemini
	}
	return imageProviderMiniMax
}

type panelPlan struct {
	Index      int
	Prompt     string
	RawText    string
	Seed       string
	Dialogue   string
	RefAssetID string
}

// comic4PanelCount is how many panels a request will run; for story mode it
// is the requested count (default 4) since the split has not happened yet.
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
		return min(n, capability.ImageMaxN), nil
	}
	return 0, fmt.Errorf("image.comic4 requires at least %d panels, or a story to auto-split", minComic4Panels)
}

// comic4StylizeRefCount bounds the reference-stylizing passes: one per bound
// character, or one for an ad hoc source image.
func comic4StylizeRefCount(spec Spec) int {
	if len(spec.Characters) > 0 {
		return len(spec.Characters)
	}
	if spec.SourceImageAssetID != "" {
		return 1
	}
	return 0
}

func (s *Service) prepareImageComic4(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	count, err := comic4PanelCount(spec)
	if err != nil {
		return nil, "", err
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	charInfos, slotRef, err := s.resolveCharacterPlanInfo(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, "", err
	}

	manual := spec.Panels
	if len(manual) < minComic4Panels {
		manual = nil
	}
	// The planner is advisory: when it is unavailable or its answer is
	// unusable the comic falls back to chained panels on an equal grid.
	var plan *minimax.ComicPlan
	if s.minimax != nil {
		if p, _, err := minimax.PlanComic(ctx, s.minimax, minimax.PlanComicRequest{
			Story: spec.Story, Panels: manual, Count: count, Characters: charInfos, HasSourceImage: spec.SourceImageAssetID != "",
		}); err == nil {
			plan = p
		}
	}
	var texts []string
	switch {
	case plan != nil:
		for _, p := range plan.Panels {
			texts = append(texts, p.Text())
		}
	case manual != nil:
		texts = manual
	default:
		if s.minimax == nil {
			return nil, "", fmt.Errorf("story auto-split is unavailable in this deployment")
		}
		if texts, _, err = minimax.SplitStory(ctx, s.minimax, spec.Story, count); err != nil {
			return nil, "", fmt.Errorf("split story into panels: %w", err)
		}
		if len(texts) != count {
			return nil, "", fmt.Errorf("story split returned %d panels, want %d", len(texts), count)
		}
	}

	strategy, layout, style := minimax.RefStrategyChain, minimax.LayoutGridEqual, minimax.DefaultComicStyle
	if plan != nil {
		strategy, layout, style = plan.ReferenceStrategy, plan.LayoutID, plan.Style
	}
	anchor := spec.SourceImageAssetID
	if anchor == "" && len(charInfos) > 0 {
		anchor = slotRef[charInfos[0].Slot]
	}
	// A text description of the anchor's subject backs up the image channel,
	// which MiniMax tunes for human portraits rather than animals or objects.
	subject := ""
	if anchor != "" && s.minimax != nil {
		if url, err := s.resolveAssetPublicURL(ctx, anchor); err == nil && url != "" {
			subject, _ = minimax.DescribeReferenceSubject(ctx, s.minimax, url)
		}
	}

	seed := formatSeed(resolveSharedSeed(characters, spec.Seed))
	plans := make([]panelPlan, len(texts))
	for i, text := range texts {
		styled := text
		if subject != "" {
			styled += "，角色具体外观（务必保持一致）：" + subject
		}
		if style != "" {
			styled += "，" + style
		}
		compiled := prompt.Compile(prompt.Input{Text: styled, Characters: characters, Presets: presets, Seed: nil})
		p := panelPlan{Index: i + 1, Prompt: compiled.Prompt, RawText: text, Seed: seed}
		switch strategy {
		case minimax.RefStrategyAnchor:
			p.RefAssetID = anchor
		case minimax.RefStrategyAnchorPerCharacter:
			slot := ""
			if plan != nil && i < len(plan.Panels) {
				slot = plan.Panels[i].CharacterSlot
			}
			if slot == "" && len(charInfos) > 0 {
				slot = charInfos[0].Slot
			}
			if p.RefAssetID = slotRef[slot]; p.RefAssetID == "" {
				p.RefAssetID = anchor
			}
		case minimax.RefStrategyChain:
			if i == 0 {
				p.RefAssetID = anchor
			}
		}
		if plan != nil && i < len(plan.Panels) {
			p.Dialogue = plan.Panels[i].Dialogue
		}
		plans[i] = p
	}
	built, err := buildComic4Plan(userID, plans, strategy, layout, style, normalizeImageProvider(spec.ImageProvider))
	return built, plans[0].RawText, err
}

func buildComic4Plan(userID uint64, plans []panelPlan, strategy, layout, style, provider string) (*workflow.Plan, error) {
	panels := make([]workflows.ComicPanel, len(plans))
	for i, p := range plans {
		panels[i] = workflows.ComicPanel{Index: p.Index, EnhancePrompt: panelEnhancePrompt(plans, p, style), Seed: p.Seed, Dialogue: p.Dialogue, RefAsset: p.RefAssetID}
	}
	return workflows.Comic4Plan(workflows.Comic4{
		UserID: userID, Provider: provider, Strategy: strategy, Layout: layout, Style: style,
		StylizePrompt: stylizeReferencePrompt(style), Panels: panels,
	})
}

// panelEnhancePrompt is what H3-Context-IR writes a panel's image prompt
// from. With a story recap present it must be told to draw only this
// panel, or it describes earlier panels too and they merge into one image.
func panelEnhancePrompt(plans []panelPlan, p panelPlan, style string) string {
	out := p.Prompt
	if outline := buildPanelOutline(plans, p.Index); outline != "" {
		out = "漫画剧情大纲（已发生的画格，仅供你理解故事上下文，不需要画出来）：" + outline +
			"\n\n本格需要表现（这一格实际要画的唯一画面）：" + p.Prompt +
			"\n\n注意：只画“本格需要表现”里的这一个瞬间，不要把大纲里之前几格的画面内容也画进同一张图里。"
	}
	return out + dialogueInstruction(p.Dialogue) + styleInstruction(style)
}

// buildPanelOutline joins the raw text of every earlier panel.
func buildPanelOutline(plans []panelPlan, index int) string {
	parts := make([]string, 0, index)
	for i := 0; i < index-1 && i < len(plans); i++ {
		if t := strings.TrimSpace(plans[i].RawText); t != "" {
			parts = append(parts, fmt.Sprintf("第%d格：%s", i+1, t))
		}
	}
	return strings.Join(parts, "；")
}

// dialogueInstruction asks the image model to draw a speech bubble with the
// exact line.
func dialogueInstruction(dialogue string) string {
	if dialogue = strings.TrimSpace(dialogue); dialogue == "" {
		return ""
	}
	return "\n\n画面中加入一个漫画对话框（气泡），气泡内文字必须精准显示为：「" + dialogue + "」，字体清晰可辨、完整排布在气泡内，不得出现其他文字或乱码。"
}

// styleInstruction is a standalone style directive: a style phrase blended
// into the scene text does not overcome a photographic reference's pull
// toward realism.
func styleInstruction(style string) string {
	if style = strings.TrimSpace(style); style == "" {
		return ""
	}
	return "\n\n整体画面风格（强制要求，优先级高于参考图本身的质感）：" + style +
		"。这是一格漫画画面，绝对不能画成写实照片质感，即使角色参考图是真实照片，也只借用其长相/花色/五官特征，" +
		"必须彻底转换成上述漫画画法重新演绎。"
}

// stylizeReferencePrompt converts a raw reference into the comic's style in
// a dedicated call, whose output then anchors the real panels.
func stylizeReferencePrompt(style string) string {
	if style = strings.TrimSpace(style); style == "" {
		style = minimax.DefaultComicStyle
	}
	return "参考图里的角色，" + style + "。请保留角色的外观特征（毛色/花纹/五官/体型等辨识度特征），" +
		"重新绘制成一张干净的漫画角色定妆照，人物居中、姿势自然、背景简单。" +
		"绝对不能保留照片本身的写实质感、真实光影或噪点，输出必须是彻底的漫画插画画法，不是照片。"
}
