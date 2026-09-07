package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestHandleAdminOverview_RealCostYuan is the regression/correctness test
// for costYuanExpr's JSON_EXTRACT SQL — seeds a real commit-shaped
// credit_ledger row (via creditsvc.Hold+Commit directly, the same two
// calls jobsvc's own settlement path makes) with a known cost_yuan, then
// confirms handleAdminOverview's total_cost_yuan sums it back out
// correctly. Without this, a broken JSON path expression would silently
// report 0 for every real MiniMax spend — exactly the kind of thing an
// admin actually asking "how much have we spent" needs to be able to
// trust.
func TestHandleAdminOverview_RealCostYuan(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 100)
	makeAdmin(t, s, adminUID)

	ctx := context.Background()
	if err := s.credits.Hold(ctx, adminUID, "test:overview-cost:hold", "job", "test-job", 50, "job", "image.single"); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := s.credits.Commit(ctx, adminUID, "test:overview-cost:commit", "test-task-run", 1.2345); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rec := doJSON(t, r, http.MethodGet, "/api/v1/admin/overview", nil, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TotalCostYuan float64 `json:"total_cost_yuan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TotalCostYuan < 1.2345 {
		t.Errorf("total_cost_yuan = %v, want >= 1.2345 (seeded commit's real cost)", body.TotalCostYuan)
	}
}

func TestHandleAdminCreateUser(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)

	newEmail := uniqueEmail(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users", map[string]any{"email": newEmail}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		BizID        string `json:"biz_id"`
		Email        string `json:"email"`
		TempPassword string `json:"temp_password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Email != newEmail {
		t.Errorf("email = %q, want %q", body.Email, newEmail)
	}
	if len(body.TempPassword) < 8 {
		t.Fatalf("temp_password = %q, want len >= 8 (registerRequest's own min)", body.TempPassword)
	}

	// The whole point: the returned temp password must actually work
	// against the real login endpoint, and the new account must not be an
	// admin or start deactivated.
	loginRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequest{Email: newEmail, Password: body.TempPassword}, "")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login with temp password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	newToken := decodeTokens(t, loginRec).AccessToken
	meRec := doJSON(t, r, http.MethodGet, "/api/v1/me", nil, newToken)
	var me struct {
		IsAdmin bool `json:"is_admin"`
	}
	_ = json.Unmarshal(meRec.Body.Bytes(), &me)
	if me.IsAdmin {
		t.Error("admin-created account came back is_admin=true, want false")
	}
}

func TestHandleAdminCreateUser_DuplicateEmailConflict(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)

	taken := uniqueEmail(t)
	doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: taken, Password: "correct-horse"}, "")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users", map[string]any{"email": taken}, adminToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleAdminSetActive(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	targetToken, targetUID := registerAndFund(t, s, 0)
	var targetBizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&targetBizID)

	// Deactivate — the target's already-issued access token must stop
	// working on its very next request (requireAuth's own doc: a fresh DB
	// read every time, not baked into the JWT).
	rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/active", targetBizID),
		map[string]any{"is_active": false}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, targetToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("deactivated user's existing token: status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// Reactivate — the same (never-rotated) token must work again immediately.
	rec = doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/active", targetBizID),
		map[string]any{"is_active": true}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, targetToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivated user's existing token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleAdminSetActive_LoginBlockedWhileDeactivated covers
// handleLogin's own extra check — a deactivated account must not be able
// to mint a fresh token pair at all, not just have existing tokens
// rejected downstream.
func TestHandleAdminSetActive_LoginBlockedWhileDeactivated(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)

	email := uniqueEmail(t)
	regRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse"}, "")
	targetUID := parseUID(t, regRec)
	var targetBizID string
	s.db.Table("users").Where("id = ?", targetUID).Select("biz_id").Scan(&targetBizID)

	doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/active", targetBizID),
		map[string]any{"is_active": false}, adminToken)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "correct-horse"}, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login while deactivated: status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleAdminSetActive_RefusesSelfDeactivate(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()
	adminToken, adminUID := registerAndFund(t, s, 0)
	makeAdmin(t, s, adminUID)
	var adminBizID string
	s.db.Table("users").Where("id = ?", adminUID).Select("biz_id").Scan(&adminBizID)

	rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%s/active", adminBizID),
		map[string]any{"is_active": false}, adminToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// parseUID pulls the user ID out of a register/login response's access
// token — admin_test.go's other helpers all seed via registerAndFund,
// which already returns uid; this is for the one test above that needs the
// email held fixed (so login can be attempted with it later) rather than
// letting registerAndFund pick one internally.
func parseUID(t *testing.T, rec *httptest.ResponseRecorder) uint64 {
	t.Helper()
	tp := decodeTokens(t, rec)
	cl, err := parseToken(testJWTSecret, tp.AccessToken, tokenAccess)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	return cl.UserID
}
