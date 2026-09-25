// Package httpapi is cmd/api's Gin layer: handlers, routing and JWT
// middleware.
package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"aigc-platform/internal/application/communitysvc"
	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/realtime"
	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/pkg/config"
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
	// community backs the daily-publish streak reward (handleUpdateAsset's
	// RecordPublish call, handleCommunityStreak's status read) — its own
	// *creditsvc.Service handle, separate from the one above, since a
	// streak bonus is granted independently of any job/topup flow.
	community *communitysvc.Service
	// orch and hub serve event streams and provider callbacks.
	orch *orchestrator.Orchestrator
	hub  *realtime.Hub
}

func NewServer(db *gorm.DB, jobs *jobsvc.Service, credits *creditsvc.Service, community *communitysvc.Service, redisClient *redis.Client, jwtSecret string, minimaxClient *minimax.Client, objectStore *storage.Store) *Server {
	return &Server{db: db, jobs: jobs, credits: credits, community: community, redis: redisClient, jwtSecret: jwtSecret, minimax: minimaxClient, objects: objectStore}
}

// WithEvents enables the event stream and provider callback wake-ups.
func (s *Server) WithEvents(orch *orchestrator.Orchestrator, hub *realtime.Hub) *Server {
	s.orch, s.hub = orch, hub
	return s
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
		// auth_oauth.go's own doc covers why these answer "not configured"
		// (config.GoogleClientID()/SMSAPIKey() empty) rather than working.
		v1.POST("/auth/google", s.handleGoogleLogin)
		v1.POST("/auth/phone/send-code", s.handlePhoneSendCode)
		v1.POST("/auth/phone/verify", s.handlePhoneVerify)
		v1.POST("/auth/email/send-code", s.handleEmailSendCode)
		v1.POST("/trial/image", s.handleTrialImage)
		v1.GET("/capabilities", s.handleGetCapabilities)
		// Public like every other unauthed route above — handleCommunityFeed's
		// own doc already covers why this is the one asset list not scoped to
		// a caller at all (never reads userID(c)), so requiring a token here
		// was never protecting anything; it only blocked the login page's
		// "browse the community first" link from working for a visitor who
		// hasn't signed up yet.
		v1.GET("/community/feed", s.handleCommunityFeed)

		authed := v1.Group("")
		authed.Use(s.requireAuth())
		authed.GET("/me", s.handleMe)
		authed.GET("/comics", s.handleListComics)
		authed.POST("/comics", s.handleSaveComic)
		authed.GET("/comics/:bizID", s.handleGetComic)
		authed.PATCH("/comics/:bizID", s.handleSaveComic)
		authed.PATCH("/me/password", s.handleChangePassword)
		authed.POST("/prompts/rewrite", s.handleRewritePrompt)
		authed.POST("/jobs", s.handleCreateJob)
		authed.GET("/jobs", s.handleListJobs)
		authed.GET("/jobs/summary", s.handleJobsSummary)
		authed.PATCH("/jobs/:bizID", s.handleUpdateJob)
		authed.POST("/jobs/estimate", s.handleEstimateJob)
		authed.POST("/jobs/preview", s.handlePreviewJob)
		authed.GET("/jobs/:bizID", s.handleGetJob)
		authed.GET("/jobs/:bizID/events", s.handleJobEvents)
		authed.GET("/stream", s.handleStream)
		authed.POST("/jobs/:bizID/resume/quote", s.handleQuoteResume)
		authed.POST("/jobs/:bizID/resume", s.handleResumeJob)
		authed.POST("/jobs/:bizID/cancel", s.handleCancelJob)
		authed.DELETE("/jobs/:bizID", s.handleDeleteJob)
		authed.POST("/jobs/:bizID/nodes/:nodeName/retry", s.handleRetryNode)
		authed.POST("/jobs/:bizID/panels/retry/quote", s.handleQuoteComicRetry)
		authed.POST("/jobs/:bizID/panels/retry", s.handleRetryComicPanels)
		authed.POST("/assets/upload-url", s.handleAssetUploadURL)
		authed.POST("/assets/:bizID/complete", s.handleCompleteAsset)
		authed.GET("/assets", s.handleListAssets)
		authed.GET("/community/streak", s.handleCommunityStreak)
		authed.GET("/assets/:bizID", s.handleGetAsset)
		authed.PATCH("/assets/:bizID", s.handleUpdateAsset)
		authed.DELETE("/assets/:bizID", s.handleDeleteAsset)
		authed.POST("/assets/:bizID/like", s.handleLikeAsset)
		authed.DELETE("/assets/:bizID/like", s.handleUnlikeAsset)
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
		authed.POST("/projects", s.handleCreateProject)
		authed.GET("/projects", s.handleListProjects)
		authed.GET("/projects/:bizID", s.handleGetProject)
		authed.PATCH("/projects/:bizID", s.handleUpdateProject)
		authed.DELETE("/projects/:bizID", s.handleDeleteProject)

		// Admin surface — nested under authed so requireAdmin's own DB lookup
		// (middleware.go) can assume userID(c) is already set. Every route
		// here needs both middlewares; handleCreditsTopup moved from the
		// plain authed group above to here (was previously reachable by any
		// authenticated user — see its own doc for why that was a real gap).
		admin := authed.Group("")
		admin.Use(s.requireAdmin())
		admin.POST("/credits/topup", s.handleCreditsTopup)
		admin.GET("/admin/overview", s.handleAdminOverview)
		admin.GET("/admin/users", s.handleAdminListUsers)
		admin.POST("/admin/users/:bizID/credits", s.handleAdminGrantCredits)
		admin.POST("/admin/users/:bizID/admin", s.handleAdminSetAdmin)
		admin.POST("/admin/users", s.handleAdminCreateUser)
		admin.POST("/admin/users/:bizID/active", s.handleAdminSetActive)
		admin.POST("/admin/users/:bizID/comic-ai", s.handleAdminSetComicAI)
		admin.GET("/admin/usage", s.handleAdminUsage)
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

// corsMiddleware allows cross-origin calls from CORS_ALLOWED_ORIGINS; with
// none configured it allows any origin in development and only same-origin
// requests in production.
func corsMiddleware() gin.HandlerFunc {
	allowed := config.CORSAllowedOrigins()
	allowAll := len(allowed) == 0 && config.Env() != "prod"
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowAll || slices.Contains(allowed, origin)) {
			if allowAll {
				c.Header("Access-Control-Allow-Origin", "*")
			} else {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, Last-Event-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
