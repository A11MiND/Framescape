// comic_plan.go is image.comic4's "AI 漫畫導演": one MiniMax-M3 call that
// reads the user's story/panel input plus whatever character reference
// images are actually bound, and decides — instead of requiring the caller
// to pick a mode by hand — how this particular comic should be built: does
// character consistency matter enough to anchor every panel on a real
// reference image, does more than one bound character need its own
// per-panel anchor, is this really a single continuous take that should
// chain panel-to-panel instead, what page layout fits the story's own pacing
// (a punchline panel earning a bigger cell vs. a plain equal grid), and —
// per panel — the scene/action/expression/detail breakdown plus an optional
// line of dialogue. This supersedes SplitStory's role in image.comic4's auto
// mode (which only ever returned N lines of scene text); SplitStory itself
// is untouched (still used as PlanComic's own fallback, and unrelated
// callers keep working) and image_comic4.go falls back to it whenever
// PlanComic returns a nil plan.
package minimax

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Whitelisted reference-strategy/layout values — jobsvc/image_comic4.go's
// DAG builder switches on these exactly, so a value outside this set must
// never reach it. See buildComic4Workflow's own doc for what each strategy
// means to the DAG.
const (
	RefStrategyNone               = "none"
	RefStrategyAnchor             = "anchor"
	RefStrategyAnchorPerCharacter = "anchor_per_character"
	RefStrategyChain              = "chain"

	LayoutGridEqual       = "grid-equal"
	LayoutFeatureLast     = "feature-last"
	LayoutFeatureFirst    = "feature-first"
	LayoutVerticalStrip   = "vertical-strip"
	LayoutHorizontalStrip = "horizontal-strip"
)

var validRefStrategies = map[string]bool{
	RefStrategyNone: true, RefStrategyAnchor: true,
	RefStrategyAnchorPerCharacter: true, RefStrategyChain: true,
}

// DefaultComicStyle is ComicPlan.Style's fallback when the planner omits it
// (or the planner didn't run at all — image_comic4.go's legacy fallback path
// uses this too). minimax.image has no style bias of its own toward
// "illustration": a bare scene description without style words comes back
// photorealistic, and — found live off a second regression, worse than the
// first — a real uploaded photo as subject_reference pulls the whole panel
// toward photographic rendering even with a mild style phrase appended, so
// this has to explicitly override that pull, not just suggest a style.
const DefaultComicStyle = "日系动漫插画风格，干净的黑色描边，大而有神的眼睛，赛璐璐平涂上色，色彩明亮；" +
	"即使角色参考图是真实照片，也必须转换成这种漫画画法重新演绎，绝对不能保留照片的写实质感"

var validLayouts = map[string]bool{
	LayoutGridEqual: true, LayoutFeatureLast: true, LayoutFeatureFirst: true,
	LayoutVerticalStrip: true, LayoutHorizontalStrip: true,
}

// PlanCharacterInfo is one bound character slot's context for the planner —
// only what it needs to decide reference_strategy/character_slot, not a full
// persistence.Character row (that stays a jobsvc-only concern).
type PlanCharacterInfo struct {
	Slot        string
	Name        string
	Description string
	HasImage    bool // this character has at least one saved F3.1 reference image
}

// PlanComicRequest is PlanComic's input. Exactly one of Story/Panels is
// meaningful — Panels wins when both are set, mirroring image.comic4's
// existing manual-over-auto precedence (image_comic4.go's own
// comic4PanelCount). When Panels is set, the planner is told not to rewrite
// the user's own scene text, only to fill in what it didn't write (dialogue/
// layout/reference strategy/per-panel character).
//
// Count is the exact panel count the caller already committed to (Story
// mode only — Panels mode's count is just len(Panels)) — image_comic4.go's
// comic4PanelCount decides this before EstimateCredits/Hold ever run, so the
// planner isn't free to pick its own count the way SplitStory's caller once
// (implicitly) assumed either: letting the model choose N here would let the
// real DAG's panel count drift from what was already estimated and held,
// breaking the credits invariant (CLAUDE.md: balance+held == ledger sum).
// Same reasoning as SplitStory's own count parameter, which this supersedes
// in auto mode.
type PlanComicRequest struct {
	Story          string
	Panels         []string
	Count          int
	Characters     []PlanCharacterInfo
	HasSourceImage bool // an ad hoc reference image outside the character bank
}

