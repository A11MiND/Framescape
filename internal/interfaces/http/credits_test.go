package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"aigc-platform/internal/application/jobsvc"
)

func TestHandleCreditsBalance(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 500)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/credits/balance", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Balance int `json:"balance"`
		Held    int `json:"held"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Balance != 500 || body.Held != 0 {
		t.Fatalf("balance/held = %d/%d, want 500/0", body.Balance, body.Held)
	}

	// Creating a job moves credits from balance into held (§12.3) — the
	// balance endpoint must reflect that immediately.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create job: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/credits/balance", nil, token)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Held <= 0 {
		t.Errorf("held = %d after creating a job, want > 0", body.Held)
	}
	if body.Balance != 500-body.Held {
		t.Errorf("balance = %d, want %d (500 - held)", body.Balance, 500-body.Held)
	}
}

func TestHandleCreditsLedger(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, uid := registerAndFund(t, s, 500)

	// idemKey uniqueness is global, not per-user (creditsvc.idemKeyExists'
	// own doc) — a fixed literal here would silently no-op as a "duplicate"
	// against a previous run's identical key, exactly as this once did.
	holdIdemKey := fmt.Sprintf("test:ledger:hold:%d", uid)
	if err := s.credits.Hold(t.Context(), uid, holdIdemKey, "job", "test-job", 3, "job", "image.single"); err != nil {
		t.Fatalf("hold: %v", err)
	}

	rec := doJSON(t, r, http.MethodGet, "/api/v1/credits/ledger", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Recharge (registerAndFund) then Hold above — newest first.
	if len(body.Entries) < 2 {
		t.Fatalf("got %d ledger entries, want at least 2 (recharge + hold)", len(body.Entries))
	}
	if body.Entries[0]["direction"] != "hold" {
		t.Errorf("newest entry direction = %v, want hold", body.Entries[0]["direction"])
	}

	// Pagination: limit=1 must return exactly 1 row plus a next_cursor.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/credits/ledger?limit=1", nil, token)
	var page struct {
		Entries    []map[string]any `json:"entries"`
		NextCursor string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Entries) != 1 {
		t.Fatalf("limit=1 returned %d entries, want 1", len(page.Entries))
	}
	if page.NextCursor == "" {
		t.Error("next_cursor missing when more rows remain")
	}
}

func TestHandleCreditsTopup(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/credits/topup", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Balance  int `json:"balance"`
		Credited int `json:"credited"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Credited != demoTopupCredits {
		t.Errorf("credited = %d, want %d", body.Credited, demoTopupCredits)
	}
	if body.Balance != demoTopupCredits {
		t.Errorf("balance = %d, want %d", body.Balance, demoTopupCredits)
	}

	// Not deduplicated — every call is a deliberate new top-up (topup.go's
	// own doc), unlike job creation's Idempotency-Key handling.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/credits/topup", nil, token)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Balance != demoTopupCredits*2 {
		t.Errorf("balance after second topup = %d, want %d", body.Balance, demoTopupCredits*2)
	}
}
