.PHONY: up down logs run deps test test-integration cover lint fmt

TEST_DATABASE_URL ?= postgres://app:app@localhost:5433/contents?sslmode=disable
TEST_REDIS_URL    ?= redis://localhost:6379/15

## up: build and start the app with PostgreSQL and Redis
up:
	docker compose up --build -d
	@echo "Dashboard: http://localhost:8080  |  API docs: http://localhost:8080/docs"

## down: stop everything and remove volumes
down:
	docker compose down -v

logs:
	docker compose logs -f app

## deps: start only PostgreSQL and Redis (for `make run` / integration tests)
deps:
	docker compose up -d --wait postgres redis

## run: run the app locally against `make deps` (reads .env if present)
run:
	set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/server

## test: unit tests
test:
	go test -race ./...

## test-integration: unit + integration tests against real PostgreSQL and Redis
test-integration: deps
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_REDIS_URL="$(TEST_REDIS_URL)" go test -race -tags=integration ./...

cover:
	go test -tags=integration -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

lint:
	go vet ./...
	golangci-lint run

fmt:
	gofmt -w .
