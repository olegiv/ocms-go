#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Initial Setup Script
# Usage: sudo ./setup.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helper.sh"

if [ "$EUID" -ne 0 ]; then
    echo_error "Run as root (use sudo)"
    exit 1
fi

echo_info "Starting oCMS setup..."

# Create user
if id "ocms" &>/dev/null; then
    echo_info "User 'ocms' exists"
else
    echo_info "Creating user 'ocms'..."
    useradd -r -s /bin/false -d /var/lib/ocms ocms
fi

# Create directories
echo_info "Creating directories..."
mkdir -p /opt/ocms/bin
mkdir -p /var/lib/ocms/data
mkdir -p /var/lib/ocms/uploads
mkdir -p /var/lib/ocms/themes
mkdir -p /etc/ocms
mkdir -p /var/backups/ocms

chown -R ocms:ocms /var/lib/ocms
chown -R ocms:ocms /opt/ocms
chown -R ocms:ocms /var/backups/ocms
chmod 755 /opt/ocms/bin

# Create env file
if [ ! -f /etc/ocms/ocms.env ]; then
    echo_info "Creating environment config..."
    SESSION_SECRET=$(openssl rand -base64 32)

    cat > /etc/ocms/ocms.env << EOF
# oCMS Configuration

OCMS_SESSION_SECRET=$SESSION_SECRET
OCMS_SERVER_HOST=127.0.0.1
OCMS_SERVER_PORT=8081
OCMS_ENV=production
OCMS_LOG_LEVEL=warn
OCMS_DB_PATH=/var/lib/ocms/data/ocms.db
OCMS_THEMES_DIR=/var/lib/ocms/themes
OCMS_ACTIVE_THEME=default
EOF

    chown ocms:ocms /etc/ocms/ocms.env
    chmod 600 /etc/ocms/ocms.env
    echo_info "Config created: /etc/ocms/ocms.env"
else
    echo_info "Config exists: /etc/ocms/ocms.env"
fi

# Install systemd service
if [ -f "$SCRIPT_DIR/ocms.service" ]; then
    echo_info "Installing systemd service..."
    cp "$SCRIPT_DIR/ocms.service" /etc/systemd/system/ocms.service
    systemctl daemon-reload
    systemctl enable ocms
fi

# Install scripts
echo_info "Installing scripts..."
for script in deploy.sh backup.sh restore.sh healthcheck.sh helper.sh; do
    if [ -f "$SCRIPT_DIR/$script" ]; then
        cp "$SCRIPT_DIR/$script" /opt/ocms/
        chmod 755 /opt/ocms/$script
    fi
done

# Setup cron
echo_info "Setting up cron..."
cat > /etc/cron.d/ocms << 'EOF'
# oCMS automated tasks
0 3 * * * root /opt/ocms/backup.sh >> /var/log/ocms-backup.log 2>&1
*/5 * * * * root /opt/ocms/healthcheck.sh 2>&1 | grep -v "^$"
EOF
chmod 644 /etc/cron.d/ocms

echo_info "Setup complete!"
echo ""
echo "Next steps:"
echo "  1. Copy binary to /opt/ocms/bin/ocms"
echo "  2. Review /etc/ocms/ocms.env"
echo "  3. Configure Plesk nginx proxy"
echo "  4. Start: sudo systemctl start ocms"
echo "  5. Change default password (admin@example.com / changeme1234)"
