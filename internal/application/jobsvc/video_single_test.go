package jobsvc

import (
	"fmt"
	"testing"
)

func TestVideoSingleRefsCheckedBeforeReserving(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		code string
	}{
		{"text with a ratio", Spec{Text: "x", Ratio: "16:9"}, ""},
		{"text without a ratio", Spec{Text: "x"}, "video_ratio_required"},
		{"text with an unknown ratio", Spec{Text: "x", Ratio: "2:1"}, "ratio_invalid"},
		{"frames follow the frame", Spec{Text: "x", FirstFrameAssetID: "a"}, ""},
		{"references follow the reference", Spec{Text: "x", ReferenceVideoAssetIDs: []string{"v"}}, ""},
		{"a bound character is a reference", Spec{Text: "x", Characters: []CharacterSlot{{Slot: "A", CharacterID: "c"}}}, ""},
		{"frames with references", Spec{Text: "x", FirstFrameAssetID: "a", ReferenceImageAssetIDs: []string{"b"}}, "video_refs_exclusive"},
		{"last frame with audio", Spec{Text: "x", LastFrameAssetID: "a", ReferenceAudioAssetIDs: []string{"b"}}, "video_refs_exclusive"},
		{"three reference videos", Spec{Text: "x", ReferenceVideoAssetIDs: []string{"a", "b", "c"}}, ""},
		{"four reference videos", Spec{Text: "x", ReferenceVideoAssetIDs: []string{"a", "b", "c", "d"}}, "reference_video_budget"},
		{"nine reference images", Spec{Text: "x", ReferenceImageAssetIDs: ids(9)}, ""},
		{"ten reference images", Spec{Text: "x", ReferenceImageAssetIDs: ids(10)}, "references_too_many"},
		{"three reference audio files", Spec{Text: "x", ReferenceAudioAssetIDs: ids(3)}, ""},
		{"four reference audio files", Spec{Text: "x", ReferenceAudioAssetIDs: ids(4)}, "reference_audios_too_many"},
	}
	for _, c := range cases {
		_, err := EstimateCredits("video.single", c.spec)
		if c.code == "" && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.code != "" && !hasCode(err, c.code) {
			t.Errorf("%s: got %v, want %s", c.name, err, c.code)
		}
	}
}

func TestVideoSequenceCheckedBeforeReserving(t *testing.T) {
	shots := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "a tram at dusk"
		}
		return out
	}
	cases := []struct {
		name string
		spec Spec
		code string
	}{
		{"within the limit", Spec{Shots: shots(12), Ratio: "9:16"}, ""},
		{"one shot over", Spec{Shots: shots(13)}, "shots_too_many"},
		{"no shots", Spec{}, "shots_required"},
		{"unknown ratio", Spec{Shots: shots(2), Ratio: "2:1"}, "ratio_invalid"},
		{"unknown reference mode", Spec{Shots: shots(2), ReferenceSelectionMode: "nearest"}, "bad_request"},
		{"forward override", Spec{Shots: shots(3), ShotReferenceOverrides: []int{0, 3, 0}}, "shot_refs_invalid"},
	}
	for _, c := range cases {
		_, err := EstimateCredits("video.sequence", c.spec)
		if c.code == "" && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.code != "" && !hasCode(err, c.code) {
			t.Errorf("%s: got %v, want %s", c.name, err, c.code)
		}
	}
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("asset-%d", i)
	}
	return out
}
