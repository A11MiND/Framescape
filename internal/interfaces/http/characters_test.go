package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func seedOf(n int64) *int64 { return &n }

func createCharacter(t *testing.T, s *Server, token string, req createCharacterRequest) map[string]any {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/characters", req, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create character: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestHandleCreateCharacter(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	_, other := registerAndFund(t, s, 0)
	img := seedAsset(t, s, uid, "image", "").BizID

	got := createCharacter(t, s, token, createCharacterRequest{Name: "  Alice  ", RefAssetIDs: []string{img}, Seed: seedOf(42)})
	if got["name"] != "Alice" || got["seed"] != float64(42) {
		t.Errorf("got name %v seed %v, want trimmed Alice and 42", got["name"], got["seed"])
	}

	// Without a seed the server picks one and keeps it fixed for the character.
	got = createCharacter(t, s, token, createCharacterRequest{Name: "Bob", RefAssetIDs: []string{img}})
	if seed, _ := got["seed"].(float64); seed <= 0 {
		t.Errorf("seed = %v, want a stored positive seed", got["seed"])
	}

	foreign := seedAsset(t, s, other, "image", "").BizID
	video := seedAsset(t, s, uid, "video", "").BizID
	cases := []struct {
		name   string
		req    createCharacterRequest
		status int
		code   string
	}{
		{"no references", createCharacterRequest{Name: "C", RefAssetIDs: []string{}}, http.StatusBadRequest, "bad_request"},
		{"four references", createCharacterRequest{Name: "C", RefAssetIDs: []string{img, img, img, img}}, http.StatusBadRequest, "bad_request"},
		{"blank name", createCharacterRequest{Name: "   ", RefAssetIDs: []string{img}}, http.StatusUnprocessableEntity, "text_length"},
		{"another user's image", createCharacterRequest{Name: "C", RefAssetIDs: []string{foreign}}, http.StatusUnprocessableEntity, "reference_unavailable"},
		{"a video as a reference", createCharacterRequest{Name: "C", RefAssetIDs: []string{video}}, http.StatusUnprocessableEntity, "reference_unavailable"},
		{"an unknown id", createCharacterRequest{Name: "C", RefAssetIDs: []string{id.New()}}, http.StatusUnprocessableEntity, "reference_unavailable"},
		{"the same image twice", createCharacterRequest{Name: "C", RefAssetIDs: []string{img, img}}, http.StatusBadRequest, "bad_request"},
	}
	for _, c := range cases {
		rec := doJSON(t, r, http.MethodPost, "/api/v1/characters", c.req, token)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.status || body.Code != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, rec.Code, body.Code, c.status, c.code)
		}
	}
}

func TestHandleListCharacters(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, uidA := registerAndFund(t, s, 0)
	tokenB, uidB := registerAndFund(t, s, 0)
	a, b := seedAsset(t, s, uidA, "image", "").BizID, seedAsset(t, s, uidB, "image", "").BizID

	createCharacter(t, s, tokenA, createCharacterRequest{Name: "A1", RefAssetIDs: []string{a}, Seed: seedOf(1)})
	createCharacter(t, s, tokenA, createCharacterRequest{Name: "A2", RefAssetIDs: []string{a}, Seed: seedOf(2)})
	createCharacter(t, s, tokenB, createCharacterRequest{Name: "B1", RefAssetIDs: []string{b}, Seed: seedOf(3)})

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
	tokenB, uidB := registerAndFund(t, s, 0)
	img := seedAsset(t, s, uidA, "image", "").BizID

	bizID := createCharacter(t, s, tokenA, createCharacterRequest{Name: "Alice", RefAssetIDs: []string{img}, Seed: seedOf(42)})["biz_id"].(string)

	// Partial update: only name changes, seed stays untouched.
	newName := "Alicia"
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{Name: &newName}, tokenA)
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

	// A no-op resave must not false-404: MySQL reports rows changed, not matched.
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{Name: &newName}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op resave: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	badRefs := []string{img, img, img, img}
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{RefAssetIDs: &badRefs}, tokenA)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("4 ref_asset_ids: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	foreign := []string{seedAsset(t, s, uidB, "image", "").BizID}
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/characters/"+bizID, updateCharacterRequest{RefAssetIDs: &foreign}, tokenA)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("another user's image: status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
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
	tokenA, uidA := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)
	img := seedAsset(t, s, uidA, "image", "").BizID

	bizID := createCharacter(t, s, tokenA, createCharacterRequest{Name: "Alice", RefAssetIDs: []string{img}, Seed: seedOf(42)})["biz_id"].(string)

	rec := doJSON(t, r, http.MethodDelete, "/api/v1/characters/"+bizID, nil, tokenB)
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
