package jobsvc

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
)

func planNodes(t *testing.T, p *workflow.Plan, err error) map[string]workflow.NodeSpec {
	t.Helper()
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if err := workflow.Validate(p.Nodes, nil); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	out := map[string]workflow.NodeSpec{}
	for _, n := range p.Nodes {
		out[n.Name] = n
	}
	return out
}

func literal(t *testing.T, in workflow.Input) any {
	t.Helper()
	if in.Ref != nil || in.IsList {
		t.Fatalf("not a literal: %+v", in)
	}
	var v any
	if err := json.Unmarshal(in.Value, &v); err != nil {
		t.Fatalf("decode literal: %v", err)
	}
	return v
}

func refNode(in workflow.Input) string {
	if in.Ref != nil {
		return in.Ref.Node
	}
	return ""
}

func testPlans(refIDs ...string) []panelPlan {
	plans := make([]panelPlan, len(refIDs))
	for i, ref := range refIDs {
		plans[i] = panelPlan{Index: i + 1, Prompt: "panel prompt", RawText: "panel raw text", RefAssetID: ref}
	}
	return plans
}

func TestComic4ChainPanelsDependSequentially(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("char-asset", "", "", ""), minimax.RefStrategyChain, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)

	if refNode(nodes["panel-1"].Inputs["source-image-asset-id"]) != "stylize-reference-1" {
		t.Errorf("panel 1 should anchor on the stylized reference, got %+v", nodes["panel-1"].Inputs["source-image-asset-id"])
	}
	if got := literal(t, nodes["stylize-reference-1"].Inputs["source-image-asset-id"]); got != "char-asset" {
		t.Errorf("stylize pass should convert the raw asset, got %v", got)
	}
	if !slices.Contains(nodes["enhance-panel-2"].Dependencies(), "panel-1") {
		t.Errorf("panel 2's enhance step should wait for panel 1, got %v", nodes["enhance-panel-2"].Dependencies())
	}
	if refNode(nodes["panel-2"].Inputs["source-image-asset-id"]) != "panel-1" {
		t.Errorf("panel 2 should reference panel 1's output in chain mode")
	}
}

func TestComic4AnchorPanelsAreIndependent(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("char-asset", "char-asset", "char-asset", "char-asset"), minimax.RefStrategyAnchor, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	for i := 1; i <= 4; i++ {
		for _, dep := range nodes["enhance-panel-"+strconv.Itoa(i)].Dependencies() {
			if strings.HasPrefix(dep, "panel-") || strings.HasPrefix(dep, "enhance-panel-") {
				t.Errorf("anchor mode panel %d depends on another panel: %v", i, dep)
			}
		}
		if refNode(nodes["panel-"+strconv.Itoa(i)].Inputs["source-image-asset-id"]) != "stylize-reference-1" {
			t.Errorf("panel %d should share the one stylized reference", i)
		}
	}
	if _, ok := nodes["stylize-reference-2"]; ok {
		t.Error("a shared raw reference must be stylized once")
	}
}

func TestComic4AnchorPerCharacterUsesPerPanelAsset(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("asset-a", "asset-b", "asset-a", "asset-b"), minimax.RefStrategyAnchorPerCharacter, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	for i, want := range []string{"stylize-reference-1", "stylize-reference-2", "stylize-reference-1", "stylize-reference-2"} {
		if got := refNode(nodes["panel-"+strconv.Itoa(i+1)].Inputs["source-image-asset-id"]); got != want {
			t.Errorf("panel %d refs %q, want %q", i+1, got, want)
		}
	}
	if literal(t, nodes["stylize-reference-2"].Inputs["source-image-asset-id"]) != "asset-b" {
		t.Error("stylize-reference-2 should convert asset-b")
	}
}

func TestComic4NoneStrategyHasNoReference(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("", "", ""), minimax.RefStrategyNone, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	for i := 1; i <= 3; i++ {
		if got := literal(t, nodes["panel-"+strconv.Itoa(i)].Inputs["source-image-asset-id"]); got != "" {
			t.Errorf("panel %d has reference %v", i, got)
		}
		if got := literal(t, nodes["enhance-panel-"+strconv.Itoa(i)].Inputs["reference-image-asset-ids"]); len(got.([]any)) != 0 {
			t.Errorf("panel %d enhance refs = %v", i, got)
		}
	}
}

