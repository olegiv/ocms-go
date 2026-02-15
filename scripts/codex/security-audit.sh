#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
AUDIT_DIR="$ROOT_DIR/.audit"

usage() {
  cat <<'EOT'
Usage:
  ./scripts/codex/security-audit.sh
EOT
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

cd "$ROOT_DIR"
mkdir -p "$AUDIT_DIR"

timestamp="$(date '+%Y-%m-%d_%H-%M-%S')"
archive_dir="$AUDIT_DIR/$timestamp"

shopt -s nullglob dotglob
existing=("$AUDIT_DIR"/*)
if [[ ${#existing[@]} -gt 0 ]]; then
  mkdir -p "$archive_dir"
  for path in "${existing[@]}"; do
    [[ "$path" == "$archive_dir" ]] && continue
    mv "$path" "$archive_dir"/
  done
  echo "Archived previous audit files to: $archive_dir"
fi
shopt -u nullglob dotglob

govuln_log="$AUDIT_DIR/govulncheck-$timestamp.log"
npm_log="$AUDIT_DIR/npm-audit-$timestamp.log"
report_file="$AUDIT_DIR/security-audit-$timestamp.md"

overall_status=0
govuln_status="skipped (govulncheck missing)"
npm_status="skipped (npm/package.json missing)"

if command -v govulncheck >/dev/null 2>&1; then
  if govulncheck ./... >"$govuln_log" 2>&1; then
    govuln_status="ok"
  else
    govuln_status="issues found"
    overall_status=1
  fi
else
  printf 'govulncheck is not installed\n' >"$govuln_log"
fi

if command -v npm >/dev/null 2>&1 && [[ -f package.json ]]; then
  if npm audit --json >"$npm_log" 2>&1; then
    npm_status="ok"
  else
    npm_status="issues found"
    overall_status=1
  fi
else
  printf 'npm is not installed or package.json is missing\n' >"$npm_log"
fi

cat >"$report_file" <<EOT
# Security Audit Report

Date: $(date -u '+%Y-%m-%d %H:%M:%SZ')
Project: $(basename "$ROOT_DIR")

## Scan Summary

- govulncheck: $govuln_status
- npm audit: $npm_status

## Raw Logs

- \`$govuln_log\`
- \`$npm_log\`
EOT

echo "Security audit report generated: $report_file"

if [[ "$overall_status" -ne 0 ]]; then
  echo "Security audit completed with findings. Review logs in .audit/."
  exit 1
fi

echo "Security audit completed with no findings from executed scanners."
