#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

usage() {
  cat <<'EOT'
Usage:
  ./scripts/codex/code-quality.sh
EOT
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

cd "$ROOT_DIR"

failures=0

echo "== Toolchain check =="
go_version="$(go version | awk '{print $3}')"
compile_version="$("$(go env GOTOOLDIR)/compile" -V | sed -E 's/.* version (go[0-9.]+).*/\1/')"
echo "go binary: $go_version"
echo "go compile: $compile_version"

if [[ "$go_version" != "$compile_version" ]]; then
  echo "ERROR: Go binary and compiler versions do not match."
  echo "Fix your local Go toolchain before continuing."
  exit 2
fi

echo
echo "== golangci-lint =="
if command -v golangci-lint >/dev/null 2>&1; then
  if ! golangci-lint run ./...; then
    failures=1
  fi
else
  echo "Skipping: golangci-lint is not installed."
fi

echo
echo "== nilaway =="
if command -v nilaway >/dev/null 2>&1; then
  if ! nilaway ./...; then
    failures=1
  fi
else
  echo "Skipping: nilaway is not installed."
fi

echo
echo "== go test =="
if [[ -z "${OCMS_SESSION_SECRET:-}" ]]; then
  export OCMS_SESSION_SECRET="test-secret-key-32-bytes-long!!!"
fi
if ! go test ./...; then
  failures=1
fi

echo
if [[ "$failures" -ne 0 ]]; then
  echo "Code quality checks completed with issues."
  exit 1
fi

echo "Code quality checks passed."
