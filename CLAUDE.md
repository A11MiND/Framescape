# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

An AIGC content-generation platform (image/video generation via MiniMax, OpenAI and Gemini APIs). Jobs run on an in-house orchestrator (`internal/infra/orchestrator`) that replaced the vendored Aether engine in the v2 overhaul; the overhaul's plan and verification records live in `docs/redesign/` (gitignored, local only). Backend is Go, frontend is React. The authoritative product/architecture reference is `AIGC生成平台-PRD-v1.0.md` (§9 lists current known gaps); `DEV_PLAN.md` tracks what's actually shipped, grouped by session/date in its later sections — check the note near its top before trusting any `§`/`F`-number cross-reference from before the PRD's v0.2→v1.0 rewrite.

## Commands

Backend (from repo root):
```
go build ./...                          # build everything
go vet ./...
go test ./...                           # full suite (many packages skip cleanly if MySQL/Redis aren't reachable)
go test ./internal/interfaces/http/...  # one package
go test ./internal/domain/prompt/... -run TestCompileVideoRefs_MutualExclusion -v  # one test
go test -race ./...                     # do this before considering backend work done, not just a plain pass
```
`make build` / `make run-api` / `make run-worker` / `make run-fakeprovider` / `make test-infra-up` / `make migrate` / `make test` / `make lint` wrap the equivalent `go`/`npm` commands — see the Makefile for the full list including Docker targets (`make docker-up` etc., using `deploy/docker-compose.yml`). `make test-infra-up` starts disposable MySQL/Redis/MinIO on 13316/16386/19000 (`deploy/docker-compose.test.yml`); point `MYSQL_DSN`/`REDIS_ADDR`/`MINIO_*` at them to run the DB-backed tests for real instead of skipping.

Frontend (from `web/`):
```
npm run dev              # vite dev server
npm run build             # tsc -b && vite build — catches errors plain `tsc --noEmit` can miss (noUnusedLocals etc.)
npx tsc --noEmit          # fast typecheck only
npm run lint              # oxlint
npm test                  # vitest (tests/unit, src/**/*.test.ts[x])
npx playwright test       # e2e (tests/e2e, mocked API)
```

Local (non-Docker) dev runs two long-lived Go processes plus Vite: `go run ./cmd/api`, `go run ./cmd/worker`, `npm run dev` in `web/`. For provider-free end-to-end runs start `go run ./cmd/fakeprovider` and set `MINIMAX_BASE_URL=http://127.0.0.1:18090`, `OPENAI_BASE_URL=http://127.0.0.1:18090/v1`. **`go run` does not load `.env`** — export the vars into the shell first (`set -a && source .env && set +a`) or the process silently falls back to `config.go`'s dev defaults (wrong/missing `MINIMAX_API_KEY`, `JWT_SECRET`, etc.). Docker Compose passes `.env` via `env_file` automatically instead. See `.env.example` / `web/.env.example` for what each variable is.

## Architecture

**Process model**: `cmd/api` (HTTP, job creation, reads, per-user SSE `GET /api/v1/stream`) and `cmd/worker` (executes nodes, runs the sweeper and maintenance) are both stateless and can run as many instances as needed; there is no single-instance process. `cmd/worker` serves four asynq queues with separate pools (`v2:interactive`, `v2:video`, `v2:media`, `v2:system`; `WORKER_QUEUES`/`WORKER_CONCURRENCY_*`).

