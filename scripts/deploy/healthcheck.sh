#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Health Check Script
# Usage: ./healthcheck.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helper.sh"

# Configuration
HEALTH_URL="http://127.0.0.1:8081/health/ready"
SERVICE_NAME="ocms"
AUTO_RESTART=true
MAX_RESTART_ATTEMPTS=3
RESTART_COOLDOWN=300

# Optional alerting (uncomment and configure)
# ALERT_EMAIL="admin@example.com"
# SLACK_WEBHOOK_URL="https://hooks.slack.com/services/xxx/yyy/zzz"

STATE_FILE="/tmp/ocms-health-state"

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
    if [ -f "$STATE_FILE" ]; then
        local state
        local last_time
        local count
        local now
        state=$(cat "$STATE_FILE")
        last_time=$(echo "$state" | cut -d: -f1)
        count=$(echo "$state" | cut -d: -f2)
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
    echo "$(date +%s):$1" > "$STATE_FILE"
}

health_check() {
    curl -sf -o /dev/null -w "%{http_code}" --max-time 10 "$HEALTH_URL" 2>/dev/null | grep -q "200"
}

restart_service() {
    local count
    count=$(get_restart_count)

    if [ "$count" -ge "$MAX_RESTART_ATTEMPTS" ]; then
        echo_error "Max restart attempts reached. Manual intervention required."
        send_alert "CRITICAL: oCMS requires manual intervention" \
            "oCMS failed $MAX_RESTART_ATTEMPTS restart attempts on $(hostname)."
        return 1
    fi

    echo_warn "Restarting (attempt $((count + 1))/$MAX_RESTART_ATTEMPTS)..."
    sudo systemctl restart "$SERVICE_NAME"
    sleep 5

    if health_check; then
        echo_info "Service restarted successfully"
        set_restart_count 0
        send_alert "oCMS recovered" "oCMS on $(hostname) was restarted and is now healthy."
        return 0
    else
        set_restart_count $((count + 1))
        echo_error "Restart failed"
        return 1
    fi
}

# Main
if health_check; then
    [ -f "$STATE_FILE" ] && rm -f "$STATE_FILE"
    exit 0
else
    echo_error "Health check failed: $HEALTH_URL"

    if ! systemctl is-active --quiet "$SERVICE_NAME"; then
        echo_error "$SERVICE_NAME is not running"
    fi

    send_alert "ALERT: oCMS is down" "Health check failed on $(hostname)"

    if [ "$AUTO_RESTART" = true ]; then
        restart_service
    fi

    exit 1
fi
