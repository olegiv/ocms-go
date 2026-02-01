#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Multi-Instance Site Setup Script
# Provisions a new oCMS site inside a Plesk vhost directory.
#
# Usage: sudo ./setup-site.sh <domain> <system-user> [port] [group]
#
# Arguments:
#   domain       - The domain name (e.g., example.com)
#   system-user  - The Plesk vhost system user
#   port         - Optional port number (auto-assigned if omitted)
#   group        - Optional group name (default: psaserv)
#
# Example:
#   sudo ./setup-site.sh example.com example_com
#   sudo ./setup-site.sh blog.example.com bloguser 8085
#   sudo ./setup-site.sh shop.example.com shopuser 8086 psacln

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
VHOSTS_BASE="/var/www/vhosts"
SITES_CONF="/etc/ocms/sites.conf"
BINARY_PATH="/opt/ocms/bin/ocms"
BASE_PORT=8081

# --- Functions ---

usage() {
    echo "Usage: sudo $0 <domain> <system-user> [port] [group]"
    echo ""
    echo "Arguments:"
    echo "  domain       Domain name (e.g., example.com)"
    echo "  system-user  Plesk vhost system user"
    echo "  port         Port number (auto-assigned from $BASE_PORT if omitted)"
    echo "  group        System group (default: psaserv)"
    echo ""
    echo "Examples:"
    echo "  sudo $0 example.com example_com"
    echo "  sudo $0 blog.example.com bloguser 8085"
    exit 1
}

domain_to_site_id() {
    echo "$1" | tr '.' '_' | tr '-' '_'
}

next_available_port() {
    local port=$BASE_PORT
    if [ -f "$SITES_CONF" ]; then
        while grep -q " ${port}$" "$SITES_CONF" 2>/dev/null; do
            port=$((port + 1))
        done
    fi
    echo "$port"
}

print_nginx_snippet() {
    local port="$1"
    local domain="$2"
    echo ""
    echo "=========================================="
    echo " Nginx Configuration for Plesk"
    echo "=========================================="
    echo ""
    echo "Paste into: Plesk > Websites & Domains > $domain"
    echo "            > Apache & nginx Settings"
    echo "            > Additional nginx directives"
    echo ""
    echo "--- START COPY ---"
    echo ""
    cat << NGINX_EOF
# oCMS reverse proxy to port $port
location ~ ^/(.*)\$ {
    proxy_pass http://127.0.0.1:${port}/\$1\$is_args\$args;
    proxy_http_version 1.1;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_set_header Upgrade \$http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_connect_timeout 60s;
    proxy_send_timeout 60s;
    proxy_read_timeout 60s;
    client_max_body_size 100M;
}

# Block access to hidden files
location ~ /\\. {
    deny all;
}
NGINX_EOF
    echo ""
    echo "--- END COPY ---"
    echo ""
}

# --- Argument Validation ---

if [ "$EUID" -ne 0 ]; then
    echo_error "Run as root (use sudo)"
    exit 1
fi

DOMAIN="${1:-}"
SYSTEM_USER="${2:-}"
PORT="${3:-}"
GROUP="${4:-psaserv}"

if [ -z "$DOMAIN" ] || [ -z "$SYSTEM_USER" ]; then
    usage
fi

SITE_ID=$(domain_to_site_id "$DOMAIN")
VHOST_PATH="$VHOSTS_BASE/$DOMAIN"
INSTANCE_DIR="$VHOST_PATH/ocms"

# Validate vhost directory
if [ ! -d "$VHOST_PATH" ]; then
    echo_error "Vhost directory not found: $VHOST_PATH"
    echo "       Make sure the domain is configured in Plesk first."
    exit 1
fi

# Validate system user
if ! id "$SYSTEM_USER" &>/dev/null; then
    echo_error "System user not found: $SYSTEM_USER"
    exit 1
fi

# Check if site already exists
if [ -f "$SITES_CONF" ] && grep -q "^${SITE_ID} " "$SITES_CONF" 2>/dev/null; then
    echo_error "Site '$SITE_ID' is already registered in $SITES_CONF"
    exit 1
fi

# Check if instance directory already exists
if [ -d "$INSTANCE_DIR" ]; then
    echo_warn "Directory $INSTANCE_DIR already exists."
    echo "       If you want to reconfigure, remove it first."
    exit 1
fi

# Assign port
if [ -z "$PORT" ]; then
    PORT=$(next_available_port)
    echo_info "Auto-assigned port: $PORT"
else
    if [ -f "$SITES_CONF" ] && grep -q " ${PORT}$" "$SITES_CONF" 2>/dev/null; then
        echo_error "Port $PORT is already assigned to another site"
        exit 1
    fi
fi

# Check binary exists
if [ ! -x "$BINARY_PATH" ]; then
    echo_warn "Binary not found at $BINARY_PATH"
    echo "       Copy it before starting the service."
