.PHONY: run stop build test clean migrate-up migrate-down migrate-status migrate-create

# Development server
run:
	go run ./cmd/ocms

# Stop development server
stop:
	@lsof -ti:8080 -sTCP:LISTEN | xargs -r kill -9 2>/dev/null || true
	@echo "Server stopped"

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
