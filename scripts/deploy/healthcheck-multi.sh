#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Multi-Instance Health Check Script
# Checks all registered oCMS instances and auto-restarts failed ones.
# Designed for cron: */5 * * * * root /opt/ocms/healthcheck-multi.sh
#
# Usage: ./healthcheck-multi.sh [site-id]
#
# If site-id is provided, only that site is checked.
# If omitted, all registered sites are checked.

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
AUTO_RESTART=true
MAX_RESTART_ATTEMPTS=3
RESTART_COOLDOWN=300  # seconds
FILTER_SITE="${1:-}"

# Optional alerting (uncomment and configure)
# ALERT_EMAIL="admin@example.com"
# SLACK_WEBHOOK_URL="https://hooks.slack.com/services/xxx/yyy/zzz"

# --- Functions ---

send_alert() {
    local subject="$1"
    local body="$2"

    if [ -n "${ALERT_EMAIL:-}" ]; then
        echo "$body" | mail -s "$subject" "$ALERT_EMAIL" 2>/dev/null || true
    fi

    if [ -n "${SLACK_WEBHOOK_URL:-}" ]; then
        curl -sf -X POST -H 'Content-type: application/json' \
            --data "{\"text\":\"$subject\n$body\"}" \
            "$SLACK_WEBHOOK_URL" >/dev/null 2>&1 || true
    fi
}

get_restart_count() {
    local site_id="$1"
    local state_file="/tmp/ocms-health-${site_id}"

    if [ -f "$state_file" ]; then
        local last_time count now
        last_time=$(cut -d: -f1 < "$state_file")
        count=$(cut -d: -f2 < "$state_file")
        now=$(date +%s)

        if [ $((now - last_time)) -gt $RESTART_COOLDOWN ]; then
            echo 0
        else
            echo "$count"
        fi
    else
        echo 0
    fi
}

set_restart_count() {
    local site_id="$1"
    local count="$2"
    local state_file="/tmp/ocms-health-${site_id}"
    echo "$(date +%s):$count" > "$state_file"
}

clear_restart_state() {
    local site_id="$1"
    local state_file="/tmp/ocms-health-${site_id}"
    [ -f "$state_file" ] && rm -f "$state_file"
}

health_check() {
    local port="$1"
    local url="http://127.0.0.1:${port}/health/ready"
    curl -sf -o /dev/null -w "%{http_code}" --max-time 10 "$url" 2>/dev/null | grep -q "200"
}

restart_site() {
    local site_id="$1"
    local port="$2"
    local service="ocms@${site_id}"

    local count
    count=$(get_restart_count "$site_id")

    if [ "$count" -ge "$MAX_RESTART_ATTEMPTS" ]; then
        echo_error "[$site_id] Max restart attempts ($MAX_RESTART_ATTEMPTS) reached. Manual intervention required."
        send_alert "CRITICAL: oCMS site $site_id requires manual intervention" \
            "oCMS site $site_id failed $MAX_RESTART_ATTEMPTS restart attempts on $(hostname)."
        return 1
    fi

    echo_warn "[$site_id] Restarting (attempt $((count + 1))/$MAX_RESTART_ATTEMPTS)..."
    systemctl restart "$service"
    sleep 5

    if health_check "$port"; then
        echo_info "[$site_id] Restarted successfully"
        set_restart_count "$site_id" 0
        send_alert "oCMS site $site_id recovered" \
            "oCMS site $site_id on $(hostname) was restarted and is now healthy."
        return 0
    else
        set_restart_count "$site_id" $((count + 1))
        echo_error "[$site_id] Restart failed"
        return 1
    fi
}

check_site() {
    local site_id="$1"
    local port="$2"
    local service="ocms@${site_id}"

    # Only check sites running under systemd
    if ! systemctl is-active --quiet "$service" 2>/dev/null; then
        # Not managed by systemd — skip (may be using ocmsctl or stopped intentionally)
        return 0
    fi

    if health_check "$port"; then
        # Healthy — clear any restart state
        clear_restart_state "$site_id"
        return 0
    else
        echo_error "[$site_id] Health check failed (port $port)"

        send_alert "ALERT: oCMS site $site_id is down" \
            "Health check failed for $site_id on $(hostname) (port $port)"

        if [ "$AUTO_RESTART" = true ]; then
            restart_site "$site_id" "$port"
        fi

        return 1
    fi
}

# --- Main ---

if [ ! -f "$SITES_CONF" ]; then
    echo_error "No sites registered: $SITES_CONF"
    exit 1
fi

FAILURES=0

while IFS=' ' read -r site_id vhost_path user port; do
    [[ "$site_id" =~ ^#.*$ ]] && continue
    [ -z "$site_id" ] && continue

    # Filter by site-id if provided
    if [ -n "$FILTER_SITE" ] && [ "$site_id" != "$FILTER_SITE" ]; then
        continue
    fi

    if ! check_site "$site_id" "$port"; then
        FAILURES=$((FAILURES + 1))
    fi
done < "$SITES_CONF"

exit $FAILURES
