package workflows

import (
	"fmt"
	"strconv"

	"aigc-platform/internal/domain/workflow"
)

// Reference strategies decided by the comic planner.
const (
	RefStrategyChain              = "chain"
	RefStrategyAnchor             = "anchor"
	RefStrategyAnchorPerCharacter = "anchor_per_character"
	RefStrategyNone               = "none"
)

// ComicPanel is one fully prepared panel of a classic comic.
type ComicPanel struct {
	Index int // 1-based
	// EnhancePrompt is what H3-Context-IR receives: outline, this panel's
	// text, dialogue and style instructions.
	EnhancePrompt string
	Seed          string
	Dialogue      string
	// RefAsset is the build-time reference: every panel's for the anchor
	// strategies, panel 1's only for chain.
	RefAsset string
}

// Comic4 describes a classic (MiniMax or Gemini) multi-panel comic.
type Comic4 struct {
	UserID        uint64
	Provider      string // minimax | gemini
	Strategy      string
	Layout        string
	Style         string
	StylizePrompt string // prompt converting a raw reference into the comic style
	Panels        []ComicPanel
}

// Comic4Plan: each raw reference is converted to the comic style once, each
// panel is written by H3-Context-IR and drawn by the image model, and the
// panels are composed into one page. Chain panels depend on the previous
// panel; anchored panels run in parallel.
func Comic4Plan(c Comic4) (*workflow.Plan, error) {
	if len(c.Panels) < 2 {
		return nil, fmt.Errorf("a comic needs at least 2 panels")
	}
	uid := strconv.FormatUint(c.UserID, 10)
	imageExec := "minimax.image"
	if c.Provider == ProviderGemini {
		imageExec = "gemini.image"
	}
	gates := func(n workflow.NodeSpec, dialogue string) workflow.NodeSpec {
		if imageExec == "minimax.image" {
			n.Inputs["expected-style"] = lit(c.Style)
			n.Inputs["expected-dialogue"] = lit(dialogue)
		}
		return n
	}

	var nodes []workflow.NodeSpec
	stylized := map[string]string{}
	stylize := func(raw string) string {
		if name, ok := stylized[raw]; ok {
			return name
		}
		name := fmt.Sprintf("stylize-reference-%d", len(stylized)+1)
		n := node(name, imageExec, imagePolicy, map[string]workflow.Input{
			"prompt": lit(c.StylizePrompt), "source-image-asset-id": lit(raw), "user-id": lit(uid), "n": lit("1"), "seed": lit(""),
		})
		n.Check = workflow.CheckAllRequested
		nodes = append(nodes, withDisplay(gates(n, ""), "group", "stylize"))
		stylized[raw] = name
		return name
	}

	panelNames := make([]workflow.Input, 0, len(c.Panels))
	for _, p := range c.Panels {
		panelName := fmt.Sprintf("panel-%d", p.Index)
		enhanceName := fmt.Sprintf("enhance-panel-%d", p.Index)
		ref := lit("")
		refList := lit([]string{})
		switch {
		case c.Strategy != RefStrategyChain && p.RefAsset != "":
			s := stylize(p.RefAsset)
			ref, refList = from(s, "asset-id"), workflow.ListOf(from(s, "asset-id"))
		case c.Strategy != RefStrategyChain:
		case p.Index == 1 && p.RefAsset != "":
			s := stylize(p.RefAsset)
			ref, refList = from(s, "asset-id"), workflow.ListOf(from(s, "asset-id"))
		case p.Index == 1:
		default:
			prev := fmt.Sprintf("panel-%d", p.Index-1)
			ref, refList = from(prev, "asset-id"), workflow.ListOf(from(prev, "asset-id"))
		}

		enhance := node(enhanceName, "minimax.prompt_enhance", panelEnhance, map[string]workflow.Input{
			"prompt": lit(p.EnhancePrompt), "duration": lit("5"), "ratio": lit("1:1"),
			"first-frame-asset-id": lit(""), "last-frame-asset-id": lit(""),
			"reference-image-asset-ids": refList, "reference-video-asset-ids": lit([]string{}), "reference-audio-asset-ids": lit([]string{}),
		})
		nodes = append(nodes, withDisplay(enhance, "panel", p.Index, "group", "enhance"))

		panel := node(panelName, imageExec, imagePolicy, map[string]workflow.Input{
			"prompt": from(enhanceName, "enhanced-prompt"), "source-image-asset-id": ref, "user-id": lit(uid), "n": lit("1"), "seed": lit(p.Seed),
		})
		panel.Check = workflow.CheckAllRequested
		nodes = append(nodes, withDisplay(gates(panel, p.Dialogue), "panel", p.Index))
		panelNames = append(panelNames, from(panelName, "asset-id"))
	}

	compose := node("compose", "local.compose", composePolicy, map[string]workflow.Input{
		"asset-ids": workflow.ListOf(panelNames...), "layout": lit(c.Layout), "user-id": lit(uid),
	})
	nodes = append(nodes, withDisplay(compose, "result", true))
	return &workflow.Plan{Deadline: defaultDeadline, Nodes: nodes}, nil
}
