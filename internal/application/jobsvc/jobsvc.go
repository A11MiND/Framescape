// Package jobsvc implements Job creation/lookup (PRD §15's
// internal/application/jobsvc: Create / Cancel / Resume / Retry). Cancel/
// Resume/Retry land with the workflows that need them (F7.4/F7.5/F7.6,
// W5-W7).
package jobsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
	workflowdefs "aigc-platform/workflows"
)

// definitions maps our business-layer workflow_name (dotted, e.g.
// "image.single") to the aether/v1 document that implements it. Static files
// only — video.sequence (W6) and image.sequence (its own cross-shot-
// referencing extension) both generate their DAG dynamically per job instead
// of loading a static file (docs/aether-validation-report.md §four, and
// image_sequence.go's package doc), so neither is listed here; Create()
// special-cases both before this map is ever consulted.
var definitions = map[string]string{
	"image.single": "image-single",
	"image.batch":  "image-batch",
	"image.comic4": "image-comic4",
	"video.single": "video-single",
}

// CharacterSlot binds a character to a generation slot (F3.2; slots beyond
// "A"/"B" are accepted but the PRD only defines those two).
type CharacterSlot struct {
	Slot        string `json:"slot"`
	CharacterID string `json:"character_id"`
}

// Spec is a growing subset of PromptSpec (PRD §5.2). `refs`/multi-modal
// fields are still out of scope — those matter once video generation
// (W5/W6) needs role-mapped references; images only need characters,
// presets, and a seed.
type Spec struct {
	Text       string          `json:"text"`
	N          int             `json:"n,omitempty"`          // image.batch only: 2..9 (PRD F5.2)
	Panels     []string        `json:"panels,omitempty"`     // image.comic4 only: exactly 4 panel prompts (PRD F5.3)
	Story      string          `json:"story,omitempty"`      // image.comic4 only, F5.4: auto-split into 4 panels instead of Panels; ignored if Panels is set
	Shots      []string        `json:"shots,omitempty"`      // image.sequence only: N shot descriptions (PRD F5.5)
	// ShotSourceRefs is image.sequence's cross-shot referencing (§07 gap: a
	// user asked to #-reference a sibling shot's about-to-be-generated
	// image, not just an existing library asset). Parallel array to Shots,
	// same length when present: ShotSourceRefs[i] is 0 for "no cross-shot
	// reference" (shot i falls back to the batch-wide SourceImageAssetID, if
	// any — unchanged default behavior), or a 1-based index j < i+1 of an
	// earlier shot in this same batch whose generated asset becomes shot
	// i's own source-image-asset-id once shot j finishes. Backward-only by
	// construction (the frontend picker only ever offers earlier shots, and
	// image_sequence.go's validateShotSourceRefs rejects anything else
	// server-side too) — a forward or self reference would be a real
	// dependency cycle, not just tasteless.
	ShotSourceRefs []int `json:"shot_source_refs,omitempty"`
	// ImageSequenceMode mirrors Comic4Mode's own two-mode split, applied to
	// image.sequence for the same reason: "甚至不只是四格漫畫" was explicit
	// about this not being comic4-only. Empty/"quick" is the default and
	// unchanged: shots stay fully independent unless a #-mention manually
	// sets ShotSourceRefs. "continuity" changes only the *default* for a
	// shot with no explicit override — it becomes the immediately preceding
	// shot's own output instead of no reference at all — while a manual
	// #-mention still wins either way (image_sequence.go's own
	// createImageSequence resolves this, ShotSourceRefs' own "值得保留" ask
	// applies here unchanged).
	ImageSequenceMode string `json:"image_sequence_mode,omitempty"` // image.sequence only: "quick" (default) | "continuity"
	Characters []CharacterSlot `json:"characters,omitempty"` // F3.2
	PresetIDs  []string        `json:"preset_ids,omitempty"` // F4.3
	Seed       *int64          `json:"seed,omitempty"`       // explicit override; else a bound character's fixed seed wins
	// SourceImageAssetID is F5.8's image-to-image input — originally
	// image.single only, extended to every image.* mode (§07 gap: a user
	// asked why reference-image support wasn't universal) since
	// minimax.image's subject_reference is already per-call, not tied to
	// any one workflow shape; image.batch applies it to the whole n-batch,
	// image.comic4/image.sequence apply it to every panel/shot the same
	// way they already share one seed (F5.5's "同 seed" reasoning extends
	// unchanged to "同 reference").
	SourceImageAssetID string `json:"source_image_asset_id,omitempty"`

	// Comic4Mode is image.comic4's own two-mode split (§07 gap: 4 panels
	// generated fully independently in parallel, sharing only a seed and
	// character text, never actually looked coherent as one comic) — an
	// explicit ask to keep both rather than replace one with the other:
	// "兩套並存，就說兩種模式". Empty/"quick" is the default and is
	// completely unchanged from before this field existed — still the
	// static workflows/image-comic4.json / image-comic4-auto.json Loop,
	// still 4 independent panels. "continuity" routes to a Go-generated DAG
	// (image_comic4.go) where every panel after the first automatically
	// references the immediately preceding panel's own output (image_
	// generation's subject_reference is verified capped at exactly one
	// image per call — video_sequence.go's package doc covers the same
	// verification for the unrelated, much higher video r2va cap) and gets
	// an H3-Context-IR-enriched prompt aware of the story so far, not just
	// its own isolated text.
	Comic4Mode string `json:"comic4_mode,omitempty"` // image.comic4 only: "quick" (default) | "continuity"

	// video.single only (F6.1-F6.3; PRD §3.2). Exactly one of
	// {FirstFrameAssetID, LastFrameAssetID} vs the three Reference*AssetIDs
	// fields may be set — enforced again in minimax.video itself, but this is
	// the shape the frontend's F6.5 dual-guard validates against too.
	// Character-driven reference_image (F6.4, §5.3 step 1) is P1 and not
	// wired yet: callers pass asset IDs directly for now.
	DurationSeconds        int      `json:"duration_seconds,omitempty"` // also video.sequence: applied to every shot
	Resolution             string   `json:"resolution,omitempty"`       // 768P (default) | 2K
	Ratio                  string   `json:"ratio,omitempty"`            // also video.sequence: used only by shots that fall back to t2va (no character bound)
	FirstFrameAssetID      string   `json:"first_frame_asset_id,omitempty"`
	LastFrameAssetID       string   `json:"last_frame_asset_id,omitempty"`
	ReferenceImageAssetIDs []string `json:"reference_image_asset_ids,omitempty"`
	ReferenceVideoAssetIDs []string `json:"reference_video_asset_ids,omitempty"`
	ReferenceAudioAssetIDs []string `json:"reference_audio_asset_ids,omitempty"`
	// PromptEnhance is F6.10 (§3.4): opt-in H3-Context-IR prompt-enhancement
	// node before gen-video, only meaningful for video.single. Routes to the
	// video-single-enhanced workflow file instead of video-single — see
	// definitions' selection logic in Create().
	PromptEnhance bool `json:"prompt_enhance,omitempty"`

	// video.sequence only (F6.7/F6.8, PRD §5.4/§5.5). Shots reuses the same
	// field image.sequence already uses (N shot descriptions); the only
	// video.sequence-specific addition is RecalibrateEvery.
	RecalibrateEvery int `json:"recalibrate_every,omitempty"` // §5.4: every N shots, re-anchor with r2va instead of i2va tail-frame continuity. 0/negative means "never" (only shot 1 uses r2va).
	// SkipPreview opts out of §12.3's 768P preview gate — the draft
	// generates directly at 2K (full cost held upfront) and the gate is
	// auto-resumed server-side (upkeep.Runner's autoResumeSkipPreview
	// duty) the moment it suspends, keeping every shot as-is with nothing
	// to redo/upgrade. §07 gap: a user found the always-preview-first flow
	// unnecessarily slow once they already trust a prompt/style combo —
	// this trades away the cost protection PreviewGate exists for
	// (catching a bad multi-shot run before paying 2K on every shot), so
	// it's opt-in, not a default.
	SkipPreview bool `json:"skip_preview,omitempty"` // video.sequence only

	// SourceVideoAssetID is video.sequence's r2va anchor when the reference
	// is a video rather than an image — MiniMax's r2va mode accepts either
	// (internal/infra/executor/minimax/video.go's videoRefs already threads
	// a video_url content item through unchanged, same as video.single's own
	// ReferenceVideoAssetIDs). §07 gap: the anchor picker only ever offered
	// images even though the provider itself doesn't require that. Mutually
	// exclusive with SourceImageAssetID in practice (the frontend picker
	// clears one when the other is chosen); if a caller somehow sets both,
	// SourceImageAssetID wins — see video_sequence.go's characterRefAssetID
	// resolution.
	SourceVideoAssetID string `json:"source_video_asset_id,omitempty"` // video.sequence only

	// NarrativeContinuity is video.sequence's opt-in upgrade to how r2va
	// anchor shots get referenced (§07 gap: comic4/image.sequence/
	// video.sequence's panel-to-panel consistency was too weak — same seed
	// and character text, no real visual history). Off (default): an
	// anchor shot still uses exactly one static reference (SourceImageAssetID/
	// SourceVideoAssetID), identical to today's behavior. On: every anchor
	// shot's reference becomes a real bundle built from the shots already
	// generated earlier in this same job — most-recent full clips (as many
	// as fit MiniMax's real reference_video budget: ≤3 clips, ≤15s combined,
	// verified directly against the live API) plus their extracted
	// keyframes plus the protagonist's own reference image — and that
	// bundle, together with a running story outline, is run through
	// MiniMax's H3-Context-IR (minimax.prompt_enhance, already wired for
	// video.single as F6.10, now reused here) before the real r2va call, so
	// the model is actually shown what happened rather than just told in
	// one static image. See image_sequence_video.go's package doc for the
	// full design and why this needed real API verification first.
	NarrativeContinuity bool `json:"narrative_continuity,omitempty"` // video.sequence only

	// ReferenceSelectionMode picks how an anchor shot's bundle gets built
	// when NarrativeContinuity is on. Only three values are recognized;
	// empty defaults to "window":
	//   - "window": most-recent-first sliding window (the default) — packs
	//     as many of the immediately preceding shots' clips as fit the
	//     15s reference_video budget.
	//   - "manual": ShotReferenceOverrides wins instead of the window —
	//     the same "#-mention picks a specific earlier shot" UX
	//     image.sequence's ShotSourceRefs already established, kept here
	//     rather than replaced by the new automatic modes (an explicit ask:
	//     "引用# 這個本身是個值得的功能，需要你保留").
	//   - "smart": one MiniMax-M3 text call (reusing minimax.text.split_story's
	//     own pattern) reads every shot's text up front and decides, per
	//     anchor, which earlier shot(s) actually matter narratively —
	//     instead of assuming "most recent" is always "most relevant".
	ReferenceSelectionMode string `json:"reference_selection_mode,omitempty"` // video.sequence only

	// ShotReferenceOverrides is video.sequence's own #-mention override,
	// same shape and same backward-only rule as image.sequence's
	// Spec.ShotSourceRefs: index i (0-based, matching Shots) holds the
	// 1-based index of an earlier shot this shot should anchor on instead
	// of whatever ReferenceSelectionMode would otherwise pick, or 0 for
	// "no override". Meaningful in every mode, not just "manual" — window
	// and smart both still let a caller pin one shot by hand without
	// switching modes; "manual" just means *only* overrides are used, with
	// no automatic window/smart fallback for shots that don't set one.
	ShotReferenceOverrides []int `json:"shot_reference_overrides,omitempty"` // video.sequence only
}

