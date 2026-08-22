// Package prompt implements PromptSpec compilation (PRD §5.2/§5.3): turning
// a structured spec — base text plus resolved characters and presets — into
// the single prompt string MiniMax actually receives, plus the seed to use.
//
// Steps 1 (characters), 2 (presets), and 5 (length trim) live in Compile
// below, for image generation. Steps 3 (refs→role mapping) and 4
// (first_frame/reference_image mutual exclusion) are CompileVideoRefs in
// video_refs.go, for video generation — that logic used to live ad-hoc in
// internal/infra/executor/minimax/video.go's own buildContent, moved here
// once video generation existed so this package is genuinely the one place
// PRD §5.3's steps live, not just steps 1/2/5. Step 6 (capability
// validation) and step 7 (asset dedup) are handled elsewhere — dedup by
// internal/infra/executor/minimax's file-upload cache (provider_files),
// capability checks by the Capability Matrix.
package prompt

import "sort"

// MaxPromptChars is MiniMax image generation's hard limit (PRD §3.1). Video
// generation's limit is higher (7000, §3.2) — callers pass Input.MaxChars to
// override this default rather than the compiler guessing from context.
const MaxPromptChars = 1500

// Character is the resolved subset of a characters row a compile needs.
type Character struct {
	Description string
	Seed        int64 // 0 means "no fixed seed"
}

// Preset is the resolved subset of a presets row a compile needs.
type Preset struct {
	PromptFragment string
	Priority       int // higher survives trimming first
}

// Input is everything Compile needs for one generation call.
type Input struct {
	Text       string
	Characters []Character // in slot order (A, B, ...)
	Presets    []Preset
	Seed       *int64 // explicit override; wins over any character's fixed seed
	MaxChars   int    // 0 means "use MaxPromptChars" (the image default)
}

// Output is the compiled result ready to hand to minimax.image's inputs.
type Output struct {
	Prompt string
	Seed   *int64 // nil means "let MiniMax pick one" (PRD §3.1)
}

// Compile expands characters and presets into Text, trims to MaxPromptChars
// by priority (presets first, lowest priority dropped first — characters
// and the base text are never dropped, matching PRD §5.3 step 5's intent
// that priority governs *preset* fragments specifically), and resolves the
// seed.
func Compile(in Input) Output {
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = MaxPromptChars
	}

	// 1. Characters: descriptions are always kept, they're the P0 identity
	// anchor (F3.3) — not subject to the priority-trim in step 5.
	fixed := make([]string, 0, len(in.Characters)+1)
	for _, c := range in.Characters {
		if c.Description != "" {
			fixed = append(fixed, c.Description)
		}
	}
	if in.Text != "" {
		fixed = append(fixed, in.Text)
	}

	// 2. Presets, sorted by priority descending so the trim loop below drops
	// the lowest-priority fragments first when over budget.
	presets := append([]Preset(nil), in.Presets...)
	sort.SliceStable(presets, func(i, j int) bool { return presets[i].Priority > presets[j].Priority })

	base := joinParts(fixed)
	prompt := base
	for _, p := range presets {
		if p.PromptFragment == "" {
			continue
		}
		candidate := joinParts([]string{prompt, p.PromptFragment})
		if len([]rune(candidate)) > maxChars {
			continue // this and any lower-priority fragment are dropped
		}
		prompt = candidate
	}
	if r := []rune(prompt); len(r) > maxChars {
		prompt = string(r[:maxChars]) // fixed content alone still over budget
	}

	seed := in.Seed
	if seed == nil {
		for _, c := range in.Characters {
			if c.Seed != 0 {
				s := c.Seed
				seed = &s
				break
			}
		}
	}

	return Output{Prompt: prompt, Seed: seed}
}

func joinParts(parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += ", " + p
	}
	return out
}
