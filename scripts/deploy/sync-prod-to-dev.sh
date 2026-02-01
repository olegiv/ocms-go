#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Sync production data (DB, uploads, logs) to local development environment
# Usage: ./scripts/deploy/sync-prod-to-dev.sh <server> <instance> [options]

set -euo pipefail

# Get script directory and source helper
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

# Hardcoded values
REMOTE_BIN_DIR="/opt/ocms/bin"
LOCAL_DATA_DIR="./data"
LOCAL_UPLOADS_DIR="./uploads"
LOCAL_LOGS_DIR="./logs"

# Defaults
SSH_USER="root"
VHOST=""
DRY_RUN=false
SYNC_DB=true
SYNC_UPLOADS=true
SYNC_LOGS=true
LOCAL_PORT=8080

usage() {
    cat <<EOF
Usage: $(basename "$0") <server> <instance> -v <vhost> [options]

Sync production data (DB, uploads, logs) to local development environment.
Stops both local and remote servers before syncing.

Arguments:
  server      Remote server hostname (e.g., server.example.com)
  instance    Instance name for ocmsctl (e.g., my_site)

Required Options:
  -v, --vhost PATH       Vhost path on server (e.g., /var/www/vhosts/example.com)

Options:
  -u, --user USER        SSH user (default: root)
  -p, --port PORT        Local server port (default: 8080)
  --no-db                Skip database sync
  --no-uploads           Skip uploads sync
  --no-logs              Skip logs sync
  --dry-run              Print commands without executing
  -h, --help             Show this help message

Examples:
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com --no-logs
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com --dry-run

WARNING: This will OVERWRITE your local data with production data!
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
        -p|--port)
            LOCAL_PORT="$2"
            shift 2
            ;;
        --no-db)
            SYNC_DB=false
            shift
            ;;
        --no-uploads)
            SYNC_UPLOADS=false
            shift
            ;;
        --no-logs)
            SYNC_LOGS=false
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

# Construct remote paths from vhost
REMOTE_INSTANCE_DIR="${VHOST}/ocms"
REMOTE_DATA_DIR="${REMOTE_INSTANCE_DIR}/data"
REMOTE_UPLOADS_DIR="${REMOTE_INSTANCE_DIR}/uploads"
REMOTE_LOGS_DIR="${REMOTE_INSTANCE_DIR}/logs"

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

rsync_cmd() {
    run rsync "$@"
}

stop_local_server() {
    echo_step "Stopping local development server (port ${LOCAL_PORT})..."
    if [[ "$DRY_RUN" == true ]]; then
        echo_dry_run "lsof -ti:${LOCAL_PORT} -sTCP:LISTEN | xargs -r kill -9"
    else
        lsof -ti:"${LOCAL_PORT}" -sTCP:LISTEN | xargs -r kill -9 2>/dev/null || true
    fi
    echo_ok "Local server stopped"
}

stop_remote_server() {
    echo_step "Stopping remote instance '${INSTANCE}'..."
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl stop ${INSTANCE}" || true
    echo_ok "Remote server stopped"
}

start_remote_server() {
    echo_step "Starting remote instance '${INSTANCE}'..."
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl start ${INSTANCE}"
    echo_ok "Remote server started"
}

checkpoint_remote_db() {
    echo_step "Checkpointing remote SQLite WAL..."
    ssh_cmd "sqlite3 ${REMOTE_DATA_DIR}/ocms.db 'PRAGMA wal_checkpoint(TRUNCATE);'"
    echo_ok "WAL checkpoint complete"
}

sync_database() {
    if [[ "$SYNC_DB" != true ]]; then
        echo_warn "Skipping database sync (--no-db)"
        return
    fi

    echo_step "Syncing database..."

    # Create local data directory if it doesn't exist
    if [[ "$DRY_RUN" != true ]]; then
        mkdir -p "${LOCAL_DATA_DIR}"
    else
        echo_dry_run "mkdir -p ${LOCAL_DATA_DIR}"
    fi

    # Sync entire data directory
    rsync_cmd -avz --progress --delete \
        "${SSH_USER}@${SERVER}:${REMOTE_DATA_DIR}/" \
        "${LOCAL_DATA_DIR}/"

    echo_ok "Database synced"
}

sync_uploads() {
    if [[ "$SYNC_UPLOADS" != true ]]; then
        echo_warn "Skipping uploads sync (--no-uploads)"
        return
    fi

    echo_step "Syncing uploads..."

    # Create local uploads directory if it doesn't exist
    if [[ "$DRY_RUN" != true ]]; then
        mkdir -p "${LOCAL_UPLOADS_DIR}"
    else
        echo_dry_run "mkdir -p ${LOCAL_UPLOADS_DIR}"
    fi

    # Sync uploads with delete to mirror production exactly
    rsync_cmd -avz --progress --delete \
        "${SSH_USER}@${SERVER}:${REMOTE_UPLOADS_DIR}/" \
        "${LOCAL_UPLOADS_DIR}/"

    echo_ok "Uploads synced"
}

sync_logs() {
    if [[ "$SYNC_LOGS" != true ]]; then
        echo_warn "Skipping logs sync (--no-logs)"
        return
    fi

    echo_step "Syncing logs..."

    # Create local logs directory if it doesn't exist
    if [[ "$DRY_RUN" != true ]]; then
        mkdir -p "${LOCAL_LOGS_DIR}"
    else
        echo_dry_run "mkdir -p ${LOCAL_LOGS_DIR}"
    fi

    # Sync logs (no delete - keep local logs that don't exist on prod)
    rsync_cmd -avz --progress \
        "${SSH_USER}@${SERVER}:${REMOTE_LOGS_DIR}/" \
        "${LOCAL_LOGS_DIR}/"

    echo_ok "Logs synced"
}

# Display configuration
echo ""
echo_info "Sync Configuration:"
echo "  Server:        ${SSH_USER}@${SERVER}"
echo "  Instance:      ${INSTANCE}"
echo "  Vhost:         ${VHOST}"
echo "  Sync DB:       ${SYNC_DB}"
echo "  Sync Uploads:  ${SYNC_UPLOADS}"
echo "  Sync Logs:     ${SYNC_LOGS}"
echo ""

if [[ "$DRY_RUN" == true ]]; then
    echo_warn "DRY RUN MODE - No changes will be made"
    echo ""
fi

# Confirmation prompt (skip for dry-run)
if [[ "$DRY_RUN" != true ]]; then
    echo_warn "This will OVERWRITE your local data with production data!"
    read -p "Continue? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo_info "Aborted."
        exit 0
    fi
    echo ""
fi

# Step 1: Stop local server
stop_local_server

# Step 2: Stop remote server
stop_remote_server

# Step 3: Checkpoint remote database (flush WAL to main file)
if [[ "$SYNC_DB" == true ]]; then
    checkpoint_remote_db
fi

# Step 4: Sync data
sync_database
sync_uploads
sync_logs

# Step 5: Start remote server
start_remote_server

# Summary
echo ""
echo_ok "Sync complete!"
echo ""
echo_info "To start your local development server:"
echo "  make dev"
echo ""
