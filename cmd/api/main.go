// cmd/api is the stateless Gin HTTP entrypoint (PRD §8.1): auth, validation,
// and job CRUD. It never holds an Aether engine instance — it talks to
// cmd/scheduler over the internal rpc client (internal/infra/workflow/rpc),
// so it can be scaled horizontally exactly as the architecture requires.
package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"aigc-platform/internal/application/communitysvc"
	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/infra/workflow/rpc"
	httpapi "aigc-platform/internal/interfaces/http"
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

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal("get sql.DB", zap.Error(err))
	}
	eng := rpc.NewClient(config.SchedulerURL())
	credits := creditsvc.New(sqlDB)
	community := communitysvc.New(sqlDB, credits)
	redisClient := cache.NewClient(config.RedisAddr(), config.RedisURL())
	// F1.2's anonymous trial only — see internal/interfaces/http/trial.go's
	// doc for why cmd/api holds a MiniMax client despite the package doc's
	// "never talks to MiniMax directly" rule. jobsvc also needs it now, for
	// video.sequence's "smart" reference-selection mode (see cmd/scheduler/
	// main.go's own jobsvc.New call for the fuller doc).
	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey())
	jobs := jobsvc.New(db, eng, credits, minimaxClient)

	// F2.1's presigned direct-upload endpoints only — see server.go's
	// `objects` field doc. A MinIO outage here must not take the whole API
	// down (every other endpoint doesn't need it), so this degrades to a
	// nil store (upload-url/complete then return 503) instead of
	// log.Fatal'ing the process the way the DB connection above does.
	objectStore, err := storage.New(ctx, storage.Config{
		Endpoint:        config.MinIOEndpoint(),
		AccessKeyID:     config.MinIOAccessKey(),
		SecretAccessKey: config.MinIOSecretKey(),
		UseSSL:          config.MinIOUseSSL(),
		Bucket:          config.MinIOBucket(),
		PublicBaseURL:   config.MinIOPublicBaseURL(),
	})
	if err != nil {
		log.Error("connect object storage — direct asset upload will be unavailable", zap.Error(err))
		objectStore = nil
	}

	srv := httpapi.NewServer(db, jobs, credits, community, redisClient, config.JWTSecret(), minimaxClient, objectStore)

	httpSrv := &http.Server{Addr: config.APIAddr(), Handler: srv.Router()}
	go func() {
		log.Info("api listening", zap.String("addr", config.APIAddr()))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("api http server", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down api")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
