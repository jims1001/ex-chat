.PHONY: test test-fast test-coverage test-docker docker-up docker-down build run lint

# Fast test isolating backend packages and avoiding frontend node_modules recursive scanning
test:
	go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1

test-fast:
	go test ./test/... -count=1

test-coverage:
	go test ./cmd/... ./internal/... ./pkg/... ./test/... -cover -count=1

docker-up:
	docker compose up -d --wait

docker-down:
	docker compose down

test-docker: docker-up
	DB_DSN='host=localhost user=postgres password=pwd124 dbname=ex_chat port=55432 sslmode=disable TimeZone=Asia/Shanghai' go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server/main.go
