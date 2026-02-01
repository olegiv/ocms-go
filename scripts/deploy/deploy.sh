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
LOCAL_THEMES_DIR="themes/"
REMOTE_BIN_DIR="/opt/ocms/bin"
REMOTE_BINARY="ocms-linux-amd64"

# Defaults
SSH_USER="root"
VHOST=""
VHOST_USER=""
VHOST_GROUP="psaserv"
SKIP_BUILD=false
DRY_RUN=false

usage() {
    cat <<EOF
Usage: $(basename "$0") <server> <instance> -v <vhost> [options]

Deploy oCMS binary to a remote server.

Arguments:
  server      Remote server hostname (e.g., server.example.com)
  instance    Instance name for ocmsctl (e.g., my_site)

Required Options:
  -v, --vhost PATH       Vhost path for themes (e.g., /var/www/vhosts/example.com/domain)
  -o, --owner USER       Vhost owner for chown (e.g., hosting)

Options:
  -g, --group GROUP  Vhost group for chown (default: psaserv)
  -u, --user USER    SSH user (default: root)
  --skip-build       Skip 'make build-linux-amd64', use existing binary
  --dry-run          Print commands without executing
  -h, --help         Show this help message

Examples:
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com -o hosting
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com -o hosting --skip-build
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com -o hosting -g mygroup --dry-run
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

if [[ -z "$VHOST" ]]; then
    echo_error "Missing required option: --vhost"
    echo "Use --help for usage information."
    exit 1
fi

if [[ -z "$VHOST_USER" ]]; then
    echo_error "Missing required option: --owner"
    echo "Use --help for usage information."
    exit 1
fi

# Construct remote themes directory from vhost
REMOTE_THEMES_DIR="${VHOST}/ocms/themes"

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
echo "  Themes:   ${REMOTE_THEMES_DIR}"
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

# Step 5: Sync themes
echo_step "Syncing themes directory..."
rsync_cmd -avz --delete "${LOCAL_THEMES_DIR}" "${SSH_USER}@${SERVER}:${REMOTE_THEMES_DIR}/"
echo_ok "Themes synced"

# Step 6: Fix themes ownership
echo_step "Setting themes ownership to ${VHOST_USER}:${VHOST_GROUP}..."
ssh_cmd "chown -R ${VHOST_USER}:${VHOST_GROUP} ${REMOTE_THEMES_DIR}"
echo_ok "Ownership set"

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
