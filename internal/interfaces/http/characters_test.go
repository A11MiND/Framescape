package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func TestHandleCreateCharacter(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Alice", RefAssetIDs: []string{"asset-1"}, Seed: 42}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", got["name"])
	}
	if got["seed"] != float64(42) {
		t.Errorf("seed = %v, want 42", got["seed"])
	}

	// ref_asset_ids is bounded 1-3 (F3.1).
	rec = doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Bob", RefAssetIDs: []string{}, Seed: 1}, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("0 ref_asset_ids: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	rec = doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Bob", RefAssetIDs: []string{"a", "b", "c", "d"}, Seed: 1}, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("4 ref_asset_ids: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Seed is `binding:"required"` — a request that omits it (zero value)
	// must be rejected rather than silently creating an unseeded character,
	// since an unfixed seed defeats F3.1's whole consistency guarantee.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Bob", RefAssetIDs: []string{"a"}}, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing seed: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleListCharacters(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	doJSON(t, r, http.MethodPost, "/api/v1/characters", createCharacterRequest{Name: "A1", RefAssetIDs: []string{"x"}, Seed: 1}, tokenA)
	doJSON(t, r, http.MethodPost, "/api/v1/characters", createCharacterRequest{Name: "A2", RefAssetIDs: []string{"x"}, Seed: 2}, tokenA)
	doJSON(t, r, http.MethodPost, "/api/v1/characters", createCharacterRequest{Name: "B1", RefAssetIDs: []string{"x"}, Seed: 3}, tokenB)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/characters", nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Characters []map[string]any `json:"characters"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Characters) != 2 {
		t.Fatalf("got %d characters, want exactly 2 (user A's own, not user B's)", len(body.Characters))
	}
}

func TestHandleUpdateCharacter(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, uidA := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Alice", RefAssetIDs: []string{"asset-1"}, Seed: 42}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	// Partial update: only name changes, seed stays untouched.
	newName := "Alicia"
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{Name: &newName}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["name"] != "Alicia" {
		t.Errorf("name = %v, want Alicia", updated["name"])
	}
	if updated["seed"] != float64(42) {
		t.Errorf("seed changed to %v after a name-only PATCH, want unchanged 42", updated["seed"])
	}

	// A no-op resave (identical name) must not false-404 — MySQL reports 0
	// rows *changed*, not rows *matched*, which handleUpdateCharacter's own
	// doc explicitly guards against.
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{Name: &newName}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op resave: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	badRefs := []string{"a", "b", "c", "d"}
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{RefAssetIDs: &badRefs}, tokenA)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("4 ref_asset_ids: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{}, tokenA)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no fields: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Ownership boundary.
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{Name: &newName}, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Errorf("other user's update: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// project_id reassignment.
	project := persistence.Project{BizID: id.New(), UserID: uidA, Name: "p"}
	if err := s.db.Create(&project).Error; err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{ProjectID: &project.BizID}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign project: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["project_id"] != project.BizID {
		t.Errorf("project_id = %v, want %q", updated["project_id"], project.BizID)
	}
}

func TestHandleDeleteCharacter(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/characters",
		createCharacterRequest{Name: "Alice", RefAssetIDs: []string{"asset-1"}, Seed: 42}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/characters/"+bizID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/characters/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/characters/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/characters", nil, tokenA)
	var body struct {
		Characters []map[string]any `json:"characters"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Characters) != 0 {
		t.Errorf("deleted character still listed: %v", body.Characters)
	}
}
