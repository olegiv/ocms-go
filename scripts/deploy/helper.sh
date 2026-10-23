# Helper for shell scripts

# Colors for output (disabled when not a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

# Timestamp for log output
_ts() { date '+%Y-%m-%d %H:%M:%S'; }

# Log functions

echo_info() {
    echo -e "$(_ts) ${BLUE}[INFO]${NC} $1"
}

echo_ok() {
    echo -e "$(_ts) ${GREEN}[OK]${NC} $1"
}

echo_warn() {
    echo -e "$(_ts) ${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "$(_ts) ${RED}[ERROR]${NC} $1" >&2
}

echo_step() {
    echo -e "$(_ts) ${BLUE}==>${NC} $1"
}

# install_binary_command prints the shell command that installs binary $1 as
# $2, the path ocmsctl and ocms@.service run. It copies $1 beside $2 and renames
# the copy over $2: a rename replaces the file even while an instance still runs
# it, where copying onto it fails with "Text file busy". The deploy scripts run
# it before stopping any instance, so a failed install leaves them running.
# The paths are single-quoted in the command, so they must not contain a
# single quote.
install_binary_command() {
    printf "cp '%s' '%s.new' && chmod 755 '%s.new' && mv -f '%s.new' '%s'" "$1" "$2" "$2" "$2" "$2"
}

# backup_binary_command prints the shell command that backs up binary $1 to
# $1.backup through a copy renamed into place, so a failed copy (a full disk)
# fails the command and leaves the previous backup whole. With no $1 yet, as on
# a fresh server, it says so and succeeds.
backup_binary_command() {
    printf "if [ -e '%s' ]; then cp -p '%s' '%s.backup.new' && mv -f '%s.backup.new' '%s.backup'; else echo 'No %s yet; nothing to back up.' >&2; fi" "$1" "$1" "$1" "$1" "$1" "$1"
}

# remote_upload_path prints where the deploy scripts upload binary name $2 in
# directory $1: $1/$2, unless $2 names a file the deploy itself uses there
# (the running binary, the install's and the backup's staging copies, the
# backup, ocmsctl), and then $1/ocms.upload. The upload happens while the
# instances still run, so scp onto ocms failed with "Text file busy", and the
# other names would have overwritten the install's copy, the backup or the
# control script.
remote_upload_path() {
    case "${2#./}" in
        ocms|ocms.new|ocms.backup|ocms.backup.new|ocmsctl) printf '%s/ocms.upload' "$1" ;;
        *) printf '%s/%s' "$1" "$2" ;;
    esac
}

# wait_healthy_command prints the shell command that waits for instance $2 to
# report healthy: it runs `$1 health $2` (ocmsctl) up to $3 times, $4 seconds
# apart, and when no attempt succeeds prints the last one's output (the HTTP
# status, or why ocmsctl could not check) and fails. Starting a systemd unit
# returns before the binary has read its config, so one that exits at startup
# still "starts": on 2026-10-23 every instance crash-looped on a missing
# OCMS_MFA_KEY while the deploy reported success. The path and instance are
# single-quoted in the command, so they must not contain a single quote.
wait_healthy_command() {
    printf "n=1; until out=\$('%s' health '%s' 2>&1); do [ \"\$n\" -lt %d ] || { echo \"\$out\"; exit 1; }; n=\$((n + 1)); sleep %d; done" "$1" "$2" "$3" "$4"
}

# check_health_settings stops the deploy unless HEALTH_RETRIES is a positive
# whole number and HEALTH_INTERVAL a whole number of seconds.
check_health_settings() {
    if [[ ! "$HEALTH_RETRIES" =~ ^[1-9][0-9]*$ ]]; then
        echo_error "HEALTH_RETRIES must be a positive whole number, got '${HEALTH_RETRIES}'"
        exit 1
    fi
    # No leading zeros: printf %d reads 08 as an invalid octal number.
    if [[ ! "$HEALTH_INTERVAL" =~ ^(0|[1-9][0-9]*)$ ]]; then
        echo_error "HEALTH_INTERVAL must be a whole number of seconds, got '${HEALTH_INTERVAL}'"
        exit 1
    fi
}

# remote_failed records that a remote step for instance $3 failed with ssh
# exit status $1: it adds the instance to UNHEALTHY, which the caller
# declares, and prints $4, or, when ssh lost the connection (exit 255), that
# the instance's state is unknown after "$2" (what the step was doing).
remote_failed() {
    if [[ "$1" -eq 255 ]]; then
        echo_error "Lost the connection to the server while ${2} '${3}'; its state is unknown"
    else
        echo_error "$4"
    fi
    UNHEALTHY+=("$3")
}

# check_instances waits for each instance named in its arguments to report
# healthy, shows each one's `ocmsctl status`, then asks each healthy one once
# more: ocms@.service restarts a binary that exits, so one that dies after its
# first answer would pass the wait alone. Each instance that failed goes to
# UNHEALTHY through remote_failed. It runs commands through the calling
# script's ssh_cmd and reads REMOTE_BIN_DIR, HEALTH_RETRIES and
# HEALTH_INTERVAL.
check_instances() {
    local instance rc
    local -a healthy=()
    for instance in "$@"; do
        rc=0
        ssh_cmd "$(wait_healthy_command "${REMOTE_BIN_DIR}/ocmsctl" "${instance}" "${HEALTH_RETRIES}" "${HEALTH_INTERVAL}")" || rc=$?
        if [[ "$rc" -eq 0 ]]; then
            echo_ok "Instance '${instance}' is healthy"
            healthy+=("${instance}")
        else
            remote_failed "$rc" "waiting for" "${instance}" "Instance '${instance}' did not report healthy after ${HEALTH_RETRIES} attempts"
        fi
    done
    for instance in "$@"; do
        ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl status ${instance}" || echo_warn "Could not get the status of '${instance}'"
    done
    for instance in ${healthy[@]+"${healthy[@]}"}; do
        rc=0
        ssh_cmd "${REMOTE_BIN_DIR}/ocmsctl health ${instance}" || rc=$?
        if [[ "$rc" -ne 0 ]]; then
            remote_failed "$rc" "checking" "${instance}" "Instance '${instance}' stopped reporting healthy after it started"
        fi
    done
}

# fail_deploy reports the instances in UNHEALTHY and where to look, and exits
# 1. With $1 true, the deploy backed up the binary it replaced, and it says
# where that copy is.
fail_deploy() {
    echo ""
    echo_error "Deployment failed for: ${UNHEALTHY[*]}"
    echo "  Logs:   ssh ${SSH_USER}@${SERVER} ${REMOTE_BIN_DIR}/ocmsctl logs <instance>"
    if [[ "$1" == true ]]; then
        echo "  Backup: ${REMOTE_RUN_BINARY}.backup holds the binary this deploy replaced, if there was one"
    fi
    exit 1
}

echo_dry_run() {
    echo -e "$(_ts) ${YELLOW}[DRY-RUN]${NC} $1"
}
