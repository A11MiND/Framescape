package jobsvc

import (
	"aigc-platform/internal/pkg/apperr"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
)

// Every defaultRecalibrateEvery-th shot re-anchors on the character (r2va)
// instead of continuing from the previous shot's last frame (i2va).
const defaultRecalibrateEvery = 3

// r2va reference_video budget: at most 3 clips, 15 seconds combined.
const referenceWindowSeconds = 15

// countAnchorShots mirrors planShots' anchor rule for pricing, assuming every
// anchor is enhanced (an upper bound).
func countAnchorShots(shotCount, recalibrateEvery int) int {
	if recalibrateEvery <= 0 {
		recalibrateEvery = defaultRecalibrateEvery
	}
	n := 0
	for idx := 1; idx <= shotCount; idx++ {
		if idx == 1 || (idx-1)%recalibrateEvery == 0 {
			n++
		}
	}
	return n
}

// planShots decides each shot's mode and references. Anchors use r2va when
// there is anything to reference (the character, or with narrative
// continuity the earlier shots themselves) and t2va otherwise; every other
// shot continues from the previous last frame.
func planShots(shots []string, characters []prompt.Character, presets []prompt.Preset, refAssetID string, refIsVideo bool, recalibrateEvery int, narrative bool, selectionMode string, overrides []int, picks map[int][]int, duration int) []workflows.Shot {
	if recalibrateEvery <= 0 {
		recalibrateEvery = defaultRecalibrateEvery
	}
	out := make([]workflows.Shot, len(shots))
	for i, text := range shots {
		idx := i + 1
		isAnchor := idx == 1 || (idx-1)%recalibrateEvery == 0
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, MaxChars: 7000})
		p := workflows.Shot{Index: idx, Prompt: compiled.Prompt}
		var bundle []int
		if isAnchor && narrative {
			bundle = selectBundleShots(selectionMode, idx, duration, overrides, picks)
		}
		hasReference := refAssetID != "" || len(bundle) > 0
		switch {
		case isAnchor && hasReference && refAssetID != "" && refIsVideo:
			p.Mode, p.StaticRefVideoAssetID = "r2va", refAssetID
		case isAnchor && hasReference:
			p.Mode, p.StaticRefImageAssetID = "r2va", refAssetID
		case isAnchor:
			p.Mode = "t2va"
		default:
			p.Mode = "i2va"
		}
		if p.Mode == "r2va" && narrative {
			p.Enhance, p.BundleShotIndexes, p.Outline = true, bundle, buildOutline(shots, idx)
		}
		out[i] = p
	}
	return out
}

// selectBundleShots picks which earlier shots an anchor references: a manual
// override wins; smart mode uses the planner's picks; otherwise the most
// recent shots that fit the reference_video budget.
func selectBundleShots(mode string, anchorIdx, duration int, overrides []int, picks map[int][]int) []int {
	if anchorIdx-1 < len(overrides) && overrides[anchorIdx-1] > 0 {
		return []int{overrides[anchorIdx-1]}
	}
	if mode == "manual" {
		return nil
	}
	if mode == "smart" {
		if p := picks[anchorIdx]; len(p) > 0 {
			return p
		}
	}
	if duration <= 0 {
		duration = 5
	}
	maxClips := max(1, min(referenceWindowSeconds/duration, workflows.MaxReferenceVideoClips))
	var out []int
	for i := anchorIdx - 1; i >= 1 && len(out) < maxClips; i-- {
		out = append([]int{i}, out...)
	}
	return out
}

// buildOutline is the plain text of every shot before anchorIdx.
func buildOutline(shots []string, anchorIdx int) string {
	parts := make([]string, 0, anchorIdx)
	for i := 0; i < anchorIdx-1 && i < len(shots); i++ {
		if t := strings.TrimSpace(shots[i]); t != "" {
			parts = append(parts, fmt.Sprintf("第%d段：%s", i+1, t))
		}
	}
	return strings.Join(parts, "；")
}

