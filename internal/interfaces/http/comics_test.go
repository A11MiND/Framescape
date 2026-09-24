package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/domain/comic"
	"aigc-platform/internal/infra/persistence"
)

func TestComicConcurrentSubmit(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-only-no-provider-call")
	t.Setenv("OPENAI_IMAGE_MODEL", "gpt-image-2.5-flare")
	s, eng := newFullTestServer(t)
	_, uid := registerAndFund(t, s, 1000)
	enableComicAI(t, s, uid)
	spec := jobsvc.Spec{Text: "四格漫画", ComicMode: "editable", ImageProvider: "openai"}
	var wg sync.WaitGroup
	ids := make(chan string, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := s.jobs.Create(context.Background(), uid, "image.comic4", spec, "same-comic-request", nil)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- job.BizID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate jobs")
		}
	}
	submissions := eng.submissions()
	if submissions != 1 {
		t.Fatalf("paid workflow dispatched %d times", submissions)
	}
	var account persistence.CreditAccount
	s.db.Where("user_id = ?", uid).First(&account)
	estimate, _ := jobsvc.EstimateCredits("image.comic4", spec)
	if account.Held != estimate {
		t.Fatalf("held %d, expected one reservation %d", account.Held, estimate)
	}
}

func testComic() comic.Document {
	return comic.Document{SchemaVersion: 1, Title: "港燈 ESG", Mode: "editable", Brief: "四格，工程師與機器人", PanelAssetIDs: []string{"", "", "", ""}, References: []comic.Reference{}, Layers: []comic.Layer{}}
}
func TestComicSaveReadConflictAndIsolation(t *testing.T) {
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 100)
	other, _ := registerAndFund(t, s, 0)
	r := s.Router()
	t.Cleanup(func() { s.db.Where("user_id = ?", uid).Delete(&persistence.ComicDocument{}) })
	doc := testComic()
	doc.Background = strings.Repeat("智", 200000)
	rec := doJSON(t, r, "POST", "/api/v1/comics", comicSaveRequest{Document: doc}, token)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var created struct {
		BizID    string         `json:"biz_id"`
		Version  int            `json:"version"`
		Document comic.Document `json:"document"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Version != 1 || created.Document.Background != doc.Background {
		t.Fatal("round-trip changed source")
	}
	path := "/api/v1/comics/" + created.BizID
	for _, method := range []string{"GET", "PATCH"} {
		rec = doJSON(t, r, method, path, comicSaveRequest{Version: 1, Document: doc}, other)
		if rec.Code != 404 {
			t.Fatalf("cross-user %s: %d", method, rec.Code)
		}
	}
	rec = doJSON(t, r, "PATCH", path, comicSaveRequest{Version: 1, Document: doc}, token)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, "PATCH", path, comicSaveRequest{Version: 1, Document: doc}, token)
	if rec.Code != 409 {
		t.Fatal("stale version overwrote document")
	}
	doc.PageAssetID = "someone-elses-image"
	rec = doJSON(t, r, "PATCH", path, comicSaveRequest{Version: 2, Document: doc}, token)
	if rec.Code != 422 {
		t.Fatal("accepted unavailable image")
	}
	rec = doJSON(t, r, "GET", path, nil, token)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/v1/comics", nil, other)
	if strings.Contains(rec.Body.String(), created.BizID) {
		t.Fatal("list leaked another user's comic")
	}
	rec = doJSON(t, r, "GET", path, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated read accepted")
	}
}
func TestDirectComicHTTPNoPlannerAndOwnedRefs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-only-no-provider-call")
	t.Setenv("OPENAI_IMAGE_MODEL", "gpt-image-2.5-flare")
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 1000)
	r := s.Router()
	spec := jobsvc.Spec{Text: "清新漫画：工程师和机器人。", ComicMode: "editable", ImageProvider: "openai"}
	// Outside the gray release: 403 before any hold or dispatch.
	if me := doJSON(t, r, "GET", "/api/v1/me", nil, token); !strings.Contains(me.Body.String(), `"comic_ai":false`) {
		t.Fatal(me.Body.String())
	}
	rec := doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: "image.comic4", Spec: spec}, token)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "openai_not_enabled") {
		t.Fatalf("non-beta user: %d %s", rec.Code, rec.Body.String())
	}
	var held persistence.CreditAccount
	if s.db.Where("user_id = ?", uid).First(&held); held.Held != 0 {
		t.Fatal("non-beta user had credits held")
	}
	enableComicAI(t, s, uid)
	if me := doJSON(t, r, "GET", "/api/v1/me", nil, token); !strings.Contains(me.Body.String(), `"comic_ai":true`) {
		t.Fatal(me.Body.String())
	}
	spec.ReferenceImageAssetIDs = []string{"foreign-image"}
	rec = doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: "image.comic4", Spec: spec}, token)
	if rec.Code != 422 {
		t.Fatal("accepted foreign reference")
	}
	var count int64
	s.db.Model(&persistence.Job{}).Where("user_id = ?", uid).Count(&count)
	if count != 0 {
		t.Fatal("invalid refs submitted a job")
	}
	spec.ReferenceImageAssetIDs = nil
	rec = doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: "image.comic4", Spec: spec}, token)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var body struct {
		BizID string `json:"biz_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	var job persistence.Job
	s.db.Where("biz_id = ?", body.BizID).First(&job)
	est, err := jobsvc.EstimateCredits("image.comic4", spec)
	if err != nil || job.CreditHeld != est {
		t.Fatalf("hold mismatch: %d %d %v", job.CreditHeld, est, err)
	}
}

