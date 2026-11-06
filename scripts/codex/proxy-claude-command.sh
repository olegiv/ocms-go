#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOT'
Usage:
  ./scripts/codex/proxy-claude-command.sh /command [args...]

Commands: /commit-prepare, /commit-do, /code-quality, /security-audit

/code-quality runs the repository's strict lint check before Claude.
Exit 1 means findings; exit 3 means the check could not run.
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
  "Bash(golangci-lint version)"
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
claude_args=(--permission-mode dontAsk --allowedTools "${tools[@]}" -p "$prompt")
if [[ "$slash_command" == /code-quality ]]; then
  # The shared command's variable-based hook invocation is not auto-approved.
  # Run the fixed local check here so dontAsk cannot silently omit it. This
  # grants no extra shell or install permissions to the Claude session.
  project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  strict_hook="$project_dir/.claude/hooks/go-strict-lint.sh"
  if [[ ! -x "$strict_hook" ]]; then
    echo "Error: /code-quality requires executable .claude/hooks/go-strict-lint.sh; initialize .claude/shared and restore the hook link." >&2
    exit 3
  fi
  echo "== Strict lint (touched Go files) =="
  strict_status=0
  cd "$project_dir"
  "$strict_hook" --check || strict_status=$?
  case "$strict_status" in
    0) ;;
    1)
      echo "Error: strict lint found issues; fix them before rerunning /code-quality." >&2
      exit 1
      ;;
    *)
      echo "Error: strict lint could not run (exit $strict_status); /code-quality is incomplete." >&2
      exit 3
      ;;
  esac
  claude_args+=(--append-system-prompt 'The repository proxy already ran .claude/hooks/go-strict-lint.sh --check successfully (exit 0). Use that result for the strict touched-file lint step; do not rerun the fallback hook invocation. Continue with the remaining /code-quality checks and report any checks that cannot run.')
fi
exec claude "${claude_args[@]}"
