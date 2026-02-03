# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# oCMS Docker Build
# Multi-stage build for minimal runtime image

# =============================================================================
# Stage 1: Builder
# =============================================================================
FROM golang:1.24-alpine AS builder

# Build arguments for version info
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# Install build dependencies
RUN apk add --no-cache \
    git \
    make \
    nodejs \
    npm \
    && npm install -g sass

WORKDIR /build

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy package.json for npm dependencies
COPY package.json package-lock.json* ./

# Copy the rest of the source code
COPY . .

# Build assets (SCSS compilation and JS dependencies)
RUN npm install --silent && npm run copy-deps --silent

# Create dist directories and compile SCSS
RUN mkdir -p web/static/dist/js \
    && sass web/static/scss/main.scss web/static/dist/main.css --style=compressed --no-source-map \
    && cp web/static/favicon.ico web/static/dist/ 2>/dev/null || true

# Copy source JS files if they exist
RUN if [ -d "web/static/js" ] && [ "$(ls -A web/static/js 2>/dev/null)" ]; then \
        cp web/static/js/*.js web/static/dist/js/ 2>/dev/null || true; \
    fi

# Build the Go binary with version info
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.appVersion=${VERSION} -X main.appGitCommit=${GIT_COMMIT} -X main.appBuildTime=${BUILD_TIME}" \
    -trimpath \
    -o /build/bin/ocms \
    ./cmd/ocms

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:3.21

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata

# Create non-root user for security
RUN addgroup -g 1000 ocms && \
    adduser -u 1000 -G ocms -s /bin/sh -D ocms

WORKDIR /app

# Create data directories
RUN mkdir -p /app/data /app/uploads /app/custom/themes /app/custom/modules \
    && chown -R ocms:ocms /app

# Copy binary from builder
COPY --from=builder /build/bin/ocms /app/ocms

# Set ownership
RUN chown ocms:ocms /app/ocms

# Switch to non-root user
USER ocms

# Environment variables with sensible defaults
ENV OCMS_SERVER_HOST=0.0.0.0
ENV OCMS_SERVER_PORT=8080
ENV OCMS_DB_PATH=/app/data/ocms.db
ENV OCMS_CUSTOM_DIR=/app/custom
ENV OCMS_ENV=production
ENV OCMS_LOG_LEVEL=info

# Expose the application port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Volume mount points for persistent data
VOLUME ["/app/data", "/app/uploads", "/app/custom"]

# Run the application
ENTRYPOINT ["/app/ocms"]
