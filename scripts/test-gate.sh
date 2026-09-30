#!/usr/bin/env bash
# Test gate: the full Go test suite. /commit-prepare runs it before drafting a
# commit message and .githooks/pre-push runs it before every push; a failing
# test blocks both. Exits non-zero when any test fails.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [[ -z "${OCMS_SESSION_SECRET:-}" ]]; then
  export OCMS_SESSION_SECRET="test-secret-key-32-bytes-long!!!"
fi

exec go test ./...
