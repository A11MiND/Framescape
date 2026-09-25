package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/persistence"
)

func seedVideo(t *testing.T, s *Server, uid uint64, seconds int) persistence.Asset {
	t.Helper()
	a := seedAsset(t, s, uid, "video", "")
	if err := s.db.Model(&persistence.Asset{}).Where("id = ?", a.ID).Update("duration_ms", seconds*1000).Error; err != nil {
		t.Fatal(err)
	}
	return a
}

func submitVideo(t *testing.T, s *Server, token string, spec jobsvc.Spec) (int, string) {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "video.single", Spec: spec}, token)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Code
}

func TestVideoRefsRejectedBeforeReserving(t *testing.T) {
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 5000)
	_, other := registerAndFund(t, s, 0)
	short, long := seedVideo(t, s, uid, 6), seedVideo(t, s, uid, 10)
	image := seedAsset(t, s, uid, "image", "")
	foreign := seedVideo(t, s, other, 4)

	balance := func() int {
		var a persistence.CreditAccount
		s.db.Where("user_id = ?", uid).First(&a)
		return a.Balance + a.Held
	}
	before := balance()
	cases := []struct {
		name string
		spec jobsvc.Spec
		code string
	}{
		{"text without a ratio", jobsvc.Spec{Text: "x"}, "video_ratio_required"},
		{"frames with a reference", jobsvc.Spec{Text: "x", FirstFrameAssetID: image.BizID, ReferenceVideoAssetIDs: []string{short.BizID}}, "video_refs_exclusive"},
		{"references over 15 seconds", jobsvc.Spec{Text: "x", ReferenceVideoAssetIDs: []string{short.BizID, long.BizID}}, "reference_video_budget"},
		{"another user's video", jobsvc.Spec{Text: "x", ReferenceVideoAssetIDs: []string{foreign.BizID}}, "reference_unavailable"},
		{"an image as a video", jobsvc.Spec{Text: "x", ReferenceVideoAssetIDs: []string{image.BizID}}, "reference_unavailable"},
	}
	for _, c := range cases {
		if status, code := submitVideo(t, s, token, c.spec); status != http.StatusUnprocessableEntity || code != c.code {
			t.Errorf("%s: %d %s, want 422 %s", c.name, status, code, c.code)
		}
	}
	if got := balance(); got != before {
		t.Fatalf("rejected requests changed the balance: %d -> %d", before, got)
	}
	if status, code := submitVideo(t, s, token, jobsvc.Spec{Text: "x", ReferenceVideoAssetIDs: []string{long.BizID}}); status != http.StatusOK {
		t.Fatalf("one 10 second reference: %d %s", status, code)
	}
}