// smartSelectBundles asks the text model which earlier shots each anchor
// should reference. Advisory: any failure falls back to the window rule.
func (s *Service) smartSelectBundles(ctx context.Context, shots []string, anchors []int) map[int][]int {
	if s.minimax == nil || len(anchors) == 0 {
		return nil
	}
	var b strings.Builder
	for i, t := range shots {
		fmt.Fprintf(&b, "第%d段：%s\n", i+1, t)
	}
	list := make([]string, len(anchors))
	for i, a := range anchors {
		list[i] = strconv.Itoa(a)
	}
	instruction := fmt.Sprintf("下面是一段连续视频的分镜文字描述，共 %d 段：\n\n%s\n"+
		"请只针对这些锚点镜头（第 %s 段）分别判断：为了画面和情节连贯，这一段最需要参考前面哪 1-2 段（只能是编号更小的段）。"+
		"只输出一个 JSON 对象，key 是锚点镜头编号（字符串），value 是建议参考的更早镜头编号数组（最多 2 个，可以是空数组），不要输出任何其他文字。",
		len(shots), b.String(), strings.Join(list, "、"))
	resp, err := s.minimax.ChatCompletion(ctx, minimax.ChatCompletionRequest{
		Model: "MiniMax-M3", Messages: []minimax.ChatMessage{{Role: "user", Content: instruction}},
		Temperature: 0.3, MaxCompletionTokens: 500, Thinking: &minimax.ThinkingConfig{Type: "disabled"},
	})
	if err != nil || len(resp.Choices) == 0 {
		return nil
	}
	return parseSmartPicks(resp.Choices[0].Message.Content, anchors)
}

// parseSmartPicks extracts {"anchor": [earlier...]} and drops anything that
// is not a backward, in-range reference.
func parseSmartPicks(raw string, anchors []int) map[int][]int {
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start == -1 || end < start {
		return nil
	}
	var parsed map[string][]int
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return nil
	}
	valid := make(map[int]bool, len(anchors))
	for _, a := range anchors {
		valid[a] = true
	}
	out := make(map[int][]int, len(parsed))
	for key, picks := range parsed {
		anchor, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || !valid[anchor] {
			continue
		}
		var keep []int
		for _, p := range picks {
			if p >= 1 && p < anchor {
				keep = append(keep, p)
			}
			if len(keep) == 2 {
				break
			}
		}
		if len(keep) > 0 {
			out[anchor] = keep
		}
	}
	return out
}

// resolveCharacterRef is the r2va anchor: an explicit image, else an
// explicit video, else the first bound character's first reference image.
func (s *Service) resolveCharacterRef(ctx context.Context, userID uint64, spec Spec) (string, bool) {
	switch {
	case spec.SourceImageAssetID != "":
		return spec.SourceImageAssetID, false
	case spec.SourceVideoAssetID != "":
		return spec.SourceVideoAssetID, true
	case len(spec.Characters) > 0:
		row, err := s.character(ctx, userID, spec.Characters[0])
		if err != nil {
			return "", false
		}
		if ids, err := characterRefIDs(row); err == nil && len(ids) > 0 {
			return ids[0], false
		}
	}
	return "", false
}

// validateShotReferenceOverrides rejects forward or self references.
func validateShotReferenceOverrides(overrides []int, shotCount int) error {
	for i, r := range overrides {
		if r == 0 {
			continue
		}
		if i >= shotCount {
			return apperr.New("shot_refs_invalid", "shot_reference_overrides has more entries than shots")
		}
		if r < 1 || r > i {
			return apperr.New("shot_refs_invalid", fmt.Sprintf("shot %d's reference override must point at an earlier shot (1..%d), got %d", i+1, i, r), "shot", i+1)
		}
	}
	return nil
}

