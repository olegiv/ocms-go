#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOT'
Usage:
  ./scripts/codex/proxy-claude-command.sh /command [args...]
EOT
}

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

slash_command="$1"
shift || true

if [[ "$slash_command" != /* ]]; then
  slash_command="/$slash_command"
fi

if ! command -v claude >/dev/null 2>&1; then
  echo "Error: 'claude' binary is not available in PATH."
  exit 127
fi

prompt="$slash_command"
if [[ $# -gt 0 ]]; then
  prompt="$prompt $*"
fi

echo "Proxying to Claude command: $prompt"
exec claude --dangerously-skip-permissions -p "$prompt"
