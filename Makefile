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

# --- Docker (deploy/docker-compose.yml): api/scheduler/worker/migrate run as
# containers alongside mysql/redis/minio, built from deploy/Dockerfile. The
# frontend is intentionally not containerized here — `make web` above still
# covers it, same as local dev. ---

docker-build:
	docker compose -f deploy/docker-compose.yml build

docker-up:
	docker compose -f deploy/docker-compose.yml up -d

docker-down:
	docker compose -f deploy/docker-compose.yml down

docker-logs:
	docker compose -f deploy/docker-compose.yml logs -f
