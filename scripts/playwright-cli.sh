#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail
exec "$(dirname -- "$0")/../node_modules/.bin/playwright" cli "$@"