func enableComicAI(t *testing.T, s *Server, uid uint64) {
	t.Helper()
	if err := persistence.SetEntitlement(context.Background(), s.db, uid, persistence.EntitlementOpenAIImage, true, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAdminSetComicAI(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	targetToken, targetUID := registerAndFund(t, s, 0)
	var bizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&bizID)
	path := "/api/v1/admin/users/" + bizID + "/comic-ai"
	if rec := doJSON(t, r, "POST", path, map[string]any{"enabled": true}, targetToken); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin toggled beta access: %d", rec.Code)
	}
	for _, enabled := range []bool{true, true, false} { // repeat = MySQL no-op update, must not 404
		if rec := doJSON(t, r, "POST", path, map[string]any{"enabled": enabled}, adminToken); rec.Code != http.StatusOK {
			t.Fatalf("toggle %v: %d %s", enabled, rec.Code, rec.Body.String())
		}
		want := fmt.Sprintf(`"comic_ai":%v`, enabled)
		if me := doJSON(t, r, "GET", "/api/v1/me", nil, targetToken); !strings.Contains(me.Body.String(), want) {
			t.Fatalf("me after toggle %v: %s", enabled, me.Body.String())
		}
	}
	if rec := doJSON(t, r, "POST", "/api/v1/admin/users/no-such-user/comic-ai", map[string]any{"enabled": true}, adminToken); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
	if me := doJSON(t, r, "GET", "/api/v1/me", nil, adminToken); !strings.Contains(me.Body.String(), `"comic_ai":true`) {
		t.Fatal("admins are always in the beta")
	}
}

func TestGeneralOpenAIImageHTTP(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-only-no-provider-call")
	t.Setenv("OPENAI_IMAGE_MODEL", "gpt-image-2.5-flare")
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 1000)
	r := s.Router()
	spec := jobsvc.Spec{Text: "a lighthouse at dawn", ImageProvider: "openai", N: 2, ImageQuality: "medium", AspectRatio: "16:9"}
	rec := doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: "image.single", Spec: spec}, token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-beta user: %d %s", rec.Code, rec.Body.String())
	}
	enableComicAI(t, s, uid)
	bad := spec
	bad.ImageSize = "4096x4096"
	if rec := doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: "image.single", Spec: bad}, token); rec.Code != 422 {
		t.Fatalf("unoffered size: %d", rec.Code)
	}
	for _, wf := range []struct {
		name string
		spec jobsvc.Spec
	}{
		{"image.single", spec},
		{"image.sequence", jobsvc.Spec{Shots: []string{"one", "two"}, ImageProvider: "openai", ImageSequenceMode: "continuity"}},
	} {
		rec := doJSON(t, r, "POST", "/api/v1/jobs", createJobRequest{WorkflowName: wf.name, Spec: wf.spec}, token)
		if rec.Code != 200 {
			t.Fatal(wf.name, rec.Code, rec.Body.String())
		}
		var body struct {
			BizID string `json:"biz_id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		var job persistence.Job
		s.db.Where("biz_id = ?", body.BizID).First(&job)
		est, err := jobsvc.EstimateCredits(wf.name, wf.spec)
		if err != nil || job.CreditHeld != est {
			t.Fatalf("%s hold mismatch: %d %d %v", wf.name, job.CreditHeld, est, err)
		}
		var inputs []string
		s.db.Table("job_nodes").Where("job_id = ?", job.ID).Order("node_name").Pluck("inputs_json", &inputs)
		if len(inputs) == 0 || !strings.Contains(inputs[0], `"quality"`) {
			t.Fatalf("%s node inputs = %v", wf.name, inputs)
		}
	}
	caps := doJSON(t, r, "GET", "/api/v1/capabilities", nil, "")
	if !strings.Contains(caps.Body.String(), `"sizes":["1024x1024","1536x1024","1024x1536"]`) {
		t.Fatal(caps.Body.String())
	}
}
