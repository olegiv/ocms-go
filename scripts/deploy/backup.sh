#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Backup Script
# Usage: ./backup.sh [backup-directory]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helper.sh"

# Configuration
BACKUP_DIR="${1:-/var/backups/ocms}"
DB_PATH="/var/lib/ocms/data/ocms.db"
UPLOADS_PATH="/var/lib/ocms/uploads"
RETENTION_DAYS=30
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

mkdir -p "$BACKUP_DIR"

echo_info "Starting backup..."
echo_info "Backup directory: $BACKUP_DIR"

if [ ! -f "$DB_PATH" ]; then
    echo_error "Database not found at $DB_PATH"
    exit 1
fi

# Backup database
echo_info "Backing up database..."
DB_BACKUP="$BACKUP_DIR/ocms_${TIMESTAMP}.db"
sqlite3 "$DB_PATH" ".backup '$DB_BACKUP'"

if [ -f "$DB_BACKUP" ]; then
    if sqlite3 "$DB_BACKUP" "PRAGMA integrity_check;" | grep -q "ok"; then
        echo_info "Database integrity verified"
    else
        echo_warn "Database integrity check failed!"
    fi
    gzip "$DB_BACKUP"
    echo_info "Database backup: ${DB_BACKUP}.gz ($(ls -lh "${DB_BACKUP}.gz" | awk '{print $5}'))"
else
    echo_error "Failed to create database backup"
    exit 1
fi

# Backup uploads
if [ -d "$UPLOADS_PATH" ] && [ "$(ls -A "$UPLOADS_PATH" 2>/dev/null)" ]; then
    echo_info "Backing up uploads..."
    UPLOADS_BACKUP="$BACKUP_DIR/uploads_${TIMESTAMP}.tar.gz"
    tar -czf "$UPLOADS_BACKUP" -C "$(dirname "$UPLOADS_PATH")" "$(basename "$UPLOADS_PATH")"
    echo_info "Uploads backup: $UPLOADS_BACKUP ($(ls -lh "$UPLOADS_BACKUP" | awk '{print $5}'))"
else
    echo_info "Uploads directory empty, skipping..."
fi

# Cleanup old backups
echo_info "Cleaning up backups older than $RETENTION_DAYS days..."
find "$BACKUP_DIR" -name "ocms_*.db.gz" -mtime +$RETENTION_DAYS -delete 2>/dev/null || true
find "$BACKUP_DIR" -name "uploads_*.tar.gz" -mtime +$RETENTION_DAYS -delete 2>/dev/null || true

echo_info "Backup complete!"
