.PHONY: build build-api build-scheduler build-worker build-cli build-migrate \
	docker-up docker-down docker-logs docker-build \
	migrate run-api run-scheduler run-worker web test lint

# --- Local (non-Docker) dev: matches the manual three-process workflow used
# throughout this project's development, just as `make` targets instead of
# retyping the same `go build`/`go run` commands. ---

build: build-api build-scheduler build-worker build-cli build-migrate

build-api:
	go build -o bin/api ./cmd/api

build-scheduler:
	go build -o bin/scheduler ./cmd/scheduler

build-worker:
	go build -o bin/worker ./cmd/worker

build-cli:
	go build -o bin/cli ./cmd/cli

build-migrate:
	go build -o bin/migrate ./cmd/migrate

run-api:
	go run ./cmd/api

run-scheduler:
	go run ./cmd/scheduler

run-worker:
	go run ./cmd/worker

migrate:
	go run ./cmd/migrate

web:
	cd web && npm run dev

test:
	go test ./...

lint:
	cd web && npx oxlint

# --- Docker (deploy/docker-compose.yml): api/scheduler/worker/migrate/web
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
