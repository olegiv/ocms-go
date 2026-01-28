#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Restore Script
# Usage: ./restore.sh <database-backup.db.gz> [uploads-backup.tar.gz]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helper.sh"

# Configuration
DB_PATH="/var/lib/ocms/data/ocms.db"
UPLOADS_PATH="/var/lib/ocms/uploads"
SERVICE_NAME="ocms"

DB_BACKUP="$1"
UPLOADS_BACKUP="$2"

if [ -z "$DB_BACKUP" ]; then
    echo "Usage: $0 <database-backup.db.gz> [uploads-backup.tar.gz]"
    exit 1
fi

if [ ! -f "$DB_BACKUP" ]; then
    echo_error "Database backup not found: $DB_BACKUP"
    exit 1
fi

echo_info "Starting restore..."
echo_warn "This will replace the current database!"
read -p "Continue? (yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
    echo_info "Cancelled."
    exit 0
fi

echo_info "Stopping $SERVICE_NAME..."
sudo systemctl stop "$SERVICE_NAME" || true
sleep 2

# Backup current before restore
if [ -f "$DB_PATH" ]; then
    CURRENT_BACKUP="${DB_PATH}.pre-restore-$(date +%Y%m%d_%H%M%S)"
    echo_info "Backing up current database to $CURRENT_BACKUP"
    cp "$DB_PATH" "$CURRENT_BACKUP"
fi

# Restore database
echo_info "Restoring database..."
TEMP_DB="/tmp/ocms_restore_$$.db"

if [[ "$DB_BACKUP" == *.gz ]]; then
    gunzip -c "$DB_BACKUP" > "$TEMP_DB"
else
    cp "$DB_BACKUP" "$TEMP_DB"
fi

if ! sqlite3 "$TEMP_DB" "PRAGMA integrity_check;" | grep -q "ok"; then
    echo_error "Database integrity check failed!"
    rm -f "$TEMP_DB"
    exit 1
fi

mv "$TEMP_DB" "$DB_PATH"
chown ocms:ocms "$DB_PATH" 2>/dev/null || true
chmod 644 "$DB_PATH"
echo_info "Database restored"

# Restore uploads
if [ -n "$UPLOADS_BACKUP" ] && [ -f "$UPLOADS_BACKUP" ]; then
    echo_info "Restoring uploads..."
    if [ -d "$UPLOADS_PATH" ] && [ "$(ls -A "$UPLOADS_PATH" 2>/dev/null)" ]; then
        mv "$UPLOADS_PATH" "${UPLOADS_PATH}.pre-restore-$(date +%Y%m%d_%H%M%S)"
    fi
    mkdir -p "$UPLOADS_PATH"
    tar -xzf "$UPLOADS_BACKUP" -C "$(dirname "$UPLOADS_PATH")"
    chown -R ocms:ocms "$UPLOADS_PATH" 2>/dev/null || true
    echo_info "Uploads restored"
fi

echo_info "Starting $SERVICE_NAME..."
sudo systemctl start "$SERVICE_NAME"
sleep 3

if curl -sf --max-time 10 "http://127.0.0.1:8081/health" > /dev/null 2>&1; then
    echo_info "Health check passed!"
else
    echo_warn "Health check failed - check service manually"
fi

echo_info "Restore complete!"
