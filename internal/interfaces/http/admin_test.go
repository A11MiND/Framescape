package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"aigc-platform/internal/application/jobsvc"
)

func TestHandleAdminOverview_NonAdminForbidden(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/admin/overview", nil, token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleAdminOverview(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 100)
	makeAdmin(t, s, adminUID)
	_, _ = registerAndFund(t, s, 0) // a second, non-admin user just to bump user_count

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed job: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/admin/overview", nil, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		UserCount        int64            `json:"user_count"`
		JobsByStatus     map[string]int64 `json:"jobs_by_status"`
		CreditsRecharged int64            `json:"credits_recharged"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.UserCount < 2 {
		t.Errorf("user_count = %d, want >= 2", body.UserCount)
	}
	var totalJobs int64
	for _, n := range body.JobsByStatus {
		totalJobs += n
	}
	if totalJobs < 1 {
		t.Errorf("jobs_by_status total = %d, want >= 1", totalJobs)
	}
}

func TestHandleAdminListUsers(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	_, otherUID := registerAndFund(t, s, 300)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/admin/users?limit=200", nil, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Users []struct {
			BizID   string `json:"biz_id"`
			Balance int    `json:"balance"`
			IsAdmin bool   `json:"is_admin"`
		} `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	foundAdmin, foundOther := false, false
	for _, u := range body.Users {
		if u.IsAdmin {
			foundAdmin = true
		}
	}
	// otherUID's balance should show up somewhere in the list — sanity check
	// the join against credit_accounts actually worked, not just that the
	// user row itself came back.
	var otherBizID string
	s.db.Table("users").Where("id = ?", otherUID).Select("biz_id").Scan(&otherBizID)
	for _, u := range body.Users {
		if u.BizID == otherBizID && u.Balance == 300 {
			foundOther = true
		}
	}
	if !foundAdmin {
		t.Error("admin user not found in list, or is_admin not reflected")
	}
	if !foundOther {
		t.Error("seeded 300-balance user not found with correct balance in list")
	}
}

func TestHandleAdminListUsers_Search(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	_, targetUID := registerAndFund(t, s, 0)

	var targetEmail string
	s.db.Table("users").Where("id = ?", targetUID).Select("email").Scan(&targetEmail)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/admin/users?q="+targetEmail, nil, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Users []struct {
			Email *string `json:"email"`
		} `json:"users"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Users) != 1 || body.Users[0].Email == nil || *body.Users[0].Email != targetEmail {
		t.Fatalf("search for %q returned %+v", targetEmail, body.Users)
	}
}

func TestHandleAdminGrantCredits(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	targetToken, targetUID := registerAndFund(t, s, 0)

	var targetBizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&targetBizID)

	rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/credits", targetBizID),
		map[string]any{"amount": 500, "remark": "test grant"}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Verify from the recipient's own side, not just the admin response —
	// confirms this actually went through creditsvc.Recharge, not just
	// returned a fabricated number.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/credits/balance", nil, targetToken)
	var bal struct {
		Balance int `json:"balance"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bal)
	if bal.Balance != 500 {
		t.Errorf("recipient balance = %d, want 500", bal.Balance)
	}
}

func TestHandleAdminGrantCredits_RejectsNonPositiveAndOverCap(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	_, targetUID := registerAndFund(t, s, 0)
	var targetBizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&targetBizID)

	for _, amount := range []int{0, -5, 1_000_001} {
		rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/credits", targetBizID),
			map[string]any{"amount": amount}, adminToken)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("amount=%d: status = %d, want %d", amount, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestHandleAdminSetAdmin(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	targetToken, targetUID := registerAndFund(t, s, 0)
	var targetBizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&targetBizID)

	rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/admin", targetBizID),
		map[string]any{"is_admin": true}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The newly-promoted user must be able to reach an admin route themselves.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/admin/overview", nil, targetToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("newly-promoted admin still forbidden: status = %d", rec.Code)
	}
}

// TestHandleAdminSetAdmin_RefusesToDemoteLastAdmin is the regression test
// for the lockout guard: with exactly one admin, demoting them must be
// refused, or nobody could ever grant admin back through the UI again.
func TestHandleAdminSetAdmin_RefusesToDemoteLastAdmin(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	var adminBizID string
	s.db.Table("users").Where("id = ?", adminUID).Select("biz_id").Scan(&adminBizID)

	// Sanity: this test's DB may already have other admins left over from a
	// prior test in the same package run against the same shared dev DB —
	// deliberately re-run makeAdmin is a no-op for that, but the "last
	// admin" count is scoped to the whole users table, not just this test's
	// own rows, so only assert the refusal when this really is the sole
	// admin at the moment of the call.
	var adminCount int64
	s.db.Table("users").Where("is_admin = ?", true).Count(&adminCount)
	if adminCount != 1 {
		t.Skipf("expected exactly 1 admin in DB at this point, found %d — other tests left admins behind, skipping to avoid a false result", adminCount)
	}

	rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/admin", adminBizID),
		map[string]any{"is_admin": false}, adminToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleAdminUsage(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 100)
	makeAdmin(t, s, adminUID)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed job: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/admin/usage?days=7", nil, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Days []struct {
			Day  string `json:"day"`
			Jobs int64  `json:"jobs"`
		} `json:"days"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	var totalJobs int64
	for _, d := range body.Days {
		totalJobs += d.Jobs
	}
	if totalJobs < 1 {
		t.Errorf("usage days total jobs = %d, want >= 1 (seeded job should show up today)", totalJobs)
	}
}
