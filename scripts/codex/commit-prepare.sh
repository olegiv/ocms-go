#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
STATE_DIR="$ROOT_DIR/.git/.codex"
MSG_FILE="$STATE_DIR/commit-message.txt"

mkdir -p "$STATE_DIR"

run_quality=false
if [[ "${1:-}" == "quality" || "${1:-}" == "q" ]]; then
  run_quality=true
fi

if $run_quality; then
  echo "Running optional quality checks..."
  quality_failed=false

  if command -v golangci-lint >/dev/null 2>&1; then
    if ! golangci-lint run ./...; then
      quality_failed=true
    fi
  else
    echo "- Skipping golangci-lint (not installed)."
  fi

  if ! go test ./...; then
    quality_failed=true
  fi

  if $quality_failed; then
    echo
    echo "Code quality issues found. Fix them first, or rerun without 'quality'."
    exit 2
  fi
fi

echo "== git status =="
git status --short

echo
if [[ -z "$(git status --porcelain)" ]]; then
  echo "No changes detected. Nothing to prepare."
  exit 0
fi

changed_files="$(git status --porcelain | sed 's/^.. //' | sed '/^$/d')"

echo "== git log -5 --oneline =="
git log -5 --oneline

echo
echo "== changed files =="
printf '%s\n' "$changed_files"

files_csv="$(printf '%s\n' "$changed_files" | tr '\n' ',' | sed 's/,$//')"

subject="Update project files"
if [[ "$files_csv" =~ (^|,)internal/store/migrations/ ]]; then
  subject="Update database migrations"
elif printf '%s\n' "$changed_files" | rg -q '\.go$'; then
  subject="Refine Go application logic"
elif printf '%s\n' "$changed_files" | rg -q '^docs/|\.md$'; then
  subject="Update documentation"
fi

cat > "$MSG_FILE" <<EOM
$subject

Update modified files to align behavior and project requirements.

- Files: $files_csv
- Prepared via scripts/codex/commit-prepare.sh
EOM

echo
echo "Draft commit message saved to: $MSG_FILE"
echo "Review/edit it, then run: ./scripts/codex/commit-do.sh"
echo
echo "----"
cat "$MSG_FILE"
echo "----"
