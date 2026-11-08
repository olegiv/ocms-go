#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

if [[ $# == 0 ]]; then
  echo 'Usage: scripts/with-go-packages.sh COMMAND [ARG ...]' >&2
  exit 2
fi

cd -P "$(dirname -- "${BASH_SOURCE[0]}")/.."
# Keep packages with errors in the list so the invoked check reports them.
# Only the repository's top-level output directory contains disposable work.
discovered=$(go list -e -f '{{.Dir}}' ./...)
args=("$@")
while IFS= read -r dir; do
  case "$dir" in
    "$PWD/output"|"$PWD/output/"*) continue ;;
    "$PWD") args+=(".") ;;
    "$PWD/"*) args+=("./${dir#"$PWD/"}") ;;
  esac
done <<< "$discovered"

if [[ ${#args[@]} -eq $# ]]; then
  echo 'No project Go packages found.' >&2
  exit 2
fi
exec "${args[@]}"
