// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"

	"github.com/olegiv/ocms-go/internal/store"
)

// =============================================================================
// TRANSLATION LOADING HELPERS
// =============================================================================

// translationBaseInfo holds common language info for any translatable entity.
type translationBaseInfo struct {
	EntityLanguage       *store.Language
	AllLanguages         []store.Language
	TranslatedIDs        map[int64]bool
	TranslationLinks     []store.GetTranslationsForEntityRow
	TranslationLanguages map[int64]store.Language // maps LanguageID to Language for each translation link
	MissingLanguages     []store.Language
}

// loadTranslationBaseInfo loads common language and translation info for any entity.
func loadTranslationBaseInfo(ctx context.Context, queries *store.Queries, entityType string, entityID int64, languageCode string) translationBaseInfo {
	info := translationBaseInfo{
		TranslatedIDs:        make(map[int64]bool),
		TranslationLanguages: make(map[int64]store.Language),
	}

	// Load all active languages
	allLanguages := ListActiveLanguagesWithFallback(ctx, queries)
	info.AllLanguages = allLanguages

	// Build language lookup maps (by ID and by code)
	langByID := make(map[int64]store.Language, len(allLanguages))
	langByCode := make(map[string]store.Language, len(allLanguages))
	for _, lang := range allLanguages {
		langByID[lang.ID] = lang
		langByCode[lang.Code] = lang
	}

	// Load entity's language
	if languageCode != "" {
		if lang, ok := langByCode[languageCode]; ok {
			info.EntityLanguage = &lang
			info.TranslatedIDs[lang.ID] = true
		}
	}

	// Load translation links
	translationLinks, err := queries.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("failed to get translations", "error", err, "entity_type", entityType, "entity_id", entityID)
	}
	info.TranslationLinks = translationLinks

	// Mark translated language IDs and cache languages
	for _, tl := range translationLinks {
		info.TranslatedIDs[tl.LanguageID] = true
		if lang, ok := langByID[tl.LanguageID]; ok {
			info.TranslationLanguages[tl.LanguageID] = lang
		}
	}

	// Find missing languages
	for _, lang := range allLanguages {
		if !info.TranslatedIDs[lang.ID] {
			info.MissingLanguages = append(info.MissingLanguages, lang)
		}
	}

	return info
}

// entityLanguageInfo holds common language and translation info for any entity type.
type entityLanguageInfo[T any] struct {
	EntityLanguage   *store.Language
	AllLanguages     []store.Language
	Translations     []T
	MissingLanguages []store.Language
}

// loadEntityTranslations loads translation info for an entity using a generic fetcher.
func loadEntityTranslations[E, T any](
	base translationBaseInfo,
	fetcher func(int64) (E, error),
	makeTranslation func(store.Language, E) T,
) entityLanguageInfo[T] {
	result := entityLanguageInfo[T]{
		EntityLanguage:   base.EntityLanguage,
		AllLanguages:     base.AllLanguages,
		MissingLanguages: base.MissingLanguages,
	}

	for _, tl := range base.TranslationLinks {
		lang, ok := base.TranslationLanguages[tl.LanguageID]
		if !ok {
			continue
		}
		entity, err := fetcher(tl.TranslationID)
		if err == nil {
			result.Translations = append(result.Translations, makeTranslation(lang, entity))
		}
	}

	return result
}

// loadLanguageInfo combines base info loading and entity translation loading into a single call.
// This is a convenience function for loading complete language info for translatable entities.
func loadLanguageInfo[E, T any](
	ctx context.Context,
	queries *store.Queries,
	entityType string,
	entityID int64,
	languageCode string,
	fetcher func(int64) (E, error),
	makeTranslation func(store.Language, E) T,
) entityLanguageInfo[T] {
	return loadEntityTranslations(
		loadTranslationBaseInfo(ctx, queries, entityType, entityID, languageCode),
		fetcher,
		makeTranslation,
	)
}

// =============================================================================
// LIST AND COUNT HELPERS
// =============================================================================

// ListAndCount executes list and count queries, returning combined results.
// This is a generic helper for paginated list endpoints.
func ListAndCount[T any](
	listFn func() ([]T, error),
	countFn func() (int64, error),
) ([]T, int64, error) {
	items, err := listFn()
	if err != nil {
		return nil, 0, err
	}
	total, err := countFn()
	return items, total, err
}

// =============================================================================
// BATCH ASSOCIATION HELPERS
// =============================================================================

// saveBatchAssociations saves associations from a list of string IDs.
// It parses each ID string and calls the save function.
func saveBatchAssociations(
	idStrs []string,
	saveFn func(id int64) error,
	logContext string,
) {
	for _, idStr := range idStrs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if err = saveFn(id); err != nil {
			slog.Error("failed to save association", "error", err, "context", logContext, "id", id)
		}
	}
}

// =============================================================================
// BATCH ENTITY FETCHING HELPERS
// =============================================================================

// batchFetchRelated fetches related entities for a list of parent entities.
// Returns a map from parent ID to the fetched related data.
func batchFetchRelated[P any, R any](
	ctx context.Context,
	items []P,
	getID func(P) int64,
	fetchFn func(ctx context.Context, id int64) (R, error),
	logContext string,
) map[int64]R {
	result := make(map[int64]R, len(items))
	for _, item := range items {
		id := getID(item)
		related, err := fetchFn(ctx, id)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("failed to fetch related entity", "error", err, "context", logContext, "id", id)
			}
			continue
		}
		result[id] = related
	}
	return result
}

// batchFetchOptional fetches related entities only when the parent has a valid optional ID.
// Returns a map from parent ID to a pointer to the fetched data.
func batchFetchOptional[P any, R any](
	ctx context.Context,
	items []P,
	getID func(P) int64,
	getOptionalID func(P) sql.NullInt64,
	fetchFn func(ctx context.Context, id int64) (R, error),
	logContext string,
) map[int64]*R {
	result := make(map[int64]*R, len(items))
	for _, item := range items {
		optID := getOptionalID(item)
		if !optID.Valid {
			continue
		}
		related, err := fetchFn(ctx, optID.Int64)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("failed to fetch related entity", "error", err, "context", logContext, "id", optID.Int64)
			}
			continue
		}
		result[getID(item)] = &related
	}
	return result
}