type Service struct {
	db      *gorm.DB
	eng     workflow.Engine
	credits *creditsvc.Service
	// minimax backs exactly one synchronous call: video.sequence's "smart"
	// reference-selection mode (Spec.ReferenceSelectionMode's own doc) needs
	// one MiniMax-M3 chat completion, at Create()/Resume() time, to decide
	// which earlier shots to reference — before any DAG exists for it to run
	// as a task node instead. Every other MiniMax call in this codebase
	// happens inside an executor (the worker process), never here; this is
	// a narrow, deliberate exception, same class as cmd/api's own trial.go
	// and cmd/scheduler's F8.3 post-hoc review.
	minimax *minimax.Client
}

func New(db *gorm.DB, eng workflow.Engine, credits *creditsvc.Service, minimaxClient *minimax.Client) *Service {
	return &Service{db: db, eng: eng, credits: credits, minimax: minimaxClient}
}

// Create submits a new Job: resolves any bound characters/presets, compiles
// the final prompt(s) via internal/domain/prompt, submits to the engine, and
// persists the jobs row with the resulting workflow_run_id (PRD §5.1: Job
// and WorkflowRun are 1:1 but kept separate — business semantics here,
// orchestration state in the Aether store).
//
// idemKey is §11.5's HTTP Idempotency-Key defense: optional (empty string
// disables it entirely — a caller that doesn't send the header gets the old
// no-dedup behavior), but when present, a retried request with the same key
// returns the original job instead of holding credits or submitting to the
// engine a second time. jobs.idem_key + its uk_user_idem unique index
// (both already existed since the W1 schema, unused until now) do the real
// enforcement; the SELECT-first check here is just the fast path that skips
// redundant work for the common case (retry arrives after the original
// request already finished).
func (s *Service) Create(ctx context.Context, userID uint64, workflowName string, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	if idemKey != "" {
		existing, err := s.findByIdemKey(ctx, userID, idemKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	if workflowName == "video.sequence" {
		// Dynamically generated DAG, not a static workflows/*.json file — see
		// video_sequence.go's package doc and docs/aether-validation-report.md
		// §四 for why (shot count N is per-job, and Aether's Loop can't chain
		// "this shot's first_frame = previous shot's runtime-produced last
		// frame", so the draft phase must be a code-generated linear DAG).
		return s.createVideoSequence(ctx, userID, spec, idemKey, projectID)
	}
	if workflowName == "image.sequence" {
		// Also dynamically generated, same reasoning as video.sequence
		// above: ShotSourceRefs' own doc explains why a shot's
		// source-image-asset-id can now depend on another shot's
		// runtime-produced output, which a static Loop-based document (the
		// old workflows/image-sequence.json) has no way to express — see
		// image_sequence.go's package doc.
		return s.createImageSequence(ctx, userID, spec, idemKey, projectID)
	}
	if workflowName == "image.comic4" && spec.Comic4Mode == "continuity" {
		// Also dynamically generated, only for this one mode — "quick" (the
		// default) keeps using the static file below entirely unchanged. See
		// Spec.Comic4Mode's own doc and image_comic4.go's package doc.
		return s.createImageComic4Continuity(ctx, userID, spec, idemKey, projectID)
	}

	defFile, ok := definitions[workflowName]
	if !ok {
		return nil, fmt.Errorf("unknown workflow_name %q", workflowName)
	}
	if workflowName == "video.single" && spec.PromptEnhance {
		defFile = "video-single-enhanced"
	}
	if workflowName == "image.comic4" && len(spec.Panels) == 0 && spec.Story != "" {
		defFile = "image-comic4-auto"
	}
	raw, err := workflowdefs.FS.ReadFile(defFile + ".json")
	if err != nil {
		return nil, fmt.Errorf("load workflow definition %q: %w", defFile, err)
	}

	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	args := map[string]any{
		"user-id": strconv.FormatUint(userID, 10),
	}
	estimatedCredits := 0
	switch workflowName {
	case "image.single":
		compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed})
		args["prompt"] = compiled.Prompt
		args["seed"] = formatSeed(compiled.Seed)
		args["source-image-asset-id"] = spec.SourceImageAssetID
		estimatedCredits = creditsvc.EstimateImageCredits(1)
	case "image.batch":
		compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed})
		args["prompt"] = compiled.Prompt
		args["seed"] = formatSeed(compiled.Seed)
		args["source-image-asset-id"] = spec.SourceImageAssetID
		n := spec.N
		if n <= 0 {
			n = 4
		}
		if n > capability.ImageMaxN {
			n = 9 // mirrors image.go's own MiniMax-hard-limit clamp, so the hold matches what actually runs
		}
		args["n"] = strconv.Itoa(n)
		estimatedCredits = creditsvc.EstimateImageCredits(n)
	case "image.comic4":
		switch {
		case len(spec.Panels) == 4:
			// Items are objects, not bare strings: a loop body task's inputs are
			// populated directly from each item's own fields — loop.arguments
			// {{...}} interpolation does not resolve for values pulled from
			// inputs.parameters/workflow.parameters inside a loop (empirically
			// verified against the real engine; see docs/aether-validation-report.md
			// §四 W4 addendum). user-id has to ride along on every item since
			// there is no other way to get a constant into the loop body.
			panels := make([]map[string]any, 4)
			for i, panelText := range spec.Panels {
				compiled := prompt.Compile(prompt.Input{Text: panelText, Characters: characters, Presets: presets, Seed: spec.Seed})
				panels[i] = map[string]any{
					"prompt": compiled.Prompt, "seed": formatSeed(compiled.Seed),
					"source-image-asset-id": spec.SourceImageAssetID, "user-id": strconv.FormatUint(userID, 10),
				}
			}
			args["panels"] = panels
			// Per-node, not EstimateImageCredits(4): each panel is its own
			// minimax.image call (Loop body), so each pays the per-node
			// credit floor independently — see EstimatePerNodeImageCredits's
			// doc for why the combined-cost formula undercounts this.
			estimatedCredits = creditsvc.EstimatePerNodeImageCredits(4)
		case spec.Story != "":
			// F5.4: routed to image-comic4-auto.json (see defFile selection
			// above), whose split-story node builds the panels array itself —
			// no character/preset compilation on the auto-split path, see
			// story_split.go's doc.
			args["story"] = spec.Story
			args["source-image-asset-id"] = spec.SourceImageAssetID
			estimatedCredits = creditsvc.EstimatePerNodeImageCredits(4) + creditsvc.EstimateStorySplitCredits()
		default:
			return nil, fmt.Errorf("image.comic4 requires exactly 4 panels, or a story to auto-split")
		}
	case "video.single":
		// §3.2's 7000-char cap, not image's 1500 (PRD §3.1) — video.go itself
		// also hard-truncates at 7000 as a backstop, same belt-and-braces
		// pattern as image.go's 1500 check.
		compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed, MaxChars: 7000})
		args["prompt"] = compiled.Prompt
		if spec.Resolution != "" && !slices.Contains(capability.VideoResolutions, spec.Resolution) {
			return nil, fmt.Errorf("resolution must be 768P or 2K, got %q", spec.Resolution)
		}
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > capability.VideoDurationMax {
			duration = 15 // mirrors video.go's normalizeDuration clamp, so the hold matches what actually runs
		}
		args["duration"] = strconv.Itoa(duration)
		resolution := spec.Resolution
		if resolution == "" {
			resolution = "768P"
		}
		args["resolution"] = resolution
		args["ratio"] = spec.Ratio
		args["first-frame-asset-id"] = spec.FirstFrameAssetID
		args["last-frame-asset-id"] = spec.LastFrameAssetID
		refImageIDs := spec.ReferenceImageAssetIDs
		// F6.4's character-driven auto reference: if the caller bound a
		// character (F3.2) but didn't already pass explicit reference
		// images/first-last-frame, fall back to that character's own F3.1
		// reference images instead of requiring the caller to re-pick the
		// same assets by hand. An explicit pass-through always wins — this
		// never overrides asset IDs the caller actually supplied.
		if len(refImageIDs) == 0 && spec.FirstFrameAssetID == "" && spec.LastFrameAssetID == "" && len(spec.Characters) > 0 {
			autoRefs, err := s.resolveCharacterRefAssetIDs(ctx, userID, spec.Characters)
			if err != nil {
				return nil, err
			}
			refImageIDs = autoRefs
		}
		args["reference-image-asset-ids"] = nonNil(refImageIDs)
		args["reference-video-asset-ids"] = nonNil(spec.ReferenceVideoAssetIDs)
		args["reference-audio-asset-ids"] = nonNil(spec.ReferenceAudioAssetIDs)
		estimatedCredits = creditsvc.EstimateVideoCredits(duration, resolution)
		if spec.PromptEnhance {
			estimatedCredits += creditsvc.EstimatePromptEnhanceCredits()
		}
	}

	// §12.3: hold before Submit, never after — a failed hold (insufficient
	// balance) must never let a job start running.
	bizID := id.New()
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, "job", workflowName); err != nil {
		return nil, fmt.Errorf("hold credits: %w", err)
	}

	runID, err := s.eng.Submit(ctx, &workflow.Definition{Name: defFile, JSON: raw}, args)
	if err != nil {
		// The hold already happened — refund it immediately rather than
		// leaving credits stuck in held with no job to ever settle them.
		_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, estimatedCredits)
		return nil, fmt.Errorf("submit workflow: %w", err)
	}

	title := spec.Text
	if title == "" && len(spec.Panels) > 0 {
		title = spec.Panels[0]
	}
	if title == "" && len(spec.Shots) > 0 {
		title = spec.Shots[0]
	}
	if title == "" && spec.Story != "" {
		// F5.4's auto-split path: spec.Text/Panels/Shots are all empty, only
		// Story is set — without this, the job (and JobDetail's page title)
		// would show a blank heading.
		title = spec.Story
	}
	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
		ProjectID:       projectID,
		WorkflowName:    workflowName,
		WorkflowRunID:   string(runID),
		Title:           truncate(title, 128),
		Status:          "running",
		Spec:            specJSON,
		CreditEstimated: estimatedCredits,
		CreditHeld:      estimatedCredits,
		IdemKey:         nullableIdemKey(idemKey),
		StartedAt:       ptrTime(time.Now()),
	}
	if err := s.db.WithContext(ctx).Create(job).Error; err != nil {
		return s.handleDuplicateIdemKey(ctx, err, userID, idemKey, bizID, estimatedCredits)
	}
	return job, nil
}

