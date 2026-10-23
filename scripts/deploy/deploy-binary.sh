#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Deploy oCMS binary to a remote server (binary only, no custom content)
# Usage: ./scripts/deploy/deploy-binary.sh <server> <instance...> [options]

set -euo pipefail

# Get script directory and source helper
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

# Binary name. Sites that override BINARY_NAME in their Makefile (see
# core/site.mk) must export the same value here: `make build-linux-amd64` then
# writes bin/<name>-linux-amd64, and this script would otherwise look for
# bin/ocms-linux-amd64 and abort.
#
# NOTE: this affects only which file is built and which path is uploaded. The
# launchers do NOT derive from it: ocmsctl and ocms@.service both run
# /opt/ocms/bin/ocms, REMOTE_RUN_BINARY with the default REMOTE_BIN_DIR, so the
# upload is installed there before the instances restart. ocms@.service is also a single template shared by
# every instance, so a per-site BINARY_NAME cannot give a site its own running
# binary, and installing replaces the binary every instance runs.
BINARY_NAME="${BINARY_NAME:-ocms}"
LOCAL_BINARY="${LOCAL_BINARY:-bin/${BINARY_NAME}-linux-amd64}"
REMOTE_BIN_DIR="${REMOTE_BIN_DIR:-/opt/ocms/bin}"
REMOTE_BINARY="${REMOTE_BINARY:-${BINARY_NAME}-linux-amd64}"
REMOTE_RUN_BINARY="${REMOTE_BIN_DIR}/ocms"
# Where the upload lands: never on the running binary or another file the
# deploy uses (remote_upload_path in helper.sh).
REMOTE_UPLOAD="$(remote_upload_path "${REMOTE_BIN_DIR}" "${REMOTE_BINARY}")"

# How long each instance gets to answer /health after the restart.
HEALTH_RETRIES="${HEALTH_RETRIES:-15}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-2}"

# Defaults
SSH_USER="root"
SKIP_BUILD=false
DRY_RUN=false

usage() {
    cat <<EOF
Usage: $(basename "$0") <server> <instance...> [options]

Deploy oCMS binary to a remote server (binary only, no custom content).
Supports multiple instances sharing the same binary.

Core themes (default, developer) are embedded in the binary.
For deploying custom themes, use deploy.sh instead.

Arguments:
  server      Remote server hostname (e.g., server.example.com)
  instance    One or more instance names for ocmsctl (e.g., my_site)

Options:
  -u, --user USER      SSH user (default: root)
  --skip-build         Skip 'make build-linux-amd64', use existing binary
  --dry-run            Print commands without executing
  -h, --help           Show this help message

Examples:
  # Single instance
  $(basename "$0") server.example.com my_site

  # Multiple instances sharing the same binary
  $(basename "$0") server.example.com site_a site_b site_c

  # Skip build, dry run
  $(basename "$0") server.example.com site_a site_b --skip-build --dry-run
EOF
    exit 0
}

# Parse arguments
SERVER=""
INSTANCES=()

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            ;;
        -u|--user)
            SSH_USER="$2"
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
            else
                INSTANCES+=("$1")
            fi
            shift
            ;;
    esac
done

# Validate required arguments
if [[ -z "$SERVER" ]] || [[ ${#INSTANCES[@]} -eq 0 ]]; then
    echo_error "Missing required arguments: server and at least one instance"
    echo "Use --help for usage information."
    exit 1
fi
check_health_settings

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

# Display configuration
echo ""
echo_info "Deployment Configuration:"
echo "  Server:    ${SSH_USER}@${SERVER}"
echo "  Instances: ${INSTANCES[*]}"
echo "  Binary:    ${LOCAL_BINARY}"
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
ssh_cmd "$(backup_binary_command "${REMOTE_RUN_BINARY}")"
echo_ok "Backup complete"

# Step 3: Transfer and install the binary while the instances still run: the
# install renames the new binary into place, which leaves a running one alone,
# so a failed upload or install stops nothing.
# Name the destination explicitly: scp to a bare directory would land the file
# under basename "$LOCAL_BINARY", so overriding only one of the two names would
# leave the install below reading a path the upload never wrote.
echo_step "Transferring binary to server..."
scp_cmd "${LOCAL_BINARY}" "${SSH_USER}@${SERVER}:${REMOTE_UPLOAD}"
echo_ok "Binary transferred"

echo_step "Installing binary as ${REMOTE_RUN_BINARY}..."
ssh_cmd "$(install_binary_command "${REMOTE_UPLOAD}" "${REMOTE_RUN_BINARY}")"
echo_ok "Binary installed"

# Step 4: Restart the instances on the new binary. A failed stop or start is
# recorded and the deploy goes on with the other instances, so one broken
# instance does not leave the rest stopped; an instance that did not stop is
# left as it is.
UNHEALTHY=()
STOPPED=()
for instance in "${INSTANCES[@]}"; do
    echo_step "Stopping instance '${instance}'..."
    rc=0
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl stop ${instance}" || rc=$?
    if [[ "$rc" -eq 0 ]]; then
        echo_ok "Instance '${instance}' stopped"
        STOPPED+=("${instance}")
    else
        remote_failed "$rc" "stopping" "${instance}" "Could not stop instance '${instance}'; leaving it as it is"
    fi
done

# Step 5: Start the instances that stopped
STARTED=()
for instance in ${STOPPED[@]+"${STOPPED[@]}"}; do
    echo_step "Starting instance '${instance}'..."
    rc=0
    ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl start ${instance}" || rc=$?
    if [[ "$rc" -eq 0 ]]; then
        echo_ok "Instance '${instance}' started"
        STARTED+=("${instance}")
    else
        remote_failed "$rc" "starting" "${instance}" "Could not start instance '${instance}'"
    fi
done

# Step 6: Health check. Every started instance must report healthy, or the
# deploy fails.
echo_step "Waiting for the instances to report healthy..."
check_instances ${STARTED[@]+"${STARTED[@]}"}
if [[ ${#UNHEALTHY[@]} -gt 0 ]]; then
    fail_deploy true
fi
echo ""
echo_ok "Deployment complete!"
