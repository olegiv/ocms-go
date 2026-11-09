#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

# Print the changelog version for a stable release tag.
set -euo pipefail

if [[ $# != 1 || ! $1 =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf '%s\n' 'Release tags must match vMAJOR.MINOR.PATCH.' >&2
    exit 1
fi

printf '%s\n' "${1#v}"
