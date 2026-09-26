// Package jobsvc owns the job lifecycle: validating a request, building its
// plan, reserving credits and submitting both in one transaction, and
// reading, cancelling, resuming, retrying or deleting jobs afterwards.
package jobsvc

import (
	"aigc-platform/internal/pkg/apperr"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// ErrNotFound is returned when a job does not exist for this user.
var ErrNotFound = apperr.New("not_found", "job not found")

// ErrNotSupported is returned for operations a job's kind or engine cannot do.
var ErrNotSupported = apperr.New("not_supported", "operation not supported for this job")

var errShotsRequired = apperr.New("shots_required", "at least one shot is required")

func errResolution(got string) error {
	return apperr.New("resolution_invalid", fmt.Sprintf("resolution must be one of %v, got %q", capability.VideoResolutions, got), "allowed", capability.VideoResolutions)
}

// checkVideoRefs applies the video reference rules before anything is
// reserved: frames and reference media are exclusive, and text-to-video needs
// one of the offered ratios. refImages are the reference images the call
// will actually send (explicit ones, or the bound characters').
func checkVideoRefs(spec Spec, refImages []string) error {
	if len(refImages) > capability.VideoMaxReferenceImages {
		return apperr.New("references_too_many", fmt.Sprintf("at most %d reference images", capability.VideoMaxReferenceImages), "max", capability.VideoMaxReferenceImages)
	}
	if len(spec.ReferenceAudioAssetIDs) > capability.VideoMaxReferenceAudios {
		return apperr.New("reference_audios_too_many", fmt.Sprintf("at most %d reference audio files", capability.VideoMaxReferenceAudios), "max", capability.VideoMaxReferenceAudios)
	}
	mode, _, _, err := prompt.CompileVideoRefs(prompt.VideoRefs{
		Ratio: spec.Ratio, FirstFrameAssetID: spec.FirstFrameAssetID, LastFrameAssetID: spec.LastFrameAssetID,
		ReferenceImageAssetIDs: refImages, ReferenceVideoAssetIDs: spec.ReferenceVideoAssetIDs, ReferenceAudioAssetIDs: spec.ReferenceAudioAssetIDs,
	})
	if err != nil {
		return err
	}
	if len(spec.ReferenceVideoAssetIDs) > workflows.MaxReferenceVideoClips {
		return errReferenceVideoBudget()
	}
	if mode == "t2va" && !slices.Contains(capability.VideoRatios, spec.Ratio) {
		return apperr.New("ratio_invalid", fmt.Sprintf("ratio must be one of %v, got %q", capability.VideoRatios, spec.Ratio), "allowed", capability.VideoRatios)
	}
	return nil
}

func errReferenceVideoBudget() error {
	return apperr.New("reference_video_budget", fmt.Sprintf("at most %d reference videos, %d seconds combined", workflows.MaxReferenceVideoClips, capability.ReferenceVideoMaxSeconds),
		"max_clips", workflows.MaxReferenceVideoClips, "max_seconds", capability.ReferenceVideoMaxSeconds)
}

// checkReferenceVideos confirms the reference videos are the caller's and fit
// the provider's combined-length budget, when their lengths are known.
func (s *Service) checkReferenceVideos(ctx context.Context, userID uint64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var rows []persistence.Asset
	if err := s.db.WithContext(ctx).Where("biz_id IN ? AND user_id = ? AND deleted_at IS NULL AND type = ?", ids, userID, "video").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(slices.Compact(slices.Sorted(slices.Values(ids)))) {
		return apperr.New("reference_unavailable", "a reference video is missing, deleted or not a video")
	}
	total := 0
	for _, r := range rows {
		total += r.DurationMs
	}
	if total > capability.ReferenceVideoMaxSeconds*1000 {
		return errReferenceVideoBudget()
	}
	return nil
}

// CharacterSlot binds a character to a generation slot.
type CharacterSlot struct {
	Slot        string `json:"slot"`
	CharacterID string `json:"character_id"`
}

// Spec is the user's generation request as submitted; it is stored verbatim
// with the job.
type Spec struct {
	// ComicMode selects the OpenAI comic flow: direct | editable.
	ComicMode    string `json:"comic_mode,omitempty"`
	ComicPanel   int    `json:"comic_panel,omitempty"`   // 0 = page, 1..4 = one-panel redraw
	ComicContext string `json:"comic_context,omitempty"` // user-reviewed source excerpts

	Text   string   `json:"text"`
	N      int      `json:"n,omitempty"`      // image.single: 1..9
	Panels []string `json:"panels,omitempty"` // image.comic4 panel texts
	Story  string   `json:"story,omitempty"`  // image.comic4: split into N panels when Panels is empty
	Shots  []string `json:"shots,omitempty"`  // image.sequence and video.sequence

	// ShotSourceRefs[i] is the 1-based index of an earlier shot whose image
	// becomes shot i's reference (0 = none). Backward only.
	ShotSourceRefs []int `json:"shot_source_refs,omitempty"`
	// ImageSequenceMode "continuity" makes each shot reference the previous
	// one unless ShotSourceRefs says otherwise.
	ImageSequenceMode string `json:"image_sequence_mode,omitempty"`

	Characters []CharacterSlot `json:"characters,omitempty"`
	PresetIDs  []string        `json:"preset_ids,omitempty"`
	Seed       *int64          `json:"seed,omitempty"`
	// SourceImageAssetID is the image-to-image reference for image modes and
	// the r2va anchor for video.sequence.
	SourceImageAssetID string `json:"source_image_asset_id,omitempty"`

	// ImageProvider: "" or minimax, gemini, or openai (image.single,
	// image.sequence and the direct/editable comic).
	ImageProvider string `json:"image_provider,omitempty"`
	// ImageSize and ImageQuality are OpenAI's output options for image.single
	// and image.sequence; empty picks the size from AspectRatio and high.
	ImageSize    string `json:"image_size,omitempty"`
	ImageQuality string `json:"image_quality,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"` // image.single

	DurationSeconds        int      `json:"duration_seconds,omitempty"`
	Resolution             string   `json:"resolution,omitempty"` // 768P | 2K
	Ratio                  string   `json:"ratio,omitempty"`
	FirstFrameAssetID      string   `json:"first_frame_asset_id,omitempty"`
	LastFrameAssetID       string   `json:"last_frame_asset_id,omitempty"`
	ReferenceImageAssetIDs []string `json:"reference_image_asset_ids,omitempty"`
	ReferenceVideoAssetIDs []string `json:"reference_video_asset_ids,omitempty"`
	ReferenceAudioAssetIDs []string `json:"reference_audio_asset_ids,omitempty"`
	PromptEnhance          bool     `json:"prompt_enhance,omitempty"` // video.single: H3-Context-IR first

	// video.sequence: every RecalibrateEvery-th shot re-anchors with r2va
	// instead of continuing from the previous last frame (0 = default 3).
	RecalibrateEvery int `json:"recalibrate_every,omitempty"`
	// SkipPreview generates the draft directly at 2K with no review gate.
	SkipPreview        bool   `json:"skip_preview,omitempty"`
	SourceVideoAssetID string `json:"source_video_asset_id,omitempty"`
	// NarrativeContinuity makes anchor shots reference earlier shots' clips
	// and frames and run H3-Context-IR with the story so far.
	NarrativeContinuity    bool   `json:"narrative_continuity,omitempty"`
	ReferenceSelectionMode string `json:"reference_selection_mode,omitempty"` // window | manual | smart
	ShotReferenceOverrides []int  `json:"shot_reference_overrides,omitempty"`
}

type Service struct {
	db      *gorm.DB
	orch    *orchestrator.Orchestrator
	credits *creditsvc.Service
	// minimax serves the planning calls made before a plan exists (comic
	// planner, story split, reference description, smart shot picks).
	minimax *minimax.Client
}

func New(db *gorm.DB, orch *orchestrator.Orchestrator, credits *creditsvc.Service, minimaxClient *minimax.Client) *Service {
	return &Service{db: db, orch: orch, credits: credits, minimax: minimaxClient}
}

// Create validates the request, builds its plan and submits it. A repeated
// idempotency key returns the original job without reserving again.
func (s *Service) Create(ctx context.Context, userID uint64, workflowName string, spec Spec, idemKey string, projectID *uint64) (*persistence.Job, error) {
	if err := checkProvider(workflowName, spec); err != nil {
		return nil, err
	}
	if err := s.checkAssetsOwned(ctx, userID, specAssetIDs(spec)); err != nil {
		return nil, err
	}
	// Planning calls made before the plan exists are recorded against the user.
	ctx = executor.WithAttribution(ctx, executor.Attribution{UserID: userID})
	if idemKey != "" {
		if existing, err := s.findByIdemKey(ctx, userID, idemKey); err != nil || existing != nil {
			return existing, err
		}
	}
	estimate, err := EstimateCredits(workflowName, spec)
	if err != nil {
		return nil, err
	}

	var (
		plan  *workflow.Plan
		title string
	)
	switch workflowName {
	case "image.single":
		plan, title, err = s.prepareImageSingle(ctx, userID, spec)
	case "video.single":
		plan, title, err = s.prepareVideoSingle(ctx, userID, spec)
	case "image.sequence":
		plan, title, err = s.prepareImageSequence(ctx, userID, spec)
	case "image.comic4":
		if spec.ComicMode != "" {
			plan, title, err = s.prepareDirectComic(ctx, userID, spec)
		} else {
			plan, title, err = s.prepareImageComic4(ctx, userID, spec)
		}
	case "video.sequence":
		plan, title, err = s.prepareVideoSequence(ctx, userID, spec)
	default:
		return nil, apperr.New("unknown_workflow", fmt.Sprintf("unknown workflow_name %q", workflowName))
	}
	if err != nil {
		return nil, err
	}
	return s.submit(ctx, submission{
		userID: userID, workflowName: workflowName, spec: spec, title: title,
		estimate: estimate, plan: plan, idemKey: idemKey, projectID: projectID,
	})
}

type submission struct {
	userID       uint64
	workflowName string
	spec         Spec
	title        string
	estimate     int
	plan         *workflow.Plan
	idemKey      string
	projectID    *uint64
	holdKind     string
	retryOf      *persistence.Job
	retryNode    string
}

// submit inserts the job, reserves its credits and persists its plan in one
// transaction: a failed reservation or plan leaves nothing behind, and a
// duplicate idempotency key rolls back before anything was dispatched.
func (s *Service) submit(ctx context.Context, sub submission) (*persistence.Job, error) {
	specJSON, err := json.Marshal(sub.spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	now := time.Now().UTC()
	job := &persistence.Job{
		BizID: id.New(), UserID: sub.userID, ProjectID: sub.projectID, WorkflowName: sub.workflowName,
		Title: truncate(sub.title, 128), Status: workflow.JobQueued, Spec: specJSON,
		CreditEstimated: sub.estimate, CreditHeld: sub.estimate, IdemKey: nullableIdemKey(sub.idemKey), StartedAt: &now,
	}
	if sub.retryOf != nil {
		loop := -1
		job.RetryOfJobID, job.RetryOfNodeName, job.RetryOfLoopIndex = &sub.retryOf.ID, &sub.retryNode, &loop
	}
	holdKind := sub.holdKind
	if holdKind == "" {
		holdKind = "job"
	}
	var pending *orchestrator.Pending
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
		if !ok {
			return fmt.Errorf("job transaction is not a *sql.Tx")
		}
		hold := creditsvc.JobHold{UserID: sub.userID, JobID: job.ID, JobBizID: job.BizID, Workflow: sub.workflowName}
		if err := s.credits.HoldForJobTx(ctx, sqlTx, hold, "job:"+job.BizID+":hold", sub.estimate, holdKind); err != nil {
			return fmt.Errorf("hold credits: %w", err)
		}
		pending, err = s.orch.SubmitTx(ctx, sqlTx, orchestrator.JobRef{ID: job.ID, BizID: job.BizID, UserID: sub.userID}, sub.plan)
		return err
	})
	if err != nil {
		if sub.idemKey != "" && isDuplicateKeyErr(err) {
			if existing, findErr := s.findByIdemKey(ctx, sub.userID, sub.idemKey); findErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, err
	}
	s.orch.Flush(ctx, pending)
	job.Engine = "v2"
	return job, nil
}

func (s *Service) prepareImageSingle(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	presets, err := s.resolvePresets(ctx, userID, spec.PresetIDs)
	if err != nil {
		return nil, "", err
	}
	compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed})
	refs := []string{}
	switch {
	case spec.SourceImageAssetID != "":
		refs = []string{spec.SourceImageAssetID}
	case len(spec.Characters) > 0:
		if refs, err = s.resolveCharacterRefAssetIDs(ctx, userID, spec.Characters); err != nil {
			return nil, "", err
		}
	}
	single := workflows.ImageSingle{
		UserID: userID, Provider: normalizeImageProvider(spec.ImageProvider), Prompt: compiled.Prompt,
		Seed: formatSeed(compiled.Seed), N: imageCount(spec.N), AspectRatio: spec.AspectRatio, References: refs,
	}
	if single.Provider == workflows.ProviderOpenAI {
		o, err := s.openAIPrepare(ctx, userID, "image.single", spec, refs)
		if err != nil {
			return nil, "", err
		}
		single.N, single.Size, single.Quality = o.N, o.Size, o.Quality
	}
	plan := workflows.ImageSinglePlan(single)
	return plan, spec.Text, nil
}

