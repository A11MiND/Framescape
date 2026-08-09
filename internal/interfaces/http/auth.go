package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
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

func (s *Server) handleRegister(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "hash password"))
		return
	}

	user := persistence.User{BizID: id.New(), Email: req.Email, PasswordHash: string(hash)}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return tx.Create(&persistence.CreditAccount{UserID: user.ID, Balance: 0}).Error
	})
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
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, errBody("invalid_credentials", "email or password incorrect"))
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
		"biz_id":  user.BizID,
		"email":   user.Email,
		"balance": acct.Balance,
	})
}