// EstimateCredits is §13.3's POST /jobs/estimate: the same Hold-time
// arithmetic Create (and createVideoSequence) run, factored out as a pure
// function with no DB access so it can be quoted to the user before they
// submit anything. Deliberately duplicates Create's per-branch numbers
// rather than having Create call this — Create's switch interleaves credit
// math with prompt compilation and defFile selection in ways that don't
// separate cleanly, and this is the one money-adjacent path in the whole
// codebase where "don't touch the tested original" outweighs "don't repeat
// yourself." Keep in sync with Create's switch and createVideoSequence's
// own estimatedCredits line by hand.
func EstimateCredits(workflowName string, spec Spec) (int, error) {
	switch workflowName {
	case "image.single":
		return creditsvc.EstimateImageCredits(1), nil
	case "image.batch":
		n := spec.N
		if n <= 0 {
			n = 4
		}
		if n > capability.ImageMaxN {
			n = 9
		}
		return creditsvc.EstimateImageCredits(n), nil
	case "image.comic4":
		switch {
		case len(spec.Panels) == 4, spec.Story != "":
			// nothing else to validate — the two sub-cases below only
			// differ in cost, not in which shot count/story check applies.
		default:
			return 0, fmt.Errorf("image.comic4 requires exactly 4 panels, or a story to auto-split")
		}
		credits := creditsvc.EstimatePerNodeImageCredits(4)
		if len(spec.Panels) != 4 && spec.Story != "" {
			credits += creditsvc.EstimateStorySplitCredits()
		}
		if spec.Comic4Mode == "continuity" {
			// Continuity Mode's own createImageComic4Continuity holds the
			// same +4*EstimatePromptEnhanceCredits() — every panel gets
			// enhanced (image_comic4.go's own doc), regardless of whether
			// it's a static or chained reference.
			credits += 4 * creditsvc.EstimatePromptEnhanceCredits()
		}
		return credits, nil
	case "image.sequence":
		if len(spec.Shots) == 0 {
			return 0, fmt.Errorf("image.sequence requires at least 1 shot")
		}
		return creditsvc.EstimatePerNodeImageCredits(len(spec.Shots)), nil
	case "video.single":
		if spec.Resolution != "" && !slices.Contains(capability.VideoResolutions, spec.Resolution) {
			return 0, fmt.Errorf("resolution must be 768P or 2K, got %q", spec.Resolution)
		}
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > capability.VideoDurationMax {
			duration = 15
		}
		resolution := spec.Resolution
		if resolution == "" {
			resolution = "768P"
		}
		credits := creditsvc.EstimateVideoCredits(duration, resolution)
		if spec.PromptEnhance {
			credits += creditsvc.EstimatePromptEnhanceCredits()
		}
		return credits, nil
	case "video.sequence":
		if len(spec.Shots) == 0 {
			return 0, fmt.Errorf("video.sequence requires at least 1 shot")
		}
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > capability.VideoDurationMax {
			duration = 15
		}
		// §12.3's "预览门只预扣 768P 部分积分" — matches createVideoSequence's
		// own estimatedCredits line exactly (the 2K upgrade delta is only ever
		// held later, at Resume) — unless SkipPreview (Spec's own doc) is
		// generating the draft directly at 2K instead.
		draftResolution := "768P"
		if spec.SkipPreview {
			draftResolution = "2K"
		}
		credits := creditsvc.EstimateVideoCredits(duration, draftResolution) * len(spec.Shots)
		if spec.NarrativeContinuity {
			credits += countAnchorShots(len(spec.Shots), spec.RecalibrateEvery) * creditsvc.EstimatePromptEnhanceCredits()
		}
		return credits, nil
	default:
		return 0, fmt.Errorf("unknown workflow_name %q", workflowName)
	}
}

