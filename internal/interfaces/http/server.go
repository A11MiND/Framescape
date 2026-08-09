// Package httpapi is cmd/api's Gin layer: handlers + routing + JWT
// middleware. It depends on jobsvc and the workflow.Engine port (via
// whatever implementation cmd/api wires in — the rpc.Client in production,
// see cmd/api/main.go) but never on aether directly (PRD §2.3 闸门三).
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/pkg/metrics"
)

type Server struct {
	db        *gorm.DB
	jobs      *jobsvc.Service
	redis     *redis.Client
	jwtSecret string
	// minimax backs F1.2's anonymous trial only — see trial.go's doc for why
	// this is a deliberate, narrow exception to "cmd/api never talks to
	// MiniMax directly".
	minimax *minimax.Client
}

func NewServer(db *gorm.DB, jobs *jobsvc.Service, redisClient *redis.Client, jwtSecret string, minimaxClient *minimax.Client) *Server {
	return &Server{db: db, jobs: jobs, redis: redisClient, jwtSecret: jwtSecret, minimax: minimaxClient}
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(metricsMiddleware())

	r.GET("/internal/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.POST("/internal/callbacks/minimax", s.handleMiniMaxCallback)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", s.handleRegister)
		v1.POST("/auth/login", s.handleLogin)
		v1.POST("/auth/refresh", s.handleRefresh)
		v1.POST("/trial/image", s.handleTrialImage)

		authed := v1.Group("")
		authed.Use(s.requireAuth())
		authed.GET("/me", s.handleMe)
		authed.POST("/jobs", s.handleCreateJob)
		authed.GET("/jobs/:bizID", s.handleGetJob)
		authed.GET("/jobs/:bizID/events", s.handleJobEvents)
		authed.POST("/jobs/:bizID/resume", s.handleResumeJob)
		authed.GET("/assets", s.handleListAssets)
		authed.GET("/assets/:bizID", s.handleGetAsset)
		authed.DELETE("/assets/:bizID", s.handleDeleteAsset)
		authed.POST("/assets/batch-download", s.handleBatchDownloadAssets)
		authed.POST("/characters", s.handleCreateCharacter)
		authed.GET("/characters", s.handleListCharacters)
		authed.GET("/presets", s.handleListPresets)
	}
	return r
}

// metricsMiddleware records aigc_http_requests_total/aigc_http_request_duration_seconds
// for every request. Uses c.FullPath() (the route pattern, e.g.
// "/api/v1/jobs/:bizID") rather than c.Request.URL.Path so a biz_id in the
// URL doesn't create a new label series per request — the classic
// Prometheus cardinality trap for path-parameterized routes.
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		metrics.HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, strconv.Itoa(c.Writer.Status())).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
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