func (s *Service) prepareVideoSequence(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	if len(spec.Shots) == 0 {
		return nil, "", errShotsRequired
	}
	if err := validateShotReferenceOverrides(spec.ShotReferenceOverrides, len(spec.Shots)); err != nil {
		return nil, "", err
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, "", err
	}
	duration := videoDuration(spec.DurationSeconds)
	refAsset, refIsVideo := s.resolveCharacterRef(ctx, userID, spec)
	var picks map[int][]int
	if spec.NarrativeContinuity && spec.ReferenceSelectionMode == "smart" {
		every := spec.RecalibrateEvery
		if every <= 0 {
			every = defaultRecalibrateEvery
		}
		var anchors []int
		for idx := 1; idx <= len(spec.Shots); idx++ {
			if idx == 1 || (idx-1)%every == 0 {
				anchors = append(anchors, idx)
			}
		}
		picks = s.smartSelectBundles(ctx, spec.Shots, anchors)
	}
	shots := planShots(spec.Shots, characters, presets, refAsset, refIsVideo, spec.RecalibrateEvery,
		spec.NarrativeContinuity, spec.ReferenceSelectionMode, spec.ShotReferenceOverrides, picks, duration)
	ratio := spec.Ratio
	if ratio == "" {
		ratio = "16:9"
	}
	draft := "768P"
	if spec.SkipPreview {
		draft = "2K"
	}
	plan, err := workflows.VideoSequencePlan(workflows.VideoSequence{
		UserID: userID, Shots: shots, Duration: duration, Ratio: ratio, DraftResolution: draft, SkipPreview: spec.SkipPreview,
	})
	return plan, spec.Shots[0], err
}

// ResumeVideoSequenceRequest is the preview-gate decision. Shot indices are
// 1-based; a shot in neither list is kept as previewed.
type ResumeVideoSequenceRequest struct {
	SelectedShots       []int          `json:"selected_shots"` // upgrade to 2K
	RedoShots           []int          `json:"redo_shots"`     // regenerate at 768P
	RedoPromptOverrides map[int]string `json:"redo_prompt_overrides"`
	// QuoteTotal, when set, must equal the server's current quote for the
	// same decision; otherwise the resume is refused rather than charging a
	// different amount than the user confirmed.
	QuoteTotal *int `json:"quote_total,omitempty"`
}

// ErrQuoteChanged is returned when a confirmed quote no longer matches.
var ErrQuoteChanged = apperr.New("price_changed", "the price changed since it was quoted")

// ResumeQuote itemizes what a gate decision will reserve.
type ResumeQuote struct {
	Items []EstimateItem `json:"items"`
	Total int            `json:"total"`
	// AllUpgrade is the total for upgrading every shot, priced the same way,
	// for comparison.
	AllUpgrade int `json:"all_upgrade"`
}

type gateContext struct {
	job       *persistence.Job
	spec      Spec
	meta      workflows.SequenceMeta
	results   map[int]workflows.ShotResult
	decisions map[int]workflows.ShotDecision
}

func (s *Service) gateContext(ctx context.Context, userID uint64, bizID string, req ResumeVideoSequenceRequest) (*gateContext, error) {
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	if job.WorkflowName != "video.sequence" || job.Engine != "v2" {
		return nil, ErrNotSupported
	}
	if job.Status != workflow.JobAwaitingReview {
		return nil, orchestrator.ErrGateNotSuspended
	}
	g := &gateContext{job: job, results: map[int]workflows.ShotResult{}, decisions: map[int]workflows.ShotDecision{}}
	if err := json.Unmarshal(job.Spec, &g.spec); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}
	rawMeta, err := s.orch.PlanMeta(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawMeta, &g.meta); err != nil {
		return nil, fmt.Errorf("decode plan meta: %w", err)
	}
	nodes, err := s.orch.Nodes(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		var idx int
		if _, err := fmt.Sscanf(n.Name, "shot-%d", &idx); err != nil {
			continue
		}
		r := g.results[idx]
		if strings.HasSuffix(n.Name, "-extract") {
			r.LastFrame, _ = n.Outputs["last-frame-asset-id"].(string)
		} else {
			r.AssetID, _ = n.Outputs["asset-id"].(string)
		}
		g.results[idx] = r
	}
	total := len(g.meta.Shots)
	for _, i := range req.RedoShots {
		if i < 1 || i > total {
			return nil, apperr.New("review_decision_invalid", fmt.Sprintf("redo shot %d out of range", i), "shot", i)
		}
		g.decisions[i] = workflows.ShotDecision{Redo: true, PromptOverride: req.RedoPromptOverrides[i]}
	}
	for _, i := range req.SelectedShots {
		if i < 1 || i > total {
			return nil, apperr.New("review_decision_invalid", fmt.Sprintf("upgrade shot %d out of range", i), "shot", i)
		}
		if g.decisions[i].Redo {
			return nil, apperr.New("review_decision_invalid", fmt.Sprintf("shot %d cannot be both redone and upgraded", i), "shot", i)
		}
		g.decisions[i] = workflows.ShotDecision{Upgrade: true}
	}
	return g, nil
}

