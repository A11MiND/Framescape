package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func TestAvatarOwnershipValidationAndRemoval(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	_, otherUID := registerAndFund(t, s, 0)
	makeAsset := func(owner uint64, mime string, size int64) persistence.Asset {
		a := persistence.Asset{BizID: id.New(), UserID: owner, Type: "image", Source: "upload", Mime: mime, SizeBytes: size, PublicURL: "https://example.test/avatar.png"}
		if err := s.db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		return a
	}
	owned := makeAsset(uid, "image/png", 1024)
	foreign := makeAsset(otherUID, "image/png", 1024)
	oversized := makeAsset(uid, "image/png", 5*1024*1024+1)
	svg := makeAsset(uid, "image/svg+xml", 1024)
	for _, tc := range []struct {
		name   string
		asset  string
		status int
	}{
		{"own image", owned.BizID, http.StatusOK},
		{"other user", foreign.BizID, http.StatusNotFound},
		{"too large", oversized.BizID, http.StatusBadRequest},
		{"unsupported format", svg.BizID, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, r, http.MethodPatch, "/api/v1/me/avatar", map[string]any{"asset_id": tc.asset}, token)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
	assertURL := func(want string) {
		t.Helper()
		rec := doJSON(t, r, http.MethodGet, "/api/v1/me", nil, token)
		var me struct {
			AvatarURL string `json:"avatar_url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || rec.Code != http.StatusOK || me.AvatarURL != want {
			t.Fatalf("avatar URL want %q: %s (%v)", want, rec.Body.String(), err)
		}
	}
	assertURL(owned.PublicURL)
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/me/avatar", map[string]any{"asset_id": nil}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	assertURL("")
}
