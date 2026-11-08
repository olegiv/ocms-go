#!/usr/bin/env bash
# Test gate: the full Go test suite. /commit-prepare and
# scripts/codex/commit-prepare.sh run it before drafting a commit message,
# scripts/codex/commit-do.sh before committing, and .githooks/pre-push before
# every push; a failing test blocks each. Exits non-zero when any test fails.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [[ -z "${OCMS_SESSION_SECRET:-}" ]]; then
  export OCMS_SESSION_SECRET="test-secret-key-32-bytes-long!!!"
fi

if [[ ${OCMS_TEST_BROWSER:-} == 1 && -z ${OCMS_PLAYWRIGHT_CLI:-} ]]; then
  export OCMS_PLAYWRIGHT_CLI="$PWD/node_modules/.bin/playwright-cli"
  if [[ ! -x "$OCMS_PLAYWRIGHT_CLI" ]]; then
    echo 'Chrome tests require the Playwright CLI. Run make assets first.' >&2
    exit 1
  fi
fi

# -count=1 disables the test cache: every test runs, no earlier pass is reused.
exec go test -count=1 ./...
