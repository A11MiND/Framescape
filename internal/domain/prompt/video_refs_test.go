package prompt

import (
	"strings"
	"testing"
)

func TestCompileVideoRefs_MutualExclusion(t *testing.T) {
	_, _, _, err := CompileVideoRefs(VideoRefs{
		FirstFrameAssetID:      "a",
		ReferenceImageAssetIDs: []string{"b"},
	})
	if err == nil || !strings.Contains(err.Error(), "mutual_exclusion") {
		t.Fatalf("err = %v, want a mutual_exclusion error", err)
	}
}

func TestCompileVideoRefs_T2VARequiresRealRatio(t *testing.T) {
	for _, ratio := range []string{"", "adaptive"} {
		_, _, _, err := CompileVideoRefs(VideoRefs{Ratio: ratio})
		if err == nil || !strings.Contains(err.Error(), "bad_params") {
			t.Errorf("ratio=%q: err = %v, want a bad_params error", ratio, err)
		}
	}

	mode, ratio, items, err := CompileVideoRefs(VideoRefs{Ratio: "16:9"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if mode != "t2va" || ratio != "16:9" {
		t.Errorf("mode/ratio = %q/%q, want t2va/16:9", mode, ratio)
	}
	if len(items) != 0 {
		t.Errorf("items = %v, want none for a pure text-to-video request", items)
	}
}

func TestCompileVideoRefs_I2VAForcesAdaptiveRatio(t *testing.T) {
	mode, ratio, items, err := CompileVideoRefs(VideoRefs{
		Ratio: "16:9", FirstFrameAssetID: "first", LastFrameAssetID: "last",
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if mode != "i2va" {
		t.Errorf("mode = %q, want i2va", mode)
	}
	if ratio != "adaptive" {
		t.Errorf("ratio = %q, want forced to adaptive even though 16:9 was supplied", ratio)
	}
	want := []VideoRefItem{
		{AssetBizID: "first", Role: "first_frame", Kind: "image"},
		{AssetBizID: "last", Role: "last_frame", Kind: "image"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v, want %+v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestCompileVideoRefs_I2VAFirstFrameOnly(t *testing.T) {
	mode, _, items, err := CompileVideoRefs(VideoRefs{Ratio: "16:9", FirstFrameAssetID: "first"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if mode != "i2va" {
		t.Errorf("mode = %q, want i2va", mode)
	}
	if len(items) != 1 || items[0].Role != "first_frame" {
		t.Errorf("items = %+v, want exactly one first_frame item", items)
	}
}

func TestCompileVideoRefs_R2VADefaultsRatioToAdaptive(t *testing.T) {
	mode, ratio, items, err := CompileVideoRefs(VideoRefs{
		ReferenceImageAssetIDs: []string{"img1", "img2"},
		ReferenceVideoAssetIDs: []string{"vid1"},
		ReferenceAudioAssetIDs: []string{"aud1"},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if mode != "r2va" || ratio != "adaptive" {
		t.Errorf("mode/ratio = %q/%q, want r2va/adaptive", mode, ratio)
	}
	want := []VideoRefItem{
		{AssetBizID: "img1", Role: "reference_image", Kind: "image"},
		{AssetBizID: "img2", Role: "reference_image", Kind: "image"},
		{AssetBizID: "vid1", Role: "reference_video", Kind: "video"},
		{AssetBizID: "aud1", Role: "reference_audio", Kind: "audio"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v, want %+v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestCompileVideoRefs_R2VAExplicitRatioPreserved(t *testing.T) {
	_, ratio, _, err := CompileVideoRefs(VideoRefs{Ratio: "1:1", ReferenceImageAssetIDs: []string{"img"}})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ratio != "1:1" {
		t.Errorf("ratio = %q, want the caller's explicit 1:1 preserved (only the default is adaptive)", ratio)
	}
}

func TestTruncateVideoPrompt(t *testing.T) {
	short := "a short prompt"
	if got := TruncateVideoPrompt(short); got != short {
		t.Errorf("short prompt was modified: %q", got)
	}
	long := strings.Repeat("x", 8000)
	got := TruncateVideoPrompt(long)
	if n := len([]rune(got)); n != VideoMaxPromptChars {
		t.Errorf("truncated length = %d, want %d", n, VideoMaxPromptChars)
	}
}