**Orchestrator** (`internal/infra/orchestrator`): `job_nodes` rows are the source of truth for execution state. Every transition is a compare-and-set taken under a lock on the `jobs` row — always lock `jobs` first, then `job_nodes`, then credit rows, or transactions on the same job can deadlock. Duplicate or lost asynq messages are harmless (TaskIDs dedupe, CAS rejects stale deliveries, `Sweep` re-dispatches ready nodes, expires leases and re-publishes events). Async executors (`executor.AsyncPlugin`: MiniMax video/regen) are submitted once, their provider task id is persisted, and they are polled from short tasks (or woken by the provider callback via `Nudge`); a crash re-attaches to the remote task instead of resubmitting. `executor.NoCapacityError` defers a node without consuming an attempt. Gates (`workflow.GateExecutor`) suspend without a worker; `Resume` completes the gate and appends nodes (the video preview's redo/upgrade/concat). State changes write `job_events` in the same transaction (outbox + SSE replay by `Last-Event-ID`).

**Layering**: `internal/domain/workflow` defines `Plan`/`NodeSpec`/`Input` (literal, `From(node, output)`, or `ListOf(...)`) and node/job statuses. `internal/application/workflows` holds pure plan builders for every workflow type; node names (`gen`, `compose`, `concat`, `shot-N`, `panel-N`) are what clients read results from, so keep them stable. `internal/application/jobsvc` resolves characters/presets/planner calls, then `submit` inserts the job, reserves credits (`creditsvc.HoldForJobTx`) and persists the plan (`orchestrator.SubmitTx`) in ONE transaction and dispatches after commit. Other layers: `domain/capability` (model limits), `domain/prompt` (prompt compilation, video ref-role rules), `application/creditsvc`, `application/communitysvc`, `application/review` (post-generation image review follow-up task), `application/upkeep` (Redis-lease-coordinated maintenance), `infra/executor/{minimax,openai,gemini,local,mock}` implementing `infra/executor/spi` (`executor.Plugin`, `SchemaOf`, `BindInputs`, `OutputFrom`, derived from Aether's BSD-3 code), `infra/realtime` (one Redis subscription per api process fanned out to SSE clients), `interfaces/http`.

**Credits invariant**: `balance + held == SUM(credit_ledger.amount)` for every user, and `held == SUM(credit_holds.remaining WHERE status='open')`. Reservations are per job (`credit_holds`): v2 jobs use `HoldForJobTx`/`CommitForJobTx`/`ReleaseJobTx` inside the orchestrator's transactions, so one job's settlement never draws on another job's reservation; overage beyond a job's reservation comes from balance and never drives it negative. The reserved amount is exactly `jobsvc.EstimateCredits` (what the user was quoted); the preview gate's `Resume` reserves exactly `QuoteResume`'s total. `EstimateImageCredits` (one combined-cost floor for n outputs of one call) and `EstimatePerNodeImageCredits` (n independent floors, one per call) are **not interchangeable** — `image.comic4`/`image.sequence` each bill one MiniMax call per panel/shot, so they need the per-node variant both server-side (`jobsvc`) and in the frontend's local estimate mirror (`web/src/lib/pricing.ts`).

**Error codes**: errors a client can act on are `apperr.New(code, message, params...)` (`internal/pkg/apperr`); handlers answer through `writeError`, which takes the HTTP status from `httpapi.ErrorCatalog` and passes `params` (limits, allowed values) for the localized text. Every code used must be in the catalog and vice versa (`TestErrorCatalogMatchesCode`). Step and job failures carry `orchestrator.FailureCodes` in `error_code`; executor failure messages are classified by prefix (`sensitive_content:`, `rate_limited:`, `bad_params:` ...), so keep those prefixes. The frontend localizes by code (both catalogs: `go run ./cmd/cli error-codes`) and never shows `message` or a step's raw error as is.

**MySQL gotcha**: the DSN uses `parseTime=true&loc=UTC`, so DATE columns come back as `time.Time`/`sql.NullTime` from the driver — scanning one into `sql.NullString` silently produces a wrong-format string and any date-equality comparison against it just always fails, with no error. Date-based business logic (e.g. `communitysvc`'s publish streak) computes "today" via `time.Now().UTC()` specifically to match this.

**Frontend**: React 19 + TypeScript + Vite + TanStack Query + Tailwind v4 (see `web/src/lib/api.ts` for the typed API client). Light/dark theme is CSS custom properties; `[data-theme='light']` overrides only the `--color-zinc-*` scale plus a few accent shades — an element with a deliberately fixed (non-theme-reactive) background must pair it with fixed (`neutral-*`, not `zinc-*`) text/border colors, or the light-theme override silently inverts the text to invisible. This codebase avoids literal emoji characters in both UI and comments (default-emoji-presentation codepoints render as colorful glyphs inconsistent with the rest of the icon set) — plain-text-presentation symbols already in use (`◷▤▧◈⬡◍✦✕✓⟳○╱⊗⊘‖●`) are fine; prefer small stroke SVGs or these existing glyphs over a new emoji-range character.

**Testing conventions**: DB/Redis-backed tests open a real connection and skip (`t.Skip`, not fail) when unreachable — see any `*_test.go` under `internal/application/*`, `internal/infra/orchestrator` or `internal/interfaces/http` for the pattern, including reserved fake user-ID ranges and `t.Cleanup`-based row deletion so repeated local runs don't accumulate data. Orchestrator tests drive execution with an in-memory dispatcher and a controllable clock (`harness_test.go`); HTTP tests use the real orchestrator with a recording dispatcher (`testEngine` in `server_test.go`). Tests never call real provider APIs — executor tests point real clients at `httptest.Server` fakes, and multi-process end-to-end/chaos runs use `cmd/fakeprovider`.

**Migrations**: goose-based, sequential-numbered (`migrations/0000N_*.sql`), applied via `go run ./cmd/migrate` (embeds the SQL files, no `goose` CLI needed) — keep numbering contiguous if a migration is added then reverted before landing.
