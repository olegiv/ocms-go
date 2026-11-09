// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
)

// PublishedPageByID is the shared target of the frontend's stable /page/{id} URL.
func PublishedPageByID(ctx context.Context, q *store.Queries, id int64) (store.Page, error) {
	return q.GetPublishedPageByID(ctx, id)
}

// Category and Tag retain the public archive's exact language boundary.
func Category(ctx context.Context, q *store.Queries, slug, language string) (store.Category, error) {
	value, err := q.GetCategoryBySlug(ctx, slug)
	if err == nil && value.LanguageCode != language {
		return store.Category{}, sql.ErrNoRows
	}
	return value, err
}
func Tag(ctx context.Context, q *store.Queries, slug, language string) (store.Tag, error) {
	value, err := q.GetTagBySlug(ctx, slug)
	if err == nil && value.LanguageCode != language {
		return store.Tag{}, sql.ErrNoRows
	}
	return value, err
}

// PublicForm preserves legacy empty-language fallback only in the unprefixed default namespace.
func PublicForm(ctx context.Context, q *store.Queries, slug, explicit string) (store.Form, error) {
	language, err := RouteLanguage(ctx, q, explicit)
	if err != nil {
		return store.Form{}, err
	}
	value, err := q.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{Slug: slug, LanguageCode: language.Code})
	if errors.Is(err, sql.ErrNoRows) && explicit == "" {
		value, err = q.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{Slug: slug, LanguageCode: ""})
		if err == nil {
			value.LanguageCode = language.Code
		}
	}
	if err == nil && !value.IsActive {
		return store.Form{}, sql.ErrNoRows
	}
	return value, err
}

// DynamicRoute distinguishes real public entities from paths eligible for a 404 alias fallback.
// A page-ID target is a canonical redirect; an archive/form is not page-reference content.
func DynamicRoute(ctx context.Context, q *store.Queries, path, explicit, language string) (*store.Page, bool, error) {
	kind, name, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || strings.Contains(name, "/") {
		return nil, false, nil
	}
	switch kind {
	case "page":
		id, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			return nil, false, nil
		}
		page, err := PublishedPageByID(ctx, q, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return &page, false, nil
	case "category":
		_, err := Category(ctx, q, name, language)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, err == nil, err
	case "tag":
		_, err := Tag(ctx, q, name, language)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, err == nil, err
	case "forms":
		_, err := PublicForm(ctx, q, name, explicit)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, err == nil, err
	}
	return nil, false, nil
}
