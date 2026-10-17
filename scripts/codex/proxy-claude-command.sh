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

# Each command runs with a fixed list of tools; anything else is denied
# without a prompt (--permission-mode dontAsk), and the command reports the
# check it could not run. The commands read diffs, logs and scan output, so a
# session that may run any shell command can be talked into one by text it
# reads. Rules for tools that run project or package code (go, the linters,
# npm) are exact: a prefix such as "go test*" would also allow "go test -exec
# <program>", "go env*" would allow "go env -w", and "npm audit*" would allow
# "npm audit fix", which installs packages. The git rules stay prefixes:
# "git commit" runs the repository's own hooks, and diff, log and show accept
# --output=<file>, which writes a file. This is a best-effort fence, not a
# sandbox.
read_tools=(
  Read Glob Grep Agent Skill TodoWrite
  "Bash(git status*)" "Bash(git diff*)" "Bash(git log*)" "Bash(git show*)"
  "Bash(git rev-parse*)" "Bash(git ls-files*)" "Bash(git write-tree*)"
  "Bash(date*)" "Bash(go version)" "Bash(go env GOWORK)" "Bash(go env GOTOOLDIR)"
  "Bash(grep *)"
)
check_tools=(
  "Bash(scripts/test-gate.sh)" "Bash(./scripts/test-gate.sh)"
  "Bash(go vet ./...)" "Bash(go build ./...)" "Bash(gofmt -l *)"
  "Bash(staticcheck ./...)" "Bash(errcheck -ignoregenerated ./...)" "Bash(golangci-lint run ./...)"
  "Bash(nilaway ./...)" "Bash(goimports -l *)" "Bash(govulncheck *)"
)
tools=("${read_tools[@]}" "${check_tools[@]}")
case "$slash_command" in
  /code-quality | /commit-prepare) ;;
  /commit-do)
    tools+=("Bash(git add -- *)" "Bash(git commit *)")
    ;;
  /security-audit)
    # No mv: a pattern such as "mv .audit/*" cannot stop a "../" target.
    tools+=("Bash(npm audit)" "Bash(npm audit --json)" "Bash(npm outdated)" "Bash(mkdir -p .audit/*)" "Edit(.audit/**)")
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
