package jobsvc

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"aigc-platform/internal/infra/executor/minimax"
)

type wfParam struct {
	Name      string `json:"name"`
	Value     any    `json:"value"`
	ValueFrom *struct {
		Parameter string `json:"parameter"`
	} `json:"valueFrom"`
}

type wfTask struct {
	Name         string   `json:"name"`
	Template     string   `json:"template"`
	Dependencies []string `json:"dependencies"`
	Arguments    struct {
		Parameters []wfParam `json:"parameters"`
	} `json:"arguments"`
}

type wfDoc struct {
	Spec struct {
		Templates []struct {
			Dag *struct {
				Tasks []wfTask `json:"tasks"`
			} `json:"dag"`
		} `json:"templates"`
	} `json:"spec"`
}

// mainTasks decodes buildComic4Workflow's output and returns the "main" dag's
// tasks keyed by name, failing the test on any parse error.
func mainTasks(t *testing.T, raw []byte) map[string]wfTask {
	t.Helper()
	var doc wfDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode workflow JSON: %v", err)
	}
	for _, tpl := range doc.Spec.Templates {
		if tpl.Dag != nil {
			out := make(map[string]wfTask, len(tpl.Dag.Tasks))
			for _, task := range tpl.Dag.Tasks {
				out[task.Name] = task
			}
			return out
		}
	}
	t.Fatal("no dag template found")
	return nil
}

