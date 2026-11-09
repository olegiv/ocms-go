#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Sync production data (DB, uploads, logs, custom) to local development.
# The copied SQLite databases lose sessions, OAuth credentials and preview
# links. Webhook and HTTP task destinations are cleared and disabled, webhook
# signing secrets and headers are cleared, and CAPTCHA and embed credentials
# and provider activation are reset.
# Treat the copy as production data. It still contains account, API-key and
# recovery-code verifiers, sealed MFA secrets, content, form submissions and
# integration history. Logs, uploads, custom settings and other copied files
# can contain private data or credentials.
# Usage: ./scripts/deploy/sync-prod-to-dev.sh <server> <instance> [options]

set -euo pipefail

# Get script directory and source helper
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"
SCRUB_POLICY="${SCRIPT_DIR}/sync-db-policy.sql"

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
SYNC_TMP=""

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
Stops production while capturing the database and uploads. Restarts it before
scrubbing and publishing the local copy. Logs and custom files are copied live.

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
if [[ "$DRY_RUN" != true ]]; then
    if ! command -v rsync >/dev/null; then
        echo_error "Install rsync locally before syncing."
        exit 1
    fi
    if [[ "$SYNC_DB" == true ]]; then
        if ! command -v sqlite3 >/dev/null; then
            echo_error "Install sqlite3 locally to scrub credentials from the database copy."
            exit 1
        fi
        if [[ ! -r "$SCRUB_POLICY" ]]; then
            echo_error "Cannot read the database scrub policy."
            exit 1
        fi
    fi
fi

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
    if [[ "$DRY_RUN" != true && -n "$SYNC_TMP" ]]; then
        rm -rf -- "$SYNC_TMP" || echo_warn "Could not remove private staging directory: $SYNC_TMP"
    fi
    exit "$status"
}
# Turn an interrupt into a normal exit, so restart_remote_on_exit runs from
# the script's own flow, once the interrupted command has finished. Left
# untrapped, bash runs the EXIT trap from inside its handling of the fatal
# signal, and on a CI runner it skipped it, leaving the instance stopped.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
trap restart_remote_on_exit EXIT

checkpoint_remote_db() {
    echo_step "Checkpointing remote SQLite WAL..."
    local database="${REMOTE_DATA_DIR}/ocms.db" command result
    database="${database//\'/\'\\\'\'}"
    command="if [ -f '$database' ]; then sqlite3 -batch -bail -init /dev/null '$database' 'PRAGMA wal_checkpoint(TRUNCATE);'; else echo missing; fi"
    if [[ "$DRY_RUN" == true ]]; then
        ssh_cmd "$command"
        return
    fi
    result=$(ssh_cmd "$command")
    case "$result" in
        missing) echo_info "Default remote database not found; capturing data/ including journal files." ;;
        0\|*) echo_ok "Default remote database checkpoint succeeded" ;;
        *) echo_error "Remote database checkpoint did not finish successfully: $result"; return 1 ;;
    esac
}

scrub_database_credentials() {
    local database="$1" statements sidecar
    for sidecar in "$database-wal" "$database-shm" "$database-journal"; do
        if [[ -L "$sidecar" ]]; then
            echo_error "Cannot scrub a SQLite database with a symlinked journal or WAL."
            return 1
        fi
    done
    statements=$(sqlite3 -batch -bail -init /dev/null "$database" < "$SCRUB_POLICY")
    if [[ -z "$statements" ]]; then
        return
    fi
    sqlite3 -batch -bail -init /dev/null "$database" "
        PRAGMA journal_mode = DELETE;
        PRAGMA secure_delete = ON;
        BEGIN IMMEDIATE;
        $statements
        COMMIT;
        VACUUM;" >/dev/null
}

