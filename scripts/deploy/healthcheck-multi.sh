#!/bin/bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oCMS Multi-Instance Health Check Script
# Checks all registered oCMS instances, restarts unhealthy ones, reports
# crash-looping and failed units, and exits with the number of sites down.
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
MAX_RESTART_ATTEMPTS=3  # per outage: the count resets once the site is healthy
# Per-site state, in a directory only root can write: in /tmp any local user
# could plant a file that silences a site's alerts and restarts. /run is
# cleared on reboot. <dir>/<site-id> holds "time:count", the restarts of the
# current outage; <dir>/<site-id>.down marks a site already reported down.
STATE_DIR="/run/ocms-health"
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

# get_restart_count prints how many times site $1 was restarted in its
# current outage. Only clear_restart_state, when the site is healthy again,
# resets it, so the cap holds however long the site stays down. (A cooldown
# as long as the cron interval used to reset it between runs, so a dead site
# was restarted, and alerted on, every five minutes for ever.)
get_restart_count() {
    local state_file="${STATE_DIR}/$1"

    if [ -f "$state_file" ]; then
        cut -d: -f2 < "$state_file"
    else
        echo 0
    fi
}

set_restart_count() {
    echo "$(date +%s):$2" > "${STATE_DIR}/$1"
}

clear_restart_state() {
    rm -f "${STATE_DIR}/$1" "${STATE_DIR}/$1.down"
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
        # Alert once: the count goes past the cap, so later runs only log.
        if [ "$count" -eq "$MAX_RESTART_ATTEMPTS" ]; then
            send_alert "CRITICAL: oCMS site $site_id requires manual intervention" \
                "oCMS site $site_id failed $MAX_RESTART_ATTEMPTS restart attempts on $(hostname)."
            set_restart_count "$site_id" $((count + 1))
        fi
        return 1
    fi

    echo_warn "[$site_id] Restarting (attempt $((count + 1))/$MAX_RESTART_ATTEMPTS)..."
    systemctl restart "$service"
    sleep 5

    if health_check "$port"; then
        echo_info "[$site_id] Restarted successfully"
        clear_restart_state "$site_id"
        send_alert "oCMS site $site_id recovered" \
            "oCMS site $site_id on $(hostname) was restarted and is now healthy."
        return 0
    else
        set_restart_count "$site_id" $((count + 1))
        echo_error "[$site_id] Restart failed"
        return 1
    fi
}

# site_down reports site $1 (port $2) as down for reason $3, alerts the
# first time in an outage (the .down marker), restarts the site when $4 is
# "restart" and AUTO_RESTART is on, and returns 1.
site_down() {
    local site_id="$1"
    local port="$2"
    local reason="$3"
    local action="$4"

    echo_error "[$site_id] $reason (port $port)"

    if [ ! -f "${STATE_DIR}/${site_id}.down" ]; then
        : > "${STATE_DIR}/${site_id}.down"
        send_alert "ALERT: oCMS site $site_id is down" \
            "$reason for $site_id on $(hostname) (port $port)"
    fi

    if [ "$action" = restart ] && [ "$AUTO_RESTART" = true ]; then
        restart_site "$site_id" "$port"
    fi

    return 1
}

check_site() {
    local site_id="$1"
    local port="$2"
    local service="ocms@${site_id}"
    local state

    # `systemctl is-active` prints the unit's state; ocms@.service restarts a
    # binary that exits (Restart=always).
    #   activating: it keeps exiting at startup and systemd keeps restarting
    #               it, so it is down, and a restart from here adds nothing.
    #   failed:     a stop that did not end cleanly (the binary exits 1 when
    #               requests outlast its shutdown timeout) or a start systemd
    #               gave up on. It is down, but may have been stopped on
    #               purpose, so it is reported and not restarted.
    #   inactive:   stopped cleanly, never started, or not managed by systemd
    #               (ocmsctl without a unit): skipped.
    state=$(systemctl is-active "$service" 2>/dev/null) || true
    case "$state" in
        active) ;;
        activating)
            site_down "$site_id" "$port" "systemd reports $service as activating: it keeps exiting and systemd keeps restarting it" report
            return 1
            ;;
        failed)
            site_down "$site_id" "$port" "systemd reports $service as failed; not restarted, since it may have been stopped on purpose (systemctl reset-failed $service clears it)" report
            return 1
            ;;
        *)
            return 0
            ;;
    esac

    if health_check "$port"; then
        if [ -f "${STATE_DIR}/${site_id}.down" ]; then
            echo_info "[$site_id] Healthy again"
            send_alert "oCMS site $site_id recovered" \
                "oCMS site $site_id on $(hostname) is healthy again."
        fi
        clear_restart_state "$site_id"
        return 0
    fi
    site_down "$site_id" "$port" "Health check failed" restart
}

# --- Main ---

if [ ! -f "$SITES_CONF" ]; then
    echo_error "No sites registered: $SITES_CONF"
    exit 1
fi

# The state directory must be ours alone (see STATE_DIR).
mkdir -p "$STATE_DIR" 2>/dev/null && chmod 700 "$STATE_DIR" 2>/dev/null
if [ -L "$STATE_DIR" ] || [ ! -d "$STATE_DIR" ] || [ ! -O "$STATE_DIR" ]; then
    echo_error "Cannot use $STATE_DIR for the health state: it must be a directory owned by $(id -un) (run this as root)"
    exit 1
fi

FAILURES=0

while IFS=' ' read -r site_id instance_dir user port; do
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
