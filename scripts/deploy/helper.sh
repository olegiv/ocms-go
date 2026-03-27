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

echo_dry_run() {
    echo -e "$(_ts) ${YELLOW}[DRY-RUN]${NC} $1"
}
