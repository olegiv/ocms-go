#!/usr/bin/env bash
# Use the approved draft and exact tested tree from the local terminal.
set -euo pipefail

if [[ $# -ne 0 ]]; then
  echo 'Usage: commit-do (approve the prepared message in your terminal)' >&2
  exit 2
fi
root=$(git rev-parse --show-toplevel)
cd "$root"
state_dir="$(git rev-parse --absolute-git-dir)/.codex"
if [[ ! -s "$state_dir/tested-tree" || ! -s "$state_dir/commit-message.txt" ]]; then
  echo 'Run commit-prepare before approving a commit.' >&2
  exit 2
fi
scratch="$state_dir/commit.lock"
if ! mkdir -- "$scratch"; then
  echo 'Another commit preparation or approval is in progress.' >&2
  exit 2
fi
trap 'rm -rf -- "$scratch"' EXIT
read -r base tree < "$state_dir/tested-tree"
cp -- "$state_dir/commit-message.txt" "$scratch/message"

check_candidate() {
  if [[ $(git rev-parse HEAD) != "$base" || $(git write-tree) != "$tree" ]]; then
    echo 'HEAD or the staged tree differs from preparation. Prepare the intended changes again.' >&2
    exit 2
  fi
  for operation in MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD rebase-merge rebase-apply; do
    if [[ -e $(git rev-parse --git-path "$operation") ]]; then
      echo 'Finish the in-progress Git operation before using commit-do.' >&2
      exit 2
    fi
  done
}

check_hooks() {
  for name in pre-commit prepare-commit-msg commit-msg; do
    hook=$(git rev-parse --git-path "hooks/$name")
    [[ -f "$hook" && -x "$hook" ]] || continue
    if [[ "$name" == pre-commit ]]; then
      # The shipped path checker and approval hook do not change the index.
      # Require their checked-in bytes, including the path checker's helper.
      if cmp -s "$hook" <(git cat-file blob "$base:.githooks/pre-commit") &&
        cmp -s scripts/check-no-absolute-paths.sh <(git cat-file blob "$base:scripts/check-no-absolute-paths.sh"); then
        continue
      fi
      shared=$(git rev-parse "$base:.claude/shared")
      if cmp -s "$hook" <(git -C .claude/shared cat-file blob "$shared:global/hooks/pre-commit"); then
        continue
      fi
    fi
    printf 'Cannot establish tree integrity with active %s hook %s. Use a guarded commit workflow.\n' "$name" "$hook" >&2
    exit 2
  done
}

check_candidate
check_hooks
if [[ ! -t 0 || ! -t 1 ]]; then
  echo 'Commit approval requires an interactive terminal.' >&2
  exit 2
fi
"$root/scripts/codex/test-snapshot.sh" "$tree"
check_candidate
"$root/scripts/check-no-absolute-paths.sh" --staged

printf '\nApprove one commit of tree %s with this message?\n\n' "$tree"
cat "$scratch/message"
printf '\nType YES to commit: '
if ! read -r answer || [[ "$answer" != YES ]]; then
  echo 'Commit declined.' >&2
  exit 2
fi
check_candidate
check_hooks

# Freeze the approved contents even if another process stages a file while
# Git is committing. Git still runs the effective commit hooks.
(
  export GIT_INDEX_FILE="$scratch/index"
  git read-tree "$tree"
  git commit --cleanup=verbatim --file "$scratch/message"
)
rm -f -- "$state_dir/tested-tree" "$state_dir/commit-message.txt"
git status --short
