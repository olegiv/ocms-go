#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  -h|--help)
    echo 'Usage: ./scripts/codex/security-audit.sh [--prepare]'
    exit 0
    ;;
  ''|--prepare) ;;
  *) echo 'Usage: ./scripts/codex/security-audit.sh [--prepare]' >&2; exit 2 ;;
esac
if [[ $# -gt 1 ]]; then
  echo 'The audit destination is fixed at .audit/.' >&2
  exit 2
fi

root=$(git rev-parse --show-toplevel)
cd -P "$root"
audit_dir="$PWD/.audit"
if [[ -L "$audit_dir" || ( -e "$audit_dir" && ! -d "$audit_dir" ) ]] ||
  [[ -n $(git ls-files -- .audit) ]] || ! git check-ignore -q -- .audit/; then
  echo 'Use a real, gitignored .audit directory in the repository.' >&2
  exit 2
fi
mkdir -p -- "$audit_dir"
lock_dir="$audit_dir/.archive.lock"
if ! mkdir -- "$lock_dir"; then
  echo 'Another audit is preparing its report directory.' >&2
  echo 'If preparation stopped and no audit is active, remove the empty lock with rmdir -- .audit/.archive.lock and retry.' >&2
  exit 2
fi
trap 'rmdir -- "$lock_dir"' EXIT
timestamp=$(date '+%Y-%m-%d_%H-%M-%S')
archive_dir=
shopt -s nullglob
# A top-level dated report marks a finished run, regardless of its producer.
# Leave logs without a matching report and active notes in place.
for report in "$audit_dir"/security-audit-[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]_[0-9][0-9]-[0-9][0-9]-[0-9][0-9].md; do
  [[ -f "$report" && ! -L "$report" ]] || continue
  report_stamp=${report##*/security-audit-}
  report_stamp=${report_stamp%.md}
  if [[ -z "$archive_dir" ]]; then
    archive_dir=$(mktemp -d "$audit_dir/archive-$timestamp.XXXXXXXX")
    printf 'Archiving previous audit files in %s\n' "$archive_dir" >&2
  fi
  for path in "$audit_dir/govulncheck-$report_stamp.log" "$audit_dir/npm-audit-$report_stamp.log" "$report"; do
    if [[ -f "$path" && ! -L "$path" ]]; then
      mv -- "$path" "$archive_dir/"
    fi
  done
done
shopt -u nullglob

run_dir=$(mktemp -d "$audit_dir/run-$timestamp.XXXXXXXX")
rmdir -- "$lock_dir"
trap - EXIT
if [[ ${1:-} == --prepare ]]; then
  printf '%s\n' "$run_dir"
  exit 0
fi

overall_status=0
govuln_status='skipped (govulncheck missing)'
npm_status='skipped (npm/package.json missing)'

if command -v govulncheck >/dev/null 2>&1; then
  if govulncheck ./... >"$run_dir/govulncheck.log" 2>&1; then
    govuln_status=ok
  else
    govuln_status='issues found'
    overall_status=1
  fi
else
  printf 'govulncheck is not installed\n' >"$run_dir/govulncheck.log"
fi

if command -v npm >/dev/null 2>&1 && [[ -f package.json ]]; then
  if npm audit --json >"$run_dir/npm-audit.log" 2>&1; then
    npm_status=ok
  else
    npm_status='issues found'
    overall_status=1
  fi
else
  printf 'npm is not installed or package.json is missing\n' >"$run_dir/npm-audit.log"
fi

cat >"$run_dir/report.md" <<EOT
# Security Audit Report

Date: $(date -u '+%Y-%m-%d %H:%M:%SZ')
Project: $(basename "$root")

## Scan Summary

- govulncheck: $govuln_status
- npm audit: $npm_status

## Raw Logs

- govulncheck.log
- npm-audit.log
EOT
printf 'Security audit report generated: %s/report.md\n' "$run_dir"
exit "$overall_status"