// ComicPanelPlan is one panel's breakdown. Scene/Action/Expression/Details
// exist for the frontend draft-preview and for Text() below — MiniMax's
// image_generation Config.Prompt itself only takes one flat string, it has
// no structured scene/action/expression fields of its own.
type ComicPanelPlan struct {
	Scene         string `json:"scene"`
	Action        string `json:"action"`
	Expression    string `json:"expression"`
	Details       string `json:"details"`
	Dialogue      string `json:"dialogue"`       // "" = no speech bubble this panel
	CharacterSlot string `json:"character_slot"` // "" = default (slot A / the only bound character)
}

// Text joins the structured fields into the single flat prompt string
// minimax.image's Config.Prompt actually wants.
func (p ComicPanelPlan) Text() string {
	parts := make([]string, 0, 4)
	for _, s := range []string{p.Scene, p.Action, p.Expression, p.Details} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "，")
}

type ComicPlan struct {
	ReferenceStrategy string `json:"reference_strategy"`
	LayoutID          string `json:"layout_id"`
	// Style is a short, whole-comic art-style phrase (e.g. "可爱卡通插画风格，
	// 色彩鲜艳") that every panel's compiled prompt repeats verbatim — found
	// live off a job whose input explicitly asked for "cute anime style,
	// manga layout" as a preamble before its "Panel 1:/Panel 2:/..." text:
	// with no per-panel field to carry it, that instruction never reached
	// any panel's actual prompt and every panel came out photorealistic
	// instead. Never left blank — parseComicPlan defaults it when the model
	// omits it (see below) so style is always at least self-consistent
	// across panels, even without an explicit user request.
	Style  string           `json:"style"`
	Panels []ComicPanelPlan `json:"panels"`
}

