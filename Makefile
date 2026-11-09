.PHONY: up down logs deps keys test test-integration lint fmt tidy

MODULES := iam-service sse-gateway sse-service

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

## keys: generate a persistent RSA key for signing access tokens (see .env.example)
keys:
	@mkdir -p deploy/keys
	@test -f deploy/keys/jwt.pem || openssl genrsa -out deploy/keys/jwt.pem 2048
	@chmod 644 deploy/keys/jwt.pem
	@echo "Set JWT_PRIVATE_KEY_FILE=/keys/jwt.pem in .env and run make up"

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