func (s *Service) prepareVideoSingle(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	if spec.Resolution != "" && !slices.Contains(capability.VideoResolutions, spec.Resolution) {
		return nil, "", errResolution(spec.Resolution)
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	presets, err := s.resolvePresets(ctx, userID, spec.PresetIDs)
	if err != nil {
		return nil, "", err
	}
	compiled := prompt.Compile(prompt.Input{Text: spec.Text, Characters: characters, Presets: presets, Seed: spec.Seed, MaxChars: capability.VideoMaxPromptChars})
	refImages := spec.ReferenceImageAssetIDs
	// A bound character supplies its own reference images unless the caller
	// chose explicit references or frames.
	if len(refImages) == 0 && spec.FirstFrameAssetID == "" && spec.LastFrameAssetID == "" && len(spec.Characters) > 0 {
		if refImages, err = s.resolveCharacterRefAssetIDs(ctx, userID, spec.Characters); err != nil {
			return nil, "", err
		}
	}
	if err := checkVideoRefs(spec, refImages); err != nil {
		return nil, "", err
	}
	if err := s.checkReferenceVideos(ctx, userID, spec.ReferenceVideoAssetIDs); err != nil {
		return nil, "", err
	}
	plan := workflows.VideoSinglePlan(workflows.VideoSingle{
		UserID: userID, Prompt: compiled.Prompt, Duration: videoDuration(spec.DurationSeconds), Resolution: videoResolution(spec.Resolution),
		Ratio: spec.Ratio, FirstFrame: spec.FirstFrameAssetID, LastFrame: spec.LastFrameAssetID,
		ReferenceImages: nonNil(refImages), ReferenceVideos: nonNil(spec.ReferenceVideoAssetIDs), ReferenceAudios: nonNil(spec.ReferenceAudioAssetIDs),
		Enhance: spec.PromptEnhance,
	})
	return plan, spec.Text, nil
}

func imageCount(n int) int {
	if n <= 0 {
		return 1
	}
	return min(n, capability.ImageMaxN)
}

func videoDuration(d int) int {
	if d <= 0 {
		return 5
	}
	return min(d, capability.VideoDurationMax)
}

func videoResolution(r string) string {
	if r == "" {
		return "768P"
	}
	return r
}

// EstimateCredits is the reservation for a request. Create reserves exactly
// this amount, so the price shown before submission is the price held.
// Every figure is an upper bound; unused credits return when the job ends.
func EstimateCredits(workflowName string, spec Spec) (int, error) {
	_, total, err := EstimateBreakdown(workflowName, spec)
	return total, err
}

// EstimateItem is one line of a quote. Kind is a machine-readable constant
// the frontend localizes. Basis "reservation" marks an upper bound settled
// from the provider's reported usage; otherwise the price is fixed.
type EstimateItem struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	Credits int    `json:"credits"`
	Basis   string `json:"basis,omitempty"`
}

