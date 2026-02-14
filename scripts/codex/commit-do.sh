#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
STATE_DIR="$ROOT_DIR/.git/.codex"
MSG_FILE="$STATE_DIR/commit-message.txt"

if [[ -z "$(git status --porcelain)" ]]; then
  echo "No changes to commit."
  exit 0
fi

if [[ ! -s "$MSG_FILE" ]]; then
  echo "No prepared commit message found at $MSG_FILE"
  echo "Run ./scripts/codex/commit-prepare.sh first."
  exit 2
fi

git add .

# Bypass project pre-commit hook only for explicit commit command usage.
git commit --no-verify -F "$MSG_FILE"

echo
echo "Commit created with message from $MSG_FILE"
git status --short
