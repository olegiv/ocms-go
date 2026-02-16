#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/check-no-absolute-paths.sh [--staged|--all]

Options:
  --staged  Scan only staged files in the git index (default).
  --all     Scan all tracked files in the repository.
EOF
}

mode="staged"
case "${1:-}" in
  ""|--staged)
    mode="staged"
    ;;
  --all)
    mode="all"
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    echo "Unknown option: $1" >&2
    usage >&2
    exit 2
    ;;
esac

# Local-machine absolute path patterns:
# - macOS: /Users/<name>/...
# - Linux: /home/<name>/...
# - Windows: C:\Users\<name>\...
path_regex='(/Users/[A-Za-z0-9._-]+/|/home/[A-Za-z0-9._-]+/|[A-Za-z]:\\Users\\[A-Za-z0-9._-]+\\)'

scan_staged() {
  mapfile -t staged_files < <(git diff --cached --name-only --diff-filter=ACMR)
  if [[ ${#staged_files[@]} -eq 0 ]]; then
    exit 0
  fi

  local matches
  local status
  if matches="$(git grep --cached -nI -E "$path_regex" -- "${staged_files[@]}")"; then
    echo "Error: local absolute path detected in staged content:" >&2
    echo "$matches" >&2
    echo >&2
    echo "Use repository-relative paths instead." >&2
    return 1
  else
    status=$?
    if [[ $status -eq 1 ]]; then
      return 0
    fi
  fi

  echo "Error: path guard failed while scanning staged files." >&2
  return $status
}

scan_all() {
  local matches
  local status
  if matches="$(git grep -nI -E "$path_regex")"; then
    echo "Error: local absolute path detected in repository content:" >&2
    echo "$matches" >&2
    echo >&2
    echo "Use repository-relative paths instead." >&2
    return 1
  else
    status=$?
    if [[ $status -eq 1 ]]; then
      return 0
    fi
  fi

  echo "Error: path guard failed while scanning repository files." >&2
  return $status
}

if [[ "$mode" == "all" ]]; then
  scan_all
else
  scan_staged
fi