// BasisReservation marks usage-settled quote lines.
const BasisReservation = "reservation"

const (
	ItemKindImageSingle     = "image_single"
	ItemKindComic4Panels    = "comic4_panels"
	ItemKindStorySplit      = "story_split"
	ItemKindSequenceShots   = "sequence_shots"
	ItemKindVideoGeneration = "video_generation"
	ItemKindPromptEnhance   = "prompt_enhance"
	ItemKindSequencePreview = "video_sequence_preview"
	ItemKindSequenceDirect  = "video_sequence_direct"
	ItemKindRedo            = "video_redo"
	ItemKindUpgrade         = "video_upgrade"
	ItemKindCompose         = "video_compose"
)

// EstimateBreakdown itemizes EstimateCredits.
func EstimateBreakdown(workflowName string, spec Spec) ([]EstimateItem, int, error) {
	if err := checkProvider(workflowName, spec); err != nil {
		return nil, 0, err
	}
	openAI := spec.ImageProvider == workflows.ProviderOpenAI
	var items []EstimateItem
	switch workflowName {
	case "image.single":
		if openAI {
			o, _ := openAIImageOptions(workflowName, spec)
			items = []EstimateItem{{Kind: ItemKindImageSingle, Count: o.N, Credits: openAICredits(o.N, o.Quality, imageSingleRefBound(spec)), Basis: BasisReservation}}
			break
		}
		n := imageCount(spec.N)
		items = []EstimateItem{{Kind: ItemKindImageSingle, Count: n, Credits: creditsvc.EstimateImageCredits(n)}}
	case "image.comic4":
		if spec.ComicMode != "" {
			if _, err := directComicPrompt(spec); err != nil {
				return nil, 0, err
			}
			items = []EstimateItem{{Kind: ItemKindComic4Panels, Count: 1, Credits: directComicCredits(spec), Basis: BasisReservation}}
			break
		}
		n, err := comic4PanelCount(spec)
		if err != nil {
			return nil, 0, err
		}
		// Each panel and each reference-stylizing pass is its own billed call.
		images := n + comic4StylizeRefCount(spec)
		items = []EstimateItem{
			{Kind: ItemKindComic4Panels, Count: images, Credits: creditsvc.EstimatePerNodeImageCredits(images)},
			{Kind: ItemKindStorySplit, Count: 1, Credits: creditsvc.EstimateStorySplitCredits()},
			{Kind: ItemKindPromptEnhance, Count: n, Credits: n * creditsvc.EstimatePromptEnhanceCredits()},
		}
	case "image.sequence":
		n := len(spec.Shots)
		if err := checkSequenceShots(n); err != nil {
			return nil, 0, err
		}
		if openAI {
			// One call per shot, each reserved on its own.
			o, _ := openAIImageOptions(workflowName, spec)
			credits := 0
			for i := range n {
				refs := 0
				if sequenceShotHasRef(spec, i) {
					refs = 1
				}
				credits += openAICredits(1, o.Quality, refs)
			}
			items = []EstimateItem{{Kind: ItemKindSequenceShots, Count: n, Credits: credits, Basis: BasisReservation}}
			break
		}
		items = []EstimateItem{{Kind: ItemKindSequenceShots, Count: n, Credits: creditsvc.EstimatePerNodeImageCredits(n)}}
	case "video.single":
		if spec.Resolution != "" && !slices.Contains(capability.VideoResolutions, spec.Resolution) {
			return nil, 0, errResolution(spec.Resolution)
		}
		// Bound characters stand in as reference images when nothing else is attached.
		refImages := spec.ReferenceImageAssetIDs
		if len(refImages) == 0 && spec.FirstFrameAssetID == "" && spec.LastFrameAssetID == "" && len(spec.Characters) > 0 {
			refImages = []string{spec.Characters[0].CharacterID}
		}
		if err := checkVideoRefs(spec, refImages); err != nil {
			return nil, 0, err
		}
		items = []EstimateItem{{Kind: ItemKindVideoGeneration, Count: 1,
			Credits: creditsvc.EstimateVideoCredits(videoDuration(spec.DurationSeconds), videoResolution(spec.Resolution))}}
		if spec.PromptEnhance {
			items = append(items, EstimateItem{Kind: ItemKindPromptEnhance, Count: 1, Credits: creditsvc.EstimatePromptEnhanceCredits()})
		}
	case "video.sequence":
		n := len(spec.Shots)
		if err := checkVideoSequence(spec); err != nil {
			return nil, 0, err
		}
		kind, resolution := ItemKindSequencePreview, "768P"
		if spec.SkipPreview {
			kind, resolution = ItemKindSequenceDirect, "2K"
		}
		items = []EstimateItem{{Kind: kind, Count: n, Credits: n * creditsvc.EstimateVideoCredits(videoDuration(spec.DurationSeconds), resolution)}}
		if spec.NarrativeContinuity {
			anchors := countAnchorShots(n, spec.RecalibrateEvery)
			items = append(items, EstimateItem{Kind: ItemKindPromptEnhance, Count: anchors, Credits: anchors * creditsvc.EstimatePromptEnhanceCredits()})
		}
	default:
		return nil, 0, apperr.New("unknown_workflow", fmt.Sprintf("unknown workflow_name %q", workflowName))
	}
	total := 0
	for _, it := range items {
		total += it.Credits
	}
	return items, total, nil
}

