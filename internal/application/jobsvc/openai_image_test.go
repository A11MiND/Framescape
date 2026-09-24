package jobsvc

import (
	"testing"

	"aigc-platform/internal/application/creditsvc"
)

func openAIEnv(t *testing.T) {
	t.Setenv("OPENAI_IMAGE_RESERVE_USD", "0.5")
	t.Setenv("OPENAI_IMAGE_RESERVE_PER_REF_USD", "0.1")
	t.Setenv("OPENAI_USD_TO_CNY", "7")
	t.Setenv("OPENAI_IMAGE_SIZES", "1024x1024,1536x1024,1024x1536")
	t.Setenv("OPENAI_IMAGE_QUALITIES", "low,medium,high")
	t.Setenv("OPENAI_IMAGE_MAX_N", "4")
}

func TestOpenAIProviderScope(t *testing.T) {
	openAIEnv(t)
	for _, tc := range []struct {
		name     string
		workflow string
		spec     Spec
		ok       bool
	}{
		{"single", "image.single", Spec{Text: "x", ImageProvider: "openai"}, true},
		{"sequence", "image.sequence", Spec{Shots: []string{"a", "b"}, ImageProvider: "openai"}, true},
		{"comic", "image.comic4", directSpec(), true},
		{"classic comic", "image.comic4", Spec{Text: "x", ImageProvider: "openai"}, false},
		{"video", "video.single", Spec{Text: "x", ImageProvider: "openai"}, false},
		{"too many", "image.single", Spec{Text: "x", ImageProvider: "openai", N: 5}, false},
		{"unknown size", "image.single", Spec{Text: "x", ImageProvider: "openai", ImageSize: "4096x4096"}, false},
		{"unknown quality", "image.single", Spec{Text: "x", ImageProvider: "openai", ImageQuality: "ultra"}, false},
		{"options without openai", "image.single", Spec{Text: "x", ImageQuality: "low"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EstimateCredits(tc.workflow, tc.spec); (err == nil) != tc.ok {
				t.Fatalf("ok=%v err=%v", tc.ok, err)
			}
		})
	}
}

func TestOpenAIOptionDefaults(t *testing.T) {
	openAIEnv(t)
	for ratio, size := range map[string]string{"": "1024x1024", "1:1": "1024x1024", "16:9": "1536x1024", "9:16": "1024x1536"} {
		o, err := openAIImageOptions("image.single", Spec{ImageProvider: "openai", AspectRatio: ratio})
		if err != nil || o.Size != size || o.Quality != "high" || o.N != 1 {
			t.Fatalf("%q -> %+v %v", ratio, o, err)
		}
	}
	o, _ := openAIImageOptions("image.sequence", Spec{ImageProvider: "openai", N: 3})
	if o.N != 1 {
		t.Fatal("a sequence shot is always one image")
	}
}

func TestOpenAIEstimates(t *testing.T) {
	openAIEnv(t)
	yuan := func(usd float64) int { return creditsvc.CreditsFromYuan(usd * 7) }

	items, total, err := EstimateBreakdown("image.single", Spec{Text: "x", ImageProvider: "openai", N: 3, ImageQuality: "medium", SourceImageAssetID: "a"})
	if want := yuan(3*0.5*0.5 + 0.1); err != nil || total != want || items[0].Count != 3 || items[0].Basis != BasisReservation {
		t.Fatalf("single: %v %d want %d %v", items, total, want, err)
	}
	_, low, _ := EstimateBreakdown("image.single", Spec{Text: "x", ImageProvider: "openai", ImageQuality: "low"})
	_, high, _ := EstimateBreakdown("image.single", Spec{Text: "x", ImageProvider: "openai"})
	if low >= high {
		t.Fatalf("low %d must reserve less than high %d", low, high)
	}
	_, chars, _ := EstimateBreakdown("image.single", Spec{Text: "x", ImageProvider: "openai", Characters: []CharacterSlot{{Slot: "a"}, {Slot: "b"}}})
	if chars != yuan(0.5+6*0.1) {
		t.Fatalf("characters reserve for their references: %d", chars)
	}

	// Continuity: shots 2..3 reference the previous shot; each shot is its own call.
	_, seq, err := EstimateBreakdown("image.sequence", Spec{Shots: []string{"a", "b", "c"}, ImageProvider: "openai", ImageSequenceMode: "continuity"})
	if want := yuan(0.5) + 2*yuan(0.6); err != nil || seq != want {
		t.Fatalf("sequence %d want %d %v", seq, want, err)
	}
}
