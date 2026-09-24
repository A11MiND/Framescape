package workflows

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"aigc-platform/internal/domain/workflow"
)

// VideoSingle is one clip.
type VideoSingle struct {
	UserID          uint64
	Prompt          string
	Duration        int
	Resolution      string
	Ratio           string
	FirstFrame      string
	LastFrame       string
	ReferenceImages []string
	ReferenceVideos []string
	ReferenceAudios []string
	Enhance         bool
}

// VideoSinglePlan: optional prompt enhancement, generation, then first/last
// frame extraction for downstream reuse.
func VideoSinglePlan(v VideoSingle) *workflow.Plan {
	uid := strconv.FormatUint(v.UserID, 10)
	refs := map[string]workflow.Input{
		"first-frame-asset-id": lit(v.FirstFrame), "last-frame-asset-id": lit(v.LastFrame),
		"reference-image-asset-ids": lit(v.ReferenceImages), "reference-video-asset-ids": lit(v.ReferenceVideos),
		"reference-audio-asset-ids": lit(v.ReferenceAudios),
	}
	var nodes []workflow.NodeSpec
	prompt := lit(v.Prompt)
	if v.Enhance {
		in := map[string]workflow.Input{"prompt": lit(v.Prompt), "duration": lit(strconv.Itoa(v.Duration)), "ratio": lit(v.Ratio)}
		for k, x := range refs {
			in[k] = x
		}
		nodes = append(nodes, node("enhance", "minimax.prompt_enhance", enhancePolicy, in))
		prompt = from("enhance", "enhanced-prompt")
	}
	in := map[string]workflow.Input{
		"prompt": prompt, "duration": lit(strconv.Itoa(v.Duration)), "resolution": lit(v.Resolution),
		"ratio": lit(v.Ratio), "user-id": lit(uid),
	}
	for k, x := range refs {
		in[k] = x
	}
	nodes = append(nodes, withDisplay(node("gen", "minimax.video", videoPolicy, in), "result", true))
	nodes = append(nodes, node("extract", "local.ffmpeg.extract", extractPolicy, map[string]workflow.Input{
		"video-asset-id": from("gen", "asset-id"), "user-id": lit(uid),
	}))
	return &workflow.Plan{Deadline: defaultDeadline, Nodes: nodes}
}

// Shot is one planned video.sequence shot.
type Shot struct {
	Index                 int    `json:"index"`
	Prompt                string `json:"prompt"`
	Mode                  string `json:"mode"` // r2va | i2va | t2va
	StaticRefImageAssetID string `json:"static_ref_image,omitempty"`
	StaticRefVideoAssetID string `json:"static_ref_video,omitempty"`
	BundleShotIndexes     []int  `json:"bundle,omitempty"`
	Enhance               bool   `json:"enhance,omitempty"`
	Outline               string `json:"outline,omitempty"`
}

// VideoSequence describes a multi-shot video.
type VideoSequence struct {
	UserID          uint64
	Shots           []Shot
	Duration        int
	Ratio           string
	DraftResolution string
	SkipPreview     bool
}

// SequenceMeta is stored with the job so a gate decision is applied to the
// exact plan that ran, not a re-derived one.
type SequenceMeta struct {
	Shots    []Shot `json:"shots"`
	Duration int    `json:"duration"`
	Ratio    string `json:"ratio"`
}

