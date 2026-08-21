package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleCreateAndListProjects(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "Shoot 1"}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["name"] != "Shoot 1" {
		t.Errorf("name = %v, want %q", created["name"], "Shoot 1")
	}

	doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "Shoot 2"}, tokenA)
	doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "B's project"}, tokenB)

	rec = doJSON(t, r, http.MethodGet, "/api/v1/projects", nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Projects []map[string]any `json:"projects"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Projects) != 2 {
		t.Fatalf("got %d projects, want exactly 2 (user A's own, not user B's)", len(body.Projects))
	}
}

func TestHandleUpdateProject(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "Shoot 1"}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	newName := "Shoot 1 (final)"
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/projects/"+bizID, updateProjectRequest{Name: &newName}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["name"] != newName {
		t.Errorf("name = %v, want %q", updated["name"], newName)
	}

	// No-op resave must not false-404 (MySQL reports rows changed, not
	// matched — same reasoning as handleUpdateCharacter's identical guard).
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/projects/"+bizID, updateProjectRequest{Name: &newName}, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op resave: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/projects/"+bizID, updateProjectRequest{}, tokenA)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no fields: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Ownership boundary.
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/projects/"+bizID, updateProjectRequest{Name: &newName}, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Errorf("other user's update: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleDeleteProject(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "Shoot 1"}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/projects/"+bizID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/projects/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/projects/"+bizID, nil, tokenA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/projects", nil, tokenA)
	var body struct {
		Projects []map[string]any `json:"projects"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Projects) != 0 {
		t.Errorf("deleted project still listed: %v", body.Projects)
	}
}
