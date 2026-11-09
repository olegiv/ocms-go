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
umask 077
mkdir -p -- "$audit_dir"
# Each run keeps its original path so links from notes and reports stay valid.
timestamp=$(date '+%Y-%m-%d_%H-%M-%S')
run_dir=$(mktemp -d "$audit_dir/run-$timestamp.XXXXXXXX")
if [[ ${1:-} == --prepare ]]; then
  printf '%s\n' "$run_dir"
  exit 0
fi

overall_status=0
govuln_status='skipped (govulncheck missing)'
npm_status='skipped (npm/package.json missing)'

if command -v govulncheck >/dev/null 2>&1; then
  if ./scripts/with-go-packages.sh govulncheck >"$run_dir/govulncheck.log" 2>&1; then
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

cat >"$run_dir/report.incomplete" <<EOT
# Security Audit Report

Date: $(date -u '+%Y-%m-%d %H:%M:%SZ')
Project: $(basename "$root")

## Scan Summary

- govulncheck: $govuln_status
- npm audit: $npm_status

## Raw Logs

- [govulncheck.log](govulncheck.log)
- [npm-audit.log](npm-audit.log)
EOT
mv -- "$run_dir/report.incomplete" "$run_dir/report.md"
printf 'Security audit report generated: %s/report.md\n' "$run_dir"
exit "$overall_status"
