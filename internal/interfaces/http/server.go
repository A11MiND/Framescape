// Package httpapi is cmd/api's Gin layer: handlers + routing + JWT
// middleware. It depends on jobsvc and the workflow.Engine port (via
// whatever implementation cmd/api wires in — the rpc.Client in production,
// see cmd/api/main.go) but never on aether directly (PRD §2.3 闸门三).
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"aigc-platform/internal/application/jobsvc"
)

type Server struct {
	db        *gorm.DB
	jobs      *jobsvc.Service
	redis     *redis.Client
	jwtSecret string
}

func NewServer(db *gorm.DB, jobs *jobsvc.Service, redisClient *redis.Client, jwtSecret string) *Server {
	return &Server{db: db, jobs: jobs, redis: redisClient, jwtSecret: jwtSecret}
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	r.GET("/internal/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/internal/callbacks/minimax", s.handleMiniMaxCallback)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", s.handleRegister)
		v1.POST("/auth/login", s.handleLogin)
		v1.POST("/auth/refresh", s.handleRefresh)

		authed := v1.Group("")
		authed.Use(s.requireAuth())
		authed.GET("/me", s.handleMe)
		authed.POST("/jobs", s.handleCreateJob)
		authed.GET("/jobs/:bizID", s.handleGetJob)
		authed.GET("/jobs/:bizID/events", s.handleJobEvents)
		authed.POST("/jobs/:bizID/resume", s.handleResumeJob)
		authed.GET("/assets", s.handleListAssets)
		authed.GET("/assets/:bizID", s.handleGetAsset)
		authed.POST("/characters", s.handleCreateCharacter)
		authed.GET("/characters", s.handleListCharacters)
		authed.GET("/presets", s.handleListPresets)
	}
	return r
}

// corsMiddleware is a permissive dev-only CORS policy so the Vite dev server
// (a different origin) can call the API directly without a proxy. Tighten
// before anything beyond local POC use.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
