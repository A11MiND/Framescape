// Package capability is the single source of truth for the generation
// constraints PRD §10.5 calls the "Capability Matrix" — model limits like
// max batch size, video duration range, and supported resolutions/ratios.
// Before this package existed these numbers were hardcoded independently
// in three places that could drift apart: jobsvc's request-boundary
// validation, each executor's own backstop clamp, and the frontend's
// hardcoded option lists. This package is what all three now read from —
// see GET /api/v1/capabilities (internal/interfaces/http/capabilities.go)
// for the frontend's side of this.
//
// Deliberately scoped to exactly the constraints already enforced
// somewhere in this codebase, not the full PRD §10.5 ModelCap struct
// (mutual-exclusion rules, conditional rules, per-model cost tables) —
// building that from scratch is a real, larger, separate design decision,
// not a rename of existing numbers. See DEV_PLAN.md's own note on why that
// stayed out of scope.
package capability

// Image generation (MiniMax image-01, PRD §3.1).
const (
	ImageMaxN           = 9    // image.single's n upper bound (covers what used to be the separate image.batch workflow) — minimax.image's own MiniMax-imposed hard limit
	ImageMaxPromptChars = 1500 // mirrors prompt.MaxPromptChars; duplicated as a plain constant here so this package stays dependency-free
)

// Video generation (MiniMax-H3, PRD §3.2).
const (
	VideoDurationMin    = 4
	VideoDurationMax    = 15
	VideoMaxPromptChars = 7000
)

// VideoResolutions is the exhaustive, ordered set video.single accepts —
// jobsvc.Create/EstimateCredits reject anything else, minimax.video's own
// normalizeResolution defends the same boundary as a backstop.
var VideoResolutions = []string{"768P", "2K"}

// VideoRatios mirrors minimax.video's buildContent (internal/infra/
// executor/minimax/video.go) — the six ratios valid for a t2va (pure
// text-to-video) submission. "adaptive" is deliberately excluded: it's
// forced automatically for i2va/r2va modes, never a user-selectable choice.
var VideoRatios = []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16"}
