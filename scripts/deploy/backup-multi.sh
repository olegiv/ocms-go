#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Multi-Instance Backup Script
# Backs up database and uploads for all registered oCMS instances.
#
# Usage: sudo ./backup-multi.sh [site-id]
#
# If site-id is provided, only that site is backed up.
# If omitted, all registered sites are backed up.
#
# Examples:
#   sudo ./backup-multi.sh                  # backup all sites
#   sudo ./backup-multi.sh example_com      # backup one site

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
SITES_CONF="/etc/ocms/sites.conf"
RETENTION_DAYS=30
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
FILTER_SITE="${1:-}"

# --- Functions ---

backup_site() {
    local site_id="$1"
    local vhost_path="$2"
    local user="$3"

    local instance_dir="$vhost_path/ocms"
    local backup_dir="$instance_dir/backups"
    local db_path="$instance_dir/data/ocms.db"
    local uploads_path="$instance_dir/uploads"

    echo_info "Backing up site: $site_id"

    if [ ! -d "$instance_dir" ]; then
        echo_warn "  Instance directory not found: $instance_dir (skipping)"
        return 1
    fi

    mkdir -p "$backup_dir"

    # Backup database
    if [ -f "$db_path" ]; then
        local db_backup="$backup_dir/ocms_${TIMESTAMP}.db"
        echo "  Database: $db_path"

        sqlite3 "$db_path" ".backup '$db_backup'"

        if [ -f "$db_backup" ]; then
            if sqlite3 "$db_backup" "PRAGMA integrity_check;" 2>/dev/null | grep -q "ok"; then
                echo "  Integrity: OK"
            else
                echo_warn "  Integrity check failed!"
            fi
            gzip "$db_backup"
            echo "  Saved: ${db_backup}.gz ($(ls -lh "${db_backup}.gz" | awk '{print $5}'))"
        else
            echo_error "  Failed to create database backup"
            return 1
        fi
    else
        echo_warn "  Database not found: $db_path (skipping)"
    fi

    # Backup uploads
    if [ -d "$uploads_path" ] && [ "$(ls -A "$uploads_path" 2>/dev/null)" ]; then
        local uploads_backup="$backup_dir/uploads_${TIMESTAMP}.tar.gz"
        tar -czf "$uploads_backup" -C "$instance_dir" uploads
        echo "  Uploads: $uploads_backup ($(ls -lh "$uploads_backup" | awk '{print $5}'))"
    else
        echo "  Uploads: empty (skipping)"
    fi

    # Set ownership
    chown -R "$user" "$backup_dir" 2>/dev/null || true

    # Cleanup old backups
    find "$backup_dir" -name "ocms_*.db.gz" -mtime +$RETENTION_DAYS -delete 2>/dev/null || true
    find "$backup_dir" -name "uploads_*.tar.gz" -mtime +$RETENTION_DAYS -delete 2>/dev/null || true

    echo_info "  Done."
    return 0
}

# --- Main ---

if [ ! -f "$SITES_CONF" ]; then
    echo_error "No sites registered: $SITES_CONF"
    exit 1
fi

TOTAL=0
SUCCESS=0
FAILED=0

while IFS=' ' read -r site_id vhost_path user port; do
    [[ "$site_id" =~ ^#.*$ ]] && continue
    [ -z "$site_id" ] && continue

    # Filter by site-id if provided
    if [ -n "$FILTER_SITE" ] && [ "$site_id" != "$FILTER_SITE" ]; then
        continue
    fi

    TOTAL=$((TOTAL + 1))
    echo ""

    if backup_site "$site_id" "$vhost_path" "$user"; then
        SUCCESS=$((SUCCESS + 1))
    else
        FAILED=$((FAILED + 1))
    fi
done < "$SITES_CONF"

if [ "$TOTAL" -eq 0 ]; then
    if [ -n "$FILTER_SITE" ]; then
        echo_error "Site '$FILTER_SITE' not found in $SITES_CONF"
    else
        echo_error "No sites found in $SITES_CONF"
    fi
    exit 1
fi

echo ""
echo_info "Backup complete: $SUCCESS/$TOTAL succeeded"
[ "$FAILED" -gt 0 ] && echo_warn "$FAILED site(s) failed"
