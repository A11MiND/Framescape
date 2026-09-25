package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/application/jobsvc"
)

// failedVideoJob creates a video.single job and marks its gen step failed
// with the given failure code, as if the provider had refused it.
func failedVideoJob(t *testing.T, s *Server, eng *testEngine, token, code string) string {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "video.single", Spec: jobsvc.Spec{Text: "a tram along the coast", Ratio: "16:9"}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID, _ := created["biz_id"].(string)
	if bizID == "" {
		t.Fatalf("create job: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := eng.db.Exec(`UPDATE job_nodes n JOIN jobs j ON j.id = n.job_id SET n.status = 'failed', n.phase = 'Failed', n.error_code = ? WHERE j.biz_id = ? AND n.node_name = 'gen'`, code, bizID); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.db.Exec(`UPDATE jobs SET status = 'failed', error_code = ?, finished_at = NOW(3) WHERE biz_id = ?`, code, bizID); err != nil {
		t.Fatal(err)
	}
	return bizID
}

func genRetryable(t *testing.T, s *Server, token, bizID string) bool {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/v1/jobs/"+bizID, nil, token)
	var body struct {
		Nodes []struct {
			Name      string `json:"name"`
			Retryable bool   `json:"retryable"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode job: %v (%s)", err, rec.Body.String())
	}
	for _, n := range body.Nodes {
		if n.Name == "gen" {
			return n.Retryable
		}
	}
	t.Fatalf("no gen node in %s", rec.Body.String())
	return false
}

func TestRetryTransient(t *testing.T) {
	s, eng := newFullTestServer(t)
	token, _ := registerAndFund(t, s, 5000)
	bizID := failedVideoJob(t, s, eng, token, "provider_busy")

	if !genRetryable(t, s, token, bizID) {
		t.Fatal("a provider_busy failure should be retryable")
	}
	// The client sends no loop_index: v2 steps are not looped.
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs/"+bizID+"/nodes/gen/retry", map[string]any{}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	var retried map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &retried)
	newID, _ := retried["biz_id"].(string)
	if newID == "" || newID == bizID {
		t.Fatalf("retry should create a new job, got %s", rec.Body.String())
	}
	detail := doJSON(t, s.Router(), http.MethodGet, "/api/v1/jobs/"+newID, nil, token)
	var job map[string]any
	_ = json.Unmarshal(detail.Body.Bytes(), &job)
	if job["retry_of_job_id"] != bizID {
		t.Fatalf("retry_of_job_id = %v, want %s", job["retry_of_job_id"], bizID)
	}
}

func TestRetryModeration(t *testing.T) {
	s, eng := newFullTestServer(t)
	token, _ := registerAndFund(t, s, 5000)
	bizID := failedVideoJob(t, s, eng, token, "moderation")

	if genRetryable(t, s, token, bizID) {
		t.Fatal("a moderation failure must not be offered as a plain retry")
	}
	for _, body := range []map[string]any{
		{},
		{"prompt_override": "a tram along the coast"},
		{"prompt_override": "  a tram along the coast  "},
	} {
		rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs/"+bizID+"/nodes/gen/retry", body, token)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("retry %v: status = %d, want 422 (%s)", body, rec.Code, rec.Body.String())
		}
		var e struct {
			Code   string         `json:"code"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if e.Code != "input_change_required" || e.Params["code"] != "moderation" {
			t.Fatalf("retry %v: got %s", body, rec.Body.String())
		}
	}
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs/"+bizID+"/nodes/gen/retry", map[string]any{"prompt_override": "a tram along the coast at dawn"}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry with a changed prompt: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRetryBadParams(t *testing.T) {
	s, eng := newFullTestServer(t)
	token, _ := registerAndFund(t, s, 5000)
	bizID := failedVideoJob(t, s, eng, token, "bad_params")
	if genRetryable(t, s, token, bizID) {
		t.Fatal("a bad_params failure must not be offered as a plain retry")
	}
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs/"+bizID+"/nodes/gen/retry", map[string]any{}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("retry: status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}
