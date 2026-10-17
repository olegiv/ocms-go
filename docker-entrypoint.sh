#!/bin/sh
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# Docker entrypoint script for oCMS
# Handles volume permissions before starting the application

set -e

# Data directories that need write access. The rest of /app/custom is a
# read-only mount of compiled-in modules and is left alone.
DATA_DIRS="/app/data /app/uploads /app/custom/themes"

# TMPDIR may point into a data volume (docker-compose.yml does, because the
# image filesystem is read-only there). It is created as the user the app
# runs as: under the compose capability set root has no DAC_OVERRIDE and
# cannot write into the ocms-owned volume. Arguments run the mkdir, e.g.
# through su-exec.
ensure_tmpdir() {
    if [ -n "${TMPDIR:-}" ]; then
        "$@" mkdir -p "$TMPDIR" 2>/dev/null || echo "warning: could not create TMPDIR $TMPDIR"
    fi
}

# If running as root, fix permissions and switch to ocms user
if [ "$(id -u)" = "0" ]; then
    echo "Fixing volume permissions..."
    for dir in $DATA_DIRS; do
        if [ -d "$dir" ]; then
            chown -R ocms:ocms "$dir" 2>/dev/null || echo "warning: could not change ownership of $dir (read-only mount?)"
        fi
    done
    ensure_tmpdir su-exec ocms
    echo "Starting as ocms user..."
    exec su-exec ocms "$@"
fi

# Already running as non-root, just exec the command
ensure_tmpdir
exec "$@"
