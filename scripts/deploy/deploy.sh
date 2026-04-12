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

# Portable readlink -f replacement. Follows the entire symlink chain
# using readlink (POSIX) and canonicalizes with cd + pwd -P.
resolve_path() {
    local target="$1"
    while [[ -L "$target" ]]; do
        local link
        link=$(readlink "$target")
        if [[ "$link" = /* ]]; then
            target="$link"
        else
            target="$(dirname "$target")/$link"
        fi
    done
    if [[ -d "$target" ]]; then
        (cd "$target" && pwd -P)
    elif [[ -e "$target" ]]; then
        local dir base
        dir=$(cd "$(dirname "$target")" && pwd -P)
        base=$(basename "$target")
        echo "$dir/$base"
    fi
}

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
SKIP_BINARY=false
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
  --skip-binary        Skip binary build, backup, and transfer (custom content only)
  --dry-run            Print commands without executing
  -h, --help           Show this help message

Examples:
  # Deploy binary only (no custom themes)
  $(basename "$0") server.example.com my_site

  # Deploy binary and sync custom themes
  $(basename "$0") server.example.com my_site -v /var/www/vhosts/example.com -o hosting

  # Deploy custom content only (skip binary)
  $(basename "$0") server.example.com my_site --skip-binary -v /var/www/vhosts/example.com -o hosting

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
        --skip-binary)
            SKIP_BINARY=true
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

# Validate CLI values to prevent shell/option injection in ssh/scp/rsync
# commands. Must be called in the main shell — exit 1 would be swallowed
# inside a subshell or pipeline.
validate_pattern() {
    local value="$1"
    local pattern="$2"
    local field="$3"

    if [[ -z "$value" ]]; then
        echo_error "Empty ${field}"
        exit 1
    fi
    if [[ ! "$value" =~ $pattern ]]; then
        echo_error "Invalid ${field} value"
        exit 1
    fi
}

# Hostnames, IPs, and SSH config aliases. IPv6 literals and port-qualified
# names (host:port) are not supported; use ~/.ssh/config for non-standard ports.
validate_pattern "$SERVER" '^[a-zA-Z0-9._-]+$' "server"
validate_pattern "$INSTANCE" '^[a-zA-Z0-9._-]+$' "instance"
validate_pattern "$SSH_USER" '^[a-zA-Z0-9._-]+$' "user"
validate_pattern "$VHOST_GROUP" '^[a-zA-Z0-9._-]+$' "group"

if [[ -n "$VHOST" ]]; then
    validate_pattern "$VHOST" '^/[a-zA-Z0-9._/-]+$' "vhost path"
    if [[ "$VHOST" == *".."* ]]; then
        echo_error "Vhost path must not contain '..'"
        exit 1
    fi
fi

if [[ -n "$VHOST_USER" ]]; then
    validate_pattern "$VHOST_USER" '^[a-zA-Z0-9._-]+$' "owner"
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
        # Follow symlinked theme/module directories and look for at least one file.
        local first_file
        first_file=$(find -L "$LOCAL_CUSTOM_DIR" -mindepth 1 -type f -print -quit 2>/dev/null || true)
        [[ -n "$first_file" ]]
    else
        return 1
    fi
}

validate_custom_symlinks() {
    if [[ ! -d "$LOCAL_CUSTOM_DIR" ]]; then
        return 0
    fi

    local custom_root
    custom_root=$(resolve_path "$LOCAL_CUSTOM_DIR")
    if [[ -z "$custom_root" ]]; then
        echo_error "Unable to resolve custom directory path: ${LOCAL_CUSTOM_DIR}"
        exit 1
    fi

    local broken_symlinks find_err
    find_err=$(mktemp)
    broken_symlinks=$(find "$LOCAL_CUSTOM_DIR" -type l ! -exec test -e {} \; -print 2>"$find_err") || {
        echo_error "Failed to scan for broken symlinks in ${LOCAL_CUSTOM_DIR}"
        cat "$find_err" >&2
        rm -f "$find_err"
        exit 1
    }
    rm -f "$find_err"

    if [[ -n "$broken_symlinks" ]]; then
        echo_error "Broken symlinks found in ${LOCAL_CUSTOM_DIR}; fix them before deploying"
        while IFS= read -r symlink; do
            [[ -n "$symlink" ]] || continue
            local target
            target=$(readlink "$symlink" 2>/dev/null || echo "<unreadable>")
            echo "  - ${symlink} -> ${target}"
        done <<< "$broken_symlinks"
        exit 1
    fi

    # Reject symlinks that resolve outside the custom directory tree.
    # This prevents rsync -aL from copying unrelated local files to the server.
    local all_symlinks
    find_err=$(mktemp)
    all_symlinks=$(find "$LOCAL_CUSTOM_DIR" -type l -print 2>"$find_err") || {
        echo_error "Failed to enumerate symlinks in ${LOCAL_CUSTOM_DIR}"
        cat "$find_err" >&2
        rm -f "$find_err"
        exit 1
    }
    rm -f "$find_err"

    local escaped_symlinks=""
    while IFS= read -r symlink; do
        [[ -n "$symlink" ]] || continue

        local resolved_target
        resolved_target=$(resolve_path "$symlink")
        if [[ -z "$resolved_target" ]]; then
            echo_error "Cannot resolve symlink target: ${symlink}"
            echo_error "Deploy aborted: all symlinks must be resolvable"
            exit 1
        fi

        case "$resolved_target" in
            "$custom_root"/*|"$custom_root")
                # Symlink stays within custom/ — allowed
                ;;
            *)
                escaped_symlinks+="${symlink} -> ${resolved_target}"$'\n'
                ;;
        esac
    done <<< "$all_symlinks"

    if [[ -n "$escaped_symlinks" ]]; then
        echo_error "Symlinks in ${LOCAL_CUSTOM_DIR} must resolve within ${LOCAL_CUSTOM_DIR}"
        while IFS= read -r symlink; do
            [[ -n "$symlink" ]] || continue
            echo "  - ${symlink}"
        done <<< "$escaped_symlinks"
        exit 1
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
    REMOTE_CUSTOM_DIR="${VHOST}/custom"
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

# Preflight custom content validation before any remote actions.
if [[ -n "$VHOST" ]]; then
    validate_custom_symlinks
fi

# Step 1: Build binary
if [[ "$SKIP_BUILD" == true ]]; then
    echo_warn "Skipping build (--skip-build)"
else
    echo_step "Building binary..."
    run make build-linux-amd64
    echo_ok "Build complete"
fi

# Verify binary exists
if [[ "$SKIP_BINARY" != true ]] && [[ "$DRY_RUN" == false ]] && [[ ! -f "$LOCAL_BINARY" ]]; then
    echo_error "Binary not found: ${LOCAL_BINARY}"
    exit 1
fi

if [[ "$SKIP_BINARY" == true ]]; then
    echo_warn "Skipping binary backup and transfer (--skip-binary)"
else
    # Step 2: Backup current binary on server
    echo_step "Backing up current binary on server..."
    ssh_cmd "cp ${REMOTE_BIN_DIR}/${REMOTE_BINARY} ${REMOTE_BIN_DIR}/${REMOTE_BINARY}.backup 2>/dev/null || true"
    echo_ok "Backup complete"
fi

# Step 3: Stop instance
echo_step "Stopping instance '${INSTANCE}'..."
ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl stop ${INSTANCE}"
echo_ok "Instance stopped"

if [[ "$SKIP_BINARY" != true ]]; then
    # Step 4: Transfer binary
    echo_step "Transferring binary to server..."
    scp_cmd "${LOCAL_BINARY}" "${SSH_USER}@${SERVER}:${REMOTE_BIN_DIR}/"
    echo_ok "Binary transferred"
fi

# Step 5: Sync custom content (if configured and enabled)
if should_sync_custom && [[ -n "$VHOST" ]]; then
    echo_step "Syncing custom content directory..."

    # Create remote custom directory if it doesn't exist
    ssh_cmd "mkdir -p ${REMOTE_CUSTOM_DIR}"

    # Dereference local symlinks so the server receives real theme/module files.
    rsync_cmd -aLz --delete "${LOCAL_CUSTOM_DIR}" "${SSH_USER}@${SERVER}:${REMOTE_CUSTOM_DIR}/"
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
