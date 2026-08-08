BINARY := bin/api

# Single source of truth for the linter version, shared with ci.yml so a local
# run and a CI run cannot disagree.
GOLANGCI_LINT_VERSION := $(shell cat .golangci-lint-version)

.PHONY: help build run test vet lint tidy clean migrate generate

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

lint: ## Run golangci-lint (version from .golangci-lint-version)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install the pinned version with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	}
	golangci-lint run

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
