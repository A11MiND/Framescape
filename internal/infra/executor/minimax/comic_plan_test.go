package minimax

import "testing"

func TestParseComicPlan_HappyPath(t *testing.T) {
	raw := `{"reference_strategy":"anchor","layout_id":"feature-last","panels":[` +
		`{"scene":"猫坐在窗边","action":"","expression":"好奇","details":"","dialogue":"","character_slot":"A"},` +
		`{"scene":"猫追蝴蝶","action":"","expression":"","details":"","dialogue":"抓住你了！","character_slot":"A"}]}`
	req := PlanComicRequest{Story: "一只猫的一天", Count: 2, Characters: []PlanCharacterInfo{{Slot: "A", Name: "小猫", HasImage: true}}}

	plan := parseComicPlan(raw, req)
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if plan.ReferenceStrategy != RefStrategyAnchor {
		t.Errorf("reference_strategy = %q, want %q", plan.ReferenceStrategy, RefStrategyAnchor)
	}
	if plan.LayoutID != LayoutFeatureLast {
		t.Errorf("layout_id = %q, want %q", plan.LayoutID, LayoutFeatureLast)
	}
	if len(plan.Panels) != 2 {
		t.Fatalf("got %d panels, want 2", len(plan.Panels))
	}
	if plan.Style == "" {
		t.Error("style should never be empty even when the model omits it")
	}
	if plan.Panels[1].Dialogue != "抓住你了！" {
		t.Errorf("panel 2 dialogue = %q, want the parsed line", plan.Panels[1].Dialogue)
	}
}

func TestParseComicPlan_MissingStyle_DefaultsToDefaultComicStyle(t *testing.T) {
	raw := `{"reference_strategy":"none","layout_id":"grid-equal","panels":[{"scene":"a"}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Count: 1})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if plan.Style != DefaultComicStyle {
		t.Errorf("style = %q, want the default %q", plan.Style, DefaultComicStyle)
	}
}

// TestParseComicPlan_PreservesExplicitStyle covers the actual failure this
// field was added for: a user's own input carrying a global style directive
// (e.g. "cute anime style") ahead of its per-panel text — the planner is
// expected to lift that into "style" and this function must keep it, not
// silently overwrite it with the default.
func TestParseComicPlan_PreservesExplicitStyle(t *testing.T) {
	raw := `{"reference_strategy":"none","layout_id":"grid-equal","style":"可爱动漫风格，明亮色彩","panels":[{"scene":"a"}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Count: 1})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if plan.Style != "可爱动漫风格，明亮色彩" {
		t.Errorf("style = %q, want the model's own explicit value preserved", plan.Style)
	}
}

func TestParseComicPlan_WrappedInProseAndFencing(t *testing.T) {
	raw := "这是规划结果：\n```json\n" +
		`{"reference_strategy":"none","layout_id":"grid-equal","panels":[{"scene":"a","action":"","expression":"","details":"","dialogue":"","character_slot":""}]}` +
		"\n```\n谢谢。"
	plan := parseComicPlan(raw, PlanComicRequest{Story: "x", Count: 1})
	if plan == nil {
		t.Fatal("expected parseComicPlan to tolerate surrounding prose/fencing, same as parseSmartPicks")
	}
	if plan.ReferenceStrategy != RefStrategyNone {
		t.Errorf("reference_strategy = %q, want %q", plan.ReferenceStrategy, RefStrategyNone)
	}
}

func TestParseComicPlan_NoJSONObject_ReturnsNil(t *testing.T) {
	if plan := parseComicPlan("sorry, I can't help with that", PlanComicRequest{Count: 4}); plan != nil {
		t.Errorf("expected nil for a response with no JSON object, got %+v", plan)
	}
}

func TestParseComicPlan_MalformedJSON_ReturnsNil(t *testing.T) {
	if plan := parseComicPlan(`{"reference_strategy": "anchor", "panels": [}`, PlanComicRequest{Count: 4}); plan != nil {
		t.Errorf("expected nil for malformed JSON, got %+v", plan)
	}
}

func TestParseComicPlan_EmptyPanels_ReturnsNil(t *testing.T) {
	if plan := parseComicPlan(`{"reference_strategy":"none","layout_id":"grid-equal","panels":[]}`, PlanComicRequest{Count: 4}); plan != nil {
		t.Errorf("expected nil when the model returns zero panels, got %+v", plan)
	}
}

func TestParseComicPlan_InvalidReferenceStrategy_FallsBackByHeuristic(t *testing.T) {
	raw := `{"reference_strategy":"made_up_value","layout_id":"grid-equal","panels":[{"scene":"a","action":"","expression":"","details":"","dialogue":"","character_slot":""}]}`
	req := PlanComicRequest{Count: 1, Characters: []PlanCharacterInfo{{Slot: "A", HasImage: true}, {Slot: "B", HasImage: true}}}
	plan := parseComicPlan(raw, req)
	if plan == nil {
		t.Fatal("expected a non-nil plan (invalid enum should fall back, not fail the whole plan)")
	}
	if plan.ReferenceStrategy != RefStrategyAnchorPerCharacter {
		t.Errorf("with 2 bound characters, fallback should be %q, got %q", RefStrategyAnchorPerCharacter, plan.ReferenceStrategy)
	}
}

