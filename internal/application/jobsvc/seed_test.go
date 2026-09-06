package jobsvc

import (
	"testing"

	"aigc-platform/internal/domain/prompt"
)

// TestResolveSharedSeed_FallsBackWhenUnset is the regression test for the
// "四格漫画/连续图组 came back looking unrelated" bug: with no explicit seed
// and no bound character, every panel/shot used to get an independently
// random seed from MiniMax. resolveSharedSeed must instead invent one seed
// and hand back the exact same value on repeated calls with the same
// (empty) inputs within one job — this test can't observe "same value
// across calls" directly (each call legitimately rolls a fresh seed when
// nothing was provided), so instead it pins the two properties that
// actually matter: a non-nil seed always comes back, and it lands in the
// range image.comic4/image.sequence's caller loop can safely reuse for
// every panel/shot (the caller calls this once and reuses the single
// returned pointer, so cross-call variance is expected and fine).
func TestResolveSharedSeed_FallsBackWhenUnset(t *testing.T) {
	seed := resolveSharedSeed(nil, nil)
	if seed == nil {
		t.Fatal("resolveSharedSeed(nil, nil) = nil, want a fallback seed")
	}
	if *seed < 0 || *seed >= (1<<31) {
		t.Errorf("fallback seed = %d, want in [0, 2^31)", *seed)
	}
}

// TestResolveSharedSeed_ExplicitWins mirrors prompt.Compile's own documented
// precedence (explicit override wins over a character's fixed seed) — this
// is the case where resolveSharedSeed must NOT invent anything.
func TestResolveSharedSeed_ExplicitWins(t *testing.T) {
	explicit := int64(42)
	characters := []prompt.Character{{Description: "a fox", Seed: 999}}
	seed := resolveSharedSeed(characters, &explicit)
	if seed == nil || *seed != 42 {
		t.Errorf("resolveSharedSeed with explicit=42 = %v, want 42", seed)
	}
}

// TestResolveSharedSeed_CharacterSeedWins covers the no-explicit-override
// case: the first bound character with a non-zero fixed seed must be used
// verbatim, never overridden by the random fallback.
func TestResolveSharedSeed_CharacterSeedWins(t *testing.T) {
	characters := []prompt.Character{{Description: "a fox", Seed: 777}}
	seed := resolveSharedSeed(characters, nil)
	if seed == nil || *seed != 777 {
		t.Errorf("resolveSharedSeed with bound character seed=777 = %v, want 777", seed)
	}
}
