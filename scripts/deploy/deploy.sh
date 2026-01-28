#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Deployment Script
# Usage: ./deploy.sh [path-to-new-binary]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helper.sh"

# Configuration
BINARY_PATH="/opt/ocms/bin/ocms"
BACKUP_PATH="/opt/ocms/bin/ocms.backup"
NEW_BINARY="${1:-/tmp/ocms-new}"
SERVICE_NAME="ocms"
HEALTH_URL="http://127.0.0.1:8081/health"
HEALTH_RETRIES=10
HEALTH_INTERVAL=2

health_check() {
    for i in $(seq 1 $HEALTH_RETRIES); do
        if curl -sf --max-time 5 "$HEALTH_URL" > /dev/null 2>&1; then
            return 0
        fi
        echo_info "Waiting for service to become healthy... ($i/$HEALTH_RETRIES)"
        sleep $HEALTH_INTERVAL
    done
    return 1
}

rollback() {
    echo_error "Deployment failed! Rolling back..."
    if [ -f "$BACKUP_PATH" ]; then
        cp "$BACKUP_PATH" "$BINARY_PATH"
        sudo systemctl restart "$SERVICE_NAME"
        sleep 3
        if health_check; then
            echo_warn "Rolled back to previous version successfully."
        else
            echo_error "Rollback also failed! Manual intervention required."
        fi
    else
        echo_error "No backup found for rollback!"
    fi
    exit 1
}

# Main
echo_info "Starting oCMS deployment..."

if [ ! -f "$NEW_BINARY" ]; then
    echo_error "New binary not found at $NEW_BINARY"
    echo "Usage: $0 [path-to-new-binary]"
    exit 1
fi

if ! file "$NEW_BINARY" | grep -q "executable\|ELF"; then
    echo_error "$NEW_BINARY is not a valid executable"
    exit 1
fi

echo_info "New binary: $NEW_BINARY ($(ls -lh "$NEW_BINARY" | awk '{print $5}'))"

if [ -f "$BINARY_PATH" ]; then
    echo_info "Creating backup..."
    cp "$BINARY_PATH" "$BACKUP_PATH"
fi

echo_info "Stopping $SERVICE_NAME..."
sudo systemctl stop "$SERVICE_NAME" || true
sleep 2

echo_info "Deploying new binary..."
cp "$NEW_BINARY" "$BINARY_PATH"
chmod 755 "$BINARY_PATH"
chown ocms:ocms "$BINARY_PATH" 2>/dev/null || true

echo_info "Starting $SERVICE_NAME..."
sudo systemctl start "$SERVICE_NAME"

echo_info "Health check..."
if health_check; then
    echo_info "Deployment successful!"
    rm -f "$NEW_BINARY"
else
    rollback
fi
