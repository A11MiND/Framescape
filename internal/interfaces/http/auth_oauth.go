// Scaffolding for Google login and phone-number registration (product ask:
// build the routes/DB shape now, real provider credentials land later via
// config.GoogleClientID()/SMSAPIKey() — see that file's own doc on why
// every handler here checks its config first and answers a real 4xx
// ("not configured") rather than silently pretending to work).
package httpapi

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/id"
)

// --- Google ID token verification ---

// googleClaims is the subset of a Google ID token's payload this app
// actually reads. `sub` is Google's own stable per-account identifier
// (never reused, unlike email which a user could theoretically change at
// their provider) — RegisteredClaims.Subject carries it, no separate field
// needed.
type googleClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	jwt.RegisteredClaims
}

const googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

// googleIssuers: Google's own token-verification guide lists both forms as
// valid `iss` values depending on token vintage/endpoint.
var googleIssuers = map[string]bool{
	"accounts.google.com":         true,
	"https://accounts.google.com": true,
}

type jwksKey struct {
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}
type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

// fetchGoogleJWKS is not cached across requests — this POC-scale scaffold
// trades one extra HTTP round trip per Google login for not having to
// think about key rotation/cache invalidation. A deployment handling real
// login volume should cache this by `kid`, honoring the JWKS response's
// own Cache-Control header.
func fetchGoogleJWKS(ctx context.Context) (*jwksResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleJWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch google jwks: HTTP %d", resp.StatusCode)
	}
	var out jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode google jwks: %w", err)
	}
	return &out, nil
}

func rsaPublicKeyFromJWK(k jwksKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode jwk n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode jwk e: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}, nil
}

// verifyGoogleIDToken checks the four things Google's own "verify the
// integrity of the ID token" guide requires for a token handed to this
// backend by the frontend's Sign-In-With-Google JS: signature (against
// Google's live JWKS, matched by the token header's `kid`), issuer,
// audience (must equal clientID — config.GoogleClientID(), never empty
// here since handleGoogleLogin checks first), and expiry (jwt's own
// validator enforces `exp` automatically from RegisteredClaims).
func verifyGoogleIDToken(ctx context.Context, idToken, clientID string) (*googleClaims, error) {
	jwks, err := fetchGoogleJWKS(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch google keys: %w", err)
	}

	var claims googleClaims
	_, err = jwt.ParseWithClaims(idToken, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, k := range jwks.Keys {
			if k.Kid == kid {
				return rsaPublicKeyFromJWK(k)
			}
		}
		return nil, fmt.Errorf("no matching google jwk for kid %q", kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(clientID))
	if err != nil {
		return nil, fmt.Errorf("verify google id token: %w", err)
	}
	if !googleIssuers[claims.Issuer] {
		return nil, fmt.Errorf("unexpected google token issuer %q", claims.Issuer)
	}
	if !claims.EmailVerified || claims.Email == "" {
		return nil, fmt.Errorf("google account has no verified email")
	}
	return &claims, nil
}

type googleLoginRequest struct {
	IDToken string `json:"id_token" binding:"required"`
}

// handleGoogleLogin is POST /api/v1/auth/google. Frontend flow: Google
// Identity Services' JS SDK renders the button and hands back a signed ID
// token (never a password) — this verifies that token server-side and
// either logs into or creates the matching account, the same shape as
// handleLogin/handleRegister but keyed on the token's own subject+email
// instead of a submitted password.
func (s *Server) handleGoogleLogin(c *gin.Context) {
	clientID := config.GoogleClientID()
	if clientID == "" {
		c.JSON(http.StatusBadRequest, errBody("google_not_configured", "Google login is not configured on this server"))
		return
	}
	var req googleLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	claims, err := verifyGoogleIDToken(c.Request.Context(), req.IDToken, clientID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errBody("invalid_token", err.Error()))
		return
	}

	var user persistence.User
	err = s.db.Where("google_sub = ?", claims.Subject).First(&user).Error
	switch {
	case err == nil:
		// already linked, nothing else to do
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Not linked yet — an existing email/password account with the same
		// verified email gets Google linked onto it instead of a duplicate
		// second account; a brand-new email creates a fresh, password-less
		// account (Google is the only way in until Settings adds one).
		email := claims.Email
		sub := claims.Subject
		if linkErr := s.db.Where("email = ?", email).First(&user).Error; linkErr == nil {
			if err := s.db.Model(&user).Update("google_sub", sub).Error; err != nil {
				c.JSON(http.StatusInternalServerError, errBody("internal", "link google account"))
				return
			}
		} else {
			user = persistence.User{BizID: id.New(), Email: &email, GoogleSub: &sub}
			txErr := s.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				return tx.Create(&persistence.CreditAccount{UserID: user.ID, Balance: 0}).Error
			})
			if txErr != nil {
				c.JSON(http.StatusInternalServerError, errBody("internal", "create user"))
				return
			}
		}
	default:
		c.JSON(http.StatusInternalServerError, errBody("internal", "lookup user"))
		return
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "issue tokens"))
		return
	}
	c.JSON(http.StatusOK, tokens)
}