// EstimateItem is one line of EstimateBreakdown's itemized quote. Kind is a
// machine-readable constant (see the ItemKind* consts below), not a
// human-readable label — this endpoint is unauthenticated-adjacent UI
// plumbing, and jobsvc.RetryNode's own history is the cautionary tale here:
// an earlier pass hardcoded a Chinese label directly into a persisted job
// title, which then rendered in whatever language the string happened to
// be written in regardless of the caller's actual locale. The frontend maps
// Kind to a translated string via i18n; the backend never renders text.
type EstimateItem struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	Credits int    `json:"credits"` // this line's share of the total, not a strict count*unit product (rounding happens per-workflow, see EstimateCredits' own doc)
}

const (
	ItemKindImageSingle     = "image_single"
	ItemKindImageBatch      = "image_batch"
	ItemKindComic4Panels    = "comic4_panels"
	ItemKindStorySplit      = "story_split"
	ItemKindSequenceShots   = "sequence_shots"
	ItemKindVideoGeneration = "video_generation"
	ItemKindPromptEnhance   = "prompt_enhance"
	ItemKindSequencePreview = "video_sequence_preview"
	ItemKindSequenceDirect  = "video_sequence_direct" // SkipPreview: draft already runs at 2K, "preview" would mislabel it
)

