// cmd/api is the stateless HTTP entrypoint: auth, validation, job creation
// and reads, event streams. Any number of instances can run side by side;
// jobs are executed by cmd/worker.
package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"aigc-platform/internal/application/communitysvc"
	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/infra/providergw"
	"aigc-platform/internal/infra/realtime"
	"aigc-platform/internal/infra/storage"
	httpapi "aigc-platform/internal/interfaces/http"
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
	redisClient := cache.NewClient(config.RedisAddr(), config.RedisURL())
	asynqClient := asynq.NewClient(cache.AsynqRedisOpt(config.RedisAddr(), config.RedisURL()))
	defer asynqClient.Close()

	credits := creditsvc.New(sqlDB)
	community := communitysvc.New(sqlDB, credits)
	orch := orchestrator.New(orchestrator.Options{
		DB: sqlDB, Dispatcher: orchestrator.NewAsynqDispatcher(asynqClient), Publisher: orchestrator.NewRedisPublisher(redisClient),
		Config:  orchestrator.Config{GateTTL: config.GateTTL()},
		Billing: jobsvc.Billing{Credits: credits}, Hooks: jobsvc.Hooks{},
	})
	// The API makes the planning calls that happen before a plan exists
	// (comic planner, story split, smart shot picks) and the guest trial.
	recorder := providergw.NewRecorder(sqlDB)
	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey()).WithTransport(recorder.Transport("minimax", nil))
	jobs := jobsvc.New(db, orch, credits, minimaxClient)

	// Object storage only backs direct uploads here; without it the rest of
	// the API still works and uploads answer 503.
	objectStore, err := storage.New(ctx, storage.Config{
		Endpoint: config.MinIOEndpoint(), AccessKeyID: config.MinIOAccessKey(), SecretAccessKey: config.MinIOSecretKey(),
		UseSSL: config.MinIOUseSSL(), Bucket: config.MinIOBucket(), PublicBaseURL: config.MinIOPublicBaseURL(),
	})
	if err != nil {
		log.Error("connect object storage; direct asset upload will be unavailable", zap.Error(err))
		objectStore = nil
	}

	hub := realtime.NewHub(ctx, redisClient)
	srv := httpapi.NewServer(db, jobs, credits, community, redisClient, config.JWTSecret(), minimaxClient, objectStore).WithEvents(orch, hub)

	httpSrv := &http.Server{Addr: config.APIAddr(), Handler: srv.Router(), ReadHeaderTimeout: 10 * time.Second}
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
