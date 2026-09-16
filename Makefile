.PHONY: test test-fast test-coverage test-race test-stress test-soak test-docker docker-up docker-down build run lint

# Fast test isolating backend packages and avoiding frontend node_modules recursive scanning
test:
	go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1

test-fast:
	go test ./test/... -count=1

test-coverage:
	go test ./cmd/... ./internal/... ./pkg/... ./test/... -cover -count=1

test-race:
	go test -race ./cmd/... ./internal/... ./pkg/... ./test/... -count=1

STRESS_COUNT ?= 10
test-stress:
	@set -eu; \
	i=1; \
	while [ "$$i" -le "$(STRESS_COUNT)" ]; do \
		echo "stress iteration $$i/$(STRESS_COUNT)"; \
		go test ./test/... -count=1; \
		i=$$((i + 1)); \
	done

SOAK_DURATION ?= 10m
test-soak:
	SOAK_DURATION=$(SOAK_DURATION) ./scripts/soak-test.sh

docker-up:
	docker compose up -d --wait

docker-down:
	docker compose down

test-docker:
	@set -eu; \
	project='ex_chat_test'; \
	export COMPOSE_PROJECT_NAME="$$project" POSTGRES_PORT=55433 REDIS_PORT=6380; \
	export POSTGRES_PASSWORD='ex-chat-local-test-postgres' REDIS_PASSWORD='ex-chat-local-test-redis'; \
	cleanup() { docker compose down -v --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	docker compose up -d --wait; \
	DB_DSN='host=localhost user=postgres password=ex-chat-local-test-postgres dbname=ex_chat port=55433 sslmode=disable TimeZone=Asia/Shanghai' \
		go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server/main.go
