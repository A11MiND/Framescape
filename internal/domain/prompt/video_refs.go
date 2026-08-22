package prompt

import "fmt"

// VideoMaxPromptChars mirrors capability.VideoMaxPromptChars (PRD §3.2) —
// duplicated as a plain constant here for the same reason MaxPromptChars
// is: this package stays dependency-free of internal/domain/capability.
const VideoMaxPromptChars = 7000

// VideoRefs is one video-generation call's reference inputs, independent of
// which executor (minimax.video / .video.regen / .prompt_enhance) ends up
// resolving them to real provider references — same shape as those
// executors' own request configs, just without anything provider-specific.
type VideoRefs struct {
	Ratio                  string
	FirstFrameAssetID      string
	LastFrameAssetID       string
	ReferenceImageAssetIDs []string
	ReferenceVideoAssetIDs []string
	ReferenceAudioAssetIDs []string
}

// VideoRefItem is one reference still waiting to be resolved to a real
// provider file reference — Kind tells the executor which content-item
// type ("image_url"/"video_url"/"audio_url") to build once it has that.
type VideoRefItem struct {
	AssetBizID string
	Role       string // first_frame | last_frame | reference_image | reference_video | reference_audio
	Kind       string // image | video | audio
}

// CompileVideoRefs implements PRD §5.3's steps 3 (refs→role mapping) and 4
// (first_frame/last_frame vs. reference_* mutual exclusion) — moved here
// from minimax/video.go's own buildContent, which decided this ad-hoc at
// the executor layer even though the compiler package already existed and
// the PRD always specced these as compiler responsibilities (this
// package's own doc used to note steps 3/4 as "not implemented yet, once
// video generation exists" — video generation now exists). Resolving an
// asset ID to an actual mm_file:// reference stays the executor's job
// (that's real provider I/O, not a domain decision); this function only
// decides mode, the ratio to send, and the ordered list of refs to resolve.
func CompileVideoRefs(r VideoRefs) (mode, ratio string, items []VideoRefItem, err error) {
	hasFirstLast := r.FirstFrameAssetID != "" || r.LastFrameAssetID != ""
	hasRef := len(r.ReferenceImageAssetIDs) > 0 || len(r.ReferenceVideoAssetIDs) > 0 || len(r.ReferenceAudioAssetIDs) > 0
	if hasFirstLast && hasRef {
		// §3.2's "🔴 最重要的一条硬约束": first_frame/last_frame and any
		// reference_* role can never coexist in one request.
		return "", "", nil, fmt.Errorf("mutual_exclusion: first_frame/last_frame cannot combine with reference_image/reference_video/reference_audio")
	}

	mode = "t2va"
	switch {
	case hasFirstLast:
		mode = "i2va"
	case hasRef:
		mode = "r2va"
	}

	ratio = r.Ratio
	switch mode {
	case "t2va":
		if ratio == "" || ratio == "adaptive" {
			return "", "", nil, fmt.Errorf("bad_params: ratio is required and must not be adaptive for t2va")
		}
	case "i2va":
		ratio = "adaptive" // §3.2: "传别的会被忽略" — normalize rather than silently send a value MiniMax ignores
	case "r2va":
		if ratio == "" {
			ratio = "adaptive"
		}
	}

	add := func(assetID, role, kind string) {
		if assetID != "" {
			items = append(items, VideoRefItem{AssetBizID: assetID, Role: role, Kind: kind})
		}
	}
	add(r.FirstFrameAssetID, "first_frame", "image")
	add(r.LastFrameAssetID, "last_frame", "image")
	for _, id := range r.ReferenceImageAssetIDs {
		add(id, "reference_image", "image")
	}
	for _, id := range r.ReferenceVideoAssetIDs {
		add(id, "reference_video", "video")
	}
	for _, id := range r.ReferenceAudioAssetIDs {
		add(id, "reference_audio", "audio")
	}
	return mode, ratio, items, nil
}

// TruncateVideoPrompt applies §3.2's hard prompt-length cap — the same
// truncate-by-rune-count backstop Compile's step 5 already does for image
// prompts, just at video's higher limit.
func TruncateVideoPrompt(text string) string {
	if r := []rune(text); len(r) > VideoMaxPromptChars {
		return string(r[:VideoMaxPromptChars])
	}
	return text
}
