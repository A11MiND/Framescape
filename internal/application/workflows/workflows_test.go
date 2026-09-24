package workflows

import (
	"encoding/json"
	"reflect"
	"testing"

	"aigc-platform/internal/domain/workflow"
)

func byName(t *testing.T, p *workflow.Plan) map[string]workflow.NodeSpec {
	t.Helper()
	if err := workflow.Validate(p.Nodes, nil); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	out := map[string]workflow.NodeSpec{}
	for _, n := range p.Nodes {
		out[n.Name] = n
	}
	return out
}

func litValue(t *testing.T, in workflow.Input) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(in.Value, &v); err != nil {
		t.Fatalf("not a literal: %+v", in)
	}
	return v
}

func TestImageSingleMiniMaxUsesFirstReferenceOnly(t *testing.T) {
	p := ImageSinglePlan(ImageSingle{UserID: 7, Provider: ProviderMiniMax, Prompt: "p", N: 4, References: []string{"r1", "r2"}})
	gen := byName(t, p)["gen"]
	if gen.Executor != "minimax.image" || gen.Check != workflow.CheckAllRequested || gen.MaxAttempts != 3 {
		t.Fatalf("gen = %+v", gen)
	}
	if got := litValue(t, gen.Inputs["source-image-asset-id"]); got != "r1" {
		t.Fatalf("source = %v", got)
	}
	if got := litValue(t, gen.Inputs["n"]); got != "4" {
		t.Fatalf("n = %v (MiniMax expects a string)", got)
	}
	if gen.Display["result"] != true {
		t.Fatal("gen must be the result node")
	}
}

