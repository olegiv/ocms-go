// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
)

// RedirectPathOccupied reports whether a redirect already claims a path.
//
// The two halves differ deliberately. An exact source_path match counts whether
// or not the redirect is enabled, because a disabled redirect is one toggle
// away from stranding the page. The wildcard sweep considers enabled redirects
// only, since matching every disabled pattern would refuse far more slugs than
// it protects.
//
// An importer consults this before claiming a slug: a page stored beneath a
// path an enabled redirect owns is unreachable, because the redirect middleware
// answers the URL before the frontend router ever matches the page.
func RedirectPathOccupied(ctx context.Context, queries *store.Queries, sourcePath string) (bool, error) {
	_, err := queries.GetRedirectBySourcePath(ctx, sourcePath)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	redirects, err := queries.ListEnabledRedirects(ctx)
	if err != nil {
		return false, err
	}
	for _, redirect := range redirects {
		if redirect.SourcePath == sourcePath ||
			(redirect.IsWildcard && WildcardRedirectMatchesPath(redirect.SourcePath, sourcePath)) {
			return true, nil
		}
	}
	return false, nil
}

// WildcardRedirectMatchesPath reports whether a wildcard redirect pattern
// matches a concrete request path.
//
// "*" matches exactly one path segment and "**" matches any number of them. A
// trailing "*" that does not follow a slash is treated as a prefix match, so
// "/news*" also owns "/news" and "/newsletter".
func WildcardRedirectMatchesPath(pattern, requestPath string) bool {
	if strings.HasSuffix(pattern, "*") && !strings.HasSuffix(pattern, "**") {
		prefix := strings.TrimSuffix(pattern, "*")
		if !strings.HasSuffix(prefix, "/") {
			requestPath = strings.TrimSuffix(requestPath, "/")
			prefixWithoutSlash := strings.TrimSuffix(prefix, "/")
			return requestPath == prefixWithoutSlash || strings.HasPrefix(requestPath, prefix)
		}
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")
	return wildcardRedirectPartsMatch(patternParts, requestParts, 0, 0)
}

// wildcardRedirectPartsMatch walks pattern and request segments in step,
// branching on "**" so it can consume any number of remaining segments.
func wildcardRedirectPartsMatch(pattern, request []string, patternIndex, requestIndex int) bool {
	if patternIndex >= len(pattern) {
		return requestIndex >= len(request)
	}
	if requestIndex >= len(request) {
		for ; patternIndex < len(pattern); patternIndex++ {
			if pattern[patternIndex] != "**" {
				return false
			}
		}
		return true
	}
	switch pattern[patternIndex] {
	case "*":
		return wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex+1)
	case "**":
		if wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex) {
			return true
		}
		for end := requestIndex + 1; end <= len(request); end++ {
			if wildcardRedirectPartsMatch(pattern, request, patternIndex+1, end) {
				return true
			}
		}
		return false
	default:
		return pattern[patternIndex] == request[requestIndex] &&
			wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex+1)
	}
}