// VideoSequencePlan chains the shots (each i2va shot starts from the previous
// shot's extracted last frame). With a preview it ends at a gate; the gate
// decision appends redo/upgrade/concat (VideoSequenceResume). Without one,
// the draft is final and is concatenated directly.
func VideoSequencePlan(v VideoSequence) (*workflow.Plan, error) {
	if len(v.Shots) == 0 {
		return nil, fmt.Errorf("video.sequence needs at least one shot")
	}
	uid := strconv.FormatUint(v.UserID, 10)
	dur := strconv.Itoa(v.Duration)
	n := len(v.Shots)
	var nodes []workflow.NodeSpec
	prevExtract := ""
	for _, s := range v.Shots {
		shot := fmt.Sprintf("shot-%d", s.Index)
		in := map[string]workflow.Input{
			"prompt": lit(s.Prompt), "duration": lit(dur), "resolution": lit(v.DraftResolution), "ratio": lit(""),
			"first-frame-asset-id": lit(""), "reference-image-asset-ids": lit([]string{}), "user-id": lit(uid),
			"shot-index": lit(strconv.Itoa(s.Index)), "reference-video-asset-ids": lit([]string{}),
		}
		var deps []string
		if prevExtract != "" {
			deps = []string{prevExtract}
		}
		switch s.Mode {
		case "r2va":
			switch {
			case s.Enhance:
				images, videos := bundleRefs(s)
				enhance := fmt.Sprintf("enhance-shot-%d", s.Index)
				prompt := s.Prompt
				if s.Outline != "" {
					prompt = "剧情大纲（已发生的镜头）：" + s.Outline + "\n\n本镜头需要表现：" + s.Prompt
				}
				en := node(enhance, "minimax.prompt_enhance", enhancePolicy, map[string]workflow.Input{
					"prompt": lit(prompt), "duration": lit(dur), "ratio": lit("adaptive"),
					"first-frame-asset-id": lit(""), "last-frame-asset-id": lit(""),
					"reference-image-asset-ids": images, "reference-video-asset-ids": videos, "reference-audio-asset-ids": lit([]string{}),
				})
				en.Deps = deps
				nodes = append(nodes, withDisplay(en, "shot", s.Index, "group", "enhance"))
				in["prompt"] = from(enhance, "enhanced-prompt")
				in["reference-image-asset-ids"] = images
				in["reference-video-asset-ids"] = videos
			case s.StaticRefVideoAssetID != "":
				in["reference-video-asset-ids"] = lit([]string{s.StaticRefVideoAssetID})
			default:
				in["reference-image-asset-ids"] = lit([]string{s.StaticRefImageAssetID})
			}
		case "t2va":
			in["ratio"] = lit(v.Ratio)
		case "i2va":
			if prevExtract == "" {
				return nil, fmt.Errorf("shot %d continues a previous shot but is first", s.Index)
			}
			in["first-frame-asset-id"] = from(prevExtract, "last-frame-asset-id")
		}
		gen := node(shot, "minimax.video", videoPolicy, in)
		gen.Deps = deps
		nodes = append(nodes, withDisplay(gen, "shot", s.Index, "group", "draft"))
		if s.Index < n {
			extract := fmt.Sprintf("shot-%d-extract", s.Index)
			nodes = append(nodes, withDisplay(node(extract, "local.ffmpeg.extract", extractPolicy, map[string]workflow.Input{
				"video-asset-id": from(shot, "asset-id"), "user-id": lit(uid),
			}), "shot", s.Index, "group", "extract"))
			prevExtract = extract
		}
	}

	last := fmt.Sprintf("shot-%d", n)
	if v.SkipPreview {
		keepIdx := make([]string, n)
		keepAssets := make([]workflow.Input, n)
		for i, s := range v.Shots {
			keepIdx[i] = strconv.Itoa(s.Index)
			keepAssets[i] = from(fmt.Sprintf("shot-%d", s.Index), "asset-id")
		}
		nodes = append(nodes, concatNode(uid, n, keepIdx, workflow.ListOf(keepAssets...), nil, nil, nil, nil))
	} else {
		nodes = append(nodes, workflow.NodeSpec{Name: "gate", Executor: workflow.GateExecutor, Deps: []string{last}})
	}
	meta, err := json.Marshal(SequenceMeta{Shots: v.Shots, Duration: v.Duration, Ratio: v.Ratio})
	if err != nil {
		return nil, err
	}
	return &workflow.Plan{Deadline: sequenceDeadline, Nodes: nodes, Meta: meta}, nil
}

// bundleRefs lists an anchor shot's references: the static anchor first,
// then each bundled earlier shot's clip and last frame.
func bundleRefs(s Shot) (images, videos workflow.Input) {
	var img, vid []workflow.Input
	if s.StaticRefImageAssetID != "" {
		img = append(img, lit(s.StaticRefImageAssetID))
	}
	if s.StaticRefVideoAssetID != "" {
		vid = append(vid, lit(s.StaticRefVideoAssetID))
	}
	for _, b := range s.BundleShotIndexes {
		vid = append(vid, from(fmt.Sprintf("shot-%d", b), "asset-id"))
		img = append(img, from(fmt.Sprintf("shot-%d-extract", b), "last-frame-asset-id"))
	}
	return workflow.ListOf(img...), workflow.ListOf(vid...)
}