// EstimateBreakdown is EstimateCredits' itemized twin (§19.4.1's "成本估算
// 逐項展開明細" gap) — same numbers, just split into the lines that make up
// the total instead of one flat figure. Deliberately calls EstimateCredits
// for the authoritative total rather than summing these lines itself: the
// two must never silently drift apart, and re-deriving the same clamps
// twice would risk exactly that.
func EstimateBreakdown(workflowName string, spec Spec) ([]EstimateItem, int, error) {
	total, err := EstimateCredits(workflowName, spec)
	if err != nil {
		return nil, 0, err
	}
	switch workflowName {
	case "image.single":
		return []EstimateItem{{Kind: ItemKindImageSingle, Count: 1, Credits: total}}, total, nil
	case "image.batch":
		n := spec.N
		if n <= 0 {
			n = 4
		}
		if n > capability.ImageMaxN {
			n = 9
		}
		return []EstimateItem{{Kind: ItemKindImageBatch, Count: n, Credits: total}}, total, nil
	case "image.comic4":
		perPanel := creditsvc.EstimatePerNodeImageCredits(1)
		items := []EstimateItem{{Kind: ItemKindComic4Panels, Count: 4, Credits: perPanel * 4}}
		if spec.Story != "" && len(spec.Panels) != 4 {
			items = append(items, EstimateItem{Kind: ItemKindStorySplit, Count: 1, Credits: creditsvc.EstimateStorySplitCredits()})
		}
		if spec.Comic4Mode == "continuity" {
			items = append(items, EstimateItem{Kind: ItemKindPromptEnhance, Count: 4, Credits: 4 * creditsvc.EstimatePromptEnhanceCredits()})
		}
		return items, total, nil
	case "image.sequence":
		perShot := creditsvc.EstimatePerNodeImageCredits(1)
		n := len(spec.Shots)
		return []EstimateItem{{Kind: ItemKindSequenceShots, Count: n, Credits: perShot * n}}, total, nil
	case "video.single":
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > capability.VideoDurationMax {
			duration = 15
		}
		resolution := spec.Resolution
		if resolution == "" {
			resolution = "768P"
		}
		videoCredits := creditsvc.EstimateVideoCredits(duration, resolution)
		items := []EstimateItem{{Kind: ItemKindVideoGeneration, Count: 1, Credits: videoCredits}}
		if spec.PromptEnhance {
			items = append(items, EstimateItem{Kind: ItemKindPromptEnhance, Count: 1, Credits: creditsvc.EstimatePromptEnhanceCredits()})
		}
		return items, total, nil
	case "video.sequence":
		n := len(spec.Shots)
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
		}
		if duration > capability.VideoDurationMax {
			duration = 15
		}
		kind := ItemKindSequencePreview
		resolution := "768P"
		if spec.SkipPreview {
			kind = ItemKindSequenceDirect
			resolution = "2K"
		}
		perShot := creditsvc.EstimateVideoCredits(duration, resolution)
		items := []EstimateItem{{Kind: kind, Count: n, Credits: perShot * n}}
		if spec.NarrativeContinuity {
			anchors := countAnchorShots(n, spec.RecalibrateEvery)
			items = append(items, EstimateItem{Kind: ItemKindPromptEnhance, Count: anchors, Credits: anchors * creditsvc.EstimatePromptEnhanceCredits()})
		}
		return items, total, nil
	default:
		return nil, 0, fmt.Errorf("unknown workflow_name %q", workflowName)
	}
}

