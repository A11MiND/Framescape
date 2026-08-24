# Framescape (帧境)

An AIGC content-generation platform for image and video, built on
[MiniMax](https://www.minimaxi.com/)'s generation API and a vendored
workflow-orchestration engine ([Aether](third_party/aether)). Users compose
structured prompts and reference material into declarative workflows — from
a single image to a multi-shot video sequence — and every generated result
is stored as a reusable asset.

[![CI](https://github.com/A11MiND/Framescape/actions/workflows/ci.yml/badge.svg)](https://github.com/A11MiND/Framescape/actions/workflows/ci.yml)

## Generation modes

- `image.single` / `image.batch` — one prompt, one or more images
- `image.comic4` — a 4-panel comic composed into a single image
- `image.sequence` — a multi-shot image sequence with cross-shot continuity
- `video.single` — text/image-to-video, with first/last-frame reference modes
- `video.sequence` — a multi-shot video, with a preview/finalize gate between
  each shot to control cost

## Architecture

Three independently-scalable Go processes, plus a React frontend:

- **`cmd/api`** — stateless Gin HTTP layer (auth, validation, job CRUD). Never
  holds a workflow engine instance; reaches one over HTTP.
- **`cmd/scheduler`** — hosts the single [Aether](third_party/aether) engine
  instance that actually runs workflow DAGs. Must stay single-instance (see
  [`CLAUDE.md`](CLAUDE.md) for why).
- **`cmd/worker`** — executes tasks (image/video generation, ffmpeg
  composition, etc.) off asynq queues and reports results back to the
  scheduler. Holds no engine instance either, so it scales independently.

Business code is layered domain-driven and never imports Aether directly:

```
internal/
  domain/        — Engine port, capability limits, prompt compilation
  application/    — job orchestration, credits ledger, community, upkeep
  infra/          — persistence (GORM), MiniMax/mock/local executors,
                    the one package allowed to import Aether, storage, cache
  interfaces/http — Gin handlers
```

## Tech stack

Go 1.25 · Gin · GORM · MySQL 8 · Redis 7 · [Aether](third_party/aether)
(vendored workflow engine) · asynq · MinIO · goose migrations · React 19 ·
TypeScript · Vite · TanStack Query · Tailwind v4

## Getting started

### Docker Compose (fastest path)

```bash
cp .env.example .env   # fill in the REQUIRED values, see comments in the file
docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
```

This builds and runs the full stack — MySQL, Redis, MinIO, `api`,
`scheduler`, `worker`, and an nginx-fronted build of the frontend — behind a
single public port (`:80`). See [`deploy/docker-compose.yml`](deploy/docker-compose.yml).

### Local development

Four long-lived processes: three Go binaries plus the Vite dev server.

```bash
set -a && source .env && set +a   # go run does NOT load .env on its own

go run ./cmd/api
go run ./cmd/scheduler
go run ./cmd/worker

cd web && npm install && npm run dev
```

Without a real `MINIMAX_API_KEY`, `cmd/worker` can still run entirely on the
`mock.*` executors for local development and testing.

## Configuration

Copy `.env.example` → `.env` (and `web/.env.example` → `web/.env`) and fill
in the values marked `REQUIRED`. Every variable is documented inline —
MySQL/Redis/MinIO connection info, `JWT_SECRET`, the MiniMax API key, and
optional login providers (Google OAuth, phone/email codes).

## Commands

```bash
go build ./...                          # build everything
go vet ./...
go test ./...                           # full suite — DB/Redis tests skip cleanly if unreachable
go test -race ./...                     # run before considering backend work done

cd web
npm run build                           # tsc -b && vite build
npm run lint                            # oxlint
```

`make build` / `make run-api` / `make run-scheduler` / `make run-worker` /
`make migrate` / `make test` / `make lint` / `make docker-*` wrap the
equivalent commands — see the [`Makefile`](Makefile).

## Project layout

| Path | What lives there |
|---|---|
| `cmd/` | Entry points for the five binaries (api/scheduler/worker/migrate/cli) |
| `internal/` | All business logic, layered as above |
| `web/` | React + TypeScript frontend |
| `third_party/aether/` | Vendored workflow-orchestration engine |
| `workflows/` | Static Aether workflow definitions (`image.single`, `video.single`) |
| `migrations/` | goose SQL migrations |
| `deploy/` | Dockerfile, docker-compose.yml, nginx config, Prometheus/Grafana |
