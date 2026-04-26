# Build variables
BINARY_NAME=ocms
BUILD_DIR=bin
GO=go
MAIN_DIR=./cmd/ocms
OCMS_SESSION_SECRET ?= test-secret-key-32-bytes-long!!!
GOLANGCI_LINT_VERSION := v2.11.4
GOFUMPT_VERSION       := v0.9.2

.DEFAULT_GOAL := help

.PHONY: all help build build-prod build-linux-amd64 build-darwin-arm64 build-all-platforms \
        test test-race coverage coverage-html fmt fmt-check vet lint lint-go check deps tidy clean install-tools \
        run stop restart clean-db migrate-up migrate-down migrate-status migrate-create assets dev sqlc templ deploy-binary \
        commit-prepare commit-do code-quality security-audit commit-prepare-local commit-do-local code-quality-local \
        security-audit-local install-hooks check-no-absolute-paths

# Version info from git
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Linker flags for version injection
LDFLAGS_VERSION=-X main.appVersion=$(VERSION) -X main.appGitCommit=$(GIT_COMMIT) -X main.appBuildTime=$(BUILD_TIME)

# Database migrations (using goose CLI)
MIGRATIONS_DIR := internal/store/migrations
DB_PATH := ./data/ocms.db

all: build ## Build the default local/dev binary

assets: ## Build frontend assets
	./scripts/build-assets.sh

install-hooks: ## Install repository-managed git hooks
	git config core.hooksPath .githooks
	@echo "Configured git hooks path: .githooks"

check-no-absolute-paths: ## Scan tracked files for local absolute path leaks
	./scripts/check-no-absolute-paths.sh --all

dev: assets ## Build assets and run development server
	go run $(MAIN_DIR)

run: ## Run development server without rebuilding assets
	go run $(MAIN_DIR)

stop: ## Stop development server on port 8080
	@lsof -ti:8080 -sTCP:LISTEN | xargs -r kill -9 2>/dev/null || true
	@echo "Server stopped"

restart: stop dev ## Restart development server

build: ## Build fast local/dev binary for host platform
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -ldflags="$(LDFLAGS_VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_DIR)

build-prod: ## Build optimized host production binary
	@echo "Building $(BINARY_NAME) $(VERSION) for production..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -ldflags="-s -w $(LDFLAGS_VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_DIR)

build-linux-amd64: ## Build optimized static Linux AMD64 production binary
	@echo "Building $(BINARY_NAME) $(VERSION) for Linux AMD64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
		$(GO) build -trimpath -ldflags="-s -w $(LDFLAGS_VERSION)" \
		-o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_DIR)

build-darwin-arm64: ## Build optimized Darwin ARM64 production binary
	@echo "Building $(BINARY_NAME) $(VERSION) for macOS ARM64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 \
		$(GO) build -trimpath -ldflags="-s -w $(LDFLAGS_VERSION)" \
		-o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(MAIN_DIR)

build-all-platforms: build-linux-amd64 build-darwin-arm64 ## Build all production platform binaries
	@echo "All platform builds complete!"
	@ls -lh $(BUILD_DIR)/$(BINARY_NAME)-*

test: ## Run all tests
	OCMS_SESSION_SECRET=$(OCMS_SESSION_SECRET) $(GO) test ./...

test-race: ## Run tests with race detector
	OCMS_SESSION_SECRET=$(OCMS_SESSION_SECRET) $(GO) test -race ./...

coverage: ## Run tests with coverage summary
	OCMS_SESSION_SECRET=$(OCMS_SESSION_SECRET) $(GO) test -cover ./...

coverage-html: ## Generate HTML coverage report
	OCMS_SESSION_SECRET=$(OCMS_SESSION_SECRET) $(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

fmt: ## Format code with gofumpt
	gofumpt -w .

fmt-check: ## Fail if gofumpt would reformat files
	@out=$$(gofumpt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofumpt would reformat:"; \
		echo "$$out"; \
		exit 1; \
	fi

vet: ## Run go vet
	$(GO) vet ./...

lint-go: ## Run golangci-lint
	golangci-lint run ./...

lint: lint-go ## Run all linters

check: fmt-check vet lint test ## Run the full local quality gate

deps: ## Download Go module dependencies
	$(GO) mod download

tidy: ## Tidy Go modules
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html

install-tools: ## Install pinned developer tools
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)

clean-db: ## Remove local database files
	rm -rf data/*.db

migrate-up: ## Apply pending SQLite migrations
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) up

migrate-down: ## Roll back one SQLite migration
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down

migrate-status: ## Show SQLite migration status
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) status

migrate-create: ## Create a new SQLite migration
	@read -p "Migration name: " name; \
	goose -dir $(MIGRATIONS_DIR) create $$name sql

sqlc: ## Generate sqlc Go code
	sqlc generate
	@sed -i '' 's/defer rows\.Close()/defer func() { _ = rows.Close() }()/g' internal/store/*.sql.go
	@echo "SQLC generated and warnings fixed"

templ: ## Generate templ Go code
	templ generate
	@echo "Templ files generated"

deploy-binary: ## Deploy binary only to remote server
	./scripts/deploy/deploy-binary.sh $(SERVER) $(INSTANCES) $(DEPLOY_OPTS)

commit-prepare: ## Prepare Codex/Claude-compatible commit workflow
	./scripts/codex-commands commit-prepare

commit-do: ## Complete Codex/Claude-compatible commit workflow
	./scripts/codex-commands commit-do

code-quality: ## Run code quality command wrapper
	./scripts/codex-commands code-quality

security-audit: ## Run security audit command wrapper
	./scripts/codex-commands security-audit

commit-prepare-local: ## Prepare local commit workflow
	./scripts/codex-commands commit-prepare-local

commit-do-local: ## Complete local commit workflow
	./scripts/codex-commands commit-do-local

code-quality-local: ## Run local code quality command wrapper
	./scripts/codex-commands code-quality-local

security-audit-local: ## Run local security audit command wrapper
	./scripts/codex-commands security-audit-local

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