fi

# --- Setup ---

echo_info "Setting up oCMS site: $DOMAIN"
echo_info "  Site ID:    $SITE_ID"
echo_info "  User:       $SYSTEM_USER:$GROUP"
echo_info "  Port:       $PORT"
echo_info "  Directory:  $INSTANCE_DIR"

# Create directory structure
# Note: Core themes (default, developer) are embedded in the binary.
# The custom/ directory is for user-created themes that override or extend core themes.
echo_info "Creating directories..."
mkdir -p "$INSTANCE_DIR"/{data,uploads,custom/themes,backups,logs}

# Generate session secret
SESSION_SECRET=$(openssl rand -base64 32)

# Create .env file
echo_info "Creating environment config..."
cat > "$INSTANCE_DIR/.env" << EOF
# oCMS Configuration for $DOMAIN
# Generated by setup-site.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)

# Session encryption secret (unique per site)
OCMS_SESSION_SECRET=$SESSION_SECRET

# Server configuration
OCMS_SERVER_HOST=127.0.0.1
OCMS_SERVER_PORT=$PORT
OCMS_ENV=production
OCMS_LOG_LEVEL=warn

# Database (relative to WorkingDirectory)
OCMS_DB_PATH=./data/ocms.db

# Custom content directory (for theme overrides and custom modules)
# Core themes (default, developer) are embedded in the binary.
# Place custom themes in ./custom/themes/ to override or extend them.
OCMS_CUSTOM_DIR=./custom
OCMS_ACTIVE_THEME=default

# Optional: Redis for distributed caching
# OCMS_REDIS_URL=redis://localhost:6379/0

# Optional: hCaptcha for bot protection
# OCMS_HCAPTCHA_SITE_KEY=your-site-key
# OCMS_HCAPTCHA_SECRET_KEY=your-secret-key
EOF

# Set ownership and permissions
echo_info "Setting ownership and permissions..."
chown -R "$SYSTEM_USER:$GROUP" "$INSTANCE_DIR"
chmod 750 "$INSTANCE_DIR"
chmod 600 "$INSTANCE_DIR/.env"
chmod 755 "$INSTANCE_DIR/data"
chmod 755 "$INSTANCE_DIR/uploads"
chmod 755 "$INSTANCE_DIR/custom"
chmod 755 "$INSTANCE_DIR/custom/themes"
chmod 755 "$INSTANCE_DIR/backups"
chmod 755 "$INSTANCE_DIR/logs"

# Create systemd drop-in override
echo_info "Creating systemd drop-in override..."
DROPIN_DIR="/etc/systemd/system/ocms@${SITE_ID}.service.d"
mkdir -p "$DROPIN_DIR"
cat > "$DROPIN_DIR/instance.conf" << EOF
# oCMS instance override for $DOMAIN
# Generated by setup-site.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)

[Service]
User=$SYSTEM_USER
Group=$GROUP
WorkingDirectory=$INSTANCE_DIR
EnvironmentFile=$INSTANCE_DIR/.env
ReadWritePaths=$INSTANCE_DIR
SyslogIdentifier=ocms-$SITE_ID
EOF

systemctl daemon-reload

# Register in sites.conf
echo_info "Registering site..."
mkdir -p "$(dirname "$SITES_CONF")"
if [ ! -f "$SITES_CONF" ]; then
    cat > "$SITES_CONF" << 'HEADER'
# oCMS Multi-Instance Site Registry
# Format: SITE_ID VHOST_PATH SYSTEM_USER PORT
# Managed by setup-site.sh — do not edit while services are running
HEADER
fi
echo "$SITE_ID $VHOST_PATH $SYSTEM_USER $PORT" >> "$SITES_CONF"

# --- Summary ---

echo ""
echo_info "Site '$SITE_ID' configured successfully!"
echo ""
echo "  Directory:  $INSTANCE_DIR"
echo "  User:       $SYSTEM_USER:$GROUP"
echo "  Port:       $PORT"
echo "  Env file:   $INSTANCE_DIR/.env"
echo "  Systemd:    ocms@$SITE_ID"
echo ""
echo "Core themes (default, developer) are embedded in the binary."
echo "To use a custom theme, place it in: $INSTANCE_DIR/custom/themes/"
echo ""
echo "Next steps:"
echo "  1. Configure Plesk nginx (paste the snippet below)"
echo "  2. Test:     sudo ocmsctl start $SITE_ID"
echo "               curl http://127.0.0.1:$PORT/health"
echo "               sudo ocmsctl stop $SITE_ID"
echo "  3. Enable:   sudo systemctl enable --now ocms@$SITE_ID"
echo "  4. Logs:     sudo journalctl -u ocms@$SITE_ID -f"
echo "  5. Login:    https://$DOMAIN/admin/"
echo "               Default: admin@example.com / changeme1234"

print_nginx_snippet "$PORT" "$DOMAIN"