// PlanComic returns a nil plan (not an error) whenever the call fails or the
// response doesn't parse into something usable — image_comic4.go's caller
// falls back to today's SplitStory-based behavior in that case, same "an
// advisory call must never block job submission" posture
// video_sequence.go's smartSelectBundles already uses for its own MiniMax-M3
// call.
func PlanComic(ctx context.Context, client *Client, req PlanComicRequest) (*ComicPlan, float64, error) {
	count := req.Count
	if len(req.Panels) > 0 {
		count = len(req.Panels)
	}
	if count < 1 {
		count = 4
	}
	req.Count = count

	instruction := buildPlannerInstruction(req)
	resp, err := client.ChatCompletion(ctx, ChatCompletionRequest{
		Model:               textModel,
		Messages:            []ChatMessage{{Role: "user", Content: instruction}},
		Temperature:         0.7,
		MaxCompletionTokens: 400 + 250*count,
		Thinking:            &ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Choices) == 0 {
		return nil, 0, nil
	}
	costYuan := float64(resp.Usage.PromptTokens)/1_000_000*textInputYuanPerM +
		float64(resp.Usage.CompletionTokens)/1_000_000*textOutputYuanPerM

	plan := parseComicPlan(resp.Choices[0].Message.Content, req)
	return plan, costYuan, nil
}

func buildPlannerInstruction(req PlanComicRequest) string {
	var b strings.Builder
	b.WriteString("你是一个漫画编导。请阅读下面的素材，规划出一份四格漫画（格数由你在范围内自行决定）的分镜方案，" +
		"以严格的 JSON 格式输出，不要输出任何 JSON 以外的文字、不要用 ```json 包裹。\n\n")

	if len(req.Panels) > 0 {
		b.WriteString("用户已经手写好了每一格的场景文字，禁止改写这些文字本身，你只需要针对每一格补充 action/expression/details/dialogue/character_slot，并决定 reference_strategy 和 layout_id：\n")
		for i, p := range req.Panels {
			b.WriteString(strconv.Itoa(i+1) + "格：" + p + "\n")
		}
	} else {
		b.WriteString("用户给的一句话剧情，请你自己展开成起承轉合、恰好 " + strconv.Itoa(req.Count) + " 格的连续画面（panels 数组长度必须正好是 " +
			strconv.Itoa(req.Count) + "，不能多也不能少）：\n" + req.Story + "\n")
	}

	if len(req.Characters) > 0 || req.HasSourceImage {
		b.WriteString("\n已绑定的角色（每一格若出现某个角色，请在该格的 character_slot 填对应槽位字母；只有一个角色时每格都填同一个字母即可）：\n")
		for _, c := range req.Characters {
			img := "无参考图"
			if c.HasImage {
				img = "有真实参考图，必须让每一格出现这个角色时都贴合这张参考图的长相"
			}
			b.WriteString("槽位 " + c.Slot + "：" + c.Name + "（" + c.Description + "，" + img + "）\n")
		}
		if req.HasSourceImage {
			b.WriteString("此外用户还上传了一张参考图（未归入角色库），槽位记为 A。\n")
		}
	}

	b.WriteString("\nreference_strategy 从以下四个值中选一个：\n" +
		"\"none\"：没有任何真实参考图，角色由你自己创作，只靠文字描述和固定 seed 保持一致；\n" +
		"\"anchor\"：只有一个真实参考图/角色，且故事是同一角色在不同场景（大多数情况应该选这个，比如同一只猫做不同的事）；\n" +
		"\"anchor_per_character\"：绑定了两个以上角色，各格出现的角色不同，需要逐格指定 character_slot；\n" +
		"\"chain\"：故事明确是同一个场景/镜头里的连续动作（例如角色从门口走到窗边），画格之间需要真正的空间连续性。\n" +
		"没有把握时优先选 \"anchor\"（有参考图）或 \"none\"（没有参考图），不要轻易选 \"chain\"。\n\n" +
		"layout_id 从以下五个值中选一个：\"grid-equal\"（等大网格，没有特别强调时的默认选择）、" +
		"\"feature-last\"（最后一格是结局/笑点，画面更大）、\"feature-first\"（开场一格更大）、" +
		"\"vertical-strip\"（竖长条排版）、\"horizontal-strip\"（横长条排版）。\n\n" +
		"style 是整套漫画统一的美术风格，用一句具体的画法描述（不是\"好看\"这种空话），必须是漫画/插画画法，" +
		"绝对不能是写实摄影质感——即使用户提供了真实照片当角色参考，那张照片也只是用来确定角色的长相/花色/五官特征，" +
		"最终每一格画面都必须彻底转换成漫画插画的画法重新演绎，不能保留照片本身的写实光影和质感，这一点比参考图本身更重要。\n" +
		"如果用户自己的文字里已经写了风格要求（例如 anime/manga/美漫/水彩/像素风等），必须原样保留那个风格意图，翻成一句具体的中文风格描述；" +
		"如果用户完全没提风格，从下面两种画法里选一种更贴合故事气氛的（日常可爱题材优先选日系）：\n" +
		"日系动漫风格范例：\"日系动漫插画风格，干净利落的黑色描边，大而有神的眼睛，赛璐璐平涂上色，色彩明亮\"；\n" +
		"美式漫画风格范例：\"美式漫画风格，粗黑轮廓线，强烈明暗对比和网点上色，动感十足的姿势\"。\n" +
		"这句 style 会被当作强制的风格指令逐字加进每一格的画面描述里，所以只写画法本身，不要写具体场景内容。\n\n" +
		"输出的 JSON 必须是这个形状：\n" +
		`{"reference_strategy":"...","layout_id":"...","style":"...","panels":[{"scene":"...","action":"...","expression":"...","details":"...","dialogue":"...或者空字符串","character_slot":"A或空字符串"}]}` +
		"\n只有画面里真的有人物在说话/喊话，且能用一句精炼的话讲清楚说的内容时才填 dialogue，其余留空字符串——不要为了填满而硬造对话。")

	return b.String()
}

// parseComicPlan defensively extracts the JSON object the planner instruction
// asked for (same "find the outermost braces, tolerate prose/fencing around
// them" approach as video_sequence.go's parseSmartPicks) and validates every
// field against a fixed whitelist before it can reach the DAG builder.
// Returns nil on any structural failure so the caller can fall back to
// legacy (SplitStory-based) behavior rather than fail the job.
func parseComicPlan(raw string, req PlanComicRequest) *ComicPlan {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return nil
	}

	var decoded struct {
		ReferenceStrategy string           `json:"reference_strategy"`
		LayoutID          string           `json:"layout_id"`
		Style             string           `json:"style"`
		Panels            []ComicPanelPlan `json:"panels"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &decoded); err != nil {
		return nil
	}
	if len(decoded.Panels) == 0 {
		return nil
	}

	validSlots := make(map[string]bool, len(req.Characters))
	for _, c := range req.Characters {
		validSlots[c.Slot] = true
	}

	plan := &ComicPlan{
		ReferenceStrategy: decoded.ReferenceStrategy,
		LayoutID:          decoded.LayoutID,
		Style:             strings.TrimSpace(decoded.Style),
	}
	if !validRefStrategies[plan.ReferenceStrategy] {
		plan.ReferenceStrategy = fallbackReferenceStrategy(req)
	}
	if !validLayouts[plan.LayoutID] {
		plan.LayoutID = LayoutGridEqual
	}
	if plan.Style == "" {
		// The model skipped it despite the instruction, or it got lost in
		// truncation — default to an explicit illustration style rather
		// than letting minimax.image fall back to its own default (found
		// live to lean photorealistic for a bare description like "an
		// orange cat", not the cute illustration look a comic implies).
		plan.Style = DefaultComicStyle
	}

	// Exactly req.Count panels, always — comic4PanelCount already fixed this
	// number before EstimateCredits/Hold ran (PlanComicRequest's own doc), so
	// a formatting slip here must never change how many panels the DAG
	// actually builds. Same pad/truncate posture as story_split.go's
	// parsePanels: short a few → pad by repeating the last panel (or the
	// user's own text in manual mode, filled in below); too many → truncate.
	panels := decoded.Panels
	for len(panels) < req.Count {
		panels = append(panels, panels[len(panels)-1])
	}
	panels = panels[:req.Count]

	for i := range panels {
		if !validSlots[panels[i].CharacterSlot] {
			panels[i].CharacterSlot = ""
		}
		if i < len(req.Panels) {
			// Manual mode: never let the model rewrite the user's own scene
			// text — only its dialogue/character_slot/layout suggestions
			// are kept.
			panels[i].Scene = req.Panels[i]
			panels[i].Action, panels[i].Expression, panels[i].Details = "", "", ""
		}
	}
	plan.Panels = panels
	return plan
}

// fallbackReferenceStrategy is the same Go-side heuristic image_comic4.go
// would otherwise have to hardcode: with a real reference image and only one
// bound character, anchor every panel on it; with 2+ bound characters, let
// each panel pick its own; with neither, there is no image to anchor on.
// Only used when the model's own reference_strategy value didn't parse or
// wasn't in the whitelist.
func fallbackReferenceStrategy(req PlanComicRequest) string {
	switch {
	case len(req.Characters) >= 2:
		return RefStrategyAnchorPerCharacter
	case len(req.Characters) == 1 && req.Characters[0].HasImage, req.HasSourceImage:
		return RefStrategyAnchor
	default:
		return RefStrategyNone
	}
}
