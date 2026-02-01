# Helper for shell scripts

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Log functions

echo_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

echo_ok() {
    echo -e "${GREEN}[OK]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

echo_step() {
    echo -e "${BLUE}==>${NC} $1"
}

echo_dry_run() {
    echo -e "${YELLOW}[DRY-RUN]${NC} $1"
}
