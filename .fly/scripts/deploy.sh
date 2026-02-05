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
if [[ "$DO_RESET" == true ]]; then
    # Force rebuild without cache when resetting to ensure latest code
    docker build --no-cache -t "$IMAGE_NAME" .
else
    docker build -t "$IMAGE_NAME" .
fi

echo "==> Deploying to Fly.io..."
fly deploy --local-only --image "$IMAGE_NAME"

if [[ "$DO_RESET" == true ]]; then
    echo "==> Ensuring machine is running..."
    # Trigger machine start via HTTP (auto-start on request)
    curl -s -o /dev/null "https://${APP_NAME}.fly.dev/health" || true
    sleep 5

    # Get machine ID (trim whitespace)
    MACHINE_ID=$(fly machines list -a "$APP_NAME" -q 2>/dev/null | head -1 | tr -d '[:space:]')
    if [[ -z "$MACHINE_ID" ]]; then
        echo "ERROR: Could not get machine ID"
        exit 1
    fi
    echo "==> Machine ID: $MACHINE_ID"

    echo "==> Running reset script..."
    fly ssh console -C "/app/scripts/reset-demo.sh" -a "$APP_NAME"

    echo "==> Restarting machine..."
    fly machines restart "$MACHINE_ID" -a "$APP_NAME"

    echo "==> Waiting for app to be ready..."
    sleep 15

    echo "==> Reset complete. Fresh seeding occurred on startup."
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
