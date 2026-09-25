.PHONY: build build-api build-worker build-cli build-migrate \
	docker-up docker-down docker-logs docker-build docker-restart-app \
	migrate run-api run-worker run-fakeprovider test-infra-up test-infra-down \
	web test lint error-codes

# --- Local (non-Docker) dev: matches the manual three-process workflow used
# throughout this project's development, just as `make` targets instead of
# retyping the same `go build`/`go run` commands. ---

build: build-api build-worker build-cli build-migrate

build-api:
	go build -o bin/api ./cmd/api

build-worker:
	go build -o bin/worker ./cmd/worker

build-cli:
	go build -o bin/cli ./cmd/cli

build-migrate:
	go build -o bin/migrate ./cmd/migrate

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

# Stand-in for MiniMax/OpenAI: set MINIMAX_BASE_URL=http://127.0.0.1:18090 and
# OPENAI_BASE_URL=http://127.0.0.1:18090/v1 to run the real executors for free.
run-fakeprovider:
	go run ./cmd/fakeprovider

# Disposable MySQL/Redis/MinIO on 13316/16386/19000 (tmpfs, never touches the
# dev stack's volumes) for integration tests.
test-infra-up:
	docker compose -f deploy/docker-compose.test.yml up -d --wait

test-infra-down:
	docker compose -f deploy/docker-compose.test.yml down

migrate:
	go run ./cmd/migrate

web:
	cd web && npm run dev

test:
	go test ./...

lint:
	cd web && npm run lint

# Regenerates the frontend snapshot of the API and failure code catalog;
# npm run lint then reports codes without zh/en text.
error-codes:
	go run ./cmd/cli error-codes > web/src/i18n/error-codes.json

# --- Docker (deploy/docker-compose.yml): api/worker/migrate/web
# (nginx-fronted frontend) run as containers alongside mysql/redis/minio,
# built from deploy/Dockerfile. --env-file is required here because Compose
# only auto-loads .env from the compose file's own directory (deploy/), not
# the invocation cwd (repo root) — without it, the required MYSQL_ROOT_PASSWORD/
# MINIO_ACCESS_KEY/etc substitutions in docker-compose.yml fail to resolve. ---

docker-build:
	docker compose -f deploy/docker-compose.yml --env-file .env build

docker-up:
	docker compose -f deploy/docker-compose.yml --env-file .env up -d

docker-down:
	docker compose -f deploy/docker-compose.yml --env-file .env down

docker-logs:
	docker compose -f deploy/docker-compose.yml --env-file .env logs -f

# api/worker bake PUBLIC_BASE_URL (via MINIO_PUBLIC_BASE_URL)
# into their own env at container-start time — none of them re-read .env
# while running. Run this after PUBLIC_BASE_URL changes in .env (e.g. a new
# Cloudflare quick tunnel address) instead of restarting just
# worker: a forgotten api restart leaves it handing out asset public_urls
# built from the previous, now-dead address, even though uploads/dispatch
# themselves keep working fine.
docker-restart-app:
	docker compose -f deploy/docker-compose.yml --env-file .env up -d --force-recreate api worker