// Status buckets group job statuses the way the task center shows them.
const (
	BucketNeedsReview = "needs_review"
	BucketActive      = "active"
	BucketSucceeded   = "succeeded"
	BucketFailed      = "failed"
	BucketCancelled   = "cancelled"
)

var bucketStatuses = map[string][]string{
	BucketNeedsReview: {workflow.JobAwaitingReview},
	BucketActive:      {workflow.JobQueued, workflow.JobRunning, workflow.JobCancelling},
	BucketSucceeded:   {workflow.JobSucceeded},
	BucketFailed:      {workflow.JobFailed, workflow.JobPartial},
	BucketCancelled:   {workflow.JobCancelled},
}

// ListFilter narrows a job listing. Status is an exact status; Bucket a
// group of statuses; Query matches the title.
type ListFilter struct {
	Status string
	Bucket string
	// Exclude drops these statuses, e.g. awaiting_review when the task
	// center shows those pinned above the list.
	Exclude   []string
	Workflow  string
	Query     string
	ProjectID *uint64
	Cursor    uint64
	Limit     int
	// Oldest lists oldest first; the cursor then pages forward.
	Oldest bool
}

// List returns a user's jobs newest first (or oldest first), paged by id cursor.
func (s *Service) List(ctx context.Context, userID uint64, f ListFilter) ([]persistence.Job, uint64, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Where("user_id = ? AND deleted_at IS NULL", userID)
	switch {
	case f.Status != "":
		if !slices.Contains(jobStatuses, f.Status) {
			return nil, 0, apperr.New("bad_request", fmt.Sprintf("unknown status %q", f.Status))
		}
		q = q.Where("status = ?", f.Status)
	case f.Bucket != "":
		statuses, ok := bucketStatuses[f.Bucket]
		if !ok {
			return nil, 0, apperr.New("bad_request", fmt.Sprintf("unknown bucket %q", f.Bucket))
		}
		q = q.Where("status IN ?", statuses)
	}
	if len(f.Exclude) > 0 {
		for _, st := range f.Exclude {
			if !slices.Contains(jobStatuses, st) {
				return nil, 0, apperr.New("bad_request", fmt.Sprintf("unknown status %q", st))
			}
		}
		q = q.Where("status NOT IN ?", f.Exclude)
	}
	if f.Workflow != "" {
		q = q.Where("workflow_name = ?", f.Workflow)
	}
	if f.Query != "" {
		q = q.Where("title LIKE ?", "%"+escapeLike(f.Query)+"%")
	}
	order := "id DESC"
	if f.Oldest {
		order = "id ASC"
	}
	if f.Cursor > 0 {
		if f.Oldest {
			q = q.Where("id > ?", f.Cursor)
		} else {
			q = q.Where("id < ?", f.Cursor)
		}
	}
	if f.ProjectID != nil {
		q = q.Where("project_id = ?", *f.ProjectID)
	}
	var rows []persistence.Job
	if err := q.Order(order).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	var next uint64
	if len(rows) == limit {
		next = rows[len(rows)-1].ID
	}
	return rows, next, nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// jobStatuses are the job statuses a listing may filter on.
var jobStatuses = []string{
	workflow.JobQueued, workflow.JobRunning, workflow.JobAwaitingReview, workflow.JobSucceeded,
	workflow.JobPartial, workflow.JobFailed, workflow.JobCancelling, workflow.JobCancelled,
}

// SummaryCounts counts a user's jobs per bucket and per exact status, so the
// task center's cards and status filter read one source.
type SummaryCounts struct {
	Buckets  map[string]int
	Statuses map[string]int
}

// Summary counts a user's jobs per bucket and per status.
func (s *Service) Summary(ctx context.Context, userID uint64, projectID *uint64) (*SummaryCounts, error) {
	q := s.db.WithContext(ctx).Model(&persistence.Job{}).Where("user_id = ? AND deleted_at IS NULL", userID)
	if projectID != nil {
		q = q.Where("project_id = ?", *projectID)
	}
	var rows []struct {
		Status string
		N      int
	}
	if err := q.Select("status, COUNT(*) AS n").Group("status").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("summarize jobs: %w", err)
	}
	out := &SummaryCounts{
		Buckets:  map[string]int{BucketNeedsReview: 0, BucketActive: 0, BucketSucceeded: 0, BucketFailed: 0, BucketCancelled: 0, "total": 0},
		Statuses: map[string]int{},
	}
	for _, st := range jobStatuses {
		out.Statuses[st] = 0
	}
	for _, r := range rows {
		out.Buckets["total"] += r.N
		if _, known := out.Statuses[r.Status]; known {
			out.Statuses[r.Status] += r.N
		}
		for bucket, statuses := range bucketStatuses {
			if slices.Contains(statuses, r.Status) {
				out.Buckets[bucket] += r.N
			}
		}
	}
	return out, nil
}

