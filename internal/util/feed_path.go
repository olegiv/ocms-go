// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "strings"

// IsFeedPath identifies core feed paths, including routable language prefixes.
// Importers reserve these URLs so legacy aliases and redirects cannot shadow them.
func IsFeedPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	last := parts[len(parts)-1]
	if last != "rss.xml" && last != "atom.xml" {
		return false
	}
	if (len(parts) == 2 || len(parts) == 4) && IsRoutableLanguageCode(parts[0]) {
		parts = parts[1:]
	}
	return len(parts) == 1 || (len(parts) == 3 &&
		(parts[0] == "category" || parts[0] == "tag") && IsValidSlug(parts[1]))
}