// List is F7.1's job list: newest-first, optionally filtered by status,
// paged by a strictly-decreasing numeric id cursor (jobs.id is an
// AUTO_INCREMENT primary key, so "id < cursor" is a stable, index-backed
// page boundary — idx_user_created/idx_status already cover the
// user_id/status lookups this filters on). Returns the page plus the
// cursor a caller should pass to fetch the next one; an empty nextCursor
// means this was the last page.
func (s *Service) List(ctx context.Context, userID uint64, status string, cursor uint64, limit int, projectID *uint64) ([]persistence.Job, uint64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	if projectID != nil {
		q = q.Where("project_id = ?", *projectID)
	}
	var rows []persistence.Job
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	var next uint64
	if len(rows) == limit {
		next = rows[len(rows)-1].ID
	}
	return rows, next, nil
}

// Cancel is F7.4: stops a still-running job and lets the existing
// terminal-phase machinery handle the rest. It deliberately does NOT touch
// credits itself — internal/application/projection.onWorkflowRun already
// calls maybeRefundCredits on every terminal phase transition it observes,
// "Cancelled" included (idempotent via the "job:{bizID}:refund" key), and
// duplicating that here under a different idemKey would double-refund
// exactly the class of bug DEV_PLAN.md's credit reconciliation fix was
// about. eng.Cancel's own contract (internal/domain/workflow.Engine) is to
// signal already-dispatched tasks to stop and cancel their in-flight
// provider calls where possible; jobs.status itself updates lazily the same
// way it always has, the next time anything calls Get (see Get's own
// terminal-phase sync, a few lines up).
func (s *Service) Cancel(ctx context.Context, userID uint64, bizID string) error {
	job, _, err := s.Get(ctx, userID, bizID)
	if err != nil {
		return err
	}
	if job.Status == "succeeded" || job.Status == "failed" || job.Status == "cancelled" {
		return nil // already stopped — same "no-op past terminal" shape as Resume
	}
	if err := s.eng.Cancel(ctx, workflow.RunID(job.WorkflowRunID)); err != nil {
		return fmt.Errorf("cancel workflow run: %w", err)
	}
	return nil
}