func (g *gateContext) quote() ResumeQuote {
	redo, upgrade := 0, 0
	for _, d := range g.decisions {
		switch {
		case d.Redo:
			redo++
		case d.Upgrade:
			upgrade++
		}
	}
	perRedo := creditsvc.EstimateVideoCredits(g.meta.Duration, "768P")
	perUpgrade := creditsvc.EstimateVideoCredits(g.meta.Duration, "2K")
	q := ResumeQuote{AllUpgrade: perUpgrade * len(g.meta.Shots)}
	if redo > 0 {
		q.Items = append(q.Items, EstimateItem{Kind: ItemKindRedo, Count: redo, Credits: redo * perRedo})
	}
	if upgrade > 0 {
		q.Items = append(q.Items, EstimateItem{Kind: ItemKindUpgrade, Count: upgrade, Credits: upgrade * perUpgrade})
	}
	// Concatenation runs locally and is not charged.
	q.Items = append(q.Items, EstimateItem{Kind: ItemKindCompose, Count: 1, Credits: 0})
	for _, it := range q.Items {
		q.Total += it.Credits
	}
	return q
}

// QuoteResume prices a gate decision without applying it.
func (s *Service) QuoteResume(ctx context.Context, userID uint64, bizID string, req ResumeVideoSequenceRequest) (*ResumeQuote, error) {
	g, err := s.gateContext(ctx, userID, bizID, req)
	if err != nil {
		return nil, err
	}
	q := g.quote()
	return &q, nil
}

// Resume applies a gate decision: it reserves exactly the quoted amount and
// appends the redo/upgrade/concat nodes in one transaction.
func (s *Service) Resume(ctx context.Context, userID uint64, bizID string, req ResumeVideoSequenceRequest) error {
	g, err := s.gateContext(ctx, userID, bizID, req)
	if err != nil {
		return err
	}
	q := g.quote()
	if req.QuoteTotal != nil && *req.QuoteTotal != q.Total {
		return ErrQuoteChanged
	}
	patch, err := workflows.VideoSequenceResume(userID, g.meta, g.results, g.decisions)
	if err != nil {
		return err
	}
	decision := map[string]any{"redo_shots": req.RedoShots, "upgrade_shots": req.SelectedShots, "quoted_total": q.Total}
	hold := func(ctx context.Context, tx *sql.Tx, job orchestrator.JobRef) error {
		if q.Total <= 0 {
			return nil
		}
		h := creditsvc.JobHold{UserID: job.UserID, JobID: job.ID, JobBizID: job.BizID, Workflow: "video.sequence"}
		if err := s.credits.HoldForJobTx(ctx, tx, h, "job:"+job.BizID+":hold:resume", q.Total, "upgrade"); err != nil {
			return fmt.Errorf("hold credits: %w", err)
		}
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET credit_held = credit_held + ?, credit_estimated = credit_estimated + ? WHERE id = ?`, q.Total, q.Total, job.ID)
		return err
	}
	return s.orch.Resume(ctx, g.job.ID, "gate", decision, patch, hold)
}
