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

echo_dry_run() {
    echo -e "$(_ts) ${YELLOW}[DRY-RUN]${NC} $1"
}
