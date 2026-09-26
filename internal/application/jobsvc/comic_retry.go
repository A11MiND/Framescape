package jobsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/apperr"
)

// comic4Meta is what a classic comic keeps for redrawing panels later: the
// planner's decisions and each panel's text, which are not in the spec when
// the story was split or planned.
type comic4Meta struct {
	Provider string            `json:"provider"`
	Style    string            `json:"style"`
	Layout   string            `json:"layout"`
	Subject  string            `json:"subject,omitempty"`
	Panels   []comic4PanelMeta `json:"panels"`
}

type comic4PanelMeta struct {
	Text     string `json:"text"`
	Seed     string `json:"seed"`
	Dialogue string `json:"dialogue,omitempty"`
}

func panelMetas(plans []panelPlan) []comic4PanelMeta {
	out := make([]comic4PanelMeta, len(plans))
	for i, p := range plans {
		out[i] = comic4PanelMeta{Text: p.RawText, Seed: p.Seed, Dialogue: p.Dialogue}
	}
	return out
}

// ComicRetryPanel is a panel that a retry would redraw.
type ComicRetryPanel struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// ComicRetryQuote is what redrawing a classic comic's unfinished panels costs.
type ComicRetryQuote struct {
	Panels       []ComicRetryPanel `json:"panels"`
	Items        []EstimateItem    `json:"items"`
	CreditsTotal int               `json:"credits_total"`
}

var errComicRetryUnavailable = apperr.New("not_supported", "only a finished classic comic with unfinished panels can redraw them")

// comicRetrySource is a classic comic's state as far as a retry needs it.
type comicRetrySource struct {
	job   *persistence.Job
	spec  Spec
	meta  comic4Meta
	nodes map[string]*orchestrator.Node
	redo  []int // 1-based panels that did not succeed
}

