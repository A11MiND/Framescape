// cmd/worker executes job nodes. Each queue has its own pool so slow work
// never starves fast work; every worker also runs the orchestrator sweeper
// and the maintenance duties, which coordinate through the database and
// Redis leases, so any number of workers can run and any one can die.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/application/review"
	"aigc-platform/internal/application/upkeep"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/gemini"
	"aigc-platform/internal/infra/executor/local"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/executor/mock"
	"aigc-platform/internal/infra/executor/openai"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/logger"
)

func main() {
	logger.Init(config.Env())
	log := logger.L()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN(), MaxOpen: config.MySQLMaxOpenConns(), MaxIdle: config.MySQLMaxIdleConns()})
	if err != nil {
		log.Fatal("open mysql", zap.Error(err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal("get sql.DB", zap.Error(err))
	}
	objectStore, err := storage.New(ctx, storage.Config{
		Endpoint: config.MinIOEndpoint(), AccessKeyID: config.MinIOAccessKey(), SecretAccessKey: config.MinIOSecretKey(),
		UseSSL: config.MinIOUseSSL(), Bucket: config.MinIOBucket(), PublicBaseURL: config.MinIOPublicBaseURL(),
	})
	if err != nil {
		log.Fatal("init object storage", zap.Error(err))
	}
	sink := persistence.NewGormAssetSinkWithStorage(db, objectStore)
	fileCache := persistence.NewGormFileCache(db)
	redisClient := cache.NewClient(config.RedisAddr(), config.RedisURL())
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal("ping redis", zap.Error(err))
	}
	redisOpt := cache.AsynqRedisOpt(config.RedisAddr(), config.RedisURL())
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey())
	registry := executor.NewRegistry()
	must(registry.Register(mock.NewImagePlugin(sink)), log)
	must(registry.Register(mock.NewVideoPlugin(sink)), log)
	must(registry.Register(mock.NewPromptEnhancePlugin()), log)
	must(registry.Register(minimax.NewImagePlugin(minimaxClient, sink, sink)), log)
	must(registry.Register(minimax.NewFileUploadPlugin(minimaxClient, sink, fileCache)), log)
	videoLimiter := minimax.NewVideoLimiter(redisClient, "default", config.MiniMaxVideoConcurrency())
	must(registry.Register(minimax.NewVideoPlugin(minimaxClient, sink, sink, fileCache, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(minimax.NewVideoRegenPlugin(minimaxClient, sink, sink, fileCache, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(minimax.NewPromptEnhancePlugin(minimaxClient, sink, fileCache)), log)
	must(registry.Register(local.NewComposePlugin(sink, sink)), log)
	must(registry.Register(local.NewExtractFramesPlugin(sink, sink)), log)
	must(registry.Register(local.NewConcatPlugin(sink, sink)), log)
	must(registry.Register(openai.NewImagePlugin(openai.Config{APIKey: config.OpenAIAPIKey(), Model: config.OpenAIImageModel(), BaseURL: config.OpenAIBaseURL(), USDToCNY: config.OpenAIUSDToCNY(), ReserveUSD: config.OpenAIImageReserveUSD(), PerRefUSD: config.OpenAIImageReservePerRefUSD()}, sink, sink)), log)
	// Gemini is optional: a missing or broken Vertex AI setup disables that
	// provider instead of taking the worker down.
	if projectID := config.GeminiVertexProjectID(); projectID != "" {
		geminiClient, err := gemini.NewClient(ctx, gemini.Config{
			ProjectID: projectID, Location: config.GeminiVertexLocation(), Model: config.GeminiImageModel(), CredentialsJSON: config.GeminiVertexCredentialsJSON(),
		})
		if err != nil {
			log.Error("gemini client init failed; the gemini provider is unavailable", zap.Error(err))
		} else {
			limiter := gemini.NewLimiter(redisClient, "default", config.GeminiVertexConcurrency(), time.Duration(config.GeminiVertexMinIntervalMs())*time.Millisecond)
			must(registry.Register(gemini.NewImagePlugin(geminiClient, sink, sink, limiter)), log)
		}
	}

	credits := creditsvc.New(sqlDB)
	orch := orchestrator.New(orchestrator.Options{
		DB: sqlDB, Dispatcher: orchestrator.NewAsynqDispatcher(asynqClient), Publisher: orchestrator.NewRedisPublisher(redisClient),
		Config:  orchestrator.Config{GateTTL: config.GateTTL()},
		Billing: jobsvc.Billing{Credits: credits}, Hooks: jobsvc.Hooks{ReviewImages: config.MiniMaxAPIKey() != ""},
		Plugins: registry,
	})
	reviewer := review.New(sqlDB, minimaxClient, sink)

	go orch.ListenCancel(ctx, redisClient)
	go orch.RunSweeper(ctx, 5*time.Second, func(err error) { log.Warn("sweep failed", zap.Error(err)) })
	upkeep.New(sqlDB, redisClient, credits, orch, objectStore, orch.Config().WorkerID).Start(ctx)

	metricsSrv := &http.Server{Addr: config.WorkerMetricsAddr(), ReadHeaderTimeout: 10 * time.Second}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/internal/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	metricsSrv.Handler = mux
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("worker metrics server", zap.Error(err))
		}
	}()

	handlers := orch.Handlers()
	handlers[jobsvc.TaskAssetReview] = func(ctx context.Context, t *asynq.Task) error { return reviewer.Handle(ctx, t.Payload()) }
	mux2 := asynq.NewServeMux()
	for kind, h := range handlers {
		mux2.HandleFunc(kind, h)
	}

	var servers []*asynq.Server
	for _, pool := range pools() {
		srv := asynq.NewServer(redisOpt, asynq.Config{
			Concurrency: pool.concurrency, Queues: map[string]int{pool.queue: 1},
			ShutdownTimeout: 2 * time.Minute, Logger: nil,
		})
		if err := srv.Start(mux2); err != nil {
			log.Fatal("start queue server", zap.String("queue", pool.queue), zap.Error(err))
		}
		servers = append(servers, srv)
		log.Info("serving queue", zap.String("queue", pool.queue), zap.Int("concurrency", pool.concurrency))
	}

	<-ctx.Done()
	log.Info("worker shutting down")
	for _, srv := range servers {
		srv.Stop()
	}
	// Executions still running give their node back without using up an
	// attempt; remote tasks keep running and are polled by another worker.
	orch.Shutdown()
	for _, srv := range servers {
		srv.Shutdown()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
	log.Info("worker shut down")
}

type pool struct {
	queue       string
	concurrency int
}

func pools() []pool {
	media := config.WorkerConcurrencyMedia()
	if media <= 0 {
		media = runtime.NumCPU()
	}
	all := map[string]pool{
		"interactive": {orchestrator.QueueInteractive, config.WorkerConcurrencyInteractive()},
		"video":       {orchestrator.QueueVideo, config.WorkerConcurrencyVideo()},
		"media":       {orchestrator.QueueMedia, media},
		"system":      {orchestrator.QueueSystem, config.WorkerConcurrencySystem()},
	}
	selected := config.WorkerQueues()
	if selected == "" {
		selected = "interactive,video,media,system"
	}
	var out []pool
	for _, name := range strings.Split(selected, ",") {
		if p, ok := all[strings.TrimSpace(name)]; ok && p.concurrency > 0 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		logger.L().Error("WORKER_QUEUES selects no known queue", zap.String("value", selected))
		os.Exit(1)
	}
	return out
}

func must(err error, log *zap.Logger) {
	if err != nil {
		log.Fatal("executor registration failed", zap.Error(err))
	}
}
