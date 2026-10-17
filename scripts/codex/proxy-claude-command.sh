#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOT'
Usage:
  ./scripts/codex/proxy-claude-command.sh /command [args...]

Commands: /commit-prepare, /commit-do, /code-quality, /security-audit
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

# Each command runs with only the tools it needs; anything else is denied
# without a prompt (--permission-mode dontAsk). The commands read diffs, logs
# and scan output, so a session that may run any shell command can be talked
# into one by text it reads.
read_tools=(
  Read Glob Grep Agent Skill TodoWrite
  "Bash(git status*)" "Bash(git diff*)" "Bash(git log*)" "Bash(git show*)"
  "Bash(git rev-parse*)" "Bash(git ls-files*)" "Bash(git write-tree*)"
  "Bash(date*)" "Bash(go version*)" "Bash(go env*)" "Bash(grep *)"
)
check_tools=(
  "Bash(scripts/test-gate.sh*)" "Bash(./scripts/test-gate.sh*)"
  "Bash(go vet*)" "Bash(go build*)" "Bash(go test*)" "Bash(gofmt -l*)"
  "Bash(staticcheck*)" "Bash(errcheck*)" "Bash(golangci-lint run*)"
  "Bash(nilaway*)" "Bash(goimports -l*)" "Bash(govulncheck*)"
)
case "$slash_command" in
  /code-quality | /commit-prepare)
    tools=("${read_tools[@]}" "${check_tools[@]}")
    ;;
  /commit-do)
    tools=("${read_tools[@]}" "${check_tools[@]}" "Bash(git add -- *)" "Bash(git commit *)")
    ;;
  /security-audit)
    tools=("${read_tools[@]}" "${check_tools[@]}"
      "Bash(npm audit*)" "Bash(npm outdated*)" "Bash(mkdir -p .audit*)" "Bash(mv .audit/*)"
      "Edit(.audit/**)")
    ;;
  *)
    echo "Error: $slash_command is not a proxied command."
    usage
    exit 2
    ;;
esac

if ! command -v claude >/dev/null 2>&1; then
  echo "Error: 'claude' binary is not available in PATH."
  exit 127
fi

prompt="$slash_command"
if [[ $# -gt 0 ]]; then
  prompt="$prompt $*"
fi

echo "Proxying to Claude command: $prompt"
exec claude --permission-mode dontAsk --allowedTools "${tools[@]}" -p "$prompt"