func (s *Service) comicRetrySource(ctx context.Context, userID uint64, bizID string) (*comicRetrySource, error) {
	job, err := s.load(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	var spec Spec
	if err := json.Unmarshal(job.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode job spec: %w", err)
	}
	if job.WorkflowName != "image.comic4" || spec.ComicMode != "" || !slices.Contains([]string{workflow.JobPartial, workflow.JobFailed}, job.Status) {
		return nil, errComicRetryUnavailable
	}
	raw, err := s.orch.PlanMeta(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	src := &comicRetrySource{job: job, spec: spec, nodes: map[string]*orchestrator.Node{}}
	if len(raw) == 0 || json.Unmarshal(raw, &src.meta) != nil || len(src.meta.Panels) == 0 {
		return nil, errComicRetryUnavailable
	}
	nodes, err := s.orch.Nodes(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		src.nodes[n.Name] = n
	}
	for i := range src.meta.Panels {
		if n := src.nodes[fmt.Sprintf("panel-%d", i+1)]; n == nil || n.Status != workflow.NodeSucceeded {
			src.redo = append(src.redo, i+1)
		}
	}
	if len(src.redo) == 0 {
		return nil, errComicRetryUnavailable
	}
	return src, nil
}

func comicRetryEstimate(panels int) ([]EstimateItem, int) {
	items := []EstimateItem{
		{Kind: ItemKindComic4Panels, Count: panels, Credits: creditsvc.EstimatePerNodeImageCredits(panels)},
		{Kind: ItemKindPromptEnhance, Count: panels, Credits: panels * creditsvc.EstimatePromptEnhanceCredits()},
	}
	return items, items[0].Credits + items[1].Credits
}

// CanRetryComicPanels reports whether a job is a classic comic whose
// unfinished panels can be redrawn.
func (s *Service) CanRetryComicPanels(ctx context.Context, job *persistence.Job) bool {
	if job.WorkflowName != "image.comic4" || !slices.Contains([]string{workflow.JobPartial, workflow.JobFailed}, job.Status) {
		return false
	}
	_, err := s.comicRetrySource(ctx, job.UserID, job.BizID)
	return err == nil
}

// QuoteComicRetry lists the panels a retry would redraw and what it costs.
func (s *Service) QuoteComicRetry(ctx context.Context, userID uint64, bizID string) (*ComicRetryQuote, error) {
	src, err := s.comicRetrySource(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	q := &ComicRetryQuote{}
	for _, i := range src.redo {
		q.Panels = append(q.Panels, ComicRetryPanel{Index: i, Text: src.meta.Panels[i-1].Text})
	}
	q.Items, q.CreditsTotal = comicRetryEstimate(len(src.redo))
	return q, nil
}

// panelOutput is a succeeded node's image.
func panelOutput(n *orchestrator.Node) string {
	if n == nil || n.Status != workflow.NodeSucceeded {
		return ""
	}
	id, _ := n.Outputs["asset-id"].(string)
	return id
}

// RetryComicPanels redraws a classic comic's unfinished panels as a new job
// linked to the original, optionally with new text for some of them, and
// composes the page again with the panels that succeeded.
func (s *Service) RetryComicPanels(ctx context.Context, userID uint64, bizID string, texts map[int]string, quoteTotal *int) (*persistence.Job, error) {
	src, err := s.comicRetrySource(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	for i := range texts {
		if !slices.Contains(src.redo, i) {
			return nil, apperr.New("bad_request", fmt.Sprintf("panel %d is not being redrawn", i), "panel", i)
		}
	}
	characters, err := s.resolveCharacters(ctx, userID, src.spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, userID, src.spec.PresetIDs)
	if err != nil {
		return nil, err
	}

	// The same prompt construction as the original, so a redrawn panel
	// matches the others; edited text also updates later panels' outlines.
	plans := make([]panelPlan, len(src.meta.Panels))
	for i, pm := range src.meta.Panels {
		text := pm.Text
		if t := strings.TrimSpace(texts[i+1]); t != "" {
			text = t
		}
		styled := text
		if src.meta.Subject != "" {
			styled += "，角色具体外观（务必保持一致）：" + src.meta.Subject
		}
		if src.meta.Style != "" {
			styled += "，" + src.meta.Style
		}
		compiled := prompt.Compile(prompt.Input{Text: styled, Characters: characters, Presets: presets})
		plans[i] = panelPlan{Index: i + 1, Prompt: compiled.Prompt, RawText: text, Seed: pm.Seed, Dialogue: pm.Dialogue}
	}

	panels := make([]workflows.Comic4RetryPanel, len(plans))
	for i, p := range plans {
		rp := workflows.Comic4RetryPanel{ComicPanel: workflows.ComicPanel{Index: p.Index, Seed: p.Seed, Dialogue: p.Dialogue}}
		orig := src.nodes[fmt.Sprintf("panel-%d", p.Index)]
		if keep := panelOutput(orig); keep != "" {
			rp.Keep = keep
			panels[i] = rp
			continue
		}
		rp.EnhancePrompt = panelEnhancePrompt(plans, p, src.meta.Style)
		if orig != nil {
			if in, ok := orig.Inputs["source-image-asset-id"]; ok {
				switch {
				case in.Ref != nil && strings.HasPrefix(in.Ref.Node, "panel-"):
					prev, _ := strconv.Atoi(strings.TrimPrefix(in.Ref.Node, "panel-"))
					if kept := panelOutput(src.nodes[in.Ref.Node]); kept != "" {
						rp.RefAsset = kept
					} else {
						rp.RefPanel = prev
					}
				case in.Ref != nil:
					// A stylized anchor: reuse its image; without it the panel cannot match the others.
					if rp.RefAsset = panelOutput(src.nodes[in.Ref.Node]); rp.RefAsset == "" {
						return nil, errComicRetryUnavailable
					}
				default:
					_ = json.Unmarshal(in.Value, &rp.RefAsset)
				}
			}
		}
		panels[i] = rp
	}
	plan, err := workflows.Comic4RetryPlan(workflows.Comic4Retry{
		UserID: userID, Provider: src.meta.Provider, Style: src.meta.Style, Layout: src.meta.Layout, Panels: panels,
	})
	if err != nil {
		return nil, err
	}
	_, estimate := comicRetryEstimate(len(src.redo))
	if quoteTotal != nil && *quoteTotal != estimate {
		return nil, apperr.New("price_changed", "the price changed since it was quoted", "credits_total", estimate)
	}
	if plan.Meta, err = json.Marshal(comic4Meta{Provider: src.meta.Provider, Style: src.meta.Style, Layout: src.meta.Layout, Subject: src.meta.Subject, Panels: panelMetas(plans)}); err != nil {
		return nil, err
	}
	return s.submit(ctx, submission{
		userID: userID, workflowName: src.job.WorkflowName, spec: src.spec, title: src.job.Title, estimate: estimate,
		plan: plan, holdKind: "retry", retryOf: src.job, retryNode: "panels", projectID: src.job.ProjectID,
	})
}