// CreditBreakdown is a job's credits over its lifetime. Nil fields are
// unknown (jobs from the previous engine kept no per-job reservation).
type CreditBreakdown struct {
	Reserved int  `json:"reserved"` // total reserved, including review top-ups
	Settled  int  `json:"settled"`  // total charged, including Overage
	Overage  *int `json:"overage"`  // charged from the balance beyond the reservation
	Released *int `json:"released"` // unused reservation returned
	Frozen   *int `json:"frozen"`   // reservation still held
}

// Credits returns each job's credit breakdown, keyed by job id.
func (s *Service) Credits(ctx context.Context, jobs []persistence.Job) (map[uint64]CreditBreakdown, error) {
	out := make(map[uint64]CreditBreakdown, len(jobs))
	ids := make([]uint64, 0, len(jobs))
	for _, j := range jobs {
		out[j.ID] = CreditBreakdown{Reserved: j.CreditHeld, Settled: j.CreditSettled}
		ids = append(ids, j.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	var holds []struct {
		JobID     uint64
		HeldTotal int
		Committed int
		Overage   int
		Released  int
		Remaining int
		Status    string
	}
	if err := s.db.WithContext(ctx).Table("credit_holds").
		Select("job_id, held_total, committed, overage, released, remaining, status").
		Where("job_id IN ?", ids).Scan(&holds).Error; err != nil {
		return nil, fmt.Errorf("read credit holds: %w", err)
	}
	for _, h := range holds {
		frozen := 0
		if h.Status == "open" {
			frozen = h.Remaining
		}
		overage, released := h.Overage, h.Released
		out[h.JobID] = CreditBreakdown{
			Reserved: h.HeldTotal, Settled: h.Committed + h.Overage,
			Overage: &overage, Released: &released, Frozen: &frozen,
		}
	}
	return out, nil
}

// Rename changes a job's title.
func (s *Service) Rename(ctx context.Context, userID uint64, bizID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 128 {
		return apperr.New("invalid_title", "title must contain 1..128 characters", "max", 128)
	}
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&persistence.Job{}).Where("id = ?", job.ID).Update("title", title).Error
}

// Get returns a user's job and its nodes.
func (s *Service) Get(ctx context.Context, userID uint64, bizID string) (*persistence.Job, *workflow.Run, error) {
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return nil, nil, err
	}
	run, err := s.snapshot(ctx, job)
	if err != nil {
		return job, nil, err
	}
	return job, run, nil
}

