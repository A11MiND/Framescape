# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

An AIGC content-generation platform (image/video generation via MiniMax's API) built on a vendored workflow-orchestration engine called Aether (`third_party/aether`). Backend is Go, frontend is React. The authoritative product/architecture reference is `AIGC生成平台-PRD-v1.0.md` (§9 lists current known gaps); `DEV_PLAN.md` tracks what's actually shipped, grouped by session/date in its later sections — check the note near its top before trusting any `§`/`F`-number cross-reference from before the PRD's v0.2→v1.0 rewrite.

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
`make build` / `make run-api` / `make run-scheduler` / `make run-worker` / `make migrate` / `make test` / `make lint` wrap the equivalent `go`/`npm` commands — see the Makefile for the full list including Docker targets (`make docker-up` etc., using `deploy/docker-compose.yml`).

Frontend (from `web/`):
```
npm run dev              # vite dev server
npm run build             # tsc -b && vite build — catches errors plain `tsc --noEmit` can miss (noUnusedLocals etc.)
npx tsc --noEmit          # fast typecheck only
npm run lint              # oxlint
```
There is no frontend test runner configured (no vitest/jest) — `npm run build` and `npm run lint` are the frontend's checks.

Local (non-Docker) dev runs three long-lived Go processes plus Vite: `go run ./cmd/api`, `go run ./cmd/scheduler`, `go run ./cmd/worker`, `npm run dev` in `web/`. **`go run` does not load `.env`** — export the vars into the shell first (`set -a && source .env && set +a`) or the process silently falls back to `config.go`'s dev defaults (wrong/missing `MINIMAX_API_KEY`, `JWT_SECRET`, etc.). Docker Compose passes `.env` via `env_file` automatically instead. See `.env.example` / `web/.env.example` for what each variable is.

## Architecture

**Three-process split** (PRD §4): `cmd/api` is a stateless Gin HTTP layer (auth, validation, job CRUD) that can be horizontally scaled — it never holds an Aether Engine instance, only reaches one through `internal/infra/workflow/rpc`'s HTTP bridge. `cmd/scheduler` hosts the single Aether Engine instance; **it must stay single-instance** — `internal/infra/workflow/aether/broker_asynq.go`'s `RunControlConsumer` relies on in-process message ordering (a worker's "started" report must be processed before its "completed" report) that a second scheduler process would silently break, with no startup-time warning. `cmd/worker` executes tasks (image/video generation, composition, etc.) via asynq queues and reports back to the scheduler; it holds no Engine instance either, so it scales independently.

**Domain-driven layering** — business code must never import Aether directly (a code/architecture convention, enforced by package structure rather than a build rule — not restated as a numbered PRD requirement post-rewrite, but still real): `internal/domain/workflow` defines the `Engine` port (`Submit`/`Get`/`Cancel`/`Resume`) that `internal/infra/workflow/aether` is the only implementer of. Layers:
- `internal/domain/` — `workflow` (the Engine port), `capability` (single source of truth for model limits — max batch size, video duration range, resolutions/ratios — read by both `jobsvc`'s validation and `GET /api/v1/capabilities`), `prompt` (PromptSpec compilation: character/preset expansion + length trim for images in `compiler.go`; video reference-role-mapping + t2va/i2va/r2va mutual-exclusion rules in `video_refs.go`).
- `internal/application/` — `jobsvc` (job creation/lookup/cancel/retry; also where the three dynamically-generated-DAG workflow types live, see below), `creditsvc` (hold/commit/refund with a real invariant, see below), `communitysvc` (publish streak/heatmap), `projection` (turns every Aether state change into a `job_nodes` upsert + Redis pub/sub event for SSE — also where credit commit/refund and moderation-record writes actually happen, driven off task/workflow completion), `upkeep` (background cleanup: suspended-job timeout, trash auto-purge, credit reconciliation).
- `internal/infra/` — `persistence` (GORM models + a couple of raw-SQL asset/file-cache adapters), `executor/minimax` (the real paid-API integration: image/video/prompt-enhance/story-split, each exposed as an `executor.Plugin` registered under a `minimax.*` type), `executor/mock` (same plugin shape, `mock.*` types, for local dev without hitting the real API), `executor/local` (compose/extract-frames/concat/gate — ffmpeg-backed), `workflow/aether` (the one package allowed to import the vendored engine), `workflow/rpc` (the HTTP bridge cmd/api uses to reach cmd/scheduler), `storage` (MinIO), `cache` (Redis/asynq wiring).
- `internal/interfaces/http` — Gin handlers, one file per resource group (`auth.go`, `jobs.go`, `assets.go`, `credits.go`, etc.).

**Workflow definitions**: `image.single` and `video.single` load a static `workflows/*.json` document. `image.sequence`, `image.comic4`, and `video.sequence` generate their DAG in Go instead (in `jobsvc`, e.g. `image_sequence.go`) because their shape depends on a per-job shot/panel count and cross-shot runtime dependencies that a static Aether `Loop` can't express — this is deliberate, not a migration in progress. Every `minimax.*`/`mock.*` executor plugin declares its `Config`/output shape via `executor.SchemaOf[...]`, and `executor.BindInputs`/`OutputFrom` map `model.Parameter` JSON values to/from those Go structs by matching each field's `json` tag against the parameter's `name`.

**Credits invariant**: `balance + held == SUM(credit_ledger.amount)` for every user, enforced by `creditsvc`'s `Hold`/`Commit`/`Refund` all going through the same ledger-writing transaction helper. `Hold` happens before `Engine.Submit`, never after (a failed hold must never let a job start). `EstimateImageCredits` (one combined-cost floor for n outputs of one call) and `EstimatePerNodeImageCredits` (n independent floors, one per call) are **not interchangeable** — `image.comic4`/`image.sequence` each bill one MiniMax call per panel/shot, so they need the per-node variant both server-side (`jobsvc`) and in the frontend's local estimate mirror (`web/src/lib/pricing.ts`).

**MySQL gotcha**: the DSN uses `parseTime=true&loc=UTC`, so DATE columns come back as `time.Time`/`sql.NullTime` from the driver — scanning one into `sql.NullString` silently produces a wrong-format string and any date-equality comparison against it just always fails, with no error. Date-based business logic (e.g. `communitysvc`'s publish streak) computes "today" via `time.Now().UTC()` specifically to match this.

**Frontend**: React 19 + TypeScript + Vite + TanStack Query + Tailwind v4 (see `web/src/lib/api.ts` for the typed API client). Light/dark theme is CSS custom properties; `[data-theme='light']` overrides only the `--color-zinc-*` scale plus a few accent shades — an element with a deliberately fixed (non-theme-reactive) background must pair it with fixed (`neutral-*`, not `zinc-*`) text/border colors, or the light-theme override silently inverts the text to invisible. This codebase avoids literal emoji characters in both UI and comments (default-emoji-presentation codepoints render as colorful glyphs inconsistent with the rest of the icon set) — plain-text-presentation symbols already in use (`◷▤▧◈⬡◍✦✕✓⟳○╱⊗⊘‖●`) are fine; prefer small stroke SVGs or these existing glyphs over a new emoji-range character.

**Testing conventions**: DB/Redis-backed tests open a real connection and skip (`t.Skip`, not fail) when unreachable — see any `*_test.go` under `internal/application/*` or `internal/interfaces/http` for the pattern, including reserved fake user-ID ranges and `t.Cleanup`-based row deletion so repeated local runs don't accumulate data. Tests never call the real MiniMax API — `internal/infra/executor/minimax`'s and `internal/interfaces/http`'s tests instead point a real client at a local `httptest.Server` that fakes MiniMax's response shapes.

**Migrations**: goose-based, sequential-numbered (`migrations/0000N_*.sql`), applied via `go run ./cmd/migrate` (embeds the SQL files, no `goose` CLI needed) — keep numbering contiguous if a migration is added then reverted before landing.
