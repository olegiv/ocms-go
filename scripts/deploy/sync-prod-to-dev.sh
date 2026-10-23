#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Sync production data (DB, uploads, logs, custom) to local development environment
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
LOCAL_CUSTOM_DIR="./custom"

# How long the remote instance gets to answer /health after the restart.
HEALTH_RETRIES="${HEALTH_RETRIES:-15}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-2}"

# Defaults
SSH_USER="root"
VHOST=""
DRY_RUN=false
SYNC_DB=true
SYNC_UPLOADS=true
SYNC_LOGS=true
SYNC_CUSTOM=false

# REMOTE_STOPPED is true from the stop until ocmsctl start succeeds, and
# REMOTE_UNCHECKED from then until its health check has run; see
# restart_remote_on_exit.
REMOTE_STOPPED=false
REMOTE_UNCHECKED=false

# Load local port from .env if available
LOCAL_PORT=""
if [[ -f .env ]]; then
    LOCAL_PORT=$(grep -E '^OCMS_SERVER_PORT=' .env | cut -d= -f2 | tr -d '[:space:]')
fi
LOCAL_PORT="${LOCAL_PORT:-8080}"

usage() {
    cat <<EOF
Usage: $(basename "$0") <server> <instance> -v <vhost> [options]

Sync production data (DB, uploads, logs) to local development environment.
Stops both local and remote servers before syncing.

Arguments:
  server      Remote server hostname (e.g., server.example.com)
  instance    Instance name for ocmsctl (e.g., my_site)

Required Options:
  -v, --vhost PATH       Instance directory on server (e.g., /var/www/vhosts/example.com/ocms)

Options:
  -u, --user USER        SSH user (default: root)
  -p, --port PORT        Local server port (default: 8080)
  --no-db                Skip database sync
  --no-uploads           Skip uploads sync
  --no-logs              Skip logs sync
  --sync-custom          Also sync custom/ directory (themes only;
                         custom/modules/ is never synced)
  --dry-run              Print commands without executing
  -h, --help             Show this help message

Examples:
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com/ocms
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com/ocms --no-logs
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com/ocms --sync-custom
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com/ocms --dry-run

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
        --sync-custom)
            SYNC_CUSTOM=true
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
check_health_settings

# Construct remote paths from instance directory
REMOTE_INSTANCE_DIR="${VHOST}"
REMOTE_DATA_DIR="${REMOTE_INSTANCE_DIR}/data"
REMOTE_UPLOADS_DIR="${REMOTE_INSTANCE_DIR}/uploads"
REMOTE_LOGS_DIR="${REMOTE_INSTANCE_DIR}/logs"
REMOTE_CUSTOM_DIR="${REMOTE_INSTANCE_DIR}/custom"

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
        echo_dry_run "kill processes on port ${LOCAL_PORT}"
    else
        local pids
        pids=$(lsof -ti:"${LOCAL_PORT}" -sTCP:LISTEN 2>/dev/null) || true
        if [[ -n "$pids" ]]; then
            echo "$pids" | xargs kill -9 2>/dev/null || true
        fi
    fi
    echo_ok "Local server stopped"
}

# stop_remote_server stops the remote instance, or ends the script before
# anything is copied: copying the database of a running instance gives a
# torn copy. When ocmsctl refused the stop the instance is still running, so
# there is nothing to start again; after a lost connection its state is
# unknown, and restart_remote_on_exit starts it to be safe.
stop_remote_server() {
    echo_step "Stopping remote instance '${INSTANCE}'..."
    local rc=0
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl stop ${INSTANCE}" || rc=$?
    if [[ "$rc" -ne 0 ]]; then
        UNHEALTHY=()
        remote_failed "$rc" "stopping" "${INSTANCE}" "Could not stop remote instance '${INSTANCE}'; nothing was synced"
        if [[ "$rc" -ne 255 ]]; then
            REMOTE_STOPPED=false
        fi
        exit 1
    fi
    echo_ok "Remote server stopped"
}

