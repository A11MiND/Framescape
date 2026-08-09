package prompt

import "testing"

func TestCompile_CharacterSeedWinsWhenNoExplicitSeed(t *testing.T) {
	out := Compile(Input{
		Text:       "standing in the rain",
		Characters: []Character{{Description: "a boy in a green jacket", Seed: 12345}},
	})
	if out.Seed == nil || *out.Seed != 12345 {
		t.Fatalf("expected character seed 12345, got %v", out.Seed)
	}
	if out.Prompt != "a boy in a green jacket, standing in the rain" {
		t.Fatalf("unexpected prompt: %q", out.Prompt)
	}
}

func TestCompile_ExplicitSeedOverridesCharacter(t *testing.T) {
	explicit := int64(999)
	out := Compile(Input{
		Text:       "x",
		Characters: []Character{{Description: "y", Seed: 12345}},
		Seed:       &explicit,
	})
	if out.Seed == nil || *out.Seed != 999 {
		t.Fatalf("expected explicit seed to win, got %v", out.Seed)
	}
}

func TestCompile_PresetsOrderedByPriorityDescending(t *testing.T) {
	out := Compile(Input{
		Text: "base",
		Presets: []Preset{
			{PromptFragment: "low-prio", Priority: 1},
			{PromptFragment: "high-prio", Priority: 100},
		},
	})
	if out.Prompt != "base, high-prio, low-prio" {
		t.Fatalf("unexpected prompt ordering: %q", out.Prompt)
	}
}

func TestCompile_DropsLowestPriorityPresetsWhenOverBudget(t *testing.T) {
	longFragment := make([]byte, MaxPromptChars-10)
	for i := range longFragment {
		longFragment[i] = 'a'
	}
	out := Compile(Input{
		Text: "base",
		Presets: []Preset{
			{PromptFragment: string(longFragment), Priority: 100},
			{PromptFragment: "this should be dropped", Priority: 1},
		},
	})
	if len([]rune(out.Prompt)) > MaxPromptChars {
		t.Fatalf("prompt exceeds MaxPromptChars: %d", len([]rune(out.Prompt)))
	}
	if contains(out.Prompt, "should be dropped") {
		t.Fatalf("expected low-priority fragment to be dropped, got: %q", out.Prompt)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
