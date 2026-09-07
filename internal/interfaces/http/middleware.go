package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
)

const ctxUserIDKey = "user_id"

// requireAuth validates the Bearer access token and stashes the user ID on
// the Gin context for handlers to read via userID(c). Also rejects a
// deactivated account (users.is_active) on every request — same "fresh DB
// read, not baked into the JWT" reasoning as requireAdmin below: an access
// token lives up to 7 days, and an admin suspending someone must take
// effect on that account's very next request, not wait out the token or
// force a mass invalidation. The extra lookup runs on every authed
// endpoint in the app (not just the admin subset requireAdmin guards), so
// it's a single indexed primary-key SELECT, kept as cheap as this check
// can be.
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errBody("unauthorized", "missing bearer token"))
			return
		}
		raw := strings.TrimPrefix(h, "Bearer ")
		cl, err := parseToken(s.jwtSecret, raw, tokenAccess)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errBody("unauthorized", err.Error()))
			return
		}
		var isActive bool
		if err := s.db.WithContext(c.Request.Context()).
			Model(&persistence.User{}).
			Select("is_active").
			Where("id = ?", cl.UserID).
			Scan(&isActive).Error; err != nil || !isActive {
			c.AbortWithStatusJSON(http.StatusForbidden, errBody("account_suspended", "this account has been deactivated"))
			return
		}
		c.Set(ctxUserIDKey, cl.UserID)
		c.Next()
	}
}

// requireAdmin chains after requireAuth (needs userID(c) already set) and
// rejects any caller whose users.is_admin isn't true. Deliberately a fresh
// DB read on every request rather than something baked into the JWT at
// login time — an access token lives up to 7 days (accessTTL), and revoking
// someone's admin rights must take effect on their very next request, not
// wait for their token to expire or force a mass token invalidation.
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		var isAdmin bool
		err := s.db.WithContext(c.Request.Context()).
			Model(&persistence.User{}).
			Select("is_admin").
			Where("id = ?", userID(c)).
			Scan(&isAdmin).Error
		if err != nil || !isAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, errBody("forbidden", "admin access required"))
			return
		}
		c.Next()
	}
}

func userID(c *gin.Context) uint64 {
	v, _ := c.Get(ctxUserIDKey)
	id, _ := v.(uint64)
	return id
}

// errBody matches PRD §13.1's error envelope: {"code","message","request_id"}.
func errBody(code, message string) gin.H {
	return gin.H{"code": code, "message": message, "request_id": ""}
}