func TestParseComicPlan_InvalidLayout_FallsBackToGridEqual(t *testing.T) {
	raw := `{"reference_strategy":"none","layout_id":"some-fancy-layout","panels":[{"scene":"a","action":"","expression":"","details":"","dialogue":"","character_slot":""}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Count: 1})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if plan.LayoutID != LayoutGridEqual {
		t.Errorf("layout_id = %q, want fallback %q", plan.LayoutID, LayoutGridEqual)
	}
}

func TestParseComicPlan_PadsShortfallToExactCount(t *testing.T) {
	raw := `{"reference_strategy":"none","layout_id":"grid-equal","panels":[{"scene":"only one","action":"","expression":"","details":"","dialogue":"","character_slot":""}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Count: 4})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if len(plan.Panels) != 4 {
		t.Fatalf("got %d panels, want exactly the requested Count=4", len(plan.Panels))
	}
}

func TestParseComicPlan_TruncatesExcessToExactCount(t *testing.T) {
	raw := `{"reference_strategy":"none","layout_id":"grid-equal","panels":[` +
		`{"scene":"1"},{"scene":"2"},{"scene":"3"},{"scene":"4"},{"scene":"5"}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Count: 3})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if len(plan.Panels) != 3 {
		t.Fatalf("got %d panels, want exactly the requested Count=3", len(plan.Panels))
	}
	if plan.Panels[2].Scene != "3" {
		t.Errorf("panels[2].Scene = %q, want the first 3 in order", plan.Panels[2].Scene)
	}
}

func TestParseComicPlan_ManualMode_NeverRewritesUserScene(t *testing.T) {
	userPanels := []string{"用户写的第一格", "用户写的第二格"}
	raw := `{"reference_strategy":"anchor","layout_id":"grid-equal","panels":[` +
		`{"scene":"AI 改写的第一格","action":"a1","expression":"e1","details":"d1","dialogue":"","character_slot":""},` +
		`{"scene":"AI 改写的第二格","action":"a2","expression":"e2","details":"d2","dialogue":"你好","character_slot":""}]}`
	plan := parseComicPlan(raw, PlanComicRequest{Panels: userPanels, Count: 2})
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	for i, want := range userPanels {
		if plan.Panels[i].Scene != want {
			t.Errorf("panel %d Scene = %q, want the user's own unmodified text %q", i, plan.Panels[i].Scene, want)
		}
		if plan.Panels[i].Action != "" || plan.Panels[i].Expression != "" || plan.Panels[i].Details != "" {
			t.Errorf("panel %d should have its rewrite-only fields cleared in manual mode, got %+v", i, plan.Panels[i])
		}
	}
	if plan.Panels[1].Dialogue != "你好" {
		t.Errorf("manual mode should still keep the planner's dialogue suggestion, got %q", plan.Panels[1].Dialogue)
	}
}

func TestParseComicPlan_InvalidCharacterSlot_ClearsToDefault(t *testing.T) {
	raw := `{"reference_strategy":"anchor_per_character","layout_id":"grid-equal","panels":[` +
		`{"scene":"a","character_slot":"Z"}]}`
	req := PlanComicRequest{Count: 1, Characters: []PlanCharacterInfo{{Slot: "A"}, {Slot: "B"}}}
	plan := parseComicPlan(raw, req)
	if plan == nil {
		t.Fatal("expected a non-nil plan")
	}
	if plan.Panels[0].CharacterSlot != "" {
		t.Errorf("character_slot referencing an unbound slot should clear to \"\", got %q", plan.Panels[0].CharacterSlot)
	}
}

func TestFallbackReferenceStrategy(t *testing.T) {
	cases := []struct {
		name string
		req  PlanComicRequest
		want string
	}{
		{"no image, no characters", PlanComicRequest{}, RefStrategyNone},
		{"one character with image", PlanComicRequest{Characters: []PlanCharacterInfo{{Slot: "A", HasImage: true}}}, RefStrategyAnchor},
		{"one character without image", PlanComicRequest{Characters: []PlanCharacterInfo{{Slot: "A", HasImage: false}}}, RefStrategyNone},
		{"ad hoc source image, no characters", PlanComicRequest{HasSourceImage: true}, RefStrategyAnchor},
		{"two characters", PlanComicRequest{Characters: []PlanCharacterInfo{{Slot: "A"}, {Slot: "B"}}}, RefStrategyAnchorPerCharacter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fallbackReferenceStrategy(c.req); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestComicPanelPlan_Text_JoinsNonEmptyFields(t *testing.T) {
	p := ComicPanelPlan{Scene: "教室", Action: "", Expression: "开心", Details: ""}
	if got, want := p.Text(), "教室，开心"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}
