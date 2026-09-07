package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/id"
)

// registerRequest's Code is only checked when config.EmailProviderAPIKey()
// is set (handleRegister's own doc) — until then it's read and ignored,
// same as today's behavior before this field existed.
type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Code     string `json:"code"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) issueTokens(userID uint64) (tokenPair, error) {
	access, err := signToken(s.jwtSecret, userID, tokenAccess, accessTTL)
	if err != nil {
		return tokenPair{}, err
	}
	refresh, err := signToken(s.jwtSecret, userID, tokenRefresh, refreshTTL)
	if err != nil {
		return tokenPair{}, err
	}
	return tokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

// createAccount inserts a new user row plus its zero-balance CreditAccount
// in one transaction — every signup path (email/password, Google, phone)
// needs exactly this pair and nothing else, so each one just fills in
// `user`'s identifier field(s) and calls this rather than repeating the
// transaction inline. Was three hand-copied versions of the same two
// lines before this — a future addition to what a new account needs
// (signup bonus, default project, etc.) now only has one place to land.
func (s *Server) createAccount(user *persistence.User) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return tx.Create(&persistence.CreditAccount{UserID: user.ID, Balance: 0}).Error
	})
}

// handleRegister requires a verified email code only once
// config.EmailProviderAPIKey() is actually set — see auth_oauth.go's
// sendEmailCode doc. Left unset (today's default), registration behaves
// exactly as it always has: email + password, no code.
func (s *Server) handleRegister(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	if config.EmailProviderAPIKey() != "" {
		if err := s.checkVerifyRateLimit(c.Request.Context(), "email", req.Email); err != nil {
			c.JSON(http.StatusTooManyRequests, errBody("too_many_attempts", err.Error()))
			return
		}
		var vc persistence.EmailVerificationCode
		err := s.db.Where("email = ? AND code = ? AND consumed_at IS NULL AND expires_at > ?", req.Email, req.Code, time.Now()).
			Order("id DESC").First(&vc).Error
		if err != nil {
			c.JSON(http.StatusUnauthorized, errBody("invalid_code", "verification code is invalid or expired"))
			return
		}
		if err := s.db.Model(&vc).Update("consumed_at", time.Now()).Error; err != nil {
			c.JSON(http.StatusInternalServerError, errBody("internal", "consume verification code"))
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "hash password"))
		return
	}

	hashStr := string(hash)
	user := persistence.User{BizID: id.New(), Email: &req.Email, PasswordHash: &hashStr}
	err = s.createAccount(&user)
	if err != nil {
		c.JSON(http.StatusConflict, errBody("email_taken", "email already registered"))
		return
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "issue tokens"))
		return
	}
	c.JSON(http.StatusOK, tokens)
}

func (s *Server) handleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	var user persistence.User
	if err := s.db.Where("email = ?", req.Email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, errBody("invalid_credentials", "email or password incorrect"))
			return
		}
		c.JSON(http.StatusInternalServerError, errBody("internal", "lookup user"))
		return
	}
	// PasswordHash is nil for a Google-/phone-only account — same
	// "incorrect" response as a real mismatch rather than a distinct error,
	// so this endpoint never confirms which accounts do or don't have a
	// password set.
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, errBody("invalid_credentials", "email or password incorrect"))
		return
	}
	// requireAuth's own doc covers the real enforcement (checked fresh on
	// every subsequent request) — this is purely a clearer error message: a
	// suspended account's password is still correct, so without this check
	// login would silently "succeed" and only fail confusingly on the very
	// next click.
	if !user.IsActive {
		c.JSON(http.StatusForbidden, errBody("account_suspended", "this account has been deactivated"))
		return
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "issue tokens"))
		return
	}
	c.JSON(http.StatusOK, tokens)
}

func (s *Server) handleRefresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	cl, err := parseToken(s.jwtSecret, req.RefreshToken, tokenRefresh)
	if err != nil {
		c.JSON(http.StatusUnauthorized, errBody("unauthorized", "invalid refresh token"))
		return
	}
	tokens, err := s.issueTokens(cl.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "issue tokens"))
		return
	}
	c.JSON(http.StatusOK, tokens)
}

func (s *Server) handleMe(c *gin.Context) {
	var user persistence.User
	if err := s.db.First(&user, userID(c)).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "user not found"))
		return
	}
	var acct persistence.CreditAccount
	_ = s.db.First(&acct, "user_id = ?", user.ID).Error

	c.JSON(http.StatusOK, gin.H{
		"biz_id":   user.BizID,
		"email":    user.Email,
		"balance":  acct.Balance,
		"is_admin": user.IsAdmin,
	})
}

// handleChangePassword is Settings' account-security addition — the app
// previously had no way to change a password short of the CLI/DB directly.
// Requires the current password (not just a valid session) so a stolen,
// still-live access token can't silently lock the real owner out.
func (s *Server) handleChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	var user persistence.User
	if err := s.db.First(&user, userID(c)).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "user not found"))
		return
	}
	// A Google-/phone-only account has no current password to check against
	// — treated as "incorrect" too, same reasoning as handleLogin's own nil
	// check just above it.
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.CurrentPassword)) != nil {
		c.JSON(http.StatusUnauthorized, errBody("invalid_credentials", "current password incorrect"))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "hash password"))
		return
	}
	if err := s.db.Model(&persistence.User{}).Where("id = ?", user.ID).Update("password_hash", string(hash)).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update password"))
		return
	}
	c.Status(http.StatusNoContent)
}