func param(t *testing.T, task wfTask, name string) wfParam {
	t.Helper()
	for _, p := range task.Arguments.Parameters {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("task %q has no parameter %q", task.Name, name)
	return wfParam{}
}

func testPlans(refIDs ...string) []panelPlan {
	plans := make([]panelPlan, len(refIDs))
	for i, ref := range refIDs {
		plans[i] = panelPlan{Index: i + 1, Prompt: "panel prompt", RawText: "panel raw text", Seed: "", RefAssetID: ref}
	}
	return plans
}

func TestBuildComic4Workflow_ChainStrategy_PanelsDependSequentially(t *testing.T) {
	plans := testPlans("char-asset", "", "", "")
	raw := buildComic4Workflow(plans, minimax.RefStrategyChain, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	// Panel 1's own literal reference is routed through the one-time
	// stylize-reference pass (buildComic4Workflow's own doc) rather than
	// used as a raw literal — that's its only dependency, no other panel.
	deps1 := tasks["enhance-panel-1"].Dependencies
	if !containsStr(deps1, "stylize-reference-1") || !containsStr(deps1, "collect-stylize-1") {
		t.Errorf("panel 1 should depend on the stylize-reference pass, got %v", deps1)
	}
	refArg := param(t, tasks["panel-1"], "source-image-asset-id")
	if refArg.ValueFrom == nil || !strings.Contains(refArg.ValueFrom.Parameter, "stylize-reference-1") {
		t.Errorf("panel 1 should anchor on the stylized reference's own output, got %+v", refArg)
	}
	stylizeArg := param(t, tasks["stylize-reference-1"], "source-image-asset-id")
	if stylizeArg.Value != "char-asset" {
		t.Errorf("stylize-reference-1 should convert the original literal asset, got %+v", stylizeArg)
	}

	enhance2 := tasks["enhance-panel-2"]
	if !containsStr(enhance2.Dependencies, "panel-1") {
		t.Errorf("panel 2's enhance step should depend on panel 1 in chain mode, got %v", enhance2.Dependencies)
	}
	panel2RefArg := param(t, tasks["panel-2"], "source-image-asset-id")
	if panel2RefArg.ValueFrom == nil || !strings.Contains(panel2RefArg.ValueFrom.Parameter, "panel-1") {
		t.Errorf("panel 2 should reference panel 1's own runtime output in chain mode, got %+v", panel2RefArg)
	}
}

func TestBuildComic4Workflow_AnchorStrategy_PanelsAreIndependent(t *testing.T) {
	plans := testPlans("char-asset", "char-asset", "char-asset", "char-asset")
	raw := buildComic4Workflow(plans, minimax.RefStrategyAnchor, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	for i := 1; i <= 4; i++ {
		name := "enhance-panel-" + strconv.Itoa(i)
		// All four panels share the one stylize-reference-1 pass (same raw
		// asset id, memoized) — that's not a cross-*panel* dependency, so
		// still no panel-N in any other panel's deps.
		for _, dep := range tasks[name].Dependencies {
			if strings.HasPrefix(dep, "panel-") || strings.HasPrefix(dep, "enhance-panel-") {
				t.Errorf("%s should have no cross-panel dependency in anchor mode, got %v", name, tasks[name].Dependencies)
			}
		}
		refArg := param(t, tasks["panel-"+strconv.Itoa(i)], "source-image-asset-id")
		if refArg.ValueFrom == nil || !strings.Contains(refArg.ValueFrom.Parameter, "stylize-reference-1") {
			t.Errorf("panel %d should anchor on the same stylized reference every time, got %+v", i, refArg)
		}
	}
	// Only one stylize pass for the one distinct raw asset id shared by all
	// four panels — not one per panel.
	if _, ok := tasks["stylize-reference-2"]; ok {
		t.Error("anchor mode should memoize the stylize pass per distinct raw asset id, not build one per panel")
	}
	// No collect-panel-N bridging tasks should exist at all in anchor mode —
	// those only exist to bridge a runtime chain dependency.
	if _, ok := tasks["collect-panel-2"]; ok {
		t.Error("anchor mode should never need a collect-panel-N bridging task")
	}
}

func TestBuildComic4Workflow_AnchorPerCharacterStrategy_UsesPerPanelAsset(t *testing.T) {
	plans := testPlans("asset-a", "asset-b", "asset-a", "asset-b")
	raw := buildComic4Workflow(plans, minimax.RefStrategyAnchorPerCharacter, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	wantStylizeTask := []string{"stylize-reference-1", "stylize-reference-2", "stylize-reference-1", "stylize-reference-2"}
	for i, wantTask := range wantStylizeTask {
		refArg := param(t, tasks["panel-"+strconv.Itoa(i+1)], "source-image-asset-id")
		if refArg.ValueFrom == nil || !strings.Contains(refArg.ValueFrom.Parameter, wantTask) {
			t.Errorf("panel %d: want ref from %q, got %+v", i+1, wantTask, refArg)
		}
	}
	if got := param(t, tasks["stylize-reference-1"], "source-image-asset-id").Value; got != "asset-a" {
		t.Errorf("stylize-reference-1 should convert asset-a, got %+v", got)
	}
	if got := param(t, tasks["stylize-reference-2"], "source-image-asset-id").Value; got != "asset-b" {
		t.Errorf("stylize-reference-2 should convert asset-b, got %+v", got)
	}
}

func TestBuildComic4Workflow_NoneStrategy_NoReferenceImage(t *testing.T) {
	plans := testPlans("", "", "")
	raw := buildComic4Workflow(plans, minimax.RefStrategyNone, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	for i := 1; i <= 3; i++ {
		refArg := param(t, tasks["panel-"+strconv.Itoa(i)], "source-image-asset-id")
		if refArg.Value != "" {
			t.Errorf("panel %d should have no reference image, got %+v", i, refArg)
		}
		refIDsArg := param(t, tasks["enhance-panel-"+strconv.Itoa(i)], "reference-image-asset-ids")
		if ids, ok := refIDsArg.Value.([]any); !ok || len(ids) != 0 {
			t.Errorf("panel %d's enhance step should get an empty reference-image-asset-ids, got %+v", i, refIDsArg.Value)
		}
	}
}

func TestBuildComic4Workflow_DialogueInjectedIntoPrompt(t *testing.T) {
	plans := testPlans("", "")
	plans[0].Dialogue = "早安，今天也要加油！"
	raw := buildComic4Workflow(plans, minimax.RefStrategyNone, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	prompt1, _ := param(t, tasks["enhance-panel-1"], "prompt").Value.(string)
	if !strings.Contains(prompt1, "早安，今天也要加油！") {
		t.Errorf("panel 1's prompt should contain the dialogue text, got %q", prompt1)
	}
	if !strings.Contains(prompt1, "对话框") {
		t.Errorf("panel 1's prompt should contain a speech-bubble rendering instruction, got %q", prompt1)
	}
	prompt2, _ := param(t, tasks["enhance-panel-2"], "prompt").Value.(string)
	if strings.Contains(prompt2, "对话框") {
		t.Errorf("panel 2 has no dialogue and should get no bubble instruction, got %q", prompt2)
	}
}

// TestBuildComic4Workflow_OutlineGuardsAgainstShotBleeding covers a real bug
// found live: with no reference image (RefStrategyNone), H3-Context-IR was
// treating the "剧情大纲" recap of earlier panels plus the current panel's
// own text as a multi-shot video continuation and rendering both the
// earlier and current panel's content into one merged image. Every panel
// after the first must carry an explicit "only draw this one moment" guard
// alongside the recap.
func TestBuildComic4Workflow_OutlineGuardsAgainstShotBleeding(t *testing.T) {
	plans := testPlans("", "", "")
	raw := buildComic4Workflow(plans, minimax.RefStrategyNone, minimax.LayoutGridEqual, "")
	tasks := mainTasks(t, raw)

	prompt1, _ := param(t, tasks["enhance-panel-1"], "prompt").Value.(string)
	if strings.Contains(prompt1, "剧情大纲") {
		t.Errorf("panel 1 has no earlier panels and should carry no outline, got %q", prompt1)
	}

	prompt2, _ := param(t, tasks["enhance-panel-2"], "prompt").Value.(string)
	if !strings.Contains(prompt2, "剧情大纲") {
		t.Errorf("panel 2 should carry the earlier-panels outline, got %q", prompt2)
	}
	if !strings.Contains(prompt2, "只画") {
		t.Errorf("panel 2's prompt should explicitly guard against rendering earlier panels' content, got %q", prompt2)
	}
}

func TestBuildComic4Workflow_LayoutPassedToCompose(t *testing.T) {
	plans := testPlans("", "")
	raw := buildComic4Workflow(plans, minimax.RefStrategyNone, minimax.LayoutFeatureLast, "")
	tasks := mainTasks(t, raw)

	layoutArg := param(t, tasks["compose"], "layout")
	if layoutArg.Value != minimax.LayoutFeatureLast {
		t.Errorf("compose's layout argument should carry the planned layout id, got %+v", layoutArg.Value)
	}
}

// TestBuildComic4Workflow_StyleInjectedIntoEveryPanel covers a real bug found
// live: a mild style phrase blended only into the compiled scene text wasn't
// enough to stop minimax.image from rendering photorealistically when a real
// uploaded photo was used as subject_reference. Every panel's prompt must
// carry an explicit, standalone style directive, regardless of reference
// strategy or panel index.
func TestBuildComic4Workflow_StyleInjectedIntoEveryPanel(t *testing.T) {
	plans := testPlans("photo-asset", "photo-asset")
	raw := buildComic4Workflow(plans, minimax.RefStrategyAnchor, minimax.LayoutGridEqual, "日系动漫插画风格")
	tasks := mainTasks(t, raw)

	for _, name := range []string{"enhance-panel-1", "enhance-panel-2"} {
		p, _ := param(t, tasks[name], "prompt").Value.(string)
		if !strings.Contains(p, "日系动漫插画风格") {
			t.Errorf("%s's prompt should carry the style directive, got %q", name, p)
		}
		if !strings.Contains(p, "写实") {
			t.Errorf("%s's prompt should explicitly rule out photorealistic rendering, got %q", name, p)
		}
	}
}

func TestStyleInstruction(t *testing.T) {
	if got := styleInstruction(""); got != "" {
		t.Errorf("empty style should produce no instruction, got %q", got)
	}
	got := styleInstruction("美式漫画风格")
	if !strings.Contains(got, "美式漫画风格") || !strings.Contains(got, "写实") {
		t.Errorf("style instruction should quote the style and rule out photorealism, got %q", got)
	}
}

func TestDialogueInstruction(t *testing.T) {
	if got := dialogueInstruction(""); got != "" {
		t.Errorf("empty dialogue should produce no instruction, got %q", got)
	}
	if got := dialogueInstruction("  "); got != "" {
		t.Errorf("blank dialogue should produce no instruction, got %q", got)
	}
	got := dialogueInstruction("你好")
	if !strings.Contains(got, "你好") || !strings.Contains(got, "对话框") {
		t.Errorf("dialogue instruction should quote the dialogue and mention 对话框, got %q", got)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
