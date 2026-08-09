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

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
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
	jobs := jobsvc.New(db, eng, credits)
	redisClient := cache.NewClient(config.RedisAddr())
	// F1.2's anonymous trial only — see internal/interfaces/http/trial.go's
	// doc for why cmd/api holds a MiniMax client despite the package doc's
	// "never talks to MiniMax directly" rule.
	minimaxClient := minimax.NewClient(config.MiniMaxBaseURL(), config.MiniMaxAPIKey())

	srv := httpapi.NewServer(db, jobs, redisClient, config.JWTSecret(), minimaxClient)

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