// --- Phone number registration/login ---

// sendSMSCode is the one function real phone login needs a provider
// plugged into — this app never picked one (Aliyun/Tencent/Twilio all have
// different SDKs, pricing, and account setup, not a decision to make on
// the product's behalf), so it's a single clearly-marked seam: implement
// the real API call here once config.SMSAPIKey() is set. Nothing else in
// this file needs to change when that happens.
func sendSMSCode(ctx context.Context, phone, code string) error {
	return fmt.Errorf("no SMS provider wired in yet — implement sendSMSCode once config.SMSAPIKey() is set")
}

type phoneSendCodeRequest struct {
	Phone string `json:"phone" binding:"required"`
}

func (s *Server) handlePhoneSendCode(c *gin.Context) {
	if config.SMSAPIKey() == "" {
		c.JSON(http.StatusBadRequest, errBody("sms_not_configured", "phone login is not configured on this server"))
		return
	}
	var req phoneSendCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	code := fmt.Sprintf("%06d", rand.Intn(1_000_000))
	if err := s.db.Create(&persistence.PhoneVerificationCode{
		Phone: req.Phone, Code: code, ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "store verification code"))
		return
	}
	if err := sendSMSCode(c.Request.Context(), req.Phone, code); err != nil {
		c.JSON(http.StatusInternalServerError, errBody("sms_send_failed", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

type phoneVerifyRequest struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// handlePhoneVerify is POST /api/v1/auth/phone/verify: find-or-create by
// phone, the same shape as handleGoogleLogin. A phone-only account also
// has no password (nil PasswordHash) — SMS-code-in only, until Settings
// gives it one.
func (s *Server) handlePhoneVerify(c *gin.Context) {
	if config.SMSAPIKey() == "" {
		c.JSON(http.StatusBadRequest, errBody("sms_not_configured", "phone login is not configured on this server"))
		return
	}
	var req phoneVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	var vc persistence.PhoneVerificationCode
	err := s.db.Where("phone = ? AND code = ? AND consumed_at IS NULL AND expires_at > ?", req.Phone, req.Code, time.Now()).
		Order("id DESC").First(&vc).Error
	if err != nil {
		c.JSON(http.StatusUnauthorized, errBody("invalid_code", "verification code is invalid or expired"))
		return
	}
	if err := s.db.Model(&vc).Update("consumed_at", time.Now()).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "consume verification code"))
		return
	}

	var user persistence.User
	err = s.db.Where("phone = ?", req.Phone).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		phone := req.Phone
		user = persistence.User{BizID: id.New(), Phone: &phone}
		txErr := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			return tx.Create(&persistence.CreditAccount{UserID: user.ID, Balance: 0}).Error
		})
		if txErr != nil {
			c.JSON(http.StatusInternalServerError, errBody("internal", "create user"))
			return
		}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "lookup user"))
		return
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "issue tokens"))
		return
	}
	c.JSON(http.StatusOK, tokens)
}

// --- Email verification (handleRegister's optional code check) ---

// sendEmailCode is sendSMSCode's exact counterpart — one seam for whichever
// transactional-email provider gets picked (SendGrid/SES/阿里云邮件推送
// all have different SDKs, not a decision this app makes for the product),
// wired in here once config.EmailProviderAPIKey() is set.
func sendEmailCode(ctx context.Context, email, code string) error {
	return fmt.Errorf("no email provider wired in yet — implement sendEmailCode once config.EmailProviderAPIKey() is set")
}

type emailSendCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

func (s *Server) handleEmailSendCode(c *gin.Context) {
	if config.EmailProviderAPIKey() == "" {
		c.JSON(http.StatusBadRequest, errBody("email_not_configured", "email verification is not configured on this server"))
		return
	}
	var req emailSendCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	code := fmt.Sprintf("%06d", rand.Intn(1_000_000))
	if err := s.db.Create(&persistence.EmailVerificationCode{
		Email: req.Email, Code: code, ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "store verification code"))
		return
	}
	if err := sendEmailCode(c.Request.Context(), req.Email, code); err != nil {
		c.JSON(http.StatusInternalServerError, errBody("email_send_failed", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