# start_remote_server starts the remote instance and waits for it to report
# healthy (check_instances in helper.sh). It returns 1 when it did not.
start_remote_server() {
    echo_step "Starting remote instance '${INSTANCE}'..."
    UNHEALTHY=()
    local rc=0
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl start ${INSTANCE}" || rc=$?
    if [[ "$rc" -ne 0 ]]; then
        remote_failed "$rc" "starting" "${INSTANCE}" "Could not start remote instance '${INSTANCE}'"
        return 1
    fi
    REMOTE_STOPPED=false
    REMOTE_UNCHECKED=true
    echo_ok "Remote server started"
    echo_step "Waiting for remote instance '${INSTANCE}' to report healthy..."
    check_instances "${INSTANCE}"
    REMOTE_UNCHECKED=false
    [[ ${#UNHEALTHY[@]} -eq 0 ]]
}

# restart_remote_on_exit runs when the script exits. If that happens while the
# remote instance is stopped, because a step failed or the sync was
# interrupted, it starts the instance again, so a failed sync does not leave
# the production site down. Another Ctrl-C cannot cut that start short. If
# the instance had started but its health check was cut short, it says so.
restart_remote_on_exit() {
    local status=$?
    trap - EXIT
    trap '' INT TERM HUP
    if [[ "$REMOTE_STOPPED" == true ]]; then
        REMOTE_STOPPED=false
        echo_error "The sync stopped before it finished (status ${status}) while the remote instance was stopped; starting it again"
        if ! start_remote_server; then
            echo_error "The remote instance '${INSTANCE}' did not come back; see ${REMOTE_BIN_DIR}/ocmsctl logs ${INSTANCE}"
        fi
    elif [[ "$REMOTE_UNCHECKED" == true ]]; then
        echo_warn "The remote instance '${INSTANCE}' was started, but its health check was cut short; check it with ${REMOTE_BIN_DIR}/ocmsctl health ${INSTANCE}"
    fi
    exit "$status"
}
trap restart_remote_on_exit EXIT

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

sync_custom() {
    if [[ "$SYNC_CUSTOM" != true ]]; then
        return
    fi

    echo_step "Syncing custom directory..."

    # Check if remote custom directory exists
    if ! ssh_cmd "test -d ${REMOTE_CUSTOM_DIR}" 2>/dev/null; then
        echo_warn "Remote custom directory not found: ${REMOTE_CUSTOM_DIR} (skipping)"
        return
    fi

    # Create local custom directory if it doesn't exist
    if [[ "$DRY_RUN" != true ]]; then
        mkdir -p "${LOCAL_CUSTOM_DIR}"
    else
        echo_dry_run "mkdir -p ${LOCAL_CUSTOM_DIR}"
    fi

    # Sync custom directory (no delete - preserve local custom content)
    #
    # custom/modules/ is excluded in both directions. deploy.sh never ships it,
    # so anything under it on the server is a leftover from an older deploy that
    # no longer matches the running binary. Pulling that back would overwrite
    # the developer's working copy — the source of truth for module code — with
    # stale sources, and this is the pull direction with no --delete and no -u,
    # so an older server file wins outright.
    rsync_cmd -avz --progress --exclude='/modules/' \
        "${SSH_USER}@${SERVER}:${REMOTE_CUSTOM_DIR}/" \
        "${LOCAL_CUSTOM_DIR}/"

    echo_ok "Custom directory synced"
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
echo "  Sync Custom:   ${SYNC_CUSTOM}"
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

# Step 2: Stop remote server. From here until it starts again, a failing step
# or an interrupt makes restart_remote_on_exit start it.
REMOTE_STOPPED=true
stop_remote_server

# Step 3: Checkpoint remote database (flush WAL to main file)
if [[ "$SYNC_DB" == true ]]; then
    checkpoint_remote_db
fi

# Step 4: Sync data
sync_database
sync_uploads
sync_logs
sync_custom

# Step 5: Start remote server; it must report healthy again. REMOTE_STOPPED
# stays set until ocmsctl start succeeds, so an interrupt before then still
# makes restart_remote_on_exit start the instance; a start that failed is
# reported here and not tried again.
STARTED=true
start_remote_server || STARTED=false
REMOTE_STOPPED=false
if [[ "$STARTED" != true ]]; then
    echo_error "The data was synced, but the remote instance '${INSTANCE}' is not healthy; see ${REMOTE_BIN_DIR}/ocmsctl logs ${INSTANCE}"
    exit 1
fi

# Summary
echo ""
echo_ok "Sync complete!"
echo ""
echo_info "To start your local development server:"
echo "  make dev"
echo ""
