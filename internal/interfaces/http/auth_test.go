package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

func doJSON(t *testing.T, r http.Handler, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeTokens(t *testing.T, rec *httptest.ResponseRecorder) tokenPair {
	t.Helper()
	var tp tokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &tp); err != nil {
		t.Fatalf("decode token pair: %v (body=%s)", err, rec.Body.String())
	}
	if tp.AccessToken == "" || tp.RefreshToken == "" {
		t.Fatalf("expected non-empty tokens, got %+v (body=%s)", tp, rec.Body.String())
	}
	return tp
}

func TestHandleRegisterLoginRoundTrip(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeTokens(t, rec)

	// Duplicate registration must fail with 409, never silently succeed or
	// leak a different status that would let a caller distinguish "this
	// email exists" some other way.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "another-password"}, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate register: status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "correct-horse"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeTokens(t, rec)

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "wrong-password"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestHandleLoginNonexistentUser(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: uniqueEmail(t), Password: "whatever123"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestHandleLoginNilPasswordHashAccount covers a Google-/phone-only account
// (PasswordHash nil) — handleLogin must answer exactly the same
// "invalid_credentials" as a real mismatch, never a distinct error that
// would let a caller enumerate which accounts have a password set at all.
func TestHandleLoginNilPasswordHashAccount(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)
	sub := uniqueGoogleSub(t)
	user := persistence.User{BizID: id.New(), Email: &email, GoogleSub: &sub}
	if err := s.createAccount(&user); err != nil {
		t.Fatalf("seed google-only account: %v", err)
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "anything"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invalid_credentials" {
		t.Errorf("code = %q, want invalid_credentials (must not reveal the account has no password)", body["code"])
	}
}

func TestHandleRefresh(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse"}, "")
	tp := decodeTokens(t, rec)

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", refreshRequest{RefreshToken: tp.RefreshToken}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeTokens(t, rec)

	// An access token must never work as a refresh token — this is the
	// entire reason tokenType exists.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", refreshRequest{RefreshToken: tp.AccessToken}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh with access token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", refreshRequest{RefreshToken: "garbage"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh with garbage: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse"}, "")
	tp := decodeTokens(t, rec)

	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", tp.AccessToken) // missing "Bearer " prefix
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("malformed header: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, "not-a-real-token")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// A refresh token must not authenticate a protected route either —
	// same boundary as TestHandleRefresh, checked from the other side.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, tp.RefreshToken)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh token as bearer: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/me", nil, tp.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid access token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if me["email"] != email {
		t.Errorf("email = %v, want %q", me["email"], email)
	}
}

func TestHandleChangePassword(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "old-password"}, "")
	tp := decodeTokens(t, rec)

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/me/password",
		changePasswordRequest{CurrentPassword: "wrong-current", NewPassword: "new-password-123"}, tp.AccessToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPatch, "/api/v1/me/password",
		changePasswordRequest{CurrentPassword: "old-password", NewPassword: "new-password-123"}, tp.AccessToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "old-password"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password after change: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", loginRequest{Email: email, Password: "new-password-123"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login with new password: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestHandleChangePasswordNilPasswordHashAccount mirrors
// TestHandleLoginNilPasswordHashAccount for the change-password path — a
// Google-only account has nothing to check "current password" against, and
// must be refused the same way a real mismatch is.
func TestHandleChangePasswordNilPasswordHashAccount(t *testing.T) {
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)
	sub := uniqueGoogleSub(t)
	user := persistence.User{BizID: id.New(), Email: &email, GoogleSub: &sub}
	if err := s.createAccount(&user); err != nil {
		t.Fatalf("seed google-only account: %v", err)
	}
	access, err := signToken(testJWTSecret, user.ID, tokenAccess, accessTTL)
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}

	rec := doJSON(t, r, http.MethodPatch, "/api/v1/me/password",
		changePasswordRequest{CurrentPassword: "anything", NewPassword: "new-password-123"}, access)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}
