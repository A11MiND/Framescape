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
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
	workflowdefs "aigc-platform/workflows"
)

// definitions maps our business-layer workflow_name (dotted, e.g.
// "image.single") to the aether/v1 document that implements it. Static
// files for W1-W4; video.sequence (W6) generates its DAG dynamically instead
// of loading a static file (docs/aether-validation-report.md §four) but
// still registers itself here under its business name once that lands.
var definitions = map[string]string{
	"image.single":   "image-single",
	"image.batch":    "image-batch",
	"image.comic4":   "image-comic4",
	"image.sequence": "image-sequence",
	"video.single":   "video-single",
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
	Shots      []string        `json:"shots,omitempty"`      // image.sequence only: N shot descriptions (PRD F5.5)
	Characters []CharacterSlot `json:"characters,omitempty"` // F3.2
	PresetIDs  []string        `json:"preset_ids,omitempty"` // F4.3
	Seed       *int64          `json:"seed,omitempty"`       // explicit override; else a bound character's fixed seed wins

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
}

type Service struct {
	db      *gorm.DB
	eng     workflow.Engine
	credits *creditsvc.Service
}

func New(db *gorm.DB, eng workflow.Engine, credits *creditsvc.Service) *Service {
	return &Service{db: db, eng: eng, credits: credits}
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
func (s *Service) Create(ctx context.Context, userID uint64, workflowName string, spec Spec, idemKey string) (*persistence.Job, error) {
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
		return s.createVideoSequence(ctx, userID, spec, idemKey)
	}

	defFile, ok := definitions[workflowName]
	if !ok {
		return nil, fmt.Errorf("unknown workflow_name %q", workflowName)
	}
	if workflowName == "video.single" && spec.PromptEnhance {
		defFile = "video-single-enhanced"
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
		estimatedCredits = creditsvc.EstimateImageCredits(1)
	case "image.batch":
		compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed})
		args["prompt"] = compiled.Prompt
		n := spec.N
		if n <= 0 {
			n = 4
		}
		args["n"] = strconv.Itoa(n)
		estimatedCredits = creditsvc.EstimateImageCredits(n)
	case "image.comic4":
		if len(spec.Panels) != 4 {
			return nil, fmt.Errorf("image.comic4 requires exactly 4 panels, got %d", len(spec.Panels))
		}
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
			panels[i] = map[string]any{"prompt": compiled.Prompt, "user-id": strconv.FormatUint(userID, 10)}
		}
		args["panels"] = panels
		estimatedCredits = creditsvc.EstimateImageCredits(4)
	case "image.sequence":
		if len(spec.Shots) == 0 {
			return nil, fmt.Errorf("image.sequence requires at least 1 shot")
		}
		// Resolve the seed once (F5.5: "同 seed") — every shot embeds it,
		// same reasoning as image.comic4's user-id above. An empty Text
		// compile is enough to run the compiler's own seed-resolution rule
		// (explicit spec.Seed, else the first bound character's fixed seed).
		seed := prompt.Compile(prompt.Input{Characters: characters, Seed: spec.Seed}).Seed
		seedStr := ""
		if seed != nil {
			seedStr = strconv.FormatInt(*seed, 10)
		}
		shots := make([]map[string]any, len(spec.Shots))
		for i, shotText := range spec.Shots {
			compiled := prompt.Compile(prompt.Input{Text: shotText, Characters: characters, Presets: presets, Seed: seed})
			shots[i] = map[string]any{"prompt": compiled.Prompt, "seed": seedStr, "user-id": strconv.FormatUint(userID, 10)}
		}
		args["shots"] = shots
		estimatedCredits = creditsvc.EstimateImageCredits(len(spec.Shots))
	case "video.single":
		// §3.2's 7000-char cap, not image's 1500 (PRD §3.1) — video.go itself
		// also hard-truncates at 7000 as a backstop, same belt-and-braces
		// pattern as image.go's 1500 check.
		compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed, MaxChars: 7000})
		args["prompt"] = compiled.Prompt
		duration := spec.DurationSeconds
		if duration <= 0 {
			duration = 5
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
		args["reference-image-asset-ids"] = nonNil(spec.ReferenceImageAssetIDs)
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
	if err := s.credits.Hold(ctx, userID, "job:"+bizID+":hold", "job", bizID, estimatedCredits, workflowName); err != nil {
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
	job := &persistence.Job{
		BizID:           bizID,
		UserID:          userID,
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
func (s *Service) Get(ctx context.Context, bizID string) (*persistence.Job, *workflow.Run, error) {
	var job persistence.Job
	if err := s.db.WithContext(ctx).Where("biz_id = ?", bizID).First(&job).Error; err != nil {
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
