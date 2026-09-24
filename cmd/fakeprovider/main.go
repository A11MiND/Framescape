// cmd/fakeprovider is a development/test stand-in for the paid providers
// (MiniMax image/video/text/files, OpenAI images). Point MINIMAX_BASE_URL
// and OPENAI_BASE_URL at it to exercise the real executor HTTP code paths,
// failure handling and load behavior without spending money. Latency,
// error and rate-limit probabilities are configurable at startup and at
// runtime (POST /_fake/config) so chaos and load tests can change them
// mid-run; GET /_fake/stats reports per-endpoint call counts, which is how
// tests assert that no paid call was issued twice.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := configFromEnv()
	srv := newServer(cfg)

	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.routes(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("fakeprovider listening on %s (public URL %s)", cfg.Addr, cfg.PublicURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	srv.close()
	os.Exit(0)
}