func enhancePrompt(t *testing.T, nodes map[string]workflow.NodeSpec, panel int) string {
	t.Helper()
	s, _ := literal(t, nodes["enhance-panel-"+strconv.Itoa(panel)].Inputs["prompt"]).(string)
	return s
}

func TestComic4DialogueInjectedIntoPrompt(t *testing.T) {
	plans := testPlans("", "")
	plans[0].Dialogue = "早安，今天也要加油！"
	p, err := buildComic4Plan(1, plans, minimax.RefStrategyNone, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	if p1 := enhancePrompt(t, nodes, 1); !strings.Contains(p1, "早安，今天也要加油！") || !strings.Contains(p1, "对话框") {
		t.Errorf("panel 1 prompt lacks the dialogue instruction: %q", p1)
	}
	if strings.Contains(enhancePrompt(t, nodes, 2), "对话框") {
		t.Error("panel 2 has no dialogue and must get no bubble instruction")
	}
	if literal(t, nodes["panel-1"].Inputs["expected-dialogue"]) != "早安，今天也要加油！" {
		t.Error("panel 1 should ask the image check for its dialogue")
	}
}

// Without a reference image H3-Context-IR otherwise describes the recap and
// the current panel as two scenes, which render merged into one image.
func TestComic4OutlineGuardsAgainstShotBleeding(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("", "", ""), minimax.RefStrategyNone, minimax.LayoutGridEqual, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	if strings.Contains(enhancePrompt(t, nodes, 1), "剧情大纲") {
		t.Error("panel 1 has no earlier panels and must carry no outline")
	}
	if p2 := enhancePrompt(t, nodes, 2); !strings.Contains(p2, "剧情大纲") || !strings.Contains(p2, "只画") {
		t.Errorf("panel 2 needs the outline and the draw-only-this-panel guard: %q", p2)
	}
}

func TestComic4LayoutPassedToCompose(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("", ""), minimax.RefStrategyNone, minimax.LayoutFeatureLast, "", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	if literal(t, nodes["compose"].Inputs["layout"]) != minimax.LayoutFeatureLast {
		t.Error("compose should get the planned layout")
	}
}

func TestComic4StyleInjectedIntoEveryPanel(t *testing.T) {
	p, err := buildComic4Plan(1, testPlans("photo-asset", "photo-asset"), minimax.RefStrategyAnchor, minimax.LayoutGridEqual, "日系动漫插画风格", imageProviderMiniMax)
	nodes := planNodes(t, p, err)
	for i := 1; i <= 2; i++ {
		if pr := enhancePrompt(t, nodes, i); !strings.Contains(pr, "日系动漫插画风格") || !strings.Contains(pr, "写实") {
			t.Errorf("panel %d prompt lacks the style directive: %q", i, pr)
		}
	}
}

func TestComic4GeminiProviderOmitsQualityGates(t *testing.T) {
	plans := testPlans("photo-asset", "photo-asset")
	plans[0].Dialogue = "早安！"
	p, err := buildComic4Plan(1, plans, minimax.RefStrategyAnchor, minimax.LayoutGridEqual, "日系动漫插画风格", imageProviderGemini)
	nodes := planNodes(t, p, err)
	for _, name := range []string{"panel-1", "panel-2", "stylize-reference-1"} {
		n := nodes[name]
		if n.Executor != "gemini.image" {
			t.Errorf("%s executor = %s, want gemini.image", name, n.Executor)
		}
		if _, ok := n.Inputs["expected-style"]; ok {
			t.Errorf("%s must not carry MiniMax quality gates", name)
		}
	}
}

func TestStyleInstruction(t *testing.T) {
	if got := styleInstruction(""); got != "" {
		t.Errorf("empty style should produce no instruction, got %q", got)
	}
	if got := styleInstruction("美式漫画风格"); !strings.Contains(got, "美式漫画风格") || !strings.Contains(got, "写实") {
		t.Errorf("style instruction should quote the style and rule out photorealism, got %q", got)
	}
}

func TestDialogueInstruction(t *testing.T) {
	if dialogueInstruction("") != "" || dialogueInstruction("  ") != "" {
		t.Error("blank dialogue should produce no instruction")
	}
	if got := dialogueInstruction("你好"); !strings.Contains(got, "你好") || !strings.Contains(got, "对话框") {
		t.Errorf("dialogue instruction = %q", got)
	}
}