scrub_staged_databases() {
    local database header found=false
    # OCMS_DB_PATH may select a different name under data/. Match SQLite's
    # file signature so the scrub does not depend on a filename or extension.
    find "${SYNC_TMP}/data" -type f -print0 > "${SYNC_TMP}/files"
    while IFS= read -r -d '' database; do
        # Scrubbing a database can remove its WAL and journal files.
        [[ -f "$database" ]] || continue
        header=$(LC_ALL=C od -An -tx1 -N16 "$database" | tr -d '[:space:]')
        if [[ "$header" == 53514c69746520666f726d6174203300 ]]; then
            found=true
            scrub_database_credentials "$database"
        fi
    done < "${SYNC_TMP}/files"
    if [[ "$found" != true ]]; then
        echo_error "No SQLite database found in the synced data directory."
        return 1
    fi
}

prepare_snapshot() {
    if [[ "$DRY_RUN" == true ]]; then
        SYNC_TMP="<private-staging>"
    else
        SYNC_TMP=$(mktemp -d "${TMPDIR:-/tmp}/ocms-sync.XXXXXXXX")
        mkdir "${SYNC_TMP}/data" "${SYNC_TMP}/uploads"
    fi
    if [[ "$SYNC_UPLOADS" == true && -d "$LOCAL_UPLOADS_DIR" ]]; then
        # Seed a private transfer basis while production is still running.
        # Independent copies keep remote rsync changes off the local originals.
        rsync_cmd -a "${LOCAL_UPLOADS_DIR}/" "${SYNC_TMP}/uploads/"
    fi
}

capture_snapshot_part() {
    local name="$1" remote="$2"
    shift 2
    echo_step "Capturing remote ${name}..."
    rsync_cmd -avz --progress --delete "$@" "${SSH_USER}@${SERVER}:${remote}/" "${SYNC_TMP}/${name}/"
}

publish_snapshot_part() {
    local name="$1" destination="$2"
    echo_step "Publishing local ${name}..."
    run mkdir -p "$destination"
    rsync_cmd -a --checksum --delete "${SYNC_TMP}/${name}/" "${destination}/"
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

# Capture the database and uploads from the same stopped instance.
if [[ "$SYNC_DB" == true || "$SYNC_UPLOADS" == true ]]; then
    prepare_snapshot
    REMOTE_STOPPED=true
    stop_remote_server
    if [[ "$SYNC_DB" == true ]]; then
        checkpoint_remote_db
        capture_snapshot_part data "$REMOTE_DATA_DIR"
    fi
    if [[ "$SYNC_UPLOADS" == true ]]; then
        # The local basis can have different content with the same size and
        # modification time. Compare it to production before accepting it.
        capture_snapshot_part uploads "$REMOTE_UPLOADS_DIR" --checksum
    fi

    # An interrupt before start succeeds still invokes recovery. An explicit
    # start or health failure is reported here and leaves local data alone.
    STARTED=true
    start_remote_server || STARTED=false
    REMOTE_STOPPED=false
    if [[ "$STARTED" != true ]]; then
        echo_error "The remote instance '${INSTANCE}' is not healthy; the local snapshot was not published. See ${REMOTE_BIN_DIR}/ocmsctl logs ${INSTANCE}"
        exit 1
    fi
fi

# All local scrub and publish work runs after production has restarted.
if [[ "$SYNC_DB" == true ]]; then
    echo_step "Scrubbing copied SQLite credentials and outbound integration settings..."
    if [[ "$DRY_RUN" == true ]]; then
        echo_dry_run "Apply sync-db-policy.sql to all staged SQLite databases"
    else
        scrub_staged_databases
    fi
    publish_snapshot_part data "$LOCAL_DATA_DIR"
fi
if [[ "$SYNC_UPLOADS" == true ]]; then
    publish_snapshot_part uploads "$LOCAL_UPLOADS_DIR"
fi
sync_logs
sync_custom

# Summary
echo ""
echo_ok "Sync complete!"
echo ""
echo_info "To start your local development server:"
echo "  make dev"
echo ""
