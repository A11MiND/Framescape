package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/domain/capability"
)

// TestHandleGetCapabilities checks against capability's own exported
// constants rather than hardcoded numbers, so this test doesn't silently
// drift from the values it's supposed to be guarding against frontend
// duplication (capabilities.go's own doc on why this endpoint exists).
func TestHandleGetCapabilities(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()

	// Public — no Authorization header, matches "loads before login".
	rec := doJSON(t, r, http.MethodGet, "/api/v1/capabilities", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Image struct {
			MaxN           int `json:"max_n"`
			MaxPromptChars int `json:"max_prompt_chars"`
		} `json:"image"`
		Video struct {
			DurationMin    int      `json:"duration_min"`
			DurationMax    int      `json:"duration_max"`
			MaxPromptChars int      `json:"max_prompt_chars"`
			Resolutions    []string `json:"resolutions"`
			Ratios         []string `json:"ratios"`
		} `json:"video"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Image.MaxN != capability.ImageMaxN {
		t.Errorf("image.max_n = %d, want %d", body.Image.MaxN, capability.ImageMaxN)
	}
	if body.Image.MaxPromptChars != capability.ImageMaxPromptChars {
		t.Errorf("image.max_prompt_chars = %d, want %d", body.Image.MaxPromptChars, capability.ImageMaxPromptChars)
	}
	if body.Video.DurationMin != capability.VideoDurationMin || body.Video.DurationMax != capability.VideoDurationMax {
		t.Errorf("video duration = %d..%d, want %d..%d", body.Video.DurationMin, body.Video.DurationMax, capability.VideoDurationMin, capability.VideoDurationMax)
	}
	if len(body.Video.Resolutions) != len(capability.VideoResolutions) {
		t.Errorf("video.resolutions = %v, want %v", body.Video.Resolutions, capability.VideoResolutions)
	}
	if len(body.Video.Ratios) != len(capability.VideoRatios) {
		t.Errorf("video.ratios = %v, want %v", body.Video.Ratios, capability.VideoRatios)
	}
}
