#!/usr/bin/env bash
# Copyright (c) 2025-2026 Oleg Ivanchenko
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Print the release notes for VERSION: the body of the "## [VERSION]" section
# of CHANGELOG.md (or the file given as the second argument), with each
# paragraph and list item joined onto one line. CHANGELOG.md wraps its lines,
# and GitHub renders every newline of a release description as a line break,
# so the wrapped text showed sentences broken mid-line. Headings, list items,
# table rows, block quotes and fenced code keep their own lines.
#
# Usage: scripts/release-notes.sh VERSION [CHANGELOG]
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 || -z "$1" ]]; then
    echo "Usage: $(basename "$0") VERSION [CHANGELOG]" >&2
    exit 2
fi

awk -v ver="$1" '
    function flush() {
        if (cur != "") {
            print cur
            cur = ""
        }
    }
    index($0, "## [" ver "]") == 1 { inblock = 1; next }
    inblock && /^## \[/ { exit }
    !inblock { next }
    /^[ \t]*```/ { flush(); fence = !fence; print; next }
    fence { print; next }
    /^[ \t]*$/ { flush(); print ""; next }
    # A heading, table row or block quote stands alone.
    /^#/ || /^[ \t]*\|/ || /^[ \t]*>/ { flush(); print; next }
    # A list item starts a new line, keeping its indentation.
    /^[ \t]*([-*+]|[0-9]+\.)[ \t]/ { flush(); cur = $0; next }
    # Anything else continues the paragraph or item before it.
    {
        line = $0
        if (cur == "") {
            cur = line
        } else {
            sub(/^[ \t]+/, "", line)
            cur = cur " " line
        }
    }
    END { flush() }
' "${2:-CHANGELOG.md}"
