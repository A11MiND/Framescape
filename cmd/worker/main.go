// cmd/worker is the executor host (PRD §8.1/§10.1): consumes the asynq
// dispatch queues, runs whichever executor.Plugin a task assignment names,
// and reports start/completion back to cmd/scheduler's control queue. It
// never touches the Aether Store or holds an Engine instance — Fat
// TaskAssignment (PRD §1④) gives it everything it needs, so it can be
// deployed and scaled completely independently of cmd/scheduler/cmd/api.
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/BabySid/aether/executor"
	"go.uber.org/zap"

	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/local"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/executor/mock"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/infra/storage"
	aetherengine "aigc-platform/internal/infra/workflow/aether"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/logger"
)

func main() {
	logger.Init(config.Env())
	log := logger.L()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()})
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
	sink := persistence.NewGormAssetSinkWithStorage(db, objectStore)
	fileCache := persistence.NewGormFileCache(db)
	redisClient := cache.NewClient(config.RedisAddr())
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal("ping redis", zap.Error(err))
	}

	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey())
	registry := executor.NewRegistry()
	must(registry.Register(mock.NewImagePlugin(sink)), log)
	must(registry.Register(mock.NewVideoPlugin(sink)), log)
	must(registry.Register(minimax.NewImagePlugin(minimaxClient, sink)), log)
	must(registry.Register(minimax.NewFileUploadPlugin(minimaxClient, sink, fileCache)), log)
	videoLimiter := minimax.NewVideoLimiter(redisClient, "default", config.MiniMaxVideoConcurrency())
	must(registry.Register(minimax.NewVideoPlugin(minimaxClient, sink, sink, fileCache, redisClient, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(minimax.NewVideoRegenPlugin(minimaxClient, sink, sink, fileCache, redisClient, config.MiniMaxCallbackURL(), videoLimiter)), log)
	must(registry.Register(local.NewComposePlugin(sink, sink)), log)
	must(registry.Register(local.NewExtractFramesPlugin(sink, sink)), log)
	must(registry.Register(local.NewGatePlugin()), log)
	must(registry.Register(local.NewConcatPlugin(sink, sink)), log)

	log.Info("worker starting", zap.Int("concurrency", config.WorkerConcurrency()))
	redisOpt := cache.AsynqRedisOpt(config.RedisAddr())
	if err := aetherengine.RunWorker(ctx, redisOpt, registry, config.WorkerConcurrency()); err != nil && ctx.Err() == nil {
		log.Fatal("worker stopped", zap.Error(err))
	}
	log.Info("worker shut down")
}

func must(err error, log *zap.Logger) {
	if err != nil {
		log.Fatal("executor registration failed", zap.Error(err))
	}
}