// resolveCharacters loads the characters bound to spec.Characters, in slot
// order, scoped to userID so one user can't reference another's characters.
func (s *Service) resolveCharacters(ctx context.Context, userID uint64, slots []CharacterSlot) ([]prompt.Character, error) {
	if len(slots) == 0 {
		return nil, nil
	}
	out := make([]prompt.Character, 0, len(slots))
	for _, slot := range slots {
		var row persistence.Character
		err := s.db.WithContext(ctx).
			Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", slot.CharacterID, userID).
			First(&row).Error
		if err != nil {
			return nil, fmt.Errorf("character %q (slot %s) not found: %w", slot.CharacterID, slot.Slot, err)
		}
		out = append(out, prompt.Character{Description: row.Description, Seed: row.Seed})
	}
	return out, nil
}

// resolveCharacterRefAssetIDs implements F6.4's character-driven auto
// reference (§5.3 step 1): the union, in slot order, of every bound
// character's own F3.1 reference images. Only used by video.single when the
// caller didn't already pass explicit reference/first-last-frame asset IDs —
// see the "video.single" case in Create(). A separate query from
// resolveCharacters (rather than widening its return type) since every other
// caller of resolveCharacters only needs the text-compilation subset.
func (s *Service) resolveCharacterRefAssetIDs(ctx context.Context, userID uint64, slots []CharacterSlot) ([]string, error) {
	if len(slots) == 0 {
		return nil, nil
	}
	var out []string
	for _, slot := range slots {
		var row persistence.Character
		if err := s.db.WithContext(ctx).
			Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", slot.CharacterID, userID).
			First(&row).Error; err != nil {
			return nil, fmt.Errorf("character %q (slot %s) not found: %w", slot.CharacterID, slot.Slot, err)
		}
		if len(row.RefAssetIDs) == 0 {
			continue
		}
		var ids []string
		if err := json.Unmarshal(row.RefAssetIDs, &ids); err != nil {
			return nil, fmt.Errorf("decode character %q ref_asset_ids: %w", slot.CharacterID, err)
		}
		out = append(out, ids...)
	}
	return out, nil
}

func (s *Service) resolvePresets(ctx context.Context, presetIDs []string) ([]prompt.Preset, error) {
	if len(presetIDs) == 0 {
		return nil, nil
	}
	var rows []persistence.Preset
	if err := s.db.WithContext(ctx).Where("biz_id IN ?", presetIDs).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load presets: %w", err)
	}
	out := make([]prompt.Preset, 0, len(rows))
	for _, r := range rows {
		out = append(out, prompt.Preset{PromptFragment: r.PromptFragment, Priority: r.Priority})
	}
	return out, nil
}

