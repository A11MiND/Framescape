// Package config reads process configuration from the environment. Kept
// deliberately tiny for the POC (PRD §15.1: don't build abstractions the
// project doesn't need yet) — one function per setting, with sane local-dev
// defaults so `go run ./cmd/...` works without a .env file.
package config

import (
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// OpenAI's native Image API powers the four-panel comic editor (openai.image).
// The key lives on API (to gate job creation) and worker (to call OpenAI) only.
func OpenAIAPIKey() string { return getEnv("OPENAI_API_KEY", "") }
func OpenAIBaseURL() string {
	return getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1")
}
func OpenAIImageModel() string { return getEnv("OPENAI_IMAGE_MODEL", "gpt-image-2.5-flare") }

// Accounting conversion, not a live exchange-rate quote. Set consistently on API and worker.
func OpenAIUSDToCNY() float64 { return positiveFloat("OPENAI_USD_TO_CNY", 7) }

// Per-call credit reservation, also billed when OpenAI omits usage. Not a
// provider-side spending cap. A high-quality 1536x1024 page is ~$0.05-0.15;
// the unused remainder is refunded when the job ends.
func OpenAIImageReserveUSD() float64 { return positiveFloat("OPENAI_IMAGE_RESERVE_USD", 0.50) }

// Extra reservation per attached reference image (each is billed as image
// input tokens), added on top of OpenAIImageReserveUSD.
func OpenAIImageReservePerRefUSD() float64 {
	return positiveFloat("OPENAI_IMAGE_RESERVE_PER_REF_USD", 0.03)
}
func positiveFloat(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(getEnv(key, ""), 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

const devOnlyJWTSecret = "dev-only-insecure-secret-change-me"

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
// Only used when RedisURL is unset — a bare host:port has no way to carry
// auth, so it's the local-dev (unauthenticated Redis) path only.
func RedisAddr() string { return getEnv("REDIS_ADDR", "127.0.0.1:6379") }

// RedisURL is a full "redis://[:password@]host:port[/db]" connection
// string — set this (Railway's managed Redis exposes it as REDIS_URL) for
// any deployment where Redis requires authentication; see cache.NewClient/
// AsynqRedisOpt for why REDIS_ADDR alone can't express that. Empty means
// "fall back to RedisAddr", same as before this variable existed.
func RedisURL() string { return getEnv("REDIS_URL", "") }

// WorkerQueues lists the queues this worker serves (comma separated):
// interactive, video, media, system. Empty means all of them.
func WorkerQueues() string { return getEnv("WORKER_QUEUES", "") }

// Per-queue worker pool sizes. Provider calls are I/O bound, so the
// interactive and video pools are large; media runs ffmpeg and is CPU bound.
func WorkerConcurrencyInteractive() int { return getEnvInt("WORKER_CONCURRENCY_INTERACTIVE", 64) }
func WorkerConcurrencyVideo() int       { return getEnvInt("WORKER_CONCURRENCY_VIDEO", 32) }
func WorkerConcurrencyMedia() int       { return getEnvInt("WORKER_CONCURRENCY_MEDIA", 0) }
func WorkerConcurrencySystem() int      { return getEnvInt("WORKER_CONCURRENCY_SYSTEM", 4) }

// WorkerMetricsAddr serves the worker's /metrics and health endpoints.
func WorkerMetricsAddr() string { return getEnv("WORKER_METRICS_ADDR", ":8091") }

// MySQLMaxOpenConns/MySQLMaxIdleConns size each process's connection pool.
func MySQLMaxOpenConns() int { return getEnvInt("MYSQL_MAX_OPEN_CONNS", 50) }
func MySQLMaxIdleConns() int { return getEnvInt("MYSQL_MAX_IDLE_CONNS", 25) }

// JWTAccessTTLMinutes is the access-token lifetime; 0 keeps one hour.
func JWTAccessTTLMinutes() int { return getEnvInt("JWT_ACCESS_TTL_MINUTES", 0) }

// CORSAllowedOrigins lists browser origins allowed to call the API from
// another origin (comma separated). Empty allows any origin outside
// production and none in production, where the frontend is same-origin.
func CORSAllowedOrigins() []string {
	var out []string
	for _, o := range strings.Split(getEnv("CORS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// APIAddr is the address cmd/api's Gin server listens on.
func APIAddr() string { return getEnv("API_ADDR", ":8080") }

// JWTSecret signs access/refresh tokens. Falls back to a known literal for
// zero-config local dev, but refuses to start under APP_ENV=prod with that
// fallback still in place — silently signing every token with a value
// that's sitting in this file would let anyone forge a valid session.
func JWTSecret() string {
	secret := getEnv("JWT_SECRET", devOnlyJWTSecret)
	if secret == devOnlyJWTSecret && Env() == "prod" {
		log.Fatal("JWT_SECRET must be set to a real secret when APP_ENV=prod")
	}
	return secret
}

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

// --- Orchestration ---

// UserActiveNodeLimit caps one user's provider steps running at once;
// further steps queue with reason user_limit (0 = unlimited).
func UserActiveNodeLimit() int { return getEnvInt("LIMIT_USER_ACTIVE_NODES", 4) }

// ExecutorConcurrencyLimits caps cluster-wide running steps per executor,
// as "type=n,type=n" (for example "minimax.image=20,openai.image=8"). Keep
// these at or below each provider account's concurrency quota.
func ExecutorConcurrencyLimits() map[string]int {
	out := map[string]int{}
	for _, part := range strings.Split(getEnv("LIMIT_EXECUTOR_CONCURRENCY", ""), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if n, err := strconv.Atoi(strings.TrimSpace(v)); ok && err == nil && n > 0 {
			out[strings.TrimSpace(k)] = n
		}
	}
	return out
}

// GateTTL is how long a preview gate waits for a decision before the job is
// cancelled and its reservation released; 0 keeps the 7-day default.
func GateTTL() time.Duration {
	seconds := getEnvInt("GATE_TTL_SECONDS", 0)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// --- Gemini / Vertex AI (image.comic4's optional second image-generation
// provider, alongside MiniMax — see internal/infra/executor/gemini). Auth is
// never read here: the Vertex AI Go SDK authenticates via Application
// Default Credentials, so a service-account key file is wired in purely by
// pointing the standard GOOGLE_APPLICATION_CREDENTIALS env var at it (same
// posture as every other provider credential in this codebase — never
// read/parsed/logged by our own code). ---

// GeminiImagePriceYuan is the CNY cost of one Gemini output image used for
// settlement and spend reporting (list price $0.039 at 7.2 CNY/USD); keep it
// in line with the Vertex AI price of GEMINI_IMAGE_MODEL.
func GeminiImagePriceYuan() float64 { return positiveFloat("GEMINI_IMAGE_PRICE_YUAN", 0.28) }

// GeminiVertexProjectID is the GCP project ID Vertex AI calls bill against.
// Empty (the default) disables Gemini registration entirely in cmd/worker —
// this feature is opt-in, MiniMax remains comic4's default provider.
func GeminiVertexProjectID() string { return getEnv("GEMINI_VERTEX_PROJECT_ID", "") }

// GeminiVertexLocation is the Vertex AI region GenerateContent calls target.
func GeminiVertexLocation() string { return getEnv("GEMINI_VERTEX_LOCATION", "us-central1") }

// GeminiImageModel is the GA (non-preview) image-output Gemini model name.
func GeminiImageModel() string { return getEnv("GEMINI_IMAGE_MODEL", "gemini-2.5-flash-image") }

// GeminiVertexCredentialsJSON is a service-account key file's raw JSON
// content, for a deployment target (Railway) with no clean way to hand the
// SDK a file path — environment variables, not files, are what survives a
// redeploy there. Empty (the default, every local/GCE deployment) leaves
// authentication to GOOGLE_APPLICATION_CREDENTIALS/Application Default
// Credentials entirely, same as before this variable existed. Never
// logged — this function's return value must never be passed to a logger,
// same posture as MiniMaxAPIKey's own doc.
func GeminiVertexCredentialsJSON() string { return getEnv("GEMINI_VERTEX_CREDENTIALS_JSON", "") }

// GeminiVertexConcurrency caps concurrent Vertex AI generateContent calls
// (gemini.Limiter's own doc: found live that comic4's anchor-mode DAG, which
// deliberately fires every panel's generation in parallel, reliably tripped
// a fresh GCP project's default per-project quota — one of four concurrent
// panels came back "429 RESOURCE_EXHAUSTED" almost every time). 0 disables
// the limiter (unbounded) — fine once this project's quota is confirmed
// sufficient, but until then set this to a conservative number like 1 or 2.
func GeminiVertexConcurrency() int { return getEnvInt("GEMINI_VERTEX_CONCURRENCY", 0) }

// GeminiVertexMinIntervalMs paces gemini.Limiter's second gate: no new
// Vertex AI call starts until this many milliseconds have passed since the
// previous one started, regardless of concurrency slots free. 0 disables it.
// Added after live testing found GeminiVertexConcurrency alone
// insufficient — two calls that started simultaneously (concurrency check
// passed for both) still both came back 429 RESOURCE_EXHAUSTED from Vertex
// AI Express Mode, evidence the real ceiling is a requests-per-time-window
// quota, not a concurrent-connections one.
func GeminiVertexMinIntervalMs() int { return getEnvInt("GEMINI_VERTEX_MIN_INTERVAL_MS", 0) }

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
