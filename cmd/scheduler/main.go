// cmd/scheduler hosts the single Aether Engine instance (PRD §8.1: the
// engine must be a single instance to avoid duplicate scope advancement).
// W2 (DEV_PLAN.md §6) swaps W1's in-memory Store/LocalBroker for the MySQL
// Store and the asynq-backed distributed broker, and adds the control-queue
// consumer that turns worker reports into engine callbacks, plus the
// projection callback that drives job_nodes + SSE. cmd/api still reaches it
// only through the internal/infra/workflow/rpc HTTP bridge (unchanged).
package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/application/projection"
	"aigc-platform/internal/application/upkeep"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/local"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/executor/mock"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/infra/storage"
	aetherengine "aigc-platform/internal/infra/workflow/aether"
	"aigc-platform/internal/infra/workflow/rpc"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/logger"
)

func main() {
	logger.Init(config.Env())
	log := logger.L()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	gormDB, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()})
	if err != nil {
		log.Fatal("open mysql", zap.Error(err))
	}

	objectStore, err := storage.New(ctx, storage.Config{
		Endpoint: config.MinIOEndpoint(), AccessKeyID: config.MinIOAccessKey(), SecretAccessKey: config.MinIOSecretKey(),
		UseSSL: config.MinIOUseSSL(), Bucket: config.MinIOBucket(), PublicBaseURL: config.MinIOPublicBaseURL(),
	})
	if err != nil {
		log.Fatal("init object storage", zap.Error(err))
	}
	sink := persistence.NewGormAssetSinkWithStorage(gormDB, objectStore)
	fileCache := persistence.NewGormFileCache(gormDB)

	sqlDB, err := gormDB.DB()
	if err != nil {
		log.Fatal("get sql.DB", zap.Error(err))
	}
	mysqlStore := aetherengine.NewMySQLStore(sqlDB)

	redisClient := cache.NewClient(config.RedisAddr())
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal("ping redis", zap.Error(err))
	}
	credits := creditsvc.New(sqlDB)
	// The scheduler never calls Execute() on these (Dispatch goes through
	// asynq to cmd/worker) — it only needs the registry so Aether can
	// validate that submitted workflows reference known executor types.
	// F8.3's post-hoc review is the one exception: it's a projection-layer
	// side effect, not a dispatched task, so it makes its own direct
	// MiniMax call from here.
	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey())
	proj := projection.New(sqlDB, redisClient, credits, minimaxClient, sink)
	mysqlStore.SetChangeCallback(proj.Callback())
	registry := executor.NewRegistry()
	must(registry.Register(mock.NewImagePlugin(sink)), log)
	must(registry.Register(mock.NewVideoPlugin(sink)), log)
	must(registry.Register(minimax.NewImagePlugin(minimaxClient, sink, sink)), log)
	must(registry.Register(minimax.NewFileUploadPlugin(minimaxClient, sink, fileCache)), log)
	videoLimiter := minimax.NewVideoLimiter(redisClient, "default", config.MiniMaxVideoConcurrency())
	must(registry.Register(minimax.NewVideoPlugin(minimaxClient, sink, sink, fileCache, redisClient, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(minimax.NewVideoRegenPlugin(minimaxClient, sink, sink, fileCache, redisClient, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(minimax.NewPromptEnhancePlugin(minimaxClient, sink, fileCache)), log)
	must(registry.Register(minimax.NewStorySplitPlugin(minimaxClient)), log)
	must(registry.Register(local.NewComposePlugin(sink, sink)), log)
	must(registry.Register(local.NewExtractFramesPlugin(sink, sink)), log)
	must(registry.Register(local.NewGatePlugin()), log)
	must(registry.Register(local.NewConcatPlugin(sink, sink)), log)
	must(registry.Register(local.NewCollectRefsPlugin()), log)

	redisOpt := cache.AsynqRedisOpt(config.RedisAddr())
	asynqBroker := aetherengine.NewAsynqBroker(redisOpt)
	defer asynqBroker.Close()

	eng, err := aetherengine.New(mysqlStore, asynqBroker, registry)
	if err != nil {
		log.Fatal("construct engine", zap.Error(err))
	}
	if err := eng.Start(ctx); err != nil {
		log.Fatal("start engine", zap.Error(err))
	}
	defer eng.Stop()

	// jobsvc.Service here backs only upkeep's autoResumeSkipPreview duty
	// (Resume's own doc) — the scheduler otherwise never touches jobsvc,
	// same "one instance, narrow exception" reasoning as this file's own
	// minimaxClient (F8.3's post-hoc review). minimaxClient itself is passed
	// through unchanged — jobsvc needs it for video.sequence's "smart"
	// reference-selection mode (Spec.ReferenceSelectionMode's own doc), one
	// synchronous MiniMax-M3 call at Create()/Resume() time, before any DAG
	// exists to run it as a task node.
	jobs := jobsvc.New(gormDB, eng, credits, minimaxClient)

	// §11.4's Scheduler duties: suspended-timeout cleanup + credit
	// reconciliation (see upkeep's package doc for which of the five listed
	// duties are and aren't implemented yet).
	upkeepRunner := upkeep.New(sqlDB, eng, jobs, objectStore)
	if d := config.SuspendedTimeout(); d > 0 {
		upkeepRunner = upkeepRunner.WithSuspendedTimeout(d)
	}
	upkeepRunner.Start(ctx)

	// Control-queue consumer: turns worker "started"/"completed" reports
	// into eng.OnTaskStarted/OnTaskCompleted. Runs until ctx is cancelled.
	go func() {
		if err := asynqBroker.RunControlConsumer(ctx); err != nil && ctx.Err() == nil {
			log.Error("control consumer stopped", zap.Error(err))
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/internal/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	rpc.NewServer(eng).Register(r)

	srv := &http.Server{Addr: config.SchedulerAddr(), Handler: r}
	go func() {
		log.Info("scheduler listening", zap.String("addr", config.SchedulerAddr()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("scheduler http server", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down scheduler")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func must(err error, log *zap.Logger) {
	if err != nil {
		log.Fatal("executor registration failed", zap.Error(err))
	}
}
