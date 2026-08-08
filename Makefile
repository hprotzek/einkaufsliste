BINARY := bin/api

.PHONY: help build run test vet tidy clean migrate generate lint

help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

## --- Wired up ---------------------------------------------------------------

build: ## Compile the API binary into bin/
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/api

run: ## Run the API from source
	go run ./cmd/api

test: ## Run all tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

tidy: ## Sync go.mod and go.sum
	go mod tidy

clean: ## Remove build output
	rm -rf bin/

## --- Not wired yet ----------------------------------------------------------
## Declared so that every command in CLAUDE.md exists and says which task
## turns it on, rather than failing with "No rule to make target".

migrate: ## goose up (task 0.4)
	@echo "migrate: not wired yet — goose arrives with task 0.4"
	@exit 1

generate: ## sqlc + oapi-codegen + openapi-typescript (tasks 0.5 and 0.9)
	@echo "generate: not wired yet — sqlc arrives with task 0.5, codegen with task 0.9"
	@exit 1

lint: ## golangci-lint (task 0.6)
	@echo "lint: not wired yet — golangci-lint arrives with task 0.6; use 'make vet' meanwhile"
	@exit 1
