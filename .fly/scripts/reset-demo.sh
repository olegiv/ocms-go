#!/bin/sh
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# oCMS Demo Reset Script
# Resets the demo database and uploads to initial state
#
# Usage:
#   Manual: fly ssh console -C "/app/scripts/reset-demo.sh"
#   Or:     fly machines restart

set -e

# Configuration
DATA_DIR="${OCMS_DATA_DIR:-/app/data}"
DB_PATH="${OCMS_DB_PATH:-$DATA_DIR/ocms.db}"
UPLOADS_DIR="${OCMS_UPLOADS_DIR:-$DATA_DIR/uploads}"
BACKUP_DIR="$DATA_DIR/backups"
LOG_FILE="$DATA_DIR/reset.log"

# Logging function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

log "=== Starting oCMS Demo Reset ==="

# Create backup directory if it doesn't exist
mkdir -p "$BACKUP_DIR"

# Backup current database (keep last 3 backups for debugging)
if [ -f "$DB_PATH" ]; then
    BACKUP_NAME="ocms-backup-$(date '+%Y%m%d-%H%M%S').db"
    log "Backing up current database to $BACKUP_NAME"
    cp "$DB_PATH" "$BACKUP_DIR/$BACKUP_NAME"

    # Keep only last 3 backups
    ls -t "$BACKUP_DIR"/ocms-backup-*.db 2>/dev/null | tail -n +4 | xargs -r rm -f
    log "Cleaned old backups, keeping last 3"
fi

# Remove current database and WAL files
log "Removing current database..."
rm -f "$DB_PATH" "$DB_PATH-wal" "$DB_PATH-shm"

# Clear uploads directory but keep structure
if [ -d "$UPLOADS_DIR" ]; then
    log "Clearing uploads directory..."
    find "$UPLOADS_DIR" -type f -delete 2>/dev/null || true
    log "Uploads cleared"
fi

# Ensure uploads directory exists with correct permissions
mkdir -p "$UPLOADS_DIR"
chown -R ocms:ocms "$DATA_DIR" 2>/dev/null || true

log "Database and uploads cleared"
log "Application will recreate database with demo content on next start"
log "=== Demo Reset Complete ==="

# Note: The application will auto-migrate and seed on startup
# when OCMS_DO_SEED=true is set

echo ""
echo "Reset complete! Restart the application to apply:"
echo "  fly machines restart"
echo ""
echo "Or the app will restart automatically if health checks fail."
