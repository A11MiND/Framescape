package jobsvc

import "testing"

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
