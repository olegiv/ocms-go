#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Deploy oCMS binary to a remote server
# Usage: ./scripts/deploy/deploy.sh <server> <instance> [options]

set -euo pipefail

# Get script directory and source helper
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

# Hardcoded values
LOCAL_BINARY="bin/ocms-linux-amd64"
LOCAL_CUSTOM_DIR="custom/"
REMOTE_BIN_DIR="/opt/ocms/bin"
REMOTE_BINARY="ocms-linux-amd64"

# Defaults
SSH_USER="root"
VHOST=""
VHOST_USER=""
VHOST_GROUP="psaserv"
SKIP_BUILD=false
DRY_RUN=false
SYNC_CUSTOM=false

usage() {
    cat <<EOF
Usage: $(basename "$0") <server> <instance> [options]

Deploy oCMS binary to a remote server.

Core themes (default, developer) are embedded in the binary and don't need syncing.
Use -v and -o options only if you have custom themes in custom/themes/.

Arguments:
  server      Remote server hostname (e.g., server.example.com)
  instance    Instance name for ocmsctl (e.g., my_site)

Options:
  -v, --vhost PATH     Vhost path for custom content sync (e.g., /var/www/vhosts/example.com/domain)
  -o, --owner USER     Vhost owner for chown (required if -v is provided)
  -g, --group GROUP    Vhost group for chown (default: psaserv)
  -u, --user USER      SSH user (default: root)
  --sync-custom        Force sync custom/ directory even if empty
  --skip-build         Skip 'make build-linux-amd64', use existing binary
  --dry-run            Print commands without executing
  -h, --help           Show this help message

Examples:
  # Deploy binary only (no custom themes)
  $(basename "$0") server.example.com my_site

  # Deploy binary and sync custom themes
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com -o hosting

  # Skip build, dry run
  $(basename "$0") server.example.com my_site --skip-build --dry-run
EOF
    exit 0
}

# Parse arguments
SERVER=""
INSTANCE=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            ;;
        -u|--user)
            SSH_USER="$2"
            shift 2
            ;;
        -v|--vhost)
            VHOST="$2"
            shift 2
            ;;
        -o|--owner)
            VHOST_USER="$2"
            shift 2
            ;;
        -g|--group)
            VHOST_GROUP="$2"
            shift 2
            ;;
        --sync-custom)
            SYNC_CUSTOM=true
            shift
            ;;
        --skip-build)
            SKIP_BUILD=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -*)
            echo_error "Unknown option: $1"
            echo "Use --help for usage information."
            exit 1
            ;;
        *)
            if [[ -z "$SERVER" ]]; then
                SERVER="$1"
            elif [[ -z "$INSTANCE" ]]; then
                INSTANCE="$1"
            else
                echo_error "Too many arguments: $1"
                exit 1
            fi
            shift
            ;;
    esac
done

# Validate required arguments
if [[ -z "$SERVER" ]] || [[ -z "$INSTANCE" ]]; then
    echo_error "Missing required arguments: server and instance"
    echo "Use --help for usage information."
    exit 1
fi

# Validate vhost/owner combination
if [[ -n "$VHOST" ]] && [[ -z "$VHOST_USER" ]]; then
    echo_error "--owner is required when --vhost is provided"
    echo "Use --help for usage information."
    exit 1
fi

# Check if custom directory has content
has_custom_content() {
    if [[ -d "$LOCAL_CUSTOM_DIR" ]]; then
        # Check for any non-empty subdirectories (themes, modules, etc.)
        local content_count
        content_count=$(find "$LOCAL_CUSTOM_DIR" -mindepth 2 -maxdepth 2 -type f 2>/dev/null | head -1 | wc -l)
        [[ "$content_count" -gt 0 ]]
    else
        return 1
    fi
}

