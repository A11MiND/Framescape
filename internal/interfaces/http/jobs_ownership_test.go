package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func TestJobsRefuseAssetsTheCallerDoesNotOwn(t *testing.T) {
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 5000)
	_, other := registerAndFund(t, s, 0)
	own := seedAsset(t, s, uid, "image", "")
	foreign := seedAsset(t, s, other, "image", "")
	deleted := seedAsset(t, s, uid, "image", "")
	now := time.Now()
	if err := s.db.Model(&persistence.Asset{}).Where("id = ?", deleted.ID).Update("deleted_at", &now).Error; err != nil {
		t.Fatal(err)
	}
	balance := func() int {
		var a persistence.CreditAccount
		s.db.Where("user_id = ?", uid).First(&a)
		return a.Balance + a.Held
	}
	before := balance()

	cases := []struct {
		name, workflow string
		spec           jobsvc.Spec
	}{
		{"another user's image to edit", "image.single", jobsvc.Spec{Text: "x", SourceImageAssetID: foreign.BizID}},
		{"a deleted image to edit", "image.single", jobsvc.Spec{Text: "x", SourceImageAssetID: deleted.BizID}},
		{"another user's first frame", "video.single", jobsvc.Spec{Text: "x", FirstFrameAssetID: foreign.BizID}},
		{"another user's reference image", "video.single", jobsvc.Spec{Text: "x", Ratio: "16:9", ReferenceImageAssetIDs: []string{own.BizID, foreign.BizID}}},
		{"an unknown sequence anchor", "video.sequence", jobsvc.Spec{Shots: []string{"a", "b"}, Ratio: "16:9", SourceImageAssetID: id.New()}},
	}
	for _, c := range cases {
		rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: c.workflow, Spec: c.spec}, token)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusUnprocessableEntity || body.Code != "reference_unavailable" {
			t.Errorf("%s: %d %s, want 422 reference_unavailable", c.name, rec.Code, body.Code)
		}
	}
	if got := balance(); got != before {
		t.Fatalf("refused requests changed the balance: %d -> %d", before, got)
	}
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "x", SourceImageAssetID: own.BizID}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("own image: %d %s", rec.Code, rec.Body.String())
	}
}

func TestJobsIgnoreAnotherUsersPreset(t *testing.T) {
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 5000)
	_, other := registerAndFund(t, s, 0)
	theirs := persistence.Preset{BizID: id.New(), Category: "style", Name: "theirs", PromptFragment: "their-private-fragment", OwnerUserID: &other}
	mine := persistence.Preset{BizID: id.New(), Category: "style", Name: "mine", PromptFragment: "my-own-fragment", OwnerUserID: &uid}
	for _, p := range []*persistence.Preset{&theirs, &mine} {
		if err := s.db.Create(p).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a harbour", PresetIDs: []string{theirs.BizID, mine.BizID}}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var job struct {
		BizID string `json:"biz_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	var inputs []string
	if err := s.db.Raw("SELECT n.inputs_json FROM job_nodes n JOIN jobs j ON j.id = n.job_id WHERE j.biz_id = ?", job.BizID).Scan(&inputs).Error; err != nil || len(inputs) == 0 {
		t.Fatalf("read node inputs: %v (%d rows)", err, len(inputs))
	}
	all := strings.Join(inputs, "\n")
	if strings.Contains(all, "their-private-fragment") {
		t.Error("another user's preset fragment reached the prompt")
	}
	if !strings.Contains(all, "my-own-fragment") {
		t.Error("the caller's own preset fragment is missing from the prompt")
	}
}
