#!/usr/bin/env bash
# Commit the staged changes with the message prepared by commit-prepare.sh.
#
# Only what is staged is committed: stage the files the message describes by
# name first. The test gate runs on the working tree, so unstaged or untracked
# changes that would make the tested tree differ from the commit are refused,
# and the index must not change while the gate runs. Gitignored files are not
# refused: the gate still compiles and tests them, but they are not committed.
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel)"
cd "$ROOT_DIR"
STATE_DIR="$ROOT_DIR/.git/.codex"
MSG_FILE="$STATE_DIR/commit-message.txt"

if git diff --cached --quiet; then
  echo "Nothing is staged. Stage the files the prepared message describes"
  echo "(git add -- <file>...), then rerun."
  exit 2
fi

if [[ ! -s "$MSG_FILE" ]]; then
  echo "No prepared commit message found at $MSG_FILE"
  echo "Run ./scripts/codex/commit-prepare.sh first."
  exit 2
fi

# Submodules hold no Go code the suite builds (same rule as .githooks/pre-push).
if ! git diff --quiet --ignore-submodules || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  echo "Unstaged or untracked changes would be tested but not committed."
  echo "Stage, stash or remove them first:"
  git status --short
  exit 2
fi

"$ROOT_DIR/scripts/check-no-absolute-paths.sh" --staged

tested_tree="$(git write-tree)"
if ! "$ROOT_DIR/scripts/test-gate.sh"; then
  echo "Tests failed; nothing was committed."
  exit 1
fi
if [[ "$(git write-tree)" != "$tested_tree" ]]; then
  echo "The index changed while the tests ran; rerun to test what will be committed."
  exit 2
fi

# Running this script is the approval the interactive pre-commit hook asks
# for, and the tree checks a hook would add ran above.
git commit --no-verify -F "$MSG_FILE"
# The message described this commit only: remove it, so a later run cannot
# commit other staged changes under it and must prepare its own.
rm -f "$MSG_FILE"

echo
echo "Commit created (tested tree $tested_tree); the prepared message was used and removed."
git status --short