func TestImageSequenceReferencesEarlierShots(t *testing.T) {
	p, err := ImageSequencePlan(1, ProviderMiniMax, []SequenceShot{
		{Index: 1, Prompt: "a", Reference: "lib"},
		{Index: 2, Prompt: "b"},
		{Index: 3, Prompt: "c", RefShot: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes := byName(t, p)
	if deps := nodes["shot-2"].Dependencies(); len(deps) != 0 {
		t.Fatalf("independent shot has deps %v", deps)
	}
	if deps := nodes["shot-3"].Dependencies(); !reflect.DeepEqual(deps, []string{"shot-1"}) {
		t.Fatalf("shot-3 deps = %v", deps)
	}
	if _, err := ImageSequencePlan(1, ProviderMiniMax, []SequenceShot{{Index: 1, RefShot: 1}}); err == nil {
		t.Fatal("self reference accepted")
	}
}

func TestComic4ChainAndAnchorShapes(t *testing.T) {
	panels := []ComicPanel{{Index: 1, EnhancePrompt: "e1", RefAsset: "raw"}, {Index: 2, EnhancePrompt: "e2"}, {Index: 3, EnhancePrompt: "e3"}}
	chain, err := Comic4Plan(Comic4{UserID: 1, Provider: ProviderMiniMax, Strategy: RefStrategyChain, Layout: "grid", Style: "s", StylizePrompt: "sp", Panels: panels})
	if err != nil {
		t.Fatal(err)
	}
	c := byName(t, chain)
	if _, ok := c["stylize-reference-1"]; !ok {
		t.Fatal("panel 1's raw reference is not stylized")
	}
	if deps := c["panel-3"].Dependencies(); !reflect.DeepEqual(deps, []string{"enhance-panel-3", "panel-2"}) {
		t.Fatalf("chain panel-3 deps = %v", deps)
	}
	if deps := c["compose"].Dependencies(); !reflect.DeepEqual(deps, []string{"panel-1", "panel-2", "panel-3"}) {
		t.Fatalf("compose deps = %v", deps)
	}
	if got := litValue(t, c["panel-2"].Inputs["expected-style"]); got != "s" {
		t.Fatalf("quality gate missing: %v", got)
	}

	anchored := []ComicPanel{{Index: 1, RefAsset: "a"}, {Index: 2, RefAsset: "a"}, {Index: 3, RefAsset: "b"}}
	anchor, err := Comic4Plan(Comic4{UserID: 1, Provider: ProviderGemini, Strategy: RefStrategyAnchorPerCharacter, Panels: anchored})
	if err != nil {
		t.Fatal(err)
	}
	a := byName(t, anchor)
	if _, ok := a["stylize-reference-2"]; !ok {
		t.Fatal("each distinct reference needs its own stylize pass")
	}
	if _, ok := a["stylize-reference-3"]; ok {
		t.Fatal("a shared reference must be stylized once")
	}
	if deps := a["panel-2"].Dependencies(); !reflect.DeepEqual(deps, []string{"enhance-panel-2", "stylize-reference-1"}) {
		t.Fatalf("anchored panel-2 deps = %v", deps)
	}
	if _, ok := a["panel-2"].Inputs["expected-style"]; ok {
		t.Fatal("Gemini panels must not get MiniMax quality gates")
	}
}

func TestVideoSinglePlanWithEnhance(t *testing.T) {
	p := VideoSinglePlan(VideoSingle{UserID: 1, Prompt: "p", Duration: 6, Resolution: "768P", Enhance: true})
	n := byName(t, p)
	if n["gen"].Inputs["prompt"].Ref == nil || n["gen"].Inputs["prompt"].Ref.Node != "enhance" {
		t.Fatalf("gen prompt not from enhance: %+v", n["gen"].Inputs["prompt"])
	}
	if deps := n["extract"].Dependencies(); !reflect.DeepEqual(deps, []string{"gen"}) {
		t.Fatalf("extract deps = %v", deps)
	}
}

func sequenceShots() []Shot {
	return []Shot{
		{Index: 1, Prompt: "p1", Mode: "r2va", StaticRefImageAssetID: "hero", Enhance: true},
		{Index: 2, Prompt: "p2", Mode: "i2va"},
		{Index: 3, Prompt: "p3", Mode: "i2va"},
		{Index: 4, Prompt: "p4", Mode: "r2va", StaticRefImageAssetID: "hero", Enhance: true, BundleShotIndexes: []int{2, 3}, Outline: "o"},
	}
}

func TestVideoSequenceDraftChainsAndEndsAtGate(t *testing.T) {
	p, err := VideoSequencePlan(VideoSequence{UserID: 1, Shots: sequenceShots(), Duration: 5, Ratio: "16:9", DraftResolution: "768P"})
	if err != nil {
		t.Fatal(err)
	}
	n := byName(t, p)
	if in := n["shot-2"].Inputs["first-frame-asset-id"]; in.Ref == nil || in.Ref.Node != "shot-1-extract" {
		t.Fatalf("shot-2 first frame = %+v", in)
	}
	if deps := n["enhance-shot-4"].Dependencies(); !reflect.DeepEqual(deps, []string{"shot-2", "shot-2-extract", "shot-3", "shot-3-extract"}) {
		t.Fatalf("anchor bundle deps = %v", deps)
	}
	if deps := n["gate"].Dependencies(); !reflect.DeepEqual(deps, []string{"shot-4"}) {
		t.Fatalf("gate deps = %v", deps)
	}
	if _, ok := n["concat"]; ok {
		t.Fatal("preview plan must not concat before the gate decision")
	}
	var meta SequenceMeta
	if err := json.Unmarshal(p.Meta, &meta); err != nil || len(meta.Shots) != 4 {
		t.Fatalf("meta = %s (%v)", p.Meta, err)
	}

	direct, err := VideoSequencePlan(VideoSequence{UserID: 1, Shots: sequenceShots(), Duration: 5, DraftResolution: "2K", SkipPreview: true})
	if err != nil {
		t.Fatal(err)
	}
	d := byName(t, direct)
	if _, ok := d["gate"]; ok {
		t.Fatal("skip-preview plan must not have a gate")
	}
	if deps := d["concat"].Dependencies(); len(deps) != 4 {
		t.Fatalf("direct concat deps = %v", deps)
	}
}

func TestVideoSequenceResumePatch(t *testing.T) {
	meta := SequenceMeta{Shots: sequenceShots(), Duration: 5, Ratio: "16:9"}
	results := map[int]ShotResult{
		1: {AssetID: "c1", LastFrame: "f1"}, 2: {AssetID: "c2", LastFrame: "f2"},
		3: {AssetID: "c3", LastFrame: "f3"}, 4: {AssetID: "c4"},
	}
	patch, err := VideoSequenceResume(1, meta, results, map[int]ShotDecision{
		2: {Redo: true, PromptOverride: "new"},
		4: {Upgrade: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workflow.Validate(patch, map[string]bool{"gate": true}); err != nil {
		t.Fatal(err)
	}
	n := map[string]workflow.NodeSpec{}
	for _, x := range patch {
		n[x.Name] = x
	}
	redo := n["redo-2"]
	if litValue(t, redo.Inputs["prompt"]) != "new" || litValue(t, redo.Inputs["first-frame-asset-id"]) != "f1" {
		t.Fatalf("redo inputs = %+v", redo.Inputs)
	}
	up := n["upgrade-4"]
	if litValue(t, up.Inputs["base-video-asset-id"]) != "c4" {
		t.Fatalf("upgrade base = %v", litValue(t, up.Inputs["base-video-asset-id"]))
	}
	if got := litValue(t, up.Inputs["reference-video-asset-ids"]); !reflect.DeepEqual(got, []any{"c2", "c3"}) {
		t.Fatalf("upgrade bundle videos = %v", got)
	}
	concat := n["concat"]
	if got := litValue(t, concat.Inputs["keep-shot-index"]); !reflect.DeepEqual(got, []any{"1", "3"}) {
		t.Fatalf("keep = %v", got)
	}
	if deps := concat.Dependencies(); !reflect.DeepEqual(deps, []string{"gate", "redo-2", "upgrade-4"}) {
		t.Fatalf("concat deps = %v", deps)
	}
	if _, err := VideoSequenceResume(1, meta, results, map[int]ShotDecision{1: {Redo: true, Upgrade: true}}); err == nil {
		t.Fatal("conflicting decision accepted")
	}
}
