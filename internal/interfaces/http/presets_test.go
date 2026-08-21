package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func hasPresetBizID(presets []map[string]any, bizID string) (row map[string]any, ok bool) {
	for _, p := range presets {
		if p["biz_id"] == bizID {
			return p, true
		}
	}
	return nil, false
}

// TestHandleCreateAndListPresets runs against a dev DB that already carries
// real seeded system presets (migration 00003/00008/00009), so it checks
// presence/absence of specific rows rather than an exact total count.
func TestHandleCreateAndListPresets(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	// A seeded system preset (owner_user_id NULL) must be visible to anyone.
	system := persistence.Preset{BizID: id.New(), Category: "style", Name: "httptest-system-preset-fixture", PromptFragment: "x"}
	if err := s.db.Create(&system).Error; err != nil {
		t.Fatalf("seed system preset: %v", err)
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/presets",
		createPresetRequest{Name: "My Preset", PromptFragment: "cinematic lighting"}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["category"] != "style" {
		t.Errorf("category defaulted to %v, want style", created["category"])
	}
	if created["mine"] != true {
		t.Errorf("mine = %v, want true for the caller's own preset", created["mine"])
	}
	mineBizID := created["biz_id"].(string)

	// A's own list: the fixture system preset plus A's own save, both present.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/presets", nil, tokenA)
	var body struct {
		Presets []map[string]any `json:"presets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := hasPresetBizID(body.Presets, system.BizID); !ok {
		t.Errorf("system preset %q missing from user A's list", system.BizID)
	}
	if _, ok := hasPresetBizID(body.Presets, mineBizID); !ok {
		t.Errorf("user A's own preset %q missing from their own list", mineBizID)
	}

	// B's list: the same system preset, but never A's own save.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/presets", nil, tokenB)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	sysRow, ok := hasPresetBizID(body.Presets, system.BizID)
	if !ok {
		t.Fatalf("system preset %q missing from user B's list", system.BizID)
	}
	if sysRow["mine"] != false {
		t.Errorf("system preset shown as mine=%v for a non-owner, want false", sysRow["mine"])
	}
	if _, ok := hasPresetBizID(body.Presets, mineBizID); ok {
		t.Errorf("user A's own preset %q leaked into user B's list", mineBizID)
	}
}

func TestHandleDeletePreset(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	system := persistence.Preset{BizID: id.New(), Category: "style", Name: "httptest-system-preset-fixture", PromptFragment: "x"}
	if err := s.db.Create(&system).Error; err != nil {
		t.Fatalf("seed system preset: %v", err)
	}
	// A seeded system preset must never be deletable through this endpoint,
	// no matter whose token calls it — owner_user_id IS NULL never matches
	// "owner_user_id = ?" for any real caller.
	rec := doJSON(t, r, http.MethodDelete, "/api/v1/presets/"+system.BizID, nil, tokenA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete system preset: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/presets",
		createPresetRequest{Name: "Mine", PromptFragment: "x"}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	// Ownership boundary: B can't delete A's own preset.
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/presets/"+bizID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/presets/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/presets/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
