#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# Deploy oCMS to Fly.io
# Usage: ./deploy.sh [--reset] [--logs]
#   --reset: Reset database and uploads after deployment (triggers fresh seeding)
#   --logs:  Show logs after deployment

set -e

APP_NAME="ocms-demo"
IMAGE_NAME="ocms-test"
SHOW_LOGS=false
DO_RESET=false

# Parse arguments
for arg in "$@"; do
    case $arg in
        --reset)
            DO_RESET=true
            ;;
        --logs)
            SHOW_LOGS=true
            ;;
        --help|-h)
            echo "Usage: $0 [--reset] [--logs]"
            echo ""
            echo "Options:"
            echo "  --reset  Reset database and uploads (fresh seeding)"
            echo "  --logs   Show logs after deployment"
            echo "  --help   Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $arg"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

echo "==> Building Docker image..."
docker build -t "$IMAGE_NAME" .

echo "==> Deploying to Fly.io..."
fly deploy --local-only --image "$IMAGE_NAME"

if [[ "$DO_RESET" == true ]]; then
    echo "==> Starting machines..."
    fly machines start -a "$APP_NAME" 2>/dev/null || true

    echo "==> Waiting for machine to start..."
    sleep 5

    # Check if machine is running
    if ! fly status -a "$APP_NAME" | grep -q "running"; then
        echo "==> Machine not running, triggering via HTTP..."
        curl -s -o /dev/null "https://${APP_NAME}.fly.dev/health" || true
        sleep 5
    fi

    echo "==> Resetting database and uploads..."
    fly ssh console -C "rm -f /app/data/ocms.db* && rm -rf /app/data/uploads/*" -a "$APP_NAME"

    echo "==> Restarting machines..."
    fly machines restart -a "$APP_NAME"

    echo "==> Reset complete. Fresh seeding will occur on startup."

    # Wait for app to be ready
    echo "==> Waiting for app to be ready..."
    sleep 10
fi

echo ""
echo "==> Deployment complete!"
echo "    App URL: https://${APP_NAME}.fly.dev"
echo "    Admin:   https://${APP_NAME}.fly.dev/admin"
echo ""

if [[ "$SHOW_LOGS" == true ]] || [[ "$DO_RESET" == true ]]; then
    echo "==> Recent logs:"
    fly logs -a "$APP_NAME" --no-tail | tail -30
fi