func concatNode(uid string, total int, keepIdx []string, keepAssets workflow.Input, redoIdx []string, redoAssets []workflow.Input, upIdx []string, upAssets []workflow.Input) workflow.NodeSpec {
	n := node("concat", "local.ffmpeg.concat", concatPolicy, map[string]workflow.Input{
		"keep-shot-index": lit(nonNil(keepIdx)), "keep-asset-id": keepAssets,
		"redone-shot-index": lit(nonNil(redoIdx)), "redone-asset-id": workflow.ListOf(redoAssets...),
		"upgraded-shot-index": lit(nonNil(upIdx)), "upgraded-asset-id": workflow.ListOf(upAssets...),
		"total-shots": lit(strconv.Itoa(total)), "user-id": lit(uid),
	})
	return withDisplay(n, "result", true)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ShotDecision is the preview-gate choice for one shot.
type ShotDecision struct {
	Redo           bool
	Upgrade        bool
	PromptOverride string
}

// ShotResult is what the draft produced for a shot.
type ShotResult struct {
	AssetID   string
	LastFrame string // of this shot's extract node, "" for the last shot
}

// VideoSequenceResume builds the nodes a gate decision appends: a 768P redo
// or a 2K upgrade per chosen shot, and the final concat. Every input comes
// from finished draft nodes, so all values are literals except the redo and
// upgrade outputs concat consumes.
func VideoSequenceResume(userID uint64, meta SequenceMeta, results map[int]ShotResult, decisions map[int]ShotDecision) ([]workflow.NodeSpec, error) {
	uid := strconv.FormatUint(userID, 10)
	dur := strconv.Itoa(meta.Duration)
	var nodes []workflow.NodeSpec
	var keepIdx, redoIdx, upIdx []string
	var keepAssets []workflow.Input
	var redoAssets, upAssets []workflow.Input

	shots := append([]Shot(nil), meta.Shots...)
	sort.Slice(shots, func(i, j int) bool { return shots[i].Index < shots[j].Index })
	for _, s := range shots {
		res, ok := results[s.Index]
		if !ok || res.AssetID == "" {
			return nil, fmt.Errorf("shot %d has no draft result", s.Index)
		}
		d := decisions[s.Index]
		if d.Redo && d.Upgrade {
			return nil, fmt.Errorf("shot %d cannot be both redone and upgraded", s.Index)
		}
		firstFrame := ""
		if s.Mode == "i2va" {
			firstFrame = results[s.Index-1].LastFrame
		}
		images, videos := literalRefs(s, results)
		idx := strconv.Itoa(s.Index)
		switch {
		case d.Redo:
			prompt := s.Prompt
			if d.PromptOverride != "" {
				prompt = d.PromptOverride
			}
			name := "redo-" + idx
			n := node(name, "minimax.video", videoPolicy, map[string]workflow.Input{
				"prompt": lit(prompt), "duration": lit(dur), "resolution": lit("768P"), "ratio": lit(meta.Ratio),
				"first-frame-asset-id": lit(firstFrame), "reference-image-asset-ids": lit(images), "user-id": lit(uid),
				"shot-index": lit(idx), "reference-video-asset-ids": lit(videos),
			})
			n.Deps = []string{"gate"}
			nodes = append(nodes, withDisplay(n, "shot", s.Index, "group", "redo"))
			redoIdx = append(redoIdx, idx)
			redoAssets = append(redoAssets, from(name, "asset-id"))
		case d.Upgrade:
			name := "upgrade-" + idx
			n := node(name, "minimax.video.regen", videoPolicy, map[string]workflow.Input{
				"prompt": lit(s.Prompt), "duration": lit(dur), "ratio": lit(meta.Ratio),
				"first-frame-asset-id": lit(firstFrame), "reference-image-asset-ids": lit(images),
				"base-video-asset-id": lit(res.AssetID), "user-id": lit(uid), "shot-index": lit(idx),
				"reference-video-asset-ids": lit(videos),
			})
			n.Deps = []string{"gate"}
			nodes = append(nodes, withDisplay(n, "shot", s.Index, "group", "upgrade"))
			upIdx = append(upIdx, idx)
			upAssets = append(upAssets, from(name, "asset-id"))
		default:
			keepIdx = append(keepIdx, idx)
			keepAssets = append(keepAssets, lit(res.AssetID))
		}
	}
	concat := concatNode(uid, len(shots), keepIdx, workflow.ListOf(keepAssets...), redoIdx, redoAssets, upIdx, upAssets)
	concat.Deps = []string{"gate"}
	return append(nodes, concat), nil
}

// literalRefs resolves an r2va shot's references from finished draft shots.
func literalRefs(s Shot, results map[int]ShotResult) (images, videos []string) {
	images, videos = []string{}, []string{}
	if s.Mode != "r2va" {
		return
	}
	if !s.Enhance {
		if s.StaticRefVideoAssetID != "" {
			return images, []string{s.StaticRefVideoAssetID}
		}
		return []string{s.StaticRefImageAssetID}, videos
	}
	for _, b := range s.BundleShotIndexes {
		if a := results[b].AssetID; a != "" && len(videos) < MaxReferenceVideoClips {
			videos = append(videos, a)
		}
		if f := results[b].LastFrame; f != "" && len(images) < MaxReferenceImages {
			images = append(images, f)
		}
	}
	if s.StaticRefImageAssetID != "" && len(images) < MaxReferenceImages {
		images = append(images, s.StaticRefImageAssetID)
	}
	if s.StaticRefVideoAssetID != "" && len(videos) < MaxReferenceVideoClips {
		videos = append(videos, s.StaticRefVideoAssetID)
	}
	return images, videos
}

// r2va reference budgets.
const (
	MaxReferenceVideoClips = 3
	MaxReferenceImages     = 5
)
