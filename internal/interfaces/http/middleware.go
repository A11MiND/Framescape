package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const ctxUserIDKey = "user_id"

// requireAuth validates the Bearer access token and stashes the user ID on
// the Gin context for handlers to read via userID(c).
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
		c.Set(ctxUserIDKey, cl.UserID)
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
