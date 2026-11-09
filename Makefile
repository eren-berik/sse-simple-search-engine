.PHONY: up down logs deps test test-integration lint fmt tidy

MODULES := sse-service

TEST_DATABASE_URL ?= postgres://app:app@localhost:5433/contents?sslmode=disable
TEST_REDIS_URL    ?= redis://localhost:6379/15

## up: build and start all services
up:
	docker compose up --build -d
	@echo "Dashboard: http://localhost:8080  |  API docs: http://localhost:8080/docs"

## down: stop everything and remove volumes
down:
	docker compose down -v

logs:
	docker compose logs -f

## deps: start only PostgreSQL and Redis
deps:
	docker compose up -d --wait postgres redis

## test: unit tests of every module
test:
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go test -race ./...) || exit 1; done

## test-integration: unit + integration tests against real PostgreSQL and Redis
test-integration: deps
	@for m in $(MODULES); do echo "==> $$m"; \
		(cd $$m && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_REDIS_URL="$(TEST_REDIS_URL)" go test -race -tags=integration ./...) || exit 1; done

lint:
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go vet ./... && golangci-lint run ./...) || exit 1; done

fmt:
	gofmt -w .

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy); done
