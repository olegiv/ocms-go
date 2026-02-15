make.PHONY: run stop restart build build-prod build-linux-amd64 build-darwin-arm64 build-all-platforms test clean clean-db migrate-up migrate-down migrate-status migrate-create assets dev sqlc templ commit-prepare commit-do code-quality security-audit commit-prepare-local commit-do-local code-quality-local security-audit-local

# Build variables
BINARY_NAME=ocms
BUILD_DIR=bin
GO=go
GOFLAGS=-v
MAIN_DIR=./cmd/ocms

# Version info from git
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Linker flags for version injection
LDFLAGS_VERSION=-X main.appVersion=$(VERSION) -X main.appGitCommit=$(GIT_COMMIT) -X main.appBuildTime=$(BUILD_TIME)

# Database migrations (using goose CLI)
MIGRATIONS_DIR := internal/store/migrations
DB_PATH := ./data/ocms.db

# Build assets (SCSS to CSS)
assets:
	./scripts/build-assets.sh

# Development server with asset build
dev: assets
	go run $(MAIN_DIR)

# Development server (without asset build)
run:
	go run $(MAIN_DIR)

# Stop development server
stop:
	@lsof -ti:8080 -sTCP:LISTEN | xargs -r kill -9 2>/dev/null || true
	@echo "Server stopped"

# Restart development server
restart: stop dev

# Build the application
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS_VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_DIR)

# Build with optimizations (smaller binary)
build-prod:
	@echo "Building $(BINARY_NAME) $(VERSION) for production..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_DIR)

# Build for Linux AMD64
build-linux-amd64:
	@echo "Building $(BINARY_NAME) $(VERSION) for Linux AMD64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_DIR)

# Build for macOS ARM64 (Apple Silicon)
build-darwin-arm64:
	@echo "Building $(BINARY_NAME) $(VERSION) for macOS ARM64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(MAIN_DIR)

# Build for all platforms
build-all-platforms: build-linux-amd64 build-darwin-arm64
	@echo "All platform builds complete!"
	@ls -lh $(BUILD_DIR)/$(BINARY_NAME)-*

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Clean DB
clean-db:
	rm -rf data/*.db

migrate-up:
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) up

migrate-down:
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down

migrate-status:
	goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) status

migrate-create:
	@read -p "Migration name: " name; \
	goose -dir $(MIGRATIONS_DIR) create $$name sql

# Generate SQLC code and fix unhandled errors
sqlc:
	sqlc generate
	@sed -i '' 's/defer rows\.Close()/defer func() { _ = rows.Close() }()/g' internal/store/*.sql.go
	@echo "SQLC generated and warnings fixed"

# Generate templ Go code from .templ files
templ:
	templ generate
	@echo "Templ files generated"

# Prepare and execute commit workflow (Codex/Claude-compatible)
commit-prepare:
	./scripts/codex-commands commit-prepare

commit-do:
	./scripts/codex-commands commit-do

code-quality:
	./scripts/codex-commands code-quality

security-audit:
	./scripts/codex-commands security-audit

commit-prepare-local:
	./scripts/codex-commands commit-prepare-local

commit-do-local:
	./scripts/codex-commands commit-do-local

code-quality-local:
	./scripts/codex-commands code-quality-local

security-audit-local:
	./scripts/codex-commands security-audit-local