func (s *Service) load(ctx context.Context, userID uint64, bizID string) (*persistence.Job, error) {
	var job persistence.Job
	err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", bizID, userID).First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load job: %w", err)
	}
	return &job, nil
}

// snapshot reads a job's nodes. Jobs from the previous engine only have
// their recorded phase and asset ids.
func (s *Service) snapshot(ctx context.Context, job *persistence.Job) (*workflow.Run, error) {
	run := &workflow.Run{Phase: job.Status}
	if job.Engine == "v2" {
		nodes, err := s.orch.Nodes(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		if job.Status == workflow.JobAwaitingReview {
			if run.ReviewDeadline, err = s.orch.ReviewDeadline(ctx, job.ID); err != nil {
				return nil, err
			}
		}
		for _, n := range nodes {
			run.Nodes = append(run.Nodes, workflow.NodeState{
				Name: n.Name, Executor: n.Executor, Status: n.Status, Phase: workflow.LegacyPhase(n.Status), LoopIndex: -1,
				Attempt: n.Attempt, QueueReason: n.QueueReason, Outputs: n.Outputs, ErrorCode: n.ErrorCode, ErrorMsg: n.ErrorMsg,
				CreditCost: n.CreditCost, StartedAt: n.StartedAt, FinishedAt: n.FinishedAt, Display: n.Display,
			})
		}
		return run, nil
	}
	var rows []persistence.JobNode
	if err := s.db.WithContext(ctx).Where("job_id = ?", job.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		outputs := map[string]any{}
		var ids []string
		if len(r.AssetIDs) > 0 && json.Unmarshal(r.AssetIDs, &ids) == nil && len(ids) > 0 {
			outputs["asset-ids"] = ids
			outputs["asset-id"] = ids[0]
		}
		run.Nodes = append(run.Nodes, workflow.NodeState{
			Name: r.NodeName, Executor: r.ExecutorType, Phase: r.Phase, LoopIndex: r.LoopIndex, Outputs: outputs,
			ErrorMsg: r.ErrorMsg, CreditCost: r.CreditCost, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		})
	}
	return run, nil
}

// Cancel stops a running job. Finished jobs are left as they are.
func (s *Service) Cancel(ctx context.Context, userID uint64, bizID string) error {
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return err
	}
	if workflow.JobTerminal(job.Status) {
		return nil
	}
	if job.Engine != "v2" {
		return ErrNotSupported
	}
	return s.orch.Cancel(ctx, job.ID)
}

