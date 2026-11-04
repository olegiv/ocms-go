#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Print the GitHub release title for VERSION, "vVERSION - <title>", from the
# heading of its CHANGELOG.md section (or the file given as the second
# argument), which reads "## [VERSION] - YYYY-MM-DD - <title>". Exits 1 when
# the section is missing or its heading has no title, so a release is never
# published under the bare tag name.
#
# Usage: scripts/release-title.sh VERSION [CHANGELOG]
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 || -z "$1" ]]; then
    echo "Usage: $(basename "$0") VERSION [CHANGELOG]" >&2
    exit 2
fi

version=$1
heading=$(awk -v ver="$version" 'index($0, "## [" ver "]") == 1 { print; exit }' "${2:-CHANGELOG.md}")

prefix="## [$version] - "
rest=${heading#"$prefix"}
if [[ $heading == "$prefix"* && $rest =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}\ -\ (.*[^[:space:]]) ]]; then
    title=${BASH_REMATCH[1]}
    title=${title#"${title%%[![:space:]]*}"}
    printf 'v%s - %s\n' "$version" "$title"
    exit 0
fi

echo "CHANGELOG has no \"## [$version] - YYYY-MM-DD - <title>\" heading" >&2
exit 1
