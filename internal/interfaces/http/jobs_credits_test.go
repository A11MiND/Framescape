package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/persistence"
)

type creditsView struct {
	Reserved int  `json:"reserved"`
	Settled  int  `json:"settled"`
	Overage  *int `json:"overage"`
	Released *int `json:"released"`
	Frozen   *int `json:"frozen"`
}

func jobCredits(t *testing.T, s *Server, token, bizID string) creditsView {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/v1/jobs/"+bizID, nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get job: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Credits creditsView `json:"credits"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Credits
}

func intp(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// TestJobCreditBreakdown settles more than the reservation and checks the
// breakdown the task list and detail report.
func TestJobCreditBreakdown(t *testing.T) {
	s, _ := newFullTestServer(t)
	token, uid := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, token)
	var job persistence.Job
	if err := s.db.Where("biz_id = ?", bizID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	reserved := job.CreditHeld

	c := jobCredits(t, s, token, bizID)
	if c.Reserved != reserved || c.Settled != 0 || intp(c.Frozen) != reserved || intp(c.Released) != 0 || intp(c.Overage) != 0 {
		t.Fatalf("fresh job credits = %+v, reserved %d", c, reserved)
	}

	// Charge the reservation plus 5 credits, then close the job.
	sqlDB, _ := s.db.DB()
	credits := creditsvc.New(sqlDB)
	hold := creditsvc.JobHold{UserID: uid, JobID: job.ID, JobBizID: bizID}
	yuan := float64(reserved+5) / float64(creditsvc.CreditsFromYuan(1))
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := credits.CommitForJobTx(context.Background(), tx, hold, "test:"+bizID+":commit", "gen", yuan)
	if err == nil {
		_, err = tx.ExecContext(context.Background(), `UPDATE jobs SET credit_settled = credit_settled + ? WHERE id = ?`, charged, job.ID)
	}
	if err == nil {
		_, err = credits.ReleaseJobTx(context.Background(), tx, hold, "test:"+bizID+":release")
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if charged <= reserved {
		t.Fatalf("charged %d, want more than the reservation %d", charged, reserved)
	}

	c = jobCredits(t, s, token, bizID)
	if c.Reserved != reserved || c.Settled != charged || intp(c.Overage) != charged-reserved || intp(c.Frozen) != 0 || intp(c.Released) != 0 {
		t.Fatalf("settled job credits = %+v, reserved %d charged %d", c, reserved, charged)
	}
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/v1/jobs", nil, token)
	var list struct {
		Jobs []struct {
			BizID   string      `json:"biz_id"`
			Credits creditsView `json:"credits"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Jobs) != 1 || list.Jobs[0].Credits.Settled != charged || intp(list.Jobs[0].Credits.Overage) != charged-reserved {
		t.Fatalf("list credits = %+v", list.Jobs)
	}
}

// TestJobStatusCountsAndReviewDeadline covers per-status counts, the exact
// status filter and the review deadline of a waiting preview gate.
func TestJobStatusCountsAndReviewDeadline(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 5000)
	queued := createJob(t, s, token)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "video.sequence",
		Spec: jobsvc.Spec{Shots: []string{"a harbour at dawn", "boats leave"}, DurationSeconds: 4}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create video.sequence: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		BizID string `json:"biz_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Suspend its gate as the orchestrator would once the drafts finish.
	suspendedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	res := s.db.Exec(`UPDATE job_nodes n JOIN jobs j ON j.id = n.job_id SET n.status = 'suspended', n.started_at = ?
		WHERE j.biz_id = ? AND n.node_name = 'gate'`, suspendedAt, created.BizID)
	if res.Error != nil || res.RowsAffected != 1 {
		t.Fatalf("suspend gate: %v (rows %d)", res.Error, res.RowsAffected)
	}
	if err := s.db.Exec(`UPDATE jobs SET status = 'awaiting_review' WHERE biz_id = ?`, created.BizID).Error; err != nil {
		t.Fatal(err)
	}
	var meta struct {
		GateTTLS int64 `json:"gate_ttl_s"`
	}
	var raw []byte
	s.db.Raw(`SELECT plan_meta FROM jobs WHERE biz_id = ?`, created.BizID).Row().Scan(&raw)
	if json.Unmarshal(raw, &meta) != nil || meta.GateTTLS <= 0 {
		t.Fatalf("plan_meta = %s", raw)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+created.BizID, nil, token)
	var detail struct {
		ReviewDeadline *time.Time `json:"review_deadline"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	want := suspendedAt.Add(time.Duration(meta.GateTTLS) * time.Second)
	if detail.ReviewDeadline == nil || !detail.ReviewDeadline.Equal(want) {
		t.Fatalf("review_deadline = %v, want %v", detail.ReviewDeadline, want)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+queued, nil, token)
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.ReviewDeadline != nil {
		t.Fatalf("queued job has a review deadline: %v", detail.ReviewDeadline)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/summary", nil, token)
	var sum struct {
		NeedsReview int            `json:"needs_review"`
		Active      int            `json:"active"`
		Statuses    map[string]int `json:"statuses"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sum)
	if sum.NeedsReview != 1 || sum.Active != 1 || sum.Statuses["queued"] != 1 || sum.Statuses["awaiting_review"] != 1 || sum.Statuses["cancelled"] != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs?status=queued", nil, token)
	var list struct {
		Jobs []struct {
			BizID string `json:"biz_id"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Jobs) != 1 || list.Jobs[0].BizID != queued {
		t.Fatalf("status=queued = %+v", list.Jobs)
	}
	if rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs?status=bogus", nil, token); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown status: %d", rec.Code)
	}
}

// TestJobListOrderAndExclude pages oldest first and drops excluded statuses.
func TestJobListOrderAndExclude(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)
	a, b, c := createJob(t, s, token), createJob(t, s, token), createJob(t, s, token)
	if err := s.db.Exec(`UPDATE jobs SET status = 'awaiting_review' WHERE biz_id = ?`, b).Error; err != nil {
		t.Fatal(err)
	}
	ids := func(url string) ([]string, string) {
		rec := doJSON(t, r, http.MethodGet, url, nil, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", url, rec.Code, rec.Body.String())
		}
		var body struct {
			Jobs []struct {
				BizID string `json:"biz_id"`
			} `json:"jobs"`
			NextCursor string `json:"next_cursor"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		var out []string
		for _, j := range body.Jobs {
			out = append(out, j.BizID)
		}
		return out, body.NextCursor
	}
	first, next := ids("/api/v1/jobs?order=oldest&limit=2")
	rest, _ := ids("/api/v1/jobs?order=oldest&limit=2&cursor=" + next)
	if got := append(first, rest...); len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Fatalf("oldest first = %v, want %v", got, []string{a, b, c})
	}
	if got, _ := ids("/api/v1/jobs?exclude_status=awaiting_review"); len(got) != 2 || got[0] != c || got[1] != a {
		t.Fatalf("excluding awaiting_review = %v", got)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/v1/jobs?exclude_status=nope", nil, token); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown excluded status: %d", rec.Code)
	}
}

// TestPreviewRequestExpandsPresets previews the compiled prompt and
// references without creating a job.
func TestPreviewRequestExpandsPresets(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 100)
	ref := seedAsset(t, s, uid, "image", "")
	var fragment string
	s.db.Raw(`SELECT prompt_fragment FROM presets WHERE biz_id = '01PRESETWATERCOLOR00000000'`).Row().Scan(&fragment)
	if fragment == "" {
		t.Skip("system presets not seeded")
	}
	spec := jobsvc.Spec{Text: "a cat on a wall", PresetIDs: []string{"01PRESETWATERCOLOR00000000"}, SourceImageAssetID: ref.BizID}
	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs/preview", createJobRequest{WorkflowName: "image.single", Spec: spec}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	var p jobsvc.Preview
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if !strings.Contains(p.Prompt, "a cat on a wall") || !strings.Contains(p.Prompt, fragment) {
		t.Fatalf("prompt = %q, want the text and the preset fragment %q", p.Prompt, fragment)
	}
	if len(p.References) != 1 || p.References[0] != ref.BizID {
		t.Fatalf("references = %v", p.References)
	}
	var jobs int64
	s.db.Model(&persistence.Job{}).Where("user_id = ?", uid).Count(&jobs)
	if jobs != 0 {
		t.Fatal("preview created a job")
	}
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/preview", createJobRequest{WorkflowName: "video.single", Spec: jobsvc.Spec{Text: "x"}}, token)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "not_supported") {
		t.Fatalf("video preview: %d %s", rec.Code, rec.Body.String())
	}
}
