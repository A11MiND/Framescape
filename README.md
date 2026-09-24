# Framescape (帧境)

An AIGC content-generation platform for image and video, built on MiniMax,
OpenAI and Gemini generation APIs. Users compose structured prompts and
reference material into generation jobs — from a single image to a
multi-shot video sequence with a preview review step — and every generated
result is stored as a reusable asset.

[![CI](https://github.com/A11MiND/Framescape/actions/workflows/ci.yml/badge.svg)](https://github.com/A11MiND/Framescape/actions/workflows/ci.yml)

## Generation modes

- `image.single` / `image.batch` — one prompt, one or more images
- `image.comic4` — a 4-panel comic composed into a single image
- `image.sequence` — a multi-shot image sequence with cross-shot continuity
- `video.single` — text/image-to-video, with first/last-frame reference modes
- `video.sequence` — a multi-shot video, with a preview/finalize gate between
  each shot to control cost

## Architecture

Two horizontally scalable Go processes plus a React frontend. Neither holds
job state in memory; any instance can be stopped or killed at any time.

- **`cmd/api`** — Gin HTTP layer: auth, validation, pricing quotes, job
  creation (job row, credit reservation and plan in one transaction), reads,
  and a per-user SSE event stream.
- **`cmd/worker`** — runs job nodes from asynq queues (`interactive`,
  `video`, `media`, `system`, each with its own pool), sweeps for work a
  crash or lost message left behind, and runs maintenance.

Execution state lives in MySQL (`job_nodes`); every transition is a
compare-and-set, so duplicated or lost queue messages are harmless. Remote
provider tasks (video) are submitted once and polled from short tasks, so
waiting never occupies a worker slot and a worker restart never resubmits
a paid task. See `internal/infra/orchestrator`.

```
internal/
  domain/         — plan and status types, capability limits, prompt compilation
  application/    — job lifecycle (jobsvc), plan builders (workflows),
                    credits, community, review, upkeep
  infra/          — orchestrator, realtime fan-out, persistence, executors
                    (MiniMax/OpenAI/Gemini/local/mock), storage, cache
  interfaces/http — Gin handlers
```

`cmd/fakeprovider` imitates MiniMax and OpenAI (configurable latency, errors
and rate limits) for local end-to-end and load testing without paid calls.

## Tech stack

Go 1.25 · Gin · GORM · MySQL 8 · Redis 7 · asynq · MinIO · goose migrations ·
React 19 · TypeScript · Vite · TanStack Query · Tailwind v4

## Getting started

### Docker Compose (fastest path)

```bash
cp .env.example .env   # fill in the REQUIRED values, see comments in the file
docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
```

This builds and runs the full stack — MySQL, Redis, MinIO, `api`,
`worker`, and an nginx-fronted build of the frontend — behind a
single public port (`:80`). See [`deploy/docker-compose.yml`](deploy/docker-compose.yml).

### Local development

Three long-lived processes: two Go binaries plus the Vite dev server. Run as
many api/worker instances as you like.

```bash
set -a && source .env && set +a   # go run does NOT load .env on its own

go run ./cmd/api
go run ./cmd/worker

cd web && npm install && npm run dev
```

Without real provider keys, run `make run-fakeprovider` and point
`MINIMAX_BASE_URL=http://127.0.0.1:18090` and
`OPENAI_BASE_URL=http://127.0.0.1:18090/v1` at it; the real executors then run
end to end at no cost. `make test-infra-up` starts disposable
MySQL/Redis/MinIO for tests.

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
npm test                                # vitest
```

`make build` / `make run-api` / `make run-worker` / `make run-fakeprovider` /
`make migrate` / `make test` / `make lint` / `make docker-*` wrap the
equivalent commands — see the [`Makefile`](Makefile).

## Project layout

| Path | What lives there |
|---|---|
| `cmd/` | Entry points: api, worker, migrate, cli, fakeprovider (dev/test) |
| `internal/` | All business logic, layered as above |
| `web/` | React + TypeScript frontend |
| `migrations/` | goose SQL migrations |
| `deploy/` | Dockerfile, docker-compose.yml, nginx config, Prometheus/Grafana |
