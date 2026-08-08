BINARY := bin/api

# Single source of truth for tool versions, shared with ci.yml so a local run
# and a CI run cannot disagree.
GOLANGCI_LINT_VERSION := $(shell cat .golangci-lint-version)
GOOSE_VERSION := $(shell cat .goose-version)
SQLC_VERSION := $(shell cat .sqlc-version)
OAPI_CODEGEN_VERSION := $(shell cat .oapi-codegen-version)

# Local development default. The binary itself requires DATABASE_URL with no
# fallback — a default pointing at localhost would let a misconfigured deploy
# start against the wrong database.
DEV_DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/einkaufsliste?sslmode=disable
DATABASE_URL ?= $(DEV_DATABASE_URL)

# goose reads the migrations from disk here. In production the same files are
# embedded in the binary and applied at start-up instead (spec §12.3), so this
# is a development convenience, not a second source of truth.
GOOSE := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir migrations postgres "$(DATABASE_URL)"

.PHONY: help build run test vet lint tidy clean migrate migrate-status migrate-create generate

help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Build and run ----------------------------------------------------------

build: ## Compile the API binary into bin/
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/api

run: ## Run the API from source against DATABASE_URL
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/api

clean: ## Remove build output
	rm -rf bin/

## --- Checks -----------------------------------------------------------------

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

## --- Database ---------------------------------------------------------------

migrate: ## Apply pending migrations (goose up)
	$(GOOSE) up

migrate-status: ## Show which migrations are applied
	$(GOOSE) status

migrate-create: ## Create a migration: make migrate-create NAME=add_users
	@test -n "$(NAME)" || { echo "NAME is required, e.g. make migrate-create NAME=add_users"; exit 1; }
	go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir migrations create $(NAME) sql

## --- Codegen ----------------------------------------------------------------

generate: ## Regenerate sqlc queries and both sides of openapi.yaml
	@command -v sqlc >/dev/null 2>&1 || { \
		echo "sqlc not found. Install the pinned version with:"; \
		echo "  go install github.com/sqlc-dev/sqlc/cmd/sqlc@v$(SQLC_VERSION)"; \
		exit 1; \
	}
	sqlc generate
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
		-config api/oapi-codegen.yaml api/openapi.yaml
	@test -d web/node_modules || npm --prefix web ci
	npm --prefix web run generate

tidy: ## Sync go.mod and go.sum
	go mod tidy
