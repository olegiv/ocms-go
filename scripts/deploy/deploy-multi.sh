#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Multi-Instance Deployment Script
# Deploys a new binary and restarts all registered oCMS instances.
#
# Usage: sudo ./deploy-multi.sh <path-to-new-binary>
#
# Example:
#   sudo ./deploy-multi.sh /tmp/ocms

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$SCRIPT_DIR/helper.sh" ]; then
    source "$SCRIPT_DIR/helper.sh"
elif [ -f "/opt/ocms/helper.sh" ]; then
    source "/opt/ocms/helper.sh"
else
    echo_info()  { echo "[INFO] $1"; }
    echo_warn()  { echo "[WARN] $1"; }
    echo_error() { echo "[ERROR] $1"; }
fi

# Configuration
BINARY_PATH="/opt/ocms/bin/ocms"
BACKUP_PATH="/opt/ocms/bin/ocms.backup"
SITES_CONF="/etc/ocms/sites.conf"
HEALTH_RETRIES=10
HEALTH_INTERVAL=2

# --- Functions ---

health_check() {
    local port="$1"
    local url="http://127.0.0.1:${port}/health"
    for i in $(seq 1 $HEALTH_RETRIES); do
        if curl -sf --max-time 5 "$url" > /dev/null 2>&1; then
            return 0
        fi
        sleep $HEALTH_INTERVAL
    done
    return 1
}

# --- Argument Validation ---

if [ "$EUID" -ne 0 ]; then
    echo_error "Run as root (use sudo)"
    exit 1
fi

NEW_BINARY="${1:-}"
if [ -z "$NEW_BINARY" ]; then
    echo "Usage: sudo $0 <path-to-new-binary>"
    exit 1
fi

if [ ! -f "$NEW_BINARY" ]; then
    echo_error "Binary not found: $NEW_BINARY"
    exit 1
fi

if ! file "$NEW_BINARY" | grep -q "executable\|ELF"; then
    echo_error "$NEW_BINARY is not a valid executable"
    exit 1
fi

if [ ! -f "$SITES_CONF" ]; then
    echo_error "No sites registered: $SITES_CONF"
    exit 1
fi

# --- Collect sites ---

declare -a SITE_IDS
declare -a SITE_PORTS

while IFS=' ' read -r site_id vhost_path user port; do
    [[ "$site_id" =~ ^#.*$ ]] && continue
    [ -z "$site_id" ] && continue
    SITE_IDS+=("$site_id")
    SITE_PORTS+=("$port")
done < "$SITES_CONF"

if [ ${#SITE_IDS[@]} -eq 0 ]; then
    echo_error "No sites found in $SITES_CONF"
    exit 1
fi

echo_info "Deploying new binary to ${#SITE_IDS[@]} site(s)"
echo_info "  Binary: $NEW_BINARY ($(ls -lh "$NEW_BINARY" | awk '{print $5}'))"
echo ""

# --- Backup current binary ---

if [ -f "$BINARY_PATH" ]; then
    echo_info "Backing up current binary..."
    cp "$BINARY_PATH" "$BACKUP_PATH"
fi

# --- Stop all instances ---

echo_info "Stopping all instances..."
for site_id in "${SITE_IDS[@]}"; do
    if systemctl is-active --quiet "ocms@${site_id}" 2>/dev/null; then
        echo "  Stopping ocms@$site_id..."
        systemctl stop "ocms@${site_id}" || true
    fi
done
sleep 2

# --- Replace binary ---

echo_info "Installing new binary..."
cp "$NEW_BINARY" "$BINARY_PATH"
chmod 755 "$BINARY_PATH"

# --- Start all instances and health check ---

echo_info "Starting all instances..."
declare -a FAILED_SITES

for i in "${!SITE_IDS[@]}"; do
    site_id="${SITE_IDS[$i]}"
    port="${SITE_PORTS[$i]}"

    echo -n "  Starting ocms@$site_id (port $port)... "
    systemctl start "ocms@${site_id}"
    sleep 2

    if health_check "$port"; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAILED${NC}"
        FAILED_SITES+=("$site_id")
    fi
done

echo ""

# --- Report ---

if [ ${#FAILED_SITES[@]} -gt 0 ]; then
    echo_error "Deployment completed with ${#FAILED_SITES[@]} failure(s):"
    for site_id in "${FAILED_SITES[@]}"; do
        echo "  - $site_id"
    done
    echo ""
    echo "To rollback:"
    echo "  sudo cp $BACKUP_PATH $BINARY_PATH"
    echo "  sudo systemctl restart 'ocms@*'"
    exit 1
else
    echo_info "All ${#SITE_IDS[@]} site(s) deployed successfully!"
    rm -f "$NEW_BINARY"
fi
