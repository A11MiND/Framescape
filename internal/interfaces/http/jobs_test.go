package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"aigc-platform/internal/application/jobsvc"
)

func TestHandleCreateJobImageSingle(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat wearing a hat"}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["biz_id"] == "" || body["biz_id"] == nil {
		t.Errorf("biz_id missing/empty: %v", body)
	}
	if body["status"] != "running" {
		t.Errorf("status = %v, want %q", body["status"], "running")
	}
	if body["workflow_run_id"] == "" || body["workflow_run_id"] == nil {
		t.Errorf("workflow_run_id missing/empty: %v", body)
	}
}

func TestHandleCreateJobUnknownWorkflowName(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "not.a.real.workflow", Spec: jobsvc.Spec{Text: "x"}}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

// TestHandleCreateJobInsufficientBalance covers §12.3's "hold before Submit,
// never after" — a user with 0 balance must never reach the engine at all.
func TestHandleCreateJobInsufficientBalance(t *testing.T) {
	s, eng := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0) // fresh account, zero balance

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if eng.next != 0 {
		t.Errorf("engine.Submit was called %d time(s), want 0 — a failed hold must never reach Submit", eng.next)
	}
}

// TestHandleCreateJobIdempotencyKey is the HTTP-layer half of this session's
// findByIdemKey fix — a retried request with the same Idempotency-Key must
// get back the exact same job, and must only ever hold credits once.
func TestHandleCreateJobIdempotencyKey(t *testing.T) {
	s, eng := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	req := httptestRequest(t, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	req.Header.Set("Idempotency-Key", "test-idem-key-1")
	rec := serveRequest(r, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var first map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &first)

	req2 := httptestRequest(t, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	req2.Header.Set("Idempotency-Key", "test-idem-key-1")
	rec2 := serveRequest(r, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second create: status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
	var second map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &second)

	if first["biz_id"] != second["biz_id"] {
		t.Errorf("biz_id differs between the two requests: %v vs %v, want identical", first["biz_id"], second["biz_id"])
	}
	if eng.next != 1 {
		t.Errorf("engine.Submit was called %d time(s), want exactly 1 — a repeated Idempotency-Key must not submit twice", eng.next)
	}
}

func TestHandleGetJob(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 1000)
	tokenB, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+bizID, nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner GET: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["workflow_name"] != "image.single" {
		t.Errorf("workflow_name = %v, want image.single", got["workflow_name"])
	}

	// Ownership boundary: user B must never be able to read user A's job.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+bizID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's GET: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleListJobs(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 1000)
	tokenB, _ := registerAndFund(t, s, 1000)

	for i := 0; i < 2; i++ {
		rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
			createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed job %d: status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
	}
	doJSON(t, r, http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a dog"}}, tokenB)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/jobs", nil, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Jobs) != 2 {
		t.Errorf("got %d jobs, want exactly 2 (user A's own, not user B's)", len(body.Jobs))
	}
}

func TestHandleCancelJob(t *testing.T) {
	s, eng := newFullTestServer(t)
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 1000)
	tokenB, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	// Ownership boundary: user B can't cancel user A's job.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/cancel", nil, tokenB)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("other user's cancel: status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/cancel", nil, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if eng.next != 1 {
		t.Fatalf("expected exactly 1 run submitted, got %d", eng.next)
	}

	// Cancelling an already-cancelled job is a documented no-op, not an error.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/cancel", nil, tokenA)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-cancel: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/does-not-exist/cancel", nil, tokenA)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown bizID: status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

func TestHandleDeleteJob(t *testing.T) {
	s, eng := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)
	runID := created["workflow_run_id"].(string)

	// Still running — must refuse, not silently soft-delete a live job.
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/jobs/"+bizID, nil, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete while running: status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	eng.setPhase(workflowRunIDFrom(runID), "Succeeded")

	rec = doJSON(t, r, http.MethodDelete, "/api/v1/jobs/"+bizID, nil, token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete after terminal: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	// Soft-deleted: Get()'s own deleted_at filter must now 404 it, same as
	// any other soft-deleted resource in this codebase.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+bizID, nil, token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleEstimateJob(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	spec := jobsvc.Spec{Text: "a cat", N: 3}
	want, err := jobsvc.EstimateCredits("image.single", spec)
	if err != nil {
		t.Fatalf("EstimateCredits: %v", err)
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs/estimate", createJobRequest{WorkflowName: "image.single", Spec: spec}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		CreditsTotal int `json:"credits_total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CreditsTotal != want {
		t.Errorf("credits_total = %d, want %d (jobsvc.EstimateCredits' own answer)", body.CreditsTotal, want)
	}

	// Estimating never holds anything — a fresh, unfunded account must still
	// get a quote back.
	zeroBalanceToken, _ := registerAndFund(t, s, 0)
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/estimate", createJobRequest{WorkflowName: "image.single", Spec: spec}, zeroBalanceToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("zero-balance estimate: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
