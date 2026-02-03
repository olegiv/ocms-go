#!/bin/sh
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# Docker entrypoint script for oCMS
# Handles volume permissions before starting the application

set -e

# Data directories that need write access
DATA_DIRS="/app/data /app/uploads /app/custom"

# If running as root, fix permissions and switch to ocms user
if [ "$(id -u)" = "0" ]; then
    echo "Fixing volume permissions..."
    for dir in $DATA_DIRS; do
        if [ -d "$dir" ]; then
            chown -R ocms:ocms "$dir"
        fi
    done
    echo "Starting as ocms user..."
    exec su-exec ocms "$@"
fi

# Already running as non-root, just exec the command
exec "$@"
