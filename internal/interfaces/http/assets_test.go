package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// seedAsset inserts an asset row directly (bypassing the real upload flow,
// which needs a live MinIO — out of scope for this package's tests, see
// TestHandleAssetUploadURLNotConfigured/TestHandleCompleteAssetNotConfigured
// for what IS covered of that path). publicURL empty is fine for every test
// that doesn't touch handleBatchDownloadAssets.
func seedAsset(t *testing.T, s *Server, uid uint64, typ, publicURL string) persistence.Asset {
	t.Helper()
	a := persistence.Asset{
		BizID:     id.New(), // assets.biz_id is CHAR(26) — must be a real ULID shape, not an arbitrary string
		UserID:    uid,
		Type:      typ,
		Source:    "upload",
		PublicURL: publicURL,
	}
	if err := s.db.Create(&a).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	return a
}

func TestHandleListAssets(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	tokenA, uidA := registerAndFund(t, s, 0)
	_, uidB := registerAndFund(t, s, 0)

	seedAsset(t, s, uidA, "image", "")
	seedAsset(t, s, uidA, "video", "")
	seedAsset(t, s, uidB, "image", "")

	rec := doJSON(t, r, http.MethodGet, "/api/v1/assets", nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Assets []map[string]any `json:"assets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Assets) != 2 {
		t.Fatalf("got %d assets, want exactly 2 (user A's own, not user B's)", len(body.Assets))
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets?type=video", nil, tokenA)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Assets) != 1 || body.Assets[0]["type"] != "video" {
		t.Errorf("type=video filter: got %v, want exactly 1 video asset", body.Assets)
	}
}

func TestHandleGetAsset(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	tokenA, uidA := registerAndFund(t, s, 0)
	tokenB, _ := registerAndFund(t, s, 0)
	a := seedAsset(t, s, uidA, "image", "")

	rec := doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Ownership boundary.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// handleGetAsset's own fixed bug: a soft-deleted asset must 404, not
	// stay fully viewable through its direct URL.
	if err := s.db.Model(&a).Update("deleted_at", "2020-01-01 00:00:00").Error; err != nil {
		t.Fatalf("soft-delete seed: %v", err)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, tokenA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("soft-deleted asset: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleUpdateAssetProject(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	tokenA, uidA := registerAndFund(t, s, 0)
	a := seedAsset(t, s, uidA, "image", "")
	project := persistence.Project{BizID: id.New(), UserID: uidA, Name: "test project"}
	if err := s.db.Create(&project).Error; err != nil {
		t.Fatalf("seed project: %v", err)
	}

	rec := doJSON(t, r, http.MethodPatch, "/api/v1/assets/"+a.BizID, updateAssetRequest{ProjectID: &project.BizID}, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign project: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, tokenA)
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["project_id"] != project.BizID {
		t.Errorf("project_id = %v, want %q", got["project_id"], project.BizID)
	}

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/assets/"+a.BizID, updateAssetRequest{Clear: true}, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear project: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, tokenA)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["project_id"] != "" {
		t.Errorf("project_id after clear = %v, want empty", got["project_id"])
	}

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/assets/"+a.BizID, updateAssetRequest{}, tokenA)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no fields: status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestHandleUpdateAssetPublish covers the community publish/unpublish path
// this session fixed (204 vs 200 status code, streak advancing on publish)
// end to end through the real HTTP layer.
func TestHandleUpdateAssetPublish(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	a := seedAsset(t, s, uid, "image", "")

	isPublic := true
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/assets/"+a.BizID, updateAssetRequest{IsPublic: &isPublic}, token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("publish: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+a.BizID, nil, token)
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["is_public"] != true {
		t.Errorf("is_public = %v, want true", got["is_public"])
	}

	// Publishing must advance the daily streak (communitysvc.RecordPublish).
	rec = doJSON(t, r, http.MethodGet, "/api/v1/community/streak", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get streak: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var streak struct {
		CurrentStreak int `json:"current_streak"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &streak)
	if streak.CurrentStreak != 1 {
		t.Errorf("current_streak = %d, want 1 after the first publish", streak.CurrentStreak)
	}

	// Published assets are visible on the public community feed...
	rec = doJSON(t, r, http.MethodGet, "/api/v1/community/feed", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("community feed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var feed struct {
		Assets []map[string]any `json:"assets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &feed)
	found := false
	for _, fa := range feed.Assets {
		if fa["biz_id"] == a.BizID {
			found = true
		}
	}
	if !found {
		t.Errorf("published asset %q not present in community feed", a.BizID)
	}

	// ...and disappear again on unpublish.
	isPublic = false
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/assets/"+a.BizID, updateAssetRequest{IsPublic: &isPublic}, token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/community/feed", nil, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &feed)
	for _, fa := range feed.Assets {
		if fa["biz_id"] == a.BizID {
			t.Errorf("unpublished asset %q still present in community feed", a.BizID)
		}
	}
}

func TestAssetTrashLifecycle(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	a := seedAsset(t, s, uid, "image", "")

	rec := doJSON(t, r, http.MethodDelete, "/api/v1/assets/"+a.BizID, nil, token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	// Repeat delete is a no-op 404, not an error (handleDeleteAsset's own doc).
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/assets/"+a.BizID, nil, token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// Deleted assets drop out of the normal list...
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets", nil, token)
	var list struct {
		Assets []map[string]any `json:"assets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Assets) != 0 {
		t.Errorf("deleted asset still in /assets: %v", list.Assets)
	}

	// ...and appear in trash instead.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/trash", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list trash: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var trash struct {
		Assets []map[string]any `json:"assets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &trash)
	if len(trash.Assets) != 1 || trash.Assets[0]["biz_id"] != a.BizID {
		t.Fatalf("trash = %v, want exactly the deleted asset", trash.Assets)
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/assets/"+a.BizID+"/restore", nil, token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("restore: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets", nil, token)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Assets) != 1 {
		t.Errorf("restored asset not back in /assets: %v", list.Assets)
	}
}

func TestHandleEmptyTrash(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)
	a1 := seedAsset(t, s, uid, "image", "")
	a2 := seedAsset(t, s, uid, "image", "")
	keep := seedAsset(t, s, uid, "image", "") // never deleted — must survive

	for _, a := range []persistence.Asset{a1, a2} {
		rec := doJSON(t, r, http.MethodDelete, "/api/v1/assets/"+a.BizID, nil, token)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("seed delete %s: status = %d", a.BizID, rec.Code)
		}
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/assets/trash/empty", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty trash: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Purged int `json:"purged"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Purged != 2 {
		t.Errorf("purged = %d, want 2", body.Purged)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/trash", nil, token)
	var trash struct {
		Assets []map[string]any `json:"assets"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &trash)
	if len(trash.Assets) != 0 {
		t.Errorf("trash not empty after empty-trash: %v", trash.Assets)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/assets/"+keep.BizID, nil, token)
	if rec.Code != http.StatusOK {
		t.Errorf("never-deleted asset was also purged: status = %d", rec.Code)
	}
}

// TestHandleBatchDownloadAssets serves fake asset bytes from a local
// httptest.Server (standing in for the real MinIO public URL) so this stays
// a self-contained test with no external network dependency.
func TestHandleBatchDownloadAssets(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 0)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("fake-image-bytes-" + req.URL.Path))
	}))
	defer fileServer.Close()

	a1 := seedAsset(t, s, uid, "image", fileServer.URL+"/one.png")
	a2 := seedAsset(t, s, uid, "image", fileServer.URL+"/two.png")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/assets/batch-download",
		struct {
			AssetIDs []string `json:"asset_ids"`
		}{AssetIDs: []string{a1.BizID, a2.BizID}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a valid zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("zip contains %d files, want 2", len(zr.File))
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s in zip: %v", f.Name, err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		if len(content) == 0 {
			t.Errorf("%s is empty in the zip", f.Name)
		}
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/assets/batch-download",
		struct {
			AssetIDs []string `json:"asset_ids"`
		}{AssetIDs: []string{"does-not-exist"}}, token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no matching assets: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestHandleAssetUploadFlowNotConfigured covers server.go's documented
// degrade-to-503 behavior for both halves of the direct-upload flow when
// s.objects is nil (no MinIO wired in) — this package's test server never
// constructs a real object store, so this is the one path through these two
// handlers it can exercise.
func TestHandleAssetUploadFlowNotConfigured(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/assets/upload-url",
		uploadURLRequest{Filename: "photo.png", Mime: "image/png"}, token)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload-url: status = %d, want %d, body = %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/assets/some-biz-id/complete",
		completeAssetRequest{StorageKey: "image/some-biz-id.png"}, token)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("complete: status = %d, want %d, body = %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
