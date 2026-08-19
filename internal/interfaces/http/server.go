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

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/storage"
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
	// objects backs F2.1's presigned direct-upload endpoints only (assets.go's
	// handleAssetUploadURL/handleCompleteAsset) — nil is fine everywhere else,
	// every other asset write still goes through an executor's
	// assetstore.Sink, never through cmd/api.
	objects *storage.Store
	// credits backs handleCreditsTopup only — every other credit mutation
	// (Hold/Commit/Refund) stays inside jobsvc, which already holds its own
	// *creditsvc.Service. Topup is the one credit-adjacent action that
	// isn't triggered by a job lifecycle event, so it needs its own handle
	// on the service rather than going through jobs.
	credits *creditsvc.Service
}

func NewServer(db *gorm.DB, jobs *jobsvc.Service, credits *creditsvc.Service, redisClient *redis.Client, jwtSecret string, minimaxClient *minimax.Client, objectStore *storage.Store) *Server {
	return &Server{db: db, jobs: jobs, credits: credits, redis: redisClient, jwtSecret: jwtSecret, minimax: minimaxClient, objects: objectStore}
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
		v1.GET("/capabilities", s.handleGetCapabilities)

		authed := v1.Group("")
		authed.Use(s.requireAuth())
		authed.GET("/me", s.handleMe)
		authed.PATCH("/me/password", s.handleChangePassword)
		authed.POST("/prompts/rewrite", s.handleRewritePrompt)
		authed.POST("/jobs", s.handleCreateJob)
		authed.GET("/jobs", s.handleListJobs)
		authed.POST("/jobs/estimate", s.handleEstimateJob)
		authed.GET("/jobs/:bizID", s.handleGetJob)
		authed.GET("/jobs/:bizID/events", s.handleJobEvents)
		authed.POST("/jobs/:bizID/resume", s.handleResumeJob)
		authed.POST("/jobs/:bizID/cancel", s.handleCancelJob)
		authed.DELETE("/jobs/:bizID", s.handleDeleteJob)
		authed.POST("/jobs/:bizID/nodes/:nodeName/retry", s.handleRetryNode)
		authed.POST("/assets/upload-url", s.handleAssetUploadURL)
		authed.POST("/assets/:bizID/complete", s.handleCompleteAsset)
		authed.GET("/assets", s.handleListAssets)
		authed.GET("/community/feed", s.handleCommunityFeed)
		authed.GET("/assets/:bizID", s.handleGetAsset)
		authed.PATCH("/assets/:bizID", s.handleUpdateAsset)
		authed.DELETE("/assets/:bizID", s.handleDeleteAsset)
		authed.GET("/assets/trash", s.handleListTrash)
		authed.POST("/assets/:bizID/restore", s.handleRestoreAsset)
		authed.POST("/assets/trash/empty", s.handleEmptyTrash)
		authed.POST("/assets/batch-download", s.handleBatchDownloadAssets)
		authed.POST("/characters", s.handleCreateCharacter)
		authed.GET("/characters", s.handleListCharacters)
		authed.PATCH("/characters/:bizID", s.handleUpdateCharacter)
		authed.DELETE("/characters/:bizID", s.handleDeleteCharacter)
		authed.GET("/presets", s.handleListPresets)
		authed.POST("/presets", s.handleCreatePreset)
		authed.DELETE("/presets/:bizID", s.handleDeletePreset)
		authed.GET("/credits/balance", s.handleCreditsBalance)
		authed.GET("/credits/ledger", s.handleCreditsLedger)
		authed.POST("/credits/topup", s.handleCreditsTopup)
		authed.POST("/projects", s.handleCreateProject)
		authed.GET("/projects", s.handleListProjects)
		authed.PATCH("/projects/:bizID", s.handleUpdateProject)
		authed.DELETE("/projects/:bizID", s.handleDeleteProject)
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
