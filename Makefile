.PHONY: run build test test-integration test-race lint migrate-up migrate-down up down tidy

run:
	go run ./cmd/server

build:
	go build -o bin/harmonia ./cmd/server

# Unit tests only — no external services required.
test:
	go test ./... -short

# Full suite against real Postgres/Redis (docker-compose must be up).
test-integration:
	go test ./... -run Integration -v

test-race:
	go test ./... -race

lint:
	golangci-lint run ./...

migrate-up:
	migrate -database "$${HARMONIA_DATABASE_URL}" -path migrations up

migrate-down:
	migrate -database "$${HARMONIA_DATABASE_URL}" -path migrations down 1

up:
	docker compose up -d

down:
	docker compose down

tidy:
	go mod tidy
	go vet ./...
