// Package config reads process configuration from the environment. Kept
// deliberately tiny for the POC (PRD §15.1: don't build abstractions the
// project doesn't need yet) — one function per setting, with sane local-dev
// defaults so `go run ./cmd/...` works without a .env file.
package config

import (
	"os"
	"strconv"
	"time"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// MySQLDSN returns the GORM/MySQL driver DSN.
func MySQLDSN() string {
	return getEnv("MYSQL_DSN", "root:root@tcp(127.0.0.1:3306)/aigc?parseTime=true&loc=UTC&charset=utf8mb4")
}

// RedisAddr is shared by asynq (queue backend) and Pub/Sub (SSE fan-out).
func RedisAddr() string { return getEnv("REDIS_ADDR", "127.0.0.1:6379") }

// WorkerConcurrency bounds how many tasks cmd/worker executes at once.
func WorkerConcurrency() int { return getEnvInt("WORKER_CONCURRENCY", 8) }

// APIAddr is the address cmd/api's Gin server listens on.
func APIAddr() string { return getEnv("API_ADDR", ":8080") }

// SchedulerAddr is the address cmd/scheduler's internal HTTP server listens on.
func SchedulerAddr() string { return getEnv("SCHEDULER_ADDR", ":8090") }

// SchedulerURL is how cmd/api reaches cmd/scheduler's internal API.
func SchedulerURL() string { return getEnv("SCHEDULER_URL", "http://127.0.0.1:8090") }

// JWTSecret signs access/refresh tokens. Must be overridden outside local dev.
func JWTSecret() string { return getEnv("JWT_SECRET", "dev-only-insecure-secret-change-me") }

// Env is "dev" or "prod"; controls logger formatting (internal/pkg/logger).
func Env() string { return getEnv("APP_ENV", "dev") }

// --- MinIO / object storage (PRD F2.2/R3: materialize before provider URLs expire) ---

func MinIOEndpoint() string  { return getEnv("MINIO_ENDPOINT", "127.0.0.1:9000") }
func MinIOAccessKey() string { return getEnv("MINIO_ACCESS_KEY", "minioadmin") }
func MinIOSecretKey() string { return getEnv("MINIO_SECRET_KEY", "minioadmin") }
func MinIOUseSSL() bool      { return getEnv("MINIO_USE_SSL", "false") == "true" }
func MinIOBucket() string    { return getEnv("MINIO_BUCKET", "aigc-assets") }
func MinIOPublicBaseURL() string {
	return getEnv("MINIO_PUBLIC_BASE_URL", "http://127.0.0.1:9000/aigc-assets")
}

// --- MiniMax (PRD §3) ---

// MiniMaxAPIKey must come from the environment only — never hardcoded,
// logged, or committed (see .env, which is gitignored).
func MiniMaxAPIKey() string  { return getEnv("MINIMAX_API_KEY", "") }
func MiniMaxBaseURL() string { return getEnv("MINIMAX_BASE_URL", "https://api.minimaxi.com") }

// MiniMaxCallbackURL is the public URL MiniMax should POST video status
// pushes to (§3.3). Empty by default — a dev machine behind NAT has no
// public endpoint, so minimax.video falls back to polling-only (§11.3).
func MiniMaxCallbackURL() string { return getEnv("MINIMAX_CALLBACK_URL", "") }

// MiniMaxCallbackToken is the shared secret checked against the callback
// request's ?token= query param (see internal/interfaces/http/callbacks.go
// for why this substitutes for header-signature verification). Empty
// disables the check — fine for local dev, must be set before exposing the
// callback endpoint publicly.
func MiniMaxCallbackToken() string { return getEnv("MINIMAX_CALLBACK_TOKEN", "") }

// MiniMaxVideoConcurrency is this account's granted MiniMax video generation
// concurrency quota (§11.1/§11.2 — MiniMax doesn't expose this via API, so
// it's a manually-configured number). 0 disables the limiter (unbounded) —
// acceptable for a POC dev account making one test call at a time, but must
// be set to the real granted quota before any concurrent load.
func MiniMaxVideoConcurrency() int { return getEnvInt("MINIMAX_VIDEO_CONCURRENCY", 0) }

// --- Scheduler upkeep (§11.4) ---

// SuspendedTimeout overrides upkeep.SuspendedTimeout's 7-day default (0 or
// unset means "use the default"). Exists for verification (waiting 7 real
// days to confirm the cleanup path works isn't practical) and so ops can
// tune it without a recompile.
func SuspendedTimeout() time.Duration {
	seconds := getEnvInt("SUSPENDED_TIMEOUT_SECONDS", 0)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// --- Google / phone login scaffolding — routes, DB columns, and frontend
// entry points exist regardless (auth_oauth.go), but every one of them
// checks these first and answers "not configured" (a real 4xx, not a
// silently-broken login) until real credentials land here. Nothing in this
// codebase should ever hardcode a fallback for either. ---

// GoogleClientID is the OAuth 2.0 web client ID from Google Cloud Console
// (APIs & Services → Credentials) — also the ID token's expected `aud`
// claim, checked in auth_oauth.go's verifyGoogleIDToken. Empty disables
// Google login entirely (handleGoogleLogin's own doc).
func GoogleClientID() string { return getEnv("GOOGLE_CLIENT_ID", "") }

// SMSAPIKey gates phone login the same way — provider unspecified on
// purpose (§ auth_oauth.go's sendSMSCode doc: this POC never picked one,
// so there's nothing to configure a base URL/region for yet either).
func SMSAPIKey() string { return getEnv("SMS_API_KEY", "") }

// EmailProviderAPIKey gates handleRegister's email-verification-code step
// (auth_oauth.go's sendEmailCode doc) — empty (the default) means
// registration works exactly as it always has, no code required.
func EmailProviderAPIKey() string { return getEnv("EMAIL_PROVIDER_API_KEY", "") }
