.PHONY: help run build test lint fmt vet tidy migrate-up migrate-down sqlc docker-up docker-down

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the binary
	go build -o bin/api ./cmd/api

test: ## Run tests with the race detector
	go test ./... -race -cover

lint: ## Run static analysis
	golangci-lint run

fmt: ## Format code
	gofmt -l -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Sync go.mod/go.sum
	go mod tidy

sqlc: ## Regenerate typed queries
	sqlc generate

migrate-up: ## Apply all migrations
	migrate -path db/migrations -database "$$DATABASE_URL" up

migrate-down: ## Roll back one migration
	migrate -path db/migrations -database "$$DATABASE_URL" down 1

docker-up: ## Start Postgres (and later the app)
	docker compose up -d

docker-down: ## Stop and remove containers
	docker compose down