// Delete hides a finished job from the user's history. The row stays as
// credit ledger provenance.
func (s *Service) Delete(ctx context.Context, userID uint64, bizID string) error {
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return err
	}
	if !workflow.JobTerminal(job.Status) {
		return apperr.New("job_active", fmt.Sprintf("job %q is still %s; cancel it before deleting", bizID, job.Status), "status", job.Status)
	}
	return s.db.WithContext(ctx).Model(&persistence.Job{}).Where("id = ?", job.ID).Update("deleted_at", time.Now().UTC()).Error
}

func (s *Service) resolveCharacters(ctx context.Context, userID uint64, slots []CharacterSlot) ([]prompt.Character, error) {
	out := make([]prompt.Character, 0, len(slots))
	for _, slot := range slots {
		row, err := s.character(ctx, userID, slot)
		if err != nil {
			return nil, err
		}
		out = append(out, prompt.Character{Description: row.Description, Seed: row.Seed})
	}
	return out, nil
}

func (s *Service) character(ctx context.Context, userID uint64, slot CharacterSlot) (*persistence.Character, error) {
	var row persistence.Character
	err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", slot.CharacterID, userID).First(&row).Error
	if err != nil {
		return nil, fmt.Errorf("character %q (slot %s) not found: %w", slot.CharacterID, slot.Slot, err)
	}
	return &row, nil
}

