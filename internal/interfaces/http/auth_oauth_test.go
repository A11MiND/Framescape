package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// Google/SMS/email provider keys are read straight from the environment on
// every call (config.go's getEnv), so these tests must pin them to empty
// regardless of what the ambient shell/.env happens to have exported —
// otherwise a developer with a real GOOGLE_CLIENT_ID in their environment
// would see this test fail for a reason that has nothing to do with the code.

func TestHandleGoogleLoginNotConfigured(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "")
	s := newTestServer(t)
	r := s.Router()
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/google", googleLoginRequest{IDToken: "irrelevant"}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandlePhoneSendCodeNotConfigured(t *testing.T) {
	t.Setenv("SMS_API_KEY", "")
	s := newTestServer(t)
	r := s.Router()
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/phone/send-code", phoneSendCodeRequest{Phone: uniquePhone(t)}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandlePhoneVerifyNotConfigured(t *testing.T) {
	t.Setenv("SMS_API_KEY", "")
	s := newTestServer(t)
	r := s.Router()
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/phone/verify", phoneVerifyRequest{Phone: uniquePhone(t), Code: "123456"}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleEmailSendCodeNotConfigured(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER_API_KEY", "")
	s := newTestServer(t)
	r := s.Router()
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/email/send-code", emailSendCodeRequest{Email: uniqueEmail(t)}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestHandleRegisterWithEmailCode exercises handleRegister's optional code
// check once config.EmailProviderAPIKey() is set — the code itself is
// seeded directly into email_verification_codes (sendEmailCode is an
// unimplemented stub, so there's no real provider to send through in a
// test), matching how a real code would land there via handleEmailSendCode.
func TestHandleRegisterWithEmailCode(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER_API_KEY", "test-key-not-a-real-provider")
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse", Code: "000000"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no code seeded yet: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	if err := s.db.Create(&persistence.EmailVerificationCode{
		Email: email, Code: "654321", ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed verification code: %v", err)
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse", Code: "wrong-code"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse", Code: "654321"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("correct code: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	decodeTokens(t, rec)

	// Consumed codes can't be replayed, e.g. against a second registration
	// attempt after the first one's tokens were lost client-side.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: uniqueEmail(t), Password: "correct-horse", Code: "654321"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed consumed code: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestHandleRegisterWithExpiredEmailCode(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER_API_KEY", "test-key-not-a-real-provider")
	s := newTestServer(t)
	r := s.Router()
	email := uniqueEmail(t)

	if err := s.db.Create(&persistence.EmailVerificationCode{
		Email: email, Code: "111111", ExpiresAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed expired verification code: %v", err)
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse", Code: "111111"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired code: status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestFindOrCreateGoogleUser is the regression test for this session's
// account-hijack fix: signing in with Google must never auto-link onto an
// existing account that merely shares the same email — see
// findOrCreateGoogleUser's own doc for the attack this closes.
func TestFindOrCreateGoogleUser(t *testing.T) {
	s := newTestServer(t)

	t.Run("creates a new account on first sign-in", func(t *testing.T) {
		email := uniqueEmail(t)
		claims := &googleClaims{Email: email, RegisteredClaims: jwt.RegisteredClaims{Subject: uniqueGoogleSub(t)}}
		user, err := s.findOrCreateGoogleUser(claims)
		if err != nil {
			t.Fatalf("findOrCreateGoogleUser: %v", err)
		}
		if user.Email == nil || *user.Email != email {
			t.Errorf("email = %v, want %q", user.Email, email)
		}
		if user.GoogleSub == nil || *user.GoogleSub != claims.Subject {
			t.Errorf("google_sub = %v, want %q", user.GoogleSub, claims.Subject)
		}
	})

	t.Run("returns the same account on repeat sign-in", func(t *testing.T) {
		email := uniqueEmail(t)
		claims := &googleClaims{Email: email, RegisteredClaims: jwt.RegisteredClaims{Subject: uniqueGoogleSub(t)}}
		first, err := s.findOrCreateGoogleUser(claims)
		if err != nil {
			t.Fatalf("first sign-in: %v", err)
		}
		second, err := s.findOrCreateGoogleUser(claims)
		if err != nil {
			t.Fatalf("second sign-in: %v", err)
		}
		if second.ID != first.ID {
			t.Errorf("second sign-in created a different user (id %d vs %d), want the same one", second.ID, first.ID)
		}
	})

	t.Run("refuses to auto-link onto an existing email — the account-hijack fix", func(t *testing.T) {
		email := uniqueEmail(t)
		victim := persistence.User{BizID: id.New(), Email: &email}
		if err := s.createAccount(&victim); err != nil {
			t.Fatalf("seed victim account: %v", err)
		}

		attackerClaims := &googleClaims{Email: email, RegisteredClaims: jwt.RegisteredClaims{Subject: uniqueGoogleSub(t)}}
		_, err := s.findOrCreateGoogleUser(attackerClaims)
		if !errors.Is(err, errGoogleEmailTaken) {
			t.Fatalf("err = %v, want errGoogleEmailTaken", err)
		}

		// And the victim's account must be untouched — no google_sub grafted on.
		var reloaded persistence.User
		if err := s.db.First(&reloaded, victim.ID).Error; err != nil {
			t.Fatalf("reload victim: %v", err)
		}
		if reloaded.GoogleSub != nil {
			t.Errorf("victim account got a google_sub attached: %v, want nil", *reloaded.GoogleSub)
		}
	})
}

func TestCheckVerifyRateLimit(t *testing.T) {
	s := newTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping rate-limit test")
	}
	ctx := context.Background()
	identifier := uniqueEmail(t) // unique per run so repeated test invocations don't share a counter

	for i := 0; i < maxVerifyAttempts; i++ {
		if err := s.checkVerifyRateLimit(ctx, "test", identifier); err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i+1, err)
		}
	}
	if err := s.checkVerifyRateLimit(ctx, "test", identifier); err == nil {
		t.Fatalf("attempt %d: expected the rate limit to trip after %d attempts", maxVerifyAttempts+1, maxVerifyAttempts)
	}
}

// TestCheckVerifyRateLimitNilRedis confirms the documented fail-open
// behavior — no Redis configured must never block signups/logins outright.
func TestCheckVerifyRateLimitNilRedis(t *testing.T) {
	s := &Server{redis: nil}
	if err := s.checkVerifyRateLimit(context.Background(), "test", "whoever"); err != nil {
		t.Fatalf("expected nil-redis to fail open, got %v", err)
	}
}
