package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/persistence"
)

func TestAssetsPagingBatchAndTrash(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	otherToken, other := registerAndFund(t, s, 0)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, seedAsset(t, s, uid, "image", "").BizID)
	}
	foreign := seedAsset(t, s, other, "image", "").BizID

	type page struct {
		Assets []struct {
			BizID     string `json:"biz_id"`
			ProjectID string `json:"project_id"`
		} `json:"assets"`
		Next  string `json:"next_cursor"`
		Total int    `json:"total"`
	}
	get := func(path string) page {
		t.Helper()
		rec := doJSON(t, r, http.MethodGet, path, nil, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		var p page
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p
	}

	// Newest first, two per page, then the rest.
	p1 := get("/api/v1/assets?limit=2")
	if len(p1.Assets) != 2 || p1.Assets[0].BizID != ids[4] || p1.Next == "" {
		t.Fatalf("first page: %+v", p1)
	}
	p2 := get("/api/v1/assets?limit=2&cursor=" + p1.Next)
	p3 := get("/api/v1/assets?limit=2&cursor=" + p2.Next)
	if len(p2.Assets) != 2 || len(p3.Assets) != 1 || p3.Next != "" || p3.Assets[0].BizID != ids[0] {
		t.Fatalf("later pages: %+v %+v", p2, p3)
	}

	batch := func(tok string, body map[string]any) (int, int) {
		t.Helper()
		rec := doJSON(t, r, http.MethodPost, "/api/v1/assets/batch", body, tok)
		var res struct {
			Affected int `json:"affected"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res.Affected
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/projects", createProjectRequest{Name: "Library"}, token)
	var project struct {
		BizID string `json:"biz_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	if code, n := batch(token, map[string]any{"op": "move", "ids": []string{ids[0], ids[1], foreign}, "project_id": project.BizID}); code != 200 || n != 2 {
		t.Fatalf("move: %d %d (another user's asset must not move)", code, n)
	}
	if got := get("/api/v1/assets?project_id=" + project.BizID); len(got.Assets) != 2 {
		t.Fatalf("project filter after move: %+v", got)
	}
	if code, _ := batch(token, map[string]any{"op": "move", "ids": ids[:1], "project_id": "nope"}); code != http.StatusNotFound {
		t.Fatalf("move to an unknown project: %d", code)
	}
	if code, n := batch(token, map[string]any{"op": "move", "ids": ids[:1], "project_id": ""}); code != 200 || n != 1 {
		t.Fatalf("clear project: %d %d", code, n)
	}

	if code, n := batch(token, map[string]any{"op": "delete", "ids": ids[2:]}); code != 200 || n != 3 {
		t.Fatalf("delete: %d %d", code, n)
	}
	if trash := get("/api/v1/assets/trash"); trash.Total != 3 || len(trash.Assets) != 3 {
		t.Fatalf("trash after delete: %+v", trash)
	}
	if code, n := batch(token, map[string]any{"op": "restore", "ids": ids[2:3]}); code != 200 || n != 1 {
		t.Fatalf("restore: %d %d", code, n)
	}
	if trash := get("/api/v1/assets/trash"); trash.Total != 2 {
		t.Fatalf("trash after restore: %+v", trash)
	}
	if code, n := batch(otherToken, map[string]any{"op": "restore", "ids": ids[3:]}); code != 200 || n != 0 {
		t.Fatalf("another user restored assets: %d %d", code, n)
	}
	if code, _ := batch(token, map[string]any{"op": "purge", "ids": ids[:1]}); code != http.StatusBadRequest {
		t.Fatalf("unknown op: %d", code)
	}

	var deleted int64
	s.db.Model(&persistence.Asset{}).Where("user_id = ? AND deleted_at IS NOT NULL", uid).Count(&deleted)
	if deleted != 2 {
		t.Fatalf("deleted rows = %d", deleted)
	}
}

func TestCapabilitiesPublishUploadLimits(t *testing.T) {
	s := newTestServer(t)
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/v1/capabilities", nil, "")
	var caps struct {
		Uploads map[string]struct {
			MaxBytes int64 `json:"max_bytes"`
		} `json:"uploads"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &caps)
	if caps.Uploads["image"].MaxBytes != 20<<20 || caps.Uploads["video"].MaxBytes != 512<<20 || caps.Uploads["audio"].MaxBytes != 50<<20 {
		t.Fatalf("uploads = %+v", caps.Uploads)
	}
}
