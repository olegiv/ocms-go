// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"context"
	"database/sql"
	"errors"

	"github.com/olegiv/ocms-go/internal/contentpath"
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
	matched, _ := contentpath.MatchRedirect(requestPath, pattern, true)
	return matched
}
