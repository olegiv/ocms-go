// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package contentpath contains the shared, database-only public content routing rules.
package contentpath

import (
	"context"
	"database/sql"
	"errors"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"log/slog"
	"strings"
)

func ActiveLanguages(activeLangs []store.Language) map[string]store.Language {
	langMap := make(map[string]store.Language, len(activeLangs))
	for _, lang := range activeLangs {
		code := lang.Code
		if !util.IsValidLangCode(code) {
			slog.Warn("ignoring active language with invalid code", "language_id", lang.ID, "code", lang.Code)
			continue
		}
		if util.IsReservedLanguageCode(code) {
			slog.Warn("ignoring active language with reserved route prefix", "language_id", lang.ID, "code", lang.Code)
			continue
		}
		langMap[code] = lang
	}
	return langMap
}

// matchLanguagePrefix matches the first path segment against the active
// language map and returns the path to route after removing that prefix.
func MatchLanguagePrefix(path string, langMap map[string]store.Language) (store.Language, string, bool) {
	path = strings.TrimPrefix(path, "/")
	code, remainder, hasRemainder := strings.Cut(path, "/")
	if !util.IsValidLangCode(code) {
		return store.Language{}, "", false
	}

	lang, ok := langMap[code]
	if !ok {
		return store.Language{}, "", false
	}

	if !hasRemainder || remainder == "" {
		return lang, "/", true
	}
	return lang, "/" + remainder, true
}

// AliasPath removes only an explicit, validated language prefix.
func AliasPath(path, prefix string) string {
	path = strings.TrimPrefix(path, "/")
	if prefix == "" {
		return path
	}
	if path == prefix {
		return ""
	}
	remainder, ok := strings.CutPrefix(path, prefix+"/")
	if !ok {
		return ""
	}
	return remainder
}

// ExplicitLanguage checks agreement between the route and its language context.
func ExplicitLanguage(prefix, contextCode string) (string, bool) {
	if prefix == "" {
		return "", true
	}
	if contextCode != prefix || !util.IsRoutableLanguageCode(prefix) {
		return "", false
	}
	return prefix, true
}

// RouteLanguage returns the active language owned by an unprefixed/default or explicitly prefixed route.
func RouteLanguage(ctx context.Context, q *store.Queries, explicit string) (store.Language, error) {
	var language store.Language
	var err error
	if explicit == "" {
		language, err = q.GetDefaultLanguage(ctx)
	} else {
		language, err = q.GetLanguageByCode(ctx, explicit)
	}
	if err != nil {
		return language, err
	}
	if !language.IsActive || !util.IsRoutableLanguageCode(language.Code) {
		return store.Language{}, sql.ErrNoRows
	}
	return language, nil
}

// PageLanguagePrefix resolves a stored page to its canonical public language namespace.
func PageLanguagePrefix(ctx context.Context, q *store.Queries, page store.Page) (string, bool, error) {
	if !util.IsValidSlug(page.Slug) {
		return "", false, nil
	}
	def, err := q.GetDefaultLanguage(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	lang, err := q.GetLanguageByCode(ctx, page.LanguageCode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !lang.IsActive || lang.Code != page.LanguageCode || !util.IsRoutableLanguageCode(lang.Code) {
		return "", false, nil
	}
	if lang.ID == def.ID && lang.Code == def.Code {
		return "", true, nil
	}
	return lang.Code, true, nil
}

// AliasLanguagePrefix allows an alias only in the target's own explicit namespace.
func AliasLanguagePrefix(ctx context.Context, q *store.Queries, page store.Page, explicit string) (string, bool, error) {
	prefix, ok, err := PageLanguagePrefix(ctx, q, page)
	if err != nil || !ok {
		return "", false, err
	}
	if explicit != "" && page.LanguageCode != explicit {
		return "", false, nil
	}
	return prefix, true, nil
}

// PublishedPage reads the page that a public slug route owns.
func PublishedPage(ctx context.Context, q *store.Queries, slug, language string) (store.Page, error) {
	return q.GetPublishedPageBySlugAndLanguage(ctx, store.GetPublishedPageBySlugAndLanguageParams{Slug: slug, LanguageCode: language})
}

// RoutingPath retains the router's raw path when present; aliases still use the decoded URL path.
func RoutingPath(decoded, routerPath string) string {
	if routerPath != "" {
		return routerPath
	}
	return decoded
}
