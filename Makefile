make.PHONY: run stop restart build test clean migrate-up migrate-down migrate-status migrate-create assets dev sqlc

# Build assets (SCSS to CSS)
assets:
	./scripts/build-assets.sh

# Development server with asset build
dev: assets
	go run ./cmd/ocms

# Development server (without asset build)
run:
	go run ./cmd/ocms

# Stop development server
stop:
	@lsof -ti:8080 -sTCP:LISTEN | xargs -r kill -9 2>/dev/null || true
	@echo "Server stopped"

# Restart development server
restart: stop dev

# Build production binary
build:
	go build -o bin/ocms ./cmd/ocms

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/ data/*.db

# Database migrations (using goose CLI)
MIGRATIONS_DIR := internal/store/migrations
DB_PATH := ./data/ocms.db

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
