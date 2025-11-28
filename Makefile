.PHONY: run stop build test clean

# Development server
run:
	go run ./cmd/ocms

# Stop development server
stop:
	@lsof -ti:8080 | xargs -r kill -9 2>/dev/null || true
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
