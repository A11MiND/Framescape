package workflows

import (
	"fmt"
	"strconv"

	"aigc-platform/internal/domain/workflow"
)

// Image providers.
const (
	ProviderMiniMax = "minimax"
	ProviderGemini  = "gemini"
	ProviderOpenAI  = "openai"
)

// ImageSingle is one call producing N images.
type ImageSingle struct {
	UserID      uint64
	Provider    string // minimax | gemini | openai
	Prompt      string
	Seed        string
	N           int
	AspectRatio string
	// References are the reference images in priority order. MiniMax's
	// subject_reference takes exactly one, so only the first is sent there.
	References []string
	// OpenAI only.
	Size    string
	Quality string
}

func ImageSinglePlan(p ImageSingle) *workflow.Plan {
	userID := strconv.FormatUint(p.UserID, 10)
	var gen workflow.NodeSpec
	switch p.Provider {
	case ProviderOpenAI:
		gen = node("gen", "openai.image", openAIPolicy, map[string]workflow.Input{
			"prompt": lit(p.Prompt), "user-id": lit(userID), "reference-image-asset-ids": lit(p.References),
			"n": lit(p.N), "size": lit(p.Size), "quality": lit(p.Quality),
		})
	case ProviderGemini:
		gen = node("gen", "gemini.image", imagePolicy, map[string]workflow.Input{
			"prompt": lit(p.Prompt), "seed": lit(p.Seed), "user-id": lit(userID), "n": lit(strconv.Itoa(p.N)),
			"aspect-ratio": lit(p.AspectRatio), "source-image-asset-id": lit(first(p.References)),
			"reference-image-asset-ids": lit(p.References),
		})
		gen.Check = workflow.CheckAllRequested
	default:
		gen = node("gen", "minimax.image", imagePolicy, map[string]workflow.Input{
			"prompt": lit(p.Prompt), "seed": lit(p.Seed), "user-id": lit(userID), "n": lit(strconv.Itoa(p.N)),
			"aspect-ratio": lit(p.AspectRatio), "source-image-asset-id": lit(first(p.References)),
		})
		gen.Check = workflow.CheckAllRequested
	}
	return &workflow.Plan{Deadline: defaultDeadline, Nodes: []workflow.NodeSpec{withDisplay(gen, "result", true)}}
}

// SequenceShot is one image.sequence shot.
type SequenceShot struct {
	Index     int // 1-based
	Prompt    string
	Seed      string
	Reference string // static library reference, ignored when RefShot > 0
	RefShot   int    // earlier shot whose output is this shot's reference, 0 = none
}

// ImageSequencePlan runs independent shots in parallel; a shot referencing
// an earlier one waits for it and uses its output as the reference.
func ImageSequencePlan(userID uint64, provider string, shots []SequenceShot) (*workflow.Plan, error) {
	uid := strconv.FormatUint(userID, 10)
	nodes := make([]workflow.NodeSpec, 0, len(shots))
	for _, s := range shots {
		if s.RefShot < 0 || s.RefShot >= s.Index {
			return nil, fmt.Errorf("shot %d may only reference an earlier shot, got %d", s.Index, s.RefShot)
		}
		ref := lit(s.Reference)
		refs := lit([]string{s.Reference})
		if s.Reference == "" {
			refs = lit([]string{})
		}
		if s.RefShot > 0 {
			ref = from(fmt.Sprintf("shot-%d", s.RefShot), "asset-id")
			refs = workflow.ListOf(ref)
		}
		name := fmt.Sprintf("shot-%d", s.Index)
		var n workflow.NodeSpec
		if provider == ProviderOpenAI {
			n = node(name, "openai.image", openAIPolicy, map[string]workflow.Input{
				"prompt": lit(s.Prompt), "user-id": lit(uid), "reference-image-asset-ids": refs, "n": lit(1),
			})
		} else {
			n = node(name, "minimax.image", imagePolicy, map[string]workflow.Input{
				"prompt": lit(s.Prompt), "seed": lit(s.Seed), "source-image-asset-id": ref, "user-id": lit(uid), "n": lit("1"),
			})
			n.Check = workflow.CheckAllRequested
		}
		nodes = append(nodes, withDisplay(n, "shot", s.Index, "result", true))
	}
	return &workflow.Plan{Deadline: defaultDeadline, Nodes: nodes}, nil
}

// DirectComicPlan is one OpenAI call for a whole comic page or one panel.
func DirectComicPlan(userID uint64, prompt string, refs []string) *workflow.Plan {
	gen := node("compose", "openai.image", openAIPolicy, map[string]workflow.Input{
		"prompt": lit(prompt), "reference-image-asset-ids": lit(refs), "user-id": lit(strconv.FormatUint(userID, 10)),
	})
	return &workflow.Plan{Deadline: defaultDeadline, Nodes: []workflow.NodeSpec{withDisplay(gen, "result", true)}}
}

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}