// Get returns the persisted job row plus its live node states, read
// straight from the engine rather than the job_nodes projection — this
// stays the authoritative source; job_nodes (populated via
// internal/application/projection, DEV_PLAN.md §6) exists for list views
// and SSE, not to replace this read path.
//
// Scoped by userID — a job's biz_id is a ULID, not a secret, and every
// direct caller of this method (handleGetJob, handleJobEvents) sits behind
// requireAuth but was never checking that the token's own user is the
// job's owner, which meant any authenticated user who obtained another
// user's job biz_id (shared link, log line, browser history) could read
// its full spec — including prompt text — and live-subscribe to its SSE
// stream. List/Cancel/Resume already scoped their own queries by user_id;
// this was the one read path that didn't.
func (s *Service) Get(ctx context.Context, userID uint64, bizID string) (*persistence.Job, *workflow.Run, error) {
	var job persistence.Job
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ?", bizID, userID).First(&job).Error; err != nil {
		return nil, nil, fmt.Errorf("job %q not found: %w", bizID, err)
	}
	run, err := s.eng.Get(ctx, workflow.RunID(job.WorkflowRunID))
	if err != nil {
		return &job, nil, fmt.Errorf("get workflow run: %w", err)
	}

	if terminal(run.Phase) && job.Status != string(run.Phase) {
		job.Status = normalizeStatus(run.Phase)
		now := time.Now()
		job.FinishedAt = &now
		_ = s.db.WithContext(ctx).Model(&persistence.Job{}).Where("id = ? AND status <> ?", job.ID, job.Status).
			Updates(map[string]any{"status": job.Status, "finished_at": now}).Error
	}
	return &job, run, nil
}

func terminal(phase string) bool {
	switch phase {
	case "Succeeded", "Failed", "Error", "Timeout", "Cancelled":
		return true
	default:
		return false
	}
}

func normalizeStatus(phase string) string {
	switch phase {
	case "Succeeded":
		return "succeeded"
	case "Cancelled":
		return "cancelled"
	case "":
		return "running"
	default:
		return "failed"
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func ptrTime(t time.Time) *time.Time { return &t }

// formatSeed renders a compiled seed (explicit spec.Seed override, else a
// bound character's fixed seed — prompt.Compile's own resolution rule) as
// the string minimax.image's node config expects; nil (no override, no
// bound character) becomes "", which image.go reads as "let MiniMax pick
// one" rather than parsing a seed at all.
func formatSeed(seed *int64) string {
	if seed == nil {
		return ""
	}
	return strconv.FormatInt(*seed, 10)
}

// nonNil turns a nil slice into an empty one so it JSON-marshals to `[]`
// instead of `null` — workflow.parameters of type "array" bind cleanly
// either way, but this keeps args consistently shaped for anything that
// inspects them before submission.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// findByIdemKey looks up a previously-created job by its idempotency key,
// scoped to userID (one user's key can never collide with another's —
// uk_user_idem is a composite unique index, not a global one). Returns
// (nil, nil) — not an error — when no such job exists yet, matching Go's
// usual "not found is a valid outcome" convention for lookup helpers.
func (s *Service) findByIdemKey(ctx context.Context, userID uint64, idemKey string) (*persistence.Job, error) {
	var job persistence.Job
	err := s.db.WithContext(ctx).Where("user_id = ? AND idem_key = ?", userID, idemKey).First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up job by idem_key: %w", err)
	}
	return &job, nil
}

// nullableIdemKey turns "" into a nil *string — jobs.idem_key is nullable
// specifically so MySQL's unique index treats "no key supplied" as never
// colliding with itself (each NULL is distinct under a UNIQUE index), which
// is exactly "idempotency is opt-in per request, not required".
func nullableIdemKey(idemKey string) *string {
	if idemKey == "" {
		return nil
	}
	return &idemKey
}

// handleDuplicateIdemKey is Create/createVideoSequence's fallback for the
// rare race two near-simultaneous requests carrying the same Idempotency-Key
// can hit: both pass the early findByIdemKey check (neither sees the other's
// row yet), both hold credits and submit to the engine, and only one wins
// the final INSERT (uk_user_idem rejects the second with MySQL error 1062).
// The loser refunds what it held — it's about to discard its own attempt
// entirely — and returns the winner's row instead, so the caller still gets
// a valid job back rather than an error for what was, from the client's
// point of view, just a retried request.
func (s *Service) handleDuplicateIdemKey(ctx context.Context, insertErr error, userID uint64, idemKey, bizID string, heldCredits int) (*persistence.Job, error) {
	if idemKey == "" || !isDuplicateKeyErr(insertErr) {
		return nil, fmt.Errorf("insert job row: %w", insertErr)
	}
	_ = s.credits.Refund(ctx, userID, "job:"+bizID+":refund", bizID, heldCredits)
	existing, err := s.findByIdemKey(ctx, userID, idemKey)
	if err != nil {
		return nil, fmt.Errorf("insert job row: %w (and could not recover the winning row: %w)", insertErr, err)
	}
	if existing == nil {
		return nil, fmt.Errorf("insert job row: %w (no existing row found despite duplicate key error)", insertErr)
	}
	return existing, nil
}

func isDuplicateKeyErr(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