func characterRefIDs(row *persistence.Character) ([]string, error) {
	if len(row.RefAssetIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(row.RefAssetIDs, &ids); err != nil {
		return nil, fmt.Errorf("decode character %q ref_asset_ids: %w", row.BizID, err)
	}
	return ids, nil
}

// resolveCharacterRefAssetIDs is every bound character's reference images, in
// slot order.
func (s *Service) resolveCharacterRefAssetIDs(ctx context.Context, userID uint64, slots []CharacterSlot) ([]string, error) {
	var out []string
	for _, slot := range slots {
		row, err := s.character(ctx, userID, slot)
		if err != nil {
			return nil, err
		}
		ids, err := characterRefIDs(row)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

// resolveCharacterPlanInfo is each bound character's identity for the comic
// planner plus its first reference image by slot.
func (s *Service) resolveCharacterPlanInfo(ctx context.Context, userID uint64, slots []CharacterSlot) ([]minimax.PlanCharacterInfo, map[string]string, error) {
	infos := make([]minimax.PlanCharacterInfo, 0, len(slots))
	slotRef := make(map[string]string, len(slots))
	for _, slot := range slots {
		row, err := s.character(ctx, userID, slot)
		if err != nil {
			return nil, nil, err
		}
		ids, err := characterRefIDs(row)
		if err != nil {
			return nil, nil, err
		}
		ref := ""
		if len(ids) > 0 {
			ref = ids[0]
			slotRef[slot.Slot] = ref
		}
		infos = append(infos, minimax.PlanCharacterInfo{Slot: slot.Slot, Name: row.Name, Description: row.Description, HasImage: ref != ""})
	}
	return infos, slotRef, nil
}

func (s *Service) resolveAssetPublicURL(ctx context.Context, userID uint64, assetBizID string) (string, error) {
	var url string
	err := s.db.WithContext(ctx).Model(&persistence.Asset{}).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", assetBizID, userID).Limit(1).Pluck("public_url", &url).Error
	return url, err
}

// resolvePresets loads system presets and the caller's own; another user's
// preset is skipped like an unknown id.
func (s *Service) resolvePresets(ctx context.Context, userID uint64, presetIDs []string) ([]prompt.Preset, error) {
	if len(presetIDs) == 0 {
		return nil, nil
	}
	var rows []persistence.Preset
	if err := s.db.WithContext(ctx).Where("biz_id IN ? AND (owner_user_id IS NULL OR owner_user_id = ?)", presetIDs, userID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load presets: %w", err)
	}
	out := make([]prompt.Preset, 0, len(rows))
	for _, r := range rows {
		out = append(out, prompt.Preset{PromptFragment: r.PromptFragment, Priority: r.Priority})
	}
	return out, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func formatSeed(seed *int64) string {
	if seed == nil {
		return ""
	}
	return strconv.FormatInt(*seed, 10)
}

// resolveSharedSeed gives every panel/shot of a batch the same seed so they
// share a visual anchor; without an explicit or character seed a random one
// is chosen once for the batch.
func resolveSharedSeed(characters []prompt.Character, explicit *int64) *int64 {
	seed := prompt.Compile(prompt.Input{Characters: characters, Seed: explicit}).Seed
	if seed == nil {
		v := rand.Int64N(1 << 31)
		seed = &v
	}
	return seed
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Service) findByIdemKey(ctx context.Context, userID uint64, idemKey string) (*persistence.Job, error) {
	var job persistence.Job
	err := s.db.WithContext(ctx).Where("user_id = ? AND idem_key = ? AND deleted_at IS NULL", userID, idemKey).First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up job by idem_key: %w", err)
	}
	return &job, nil
}

// nullableIdemKey stores "" as NULL so jobs without a key never collide in
// uk_user_idem.
func nullableIdemKey(idemKey string) *string {
	if idemKey == "" {
		return nil
	}
	return &idemKey
}

func isDuplicateKeyErr(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