# Determine if we should sync custom content
should_sync_custom() {
    if [[ "$SYNC_CUSTOM" == true ]]; then
        return 0
    fi
    if [[ -n "$VHOST" ]] && has_custom_content; then
        return 0
    fi
    return 1
}

# Construct remote custom directory from vhost
if [[ -n "$VHOST" ]]; then
    REMOTE_CUSTOM_DIR="${VHOST}/ocms/custom"
fi

# Helper for running or printing commands
run() {
    if [[ "$DRY_RUN" == true ]]; then
        echo_dry_run "$*"
    else
        "$@"
    fi
}

ssh_cmd() {
    run ssh "${SSH_USER}@${SERVER}" "$@"
}

scp_cmd() {
    run scp "$@"
}

rsync_cmd() {
    run rsync "$@"
}

# Display configuration
echo ""
echo_info "Deployment Configuration:"
echo "  Server:   ${SSH_USER}@${SERVER}"
echo "  Instance: ${INSTANCE}"
echo "  Binary:   ${LOCAL_BINARY}"
if [[ -n "$VHOST" ]]; then
    echo "  Custom:   ${REMOTE_CUSTOM_DIR}"
    if should_sync_custom; then
        echo "  Sync:     Custom content will be synced"
    else
        echo "  Sync:     No custom content to sync"
    fi
else
    echo "  Custom:   Not configured (embedded themes only)"
fi
echo ""

# Step 1: Build binary
if [[ "$SKIP_BUILD" == true ]]; then
    echo_warn "Skipping build (--skip-build)"
else
    echo_step "Building binary..."
    run make build-linux-amd64
    echo_ok "Build complete"
fi

# Verify binary exists
if [[ "$DRY_RUN" == false ]] && [[ ! -f "$LOCAL_BINARY" ]]; then
    echo_error "Binary not found: ${LOCAL_BINARY}"
    exit 1
fi

# Step 2: Backup current binary on server
echo_step "Backing up current binary on server..."
ssh_cmd "cp ${REMOTE_BIN_DIR}/${REMOTE_BINARY} ${REMOTE_BIN_DIR}/${REMOTE_BINARY}.backup 2>/dev/null || true"
echo_ok "Backup complete"

# Step 3: Stop instance
echo_step "Stopping instance '${INSTANCE}'..."
ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl stop ${INSTANCE}"
echo_ok "Instance stopped"

# Step 4: Transfer binary
echo_step "Transferring binary to server..."
scp_cmd "${LOCAL_BINARY}" "${SSH_USER}@${SERVER}:${REMOTE_BIN_DIR}/"
echo_ok "Binary transferred"

# Step 5: Sync custom content (if configured and has content)
if should_sync_custom && [[ -n "$VHOST" ]]; then
    echo_step "Syncing custom content directory..."

    # Create remote custom directory if it doesn't exist
    ssh_cmd "mkdir -p ${REMOTE_CUSTOM_DIR}"

    # Sync custom directory
    rsync_cmd -avz --delete "${LOCAL_CUSTOM_DIR}" "${SSH_USER}@${SERVER}:${REMOTE_CUSTOM_DIR}/"
    echo_ok "Custom content synced"

    # Step 6: Fix custom content ownership
    echo_step "Setting custom content ownership to ${VHOST_USER}:${VHOST_GROUP}..."
    ssh_cmd "chown -R ${VHOST_USER}:${VHOST_GROUP} ${REMOTE_CUSTOM_DIR}"
    echo_ok "Ownership set"
elif [[ -n "$VHOST" ]]; then
    echo_info "Skipping custom sync (no custom content found in ${LOCAL_CUSTOM_DIR})"
fi

# Step 7: Start instance
echo_step "Starting instance '${INSTANCE}'..."
ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl start ${INSTANCE}"
echo_ok "Instance started"

# Step 8: Health check
echo_step "Checking instance status..."
sleep 2  # Give it a moment to start
ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl status ${INSTANCE}"

echo ""
echo_ok "Deployment complete!"
