// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func languageOnlyImportOptions() ImportOptions {
	return ImportOptions{
		ConflictStrategy: ConflictSkip,
		ImportLanguages:  true,
	}
}

func TestImporterValidateRequiresExactlyOneActiveDefault(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		languages []ExportLanguage
	}{
		{
			name: "no default",
			languages: []ExportLanguage{
				{Code: "en", Name: "English", IsActive: true},
				{Code: "de", Name: "German", IsActive: true},
			},
		},
		{
			name: "multiple defaults",
			languages: []ExportLanguage{
				{Code: "en", Name: "English", IsActive: true, IsDefault: true},
				{Code: "de", Name: "German", IsActive: true, IsDefault: true},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			importer := NewImporter(nil, nil, slog.Default())
			errs := importer.Validate(&ExportData{Version: ExportVersion, Languages: tt.languages})
			require.NotEmpty(t, errs)
			assert.Contains(t, errs[len(errs)-1].Message, "exactly one active default")
		})
	}
}

func TestContentOnlyPreviewDryRunAndImportIgnoreUnusedArchiveLanguageState(t *testing.T) {
	data := &ExportData{
		Version: ExportVersion,
		Languages: []ExportLanguage{
			{Code: "admin", Name: "Legacy Admin", IsActive: true, IsDefault: true},
			{Code: "fr", Name: "French", IsActive: true, IsDefault: true},
		},
		Pages: []ExportPage{{
			ID: 1, Title: "Content only", Slug: "content-only", Status: "published", LanguageCode: "en",
		}},
	}

	previewDB := setupTest(t)
	defer previewDB.Cleanup()
	preview, err := NewImporter(previewDB.Queries, previewDB.DB, slog.Default()).ValidateData(previewDB.Ctx, data)
	require.NoError(t, err)
	require.True(t, preview.Valid, "preview errors: %+v", preview.Errors)

	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry run", false: "real import"}[dryRun], func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true, ImportLanguages: false,
			})
			require.NoError(t, err)
			require.True(t, result.Success, "result errors: %+v", result.Errors)
			assert.Equal(t, 1, result.Created["pages"])
			_, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, "content-only")
			if dryRun {
				require.ErrorIs(t, lookupErr, sql.ErrNoRows)
			} else {
				require.NoError(t, lookupErr)
			}
		})
	}
}

func TestPreviewDryRunAndImportRejectMissingSlugRelationsBeforeWrites(t *testing.T) {
	tests := []struct {
		name string
		data *ExportData
		opts ImportOptions
	}{
		{
			name: "category parent",
			data: &ExportData{Version: ExportVersion, Categories: []ExportCategory{{
				ID: 1, Name: "Child", Slug: "child", ParentSlug: "missing-parent", LanguageCode: "en",
			}}},
			opts: ImportOptions{ImportCategories: true},
		},
		{
			name: "page taxonomy",
			data: &ExportData{Version: ExportVersion, Pages: []ExportPage{{
				ID: 1, Title: "Page", Slug: "page", Status: "published", LanguageCode: "en",
				Categories: []string{"missing-category"}, Tags: []string{"missing-tag"},
			}}},
			opts: ImportOptions{ImportPages: true},
		},
		{
			name: "menu page",
			data: &ExportData{Version: ExportVersion, Menus: []ExportMenu{{
				ID: 1, Name: "Main", Slug: "main", LanguageCode: "en",
				Items: []ExportMenuItem{{ID: 1, Title: "Missing", PageSlug: "missing-page", IsActive: true}},
			}}},
			opts: ImportOptions{ImportMenus: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.data.Version = ExportVersion
			ts := setupTest(t)
			defer ts.Cleanup()
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			preview, err := importer.ValidateData(ts.Ctx, tt.data)
			require.NoError(t, err)
			require.False(t, preview.Valid)

			for _, dryRun := range []bool{true, false} {
				opts := tt.opts
				opts.DryRun = dryRun
				opts.ConflictStrategy = ConflictSkip
				result, err := importer.Import(ts.Ctx, tt.data, opts)
				require.Error(t, err)
				require.False(t, result.Success)
			}

			var total int
			require.NoError(t, ts.DB.QueryRowContext(ts.Ctx,
				`SELECT (SELECT COUNT(*) FROM categories) + (SELECT COUNT(*) FROM pages) +
				        (SELECT COUNT(*) FROM menus) + (SELECT COUNT(*) FROM menu_items)`).Scan(&total))
			assert.Zero(t, total)
		})
	}
}

func TestImportLanguagesReconcilesDefaultAtomically(t *testing.T) {
	data := &ExportData{
		Version: ExportVersion,
		Languages: []ExportLanguage{
			{Code: "en", Name: "English", NativeName: "English", IsActive: true, Direction: "ltr"},
			{Code: "fr", Name: "French", NativeName: "Français", IsActive: true, IsDefault: true, Direction: "ltr", Position: 1},
		},
	}

	t.Run("skip still applies archive default", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()

		fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
			Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
			Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)

		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		result, err := importer.Import(ts.Ctx, data, languageOnlyImportOptions())
		require.NoError(t, err)
		require.True(t, result.Success, "import errors: %v", result.Errors)

		defaultLang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
		require.NoError(t, err)
		assert.Equal(t, fr.ID, defaultLang.ID)
		var defaults int
		require.NoError(t, ts.DB.QueryRowContext(ts.Ctx,
			`SELECT COUNT(*) FROM languages WHERE is_default = 1 AND is_active = 1`).Scan(&defaults))
		assert.Equal(t, 1, defaults)
	})

	t.Run("failed default write rolls back prior default", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()

		fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
			Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
			Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)
		_, err = ts.DB.ExecContext(ts.Ctx, `
			CREATE TRIGGER fail_imported_default
			BEFORE UPDATE OF is_default ON languages
			WHEN NEW.id = `+sqlQuoteInt(fr.ID)+` AND NEW.is_default = 1
			BEGIN SELECT RAISE(ABORT, 'forced default failure'); END`)
		require.NoError(t, err)

		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		_, err = importer.Import(ts.Ctx, data, languageOnlyImportOptions())
		require.ErrorContains(t, err, "forced default failure")

		defaultLang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
		require.NoError(t, err)
		assert.Equal(t, "en", defaultLang.Code)
		var defaults int
		require.NoError(t, ts.DB.QueryRowContext(ts.Ctx,
			`SELECT COUNT(*) FROM languages WHERE is_default = 1 AND is_active = 1`).Scan(&defaults))
		assert.Equal(t, 1, defaults)
	})
}

func sqlQuoteInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func TestImporterRejectsUnknownEntityLanguageBeforeWriting(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	data := &ExportData{
		Version: ExportVersion,
		Pages: []ExportPage{{
			ID: 1, Title: "French page", Slug: "french-page", Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "fr",
		}},
	}
	opts := ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	result, err := importer.Import(ts.Ctx, data, opts)
	require.ErrorContains(t, err, "unknown language")
	require.NotNil(t, result)
	_, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, "french-page")
	require.ErrorIs(t, lookupErr, sql.ErrNoRows)
}

func TestImporterRejectsUnroutableDestinationDefaultBeforeDryRunAndWrites(t *testing.T) {
	for _, code := range []string{"admin", "x"} {
		t.Run(code, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			_, err := ts.DB.ExecContext(ts.Ctx,
				`UPDATE languages SET code = ?, name = 'Legacy default', native_name = 'Legacy default' WHERE is_default = 1`,
				code)
			require.NoError(t, err)
			data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
				ID: 1, Title: "Imported", Slug: "imported", Status: "published",
				AuthorEmail: ts.User.Email,
			}}}
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			for _, dryRun := range []bool{true, false} {
				result, importErr := importer.Import(ts.Ctx, data, ImportOptions{
					DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
				})
				require.Error(t, importErr)
				require.NotNil(t, result)
				require.False(t, result.Success)
			}
			_, err = ts.Queries.GetPageBySlug(ts.Ctx, "imported")
			require.ErrorIs(t, err, sql.ErrNoRows)
		})
	}
}

func TestImporterRejectsExplicitUnroutableDestinationLanguage(t *testing.T) {
	for _, language := range []store.CreateLanguageParams{
		{Code: "admin", Name: "Reserved", NativeName: "Reserved", IsActive: true, Direction: "ltr"},
		{Code: "x", Name: "Invalid", NativeName: "Invalid", IsActive: true, Direction: "ltr"},
		{Code: "fr", Name: "Inactive", NativeName: "Inactive", IsActive: false, Direction: "ltr"},
	} {
		t.Run(language.Code, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language.CreatedAt = ts.Now
			language.UpdatedAt = ts.Now
			_, err := ts.Queries.CreateLanguage(ts.Ctx, language)
			require.NoError(t, err)
			data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
				ID: 1, Title: "Imported", Slug: "imported", Status: "published",
				AuthorEmail: ts.User.Email, LanguageCode: language.Code,
			}}}
			for _, dryRun := range []bool{true, false} {
				result, importErr := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(
					ts.Ctx, data, ImportOptions{
						DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
					})
				require.ErrorContains(t, importErr, "language")
				require.NotNil(t, result)
				require.False(t, result.Success)
			}
			_, err = ts.Queries.GetPageBySlug(ts.Ctx, "imported")
			require.ErrorIs(t, err, sql.ErrNoRows)
		})
	}
}

func TestValidateAndDryRunScopeMenusAndFormsByLanguage(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	for position, language := range []struct {
		code string
		name string
	}{
		{code: "de", name: "German"},
		{code: "fr", name: "French"},
	} {
		_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
			Code: language.code, Name: language.name, NativeName: language.name,
			IsActive: true, Direction: "ltr", Position: int64(position + 1),
			CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)
	}

	_, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
		Name: "Deutsch", Slug: "main", LanguageCode: "de",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{
		Name: "Kontakt", Slug: "contact", Title: "Kontakt", IsActive: true,
		LanguageCode: "de", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)

	data := &ExportData{
		Version: ExportVersion,
		Menus:   []ExportMenu{{Name: "Principal", Slug: "main", LanguageCode: "fr"}},
		Forms:   []ExportForm{{Name: "Contact", Slug: "contact", LanguageCode: "fr"}},
	}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	validation, err := importer.ValidateData(ts.Ctx, data)
	require.NoError(t, err)
	assert.Empty(t, validation.Conflicts["menus"])
	assert.Empty(t, validation.Conflicts["forms"])

	result, err := importer.Import(ts.Ctx, data, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictSkip, ImportMenus: true, ImportForms: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Created["menus"])
	assert.Equal(t, 1, result.Created["forms"])
	assert.Zero(t, result.Skipped["menus"])
	assert.Zero(t, result.Skipped["forms"])
}

func TestLanguageScopedImportDoesNotCreateAfterOperationalLookupFailure(t *testing.T) {
	for _, tt := range []struct {
		name       string
		table      string
		entity     string
		importItem func(context.Context, *Importer, *store.Queries, *ImportResult)
	}{
		{
			name: "menu", table: "menus", entity: "menu",
			importItem: func(ctx context.Context, importer *Importer, queries *store.Queries, result *ImportResult) {
				_ = importer.importMenus(ctx, queries, []ExportMenu{{Name: "Main", Slug: "main", LanguageCode: "en"}},
					nil, "en", ImportOptions{ConflictStrategy: ConflictSkip}, result)
			},
		},
		{
			name: "form", table: "forms", entity: "form",
			importItem: func(ctx context.Context, importer *Importer, queries *store.Queries, result *ImportResult) {
				importer.importForms(ctx, queries, []ExportForm{{Name: "Contact", Slug: "contact", LanguageCode: "en"}},
					"en", ImportOptions{ConflictStrategy: ConflictSkip}, result)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			require.NoError(t, func() error {
				_, err := ts.DB.Exec(`DROP TABLE ` + tt.table)
				return err
			}())

			result := NewImportResult(false)
			tt.importItem(ts.Ctx, NewImporter(ts.Queries, ts.DB, slog.Default()), ts.Queries, result)
			require.Len(t, result.Errors, 1)
			assert.Equal(t, tt.entity, result.Errors[0].Entity)
			assert.Contains(t, result.Errors[0].Message, "check for existing")
			assert.NotContains(t, result.Errors[0].Message, "create")
			assert.Zero(t, result.Created[tt.table])
		})
	}
}

func TestPreviewDryRunAndImportRejectAmbiguousDestinationDefault(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.DB.ExecContext(ts.Ctx, `UPDATE languages SET is_default = 1 WHERE id = ?`, fr.ID)
	require.NoError(t, err)

	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
		ID: 1, Title: "Page", Slug: "page", Status: "draft", AuthorEmail: ts.User.Email,
	}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	validation, err := importer.ValidateData(ts.Ctx, data)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Contains(t, validation.Errors[0].Message, "exactly one default")

	for _, dryRun := range []bool{true, false} {
		result, importErr := importer.Import(ts.Ctx, data, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
		})
		require.ErrorContains(t, importErr, "exactly one default")
		require.NotNil(t, result)
		_, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, "page")
		require.ErrorIs(t, lookupErr, sql.ErrNoRows)
	}
}

func TestPreviewAndDryRunValidateTranslationGraphAndCounts(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	invalid := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
		ID: 1, Title: "English", Slug: "english", Status: "draft", LanguageCode: "en",
		AuthorEmail: ts.User.Email, Translations: map[string]int64{"fr": 99},
	}}}
	validation, err := importer.ValidateData(ts.Ctx, invalid)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Equal(t, 1, validation.Entities["translations"])
	require.Contains(t, validation.Errors[len(validation.Errors)-1].Message, "missing translation target")
	_, err = importer.Import(ts.Ctx, invalid, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictSkip, ImportPages: true,
	})
	require.ErrorContains(t, err, "missing translation target")

	mismatched := &ExportData{Version: ExportVersion, Pages: []ExportPage{
		{ID: 1, Title: "English", Slug: "english", Status: "draft", LanguageCode: "en",
			AuthorEmail: ts.User.Email, Translations: map[string]int64{"fr": 2}},
		{ID: 2, Title: "Wrong language", Slug: "wrong", Status: "draft", LanguageCode: "en",
			AuthorEmail: ts.User.Email},
	}}
	validation, err = importer.ValidateData(ts.Ctx, mismatched)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Contains(t, validation.Errors[len(validation.Errors)-1].Message, `language "en", not "fr"`)

	valid := &ExportData{Version: ExportVersion, Pages: []ExportPage{
		{ID: 1, Title: "English", Slug: "english", Status: "draft", LanguageCode: "en",
			AuthorEmail: ts.User.Email, Translations: map[string]int64{"fr": 2}},
		{ID: 2, Title: "French", Slug: "french", Status: "draft", LanguageCode: "fr",
			AuthorEmail: ts.User.Email},
	}}
	result, err := importer.Import(ts.Ctx, valid, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictSkip, ImportPages: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Created["translations"])
}

func TestConflictOverwriteReplacesIncomingAndOutgoingTranslationEdges(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	en, err := ts.Queries.GetLanguageByCode(ts.Ctx, "en")
	require.NoError(t, err)

	pageTarget, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "English page", Slug: "page-en", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	pageSource, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "French page", Slug: "page-fr", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)

	categoryTarget, err := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name: "English category", Slug: "category-en", LanguageCode: "en",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	categorySource, err := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name: "French category", Slug: "category-fr", LanguageCode: "fr",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)

	tagTarget, err := ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{
		Name: "English tag", Slug: "tag-en", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	tagSource, err := ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{
		Name: "French tag", Slug: "tag-fr", LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)

	formTarget, err := ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{
		Name: "English form", Slug: "form-en", Title: "English form", IsActive: true,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	formSource, err := ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{
		Name: "French form", Slug: "form-fr", Title: "French form", IsActive: true,
		LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)

	for entityType, ids := range map[string][2]int64{
		"page":     {pageSource.ID, pageTarget.ID},
		"category": {categorySource.ID, categoryTarget.ID},
		"tag":      {tagSource.ID, tagTarget.ID},
		"form":     {formSource.ID, formTarget.ID},
	} {
		_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{
			EntityType: entityType, EntityID: ids[0], LanguageID: en.ID,
			TranslationID: ids[1], CreatedAt: ts.Now,
		})
		require.NoError(t, err)
	}

	data := &ExportData{
		Version: ExportVersion,
		Pages: []ExportPage{{
			ID: 1, Title: "Overwritten page", Slug: pageTarget.Slug, Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "fr",
		}},
		Categories: []ExportCategory{{
			ID: 2, Name: "Overwritten category", Slug: categoryTarget.Slug, LanguageCode: "fr",
		}},
		Tags: []ExportTag{{
			ID: 3, Name: "Overwritten tag", Slug: tagTarget.Slug, LanguageCode: "fr",
		}},
		Forms: []ExportForm{{
			ID: 4, Name: "Overwritten form", Slug: formTarget.Slug, Title: "Overwritten form",
			IsActive: true, LanguageCode: "en",
		}},
	}
	dryResult, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		DryRun:           true,
		ConflictStrategy: ConflictOverwrite,
		ImportPages:      true, ImportCategories: true, ImportTags: true, ImportForms: true,
	})
	require.NoError(t, err)
	require.True(t, dryResult.Success, "dry-run errors: %+v", dryResult.Errors)
	require.Equal(t, 1, dryResult.Updated["pages"])
	require.Equal(t, 1, dryResult.Updated["categories"])
	require.Equal(t, 1, dryResult.Updated["tags"])

	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictOverwrite,
		ImportPages:      true, ImportCategories: true, ImportTags: true, ImportForms: true,
	})
	require.NoError(t, err)
	require.True(t, result.Success, "result errors: %+v", result.Errors)

	pageAfter, err := ts.Queries.GetPageByID(ts.Ctx, pageTarget.ID)
	require.NoError(t, err)
	require.Equal(t, "fr", pageAfter.LanguageCode)
	categoryAfter, err := ts.Queries.GetCategoryByID(ts.Ctx, categoryTarget.ID)
	require.NoError(t, err)
	require.Equal(t, "fr", categoryAfter.LanguageCode)
	tagAfter, err := ts.Queries.GetTagByID(ts.Ctx, tagTarget.ID)
	require.NoError(t, err)
	require.Equal(t, "fr", tagAfter.LanguageCode)

	for entityType, targetID := range map[string]int64{
		"page": pageTarget.ID, "category": categoryTarget.ID, "tag": tagTarget.ID, "form": formTarget.ID,
	} {
		var count int
		require.NoError(t, ts.DB.QueryRowContext(ts.Ctx, `
			SELECT COUNT(*) FROM translations
			WHERE entity_type = ? AND (entity_id = ? OR translation_id = ?)
		`, entityType, targetID, targetID).Scan(&count))
		require.Zero(t, count, "%s translation edges survived overwrite", entityType)
	}
}

func TestLanguageImportSkipRejectsInactiveArchiveDefaultInPreviewAndDryRun(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: false,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Languages: []ExportLanguage{
		{Code: "en", Name: "English", NativeName: "English", IsActive: true, Direction: "ltr"},
		{Code: "fr", Name: "French", NativeName: "Français", IsActive: true, IsDefault: true, Direction: "ltr"},
	}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	validation, err := importer.ValidateData(ts.Ctx, data)
	require.NoError(t, err)
	require.True(t, validation.Valid, "overwrite can reactivate the archive default: %v", validation.Errors)
	_, err = importer.Import(ts.Ctx, data, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictSkip, ImportLanguages: true,
	})
	require.ErrorContains(t, err, "inactive")
}

func TestPreviewUsesArchiveDefaultForBlankLanguageConflicts(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
		Name: "English main", Slug: "main", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{
		Name: "English contact", Slug: "contact", Title: "Contact", IsActive: true,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{
		Version: ExportVersion,
		Languages: []ExportLanguage{
			{Code: "en", Name: "English", NativeName: "English", IsActive: true, Direction: "ltr"},
			{Code: "fr", Name: "French", NativeName: "Français", IsActive: true, IsDefault: true, Direction: "ltr"},
		},
		Menus: []ExportMenu{{Name: "Principal", Slug: "main"}},
		Forms: []ExportForm{{Name: "Contact FR", Slug: "contact"}},
	}
	validation, err := NewImporter(ts.Queries, ts.DB, slog.Default()).ValidateData(ts.Ctx, data)
	require.NoError(t, err)
	require.True(t, validation.Valid, "validation errors: %v", validation.Errors)
	require.Empty(t, validation.Conflicts["menus"])
	require.Empty(t, validation.Conflicts["forms"])
}

func TestConflictSkipRejectsCrossLanguageReuseAndPreservesCategory(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	existing, err := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name: "Admin child", Slug: "child", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Categories: []ExportCategory{
		{ID: 1, Name: "Parent", Slug: "parent", LanguageCode: "fr"},
		{ID: 2, Name: "Enfant", Slug: "child", ParentSlug: "parent", LanguageCode: "fr"},
	}}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportCategories: true,
	})
	require.ErrorContains(t, err, "belongs to language")
	require.NotNil(t, result)
	after, err := ts.Queries.GetCategoryByID(ts.Ctx, existing.ID)
	require.NoError(t, err)
	require.Equal(t, "Admin child", after.Name)
	require.Equal(t, "en", after.LanguageCode)
	require.False(t, after.ParentID.Valid)
	_, parentErr := ts.Queries.GetCategoryBySlug(ts.Ctx, "parent")
	require.ErrorIs(t, parentErr, sql.ErrNoRows)
}

func TestConflictSkipDoesNotCreateCrossLanguageTranslation(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Administrator page", Slug: "bonjour", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{
		{ID: 1, Title: "Hello", Slug: "hello", Status: "published", LanguageCode: "en",
			AuthorEmail: ts.User.Email, Translations: map[string]int64{"fr": 2}},
		{ID: 2, Title: "Bonjour", Slug: "bonjour", Status: "published", LanguageCode: "fr",
			AuthorEmail: ts.User.Email},
	}}
	_, err = NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportPages: true,
	})
	require.ErrorContains(t, err, "belongs to language")
	var translationCount int
	require.NoError(t, ts.DB.QueryRowContext(ts.Ctx,
		`SELECT COUNT(*) FROM translations WHERE language_id = ?`, fr.ID).Scan(&translationCount))
	require.Zero(t, translationCount)
	_, helloErr := ts.Queries.GetPageBySlug(ts.Ctx, "hello")
	require.ErrorIs(t, helloErr, sql.ErrNoRows)
}

func TestDryRunRenameCountsCreatedAndInvalidStrategyFails(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Existing", Slug: "existing", Status: "draft", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
		ID: 1, Title: "Imported", Slug: "existing", Status: "draft", LanguageCode: "en",
		AuthorEmail: ts.User.Email,
	}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	result, err := importer.Import(ts.Ctx, data, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictRename, ImportPages: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Created["pages"])
	require.Zero(t, result.Skipped["pages"])

	result, err = importer.Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictStrategy("bogus"), ImportPages: true,
	})
	require.ErrorContains(t, err, "invalid conflict strategy")
	require.False(t, result.Success)
	var pageCount int
	require.NoError(t, ts.DB.QueryRowContext(ts.Ctx, `SELECT COUNT(*) FROM pages`).Scan(&pageCount))
	require.Equal(t, 1, pageCount)
}

func TestConflictRenameMenuItemTargetsRenamedImportedPage(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	existing, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Existing", Slug: "about", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{
		Version: ExportVersion,
		Pages: []ExportPage{{
			ID: 10, Title: "Imported", Slug: "about", Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "en",
		}},
		Menus: []ExportMenu{{
			ID: 20, Name: "Main", Slug: "main", LanguageCode: "en",
			Items: []ExportMenuItem{{ID: 30, Title: "Imported about", PageSlug: "about", IsActive: true}},
		}},
	}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictRename, ImportPages: true, ImportMenus: true,
	})
	require.NoError(t, err)
	importedID := result.GetIDMap("pages")[10]
	require.NotZero(t, importedID)
	require.NotEqual(t, existing.ID, importedID)
	imported, err := ts.Queries.GetPageByID(ts.Ctx, importedID)
	require.NoError(t, err)
	require.NotEqual(t, "about", imported.Slug)
	menu, err := ts.Queries.GetMenuBySlugAndLanguage(ts.Ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: "en",
	})
	require.NoError(t, err)
	items, err := ts.Queries.ListTopLevelMenuItems(ts.Ctx, menu.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].PageID.Valid)
	assert.Equal(t, importedID, items[0].PageID.Int64)
}

func TestConflictRenameFailedPageDoesNotRebindMenuToExistingPage(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	existing, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Existing", Slug: "about", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.DB.ExecContext(ts.Ctx, `
		CREATE TRIGGER fail_renamed_page
		BEFORE INSERT ON pages WHEN NEW.slug <> 'about'
		BEGIN SELECT RAISE(FAIL, 'forced renamed page failure'); END;
	`)
	require.NoError(t, err)
	data := &ExportData{
		Version: ExportVersion,
		Pages: []ExportPage{{
			ID: 10, Title: "Imported", Slug: "about", Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "en",
		}},
		Menus: []ExportMenu{{
			ID: 20, Name: "Main", Slug: "main", LanguageCode: "en",
			Items: []ExportMenuItem{{ID: 30, Title: "Imported about", PageSlug: "about", IsActive: true}},
		}},
	}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictRename, ImportPages: true, ImportMenus: true,
	})
	require.ErrorContains(t, err, "referenced page \"about\" was not imported")
	require.False(t, result.Success)
	require.Zero(t, result.GetIDMap("pages")[10])
	_, err = ts.Queries.GetMenuBySlugAndLanguage(ts.Ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: "en",
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	unchanged, err := ts.Queries.GetPageBySlug(ts.Ctx, "about")
	require.NoError(t, err)
	assert.Equal(t, existing.ID, unchanged.ID)
}

func TestConflictRenameFailedTaxonomyRollsBackInsteadOfRebinding(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name: "Existing parent", Slug: "parent", LanguageCode: "en",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{
		Name: "Existing topic", Slug: "topic", LanguageCode: "en",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.DB.ExecContext(ts.Ctx, `
		CREATE TRIGGER fail_renamed_category
		BEFORE INSERT ON categories WHEN NEW.slug LIKE 'parent-%'
		BEGIN SELECT RAISE(FAIL, 'forced renamed category failure'); END;
		CREATE TRIGGER fail_renamed_tag
		BEFORE INSERT ON tags WHEN NEW.slug LIKE 'topic-%'
		BEGIN SELECT RAISE(FAIL, 'forced renamed tag failure'); END;
	`)
	require.NoError(t, err)
	data := &ExportData{
		Version: ExportVersion,
		Categories: []ExportCategory{
			{ID: 1, Name: "Imported parent", Slug: "parent", LanguageCode: "en"},
			{ID: 2, Name: "Imported child", Slug: "child", ParentSlug: "parent", LanguageCode: "en"},
		},
		Tags: []ExportTag{{ID: 3, Name: "Imported topic", Slug: "topic", LanguageCode: "en"}},
		Pages: []ExportPage{{
			ID: 4, Title: "Imported page", Slug: "imported-page", Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "en",
			Categories: []string{"parent"}, Tags: []string{"topic"},
		}},
	}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictRename, ImportCategories: true, ImportTags: true, ImportPages: true,
	})
	require.ErrorContains(t, err, "imported parent category")
	require.False(t, result.Success)
	require.Zero(t, result.GetIDMap("pages")[4])
	_, err = ts.Queries.GetPageBySlug(ts.Ctx, "imported-page")
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = ts.Queries.GetCategoryBySlug(ts.Ctx, "child")
	require.ErrorIs(t, err, sql.ErrNoRows)
	parent, err := ts.Queries.GetCategoryBySlug(ts.Ctx, "parent")
	require.NoError(t, err)
	assert.Equal(t, "Existing parent", parent.Name)
}

func TestTaxonomyAssociationFailureRollsBackImport(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.DB.ExecContext(ts.Ctx, `
		CREATE TRIGGER fail_page_tag
		BEFORE INSERT ON page_tags
		BEGIN SELECT RAISE(FAIL, 'forced page tag failure'); END;
	`)
	require.NoError(t, err)
	data := &ExportData{
		Version: ExportVersion,
		Tags:    []ExportTag{{ID: 1, Name: "Topic", Slug: "topic", LanguageCode: "en"}},
		Pages: []ExportPage{{
			ID: 2, Title: "Imported", Slug: "imported", Status: "published",
			AuthorEmail: ts.User.Email, LanguageCode: "en", Tags: []string{"topic"},
		}},
	}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportTags: true, ImportPages: true,
	})
	require.ErrorContains(t, err, "forced page tag failure")
	require.False(t, result.Success)
	_, err = ts.Queries.GetPageBySlug(ts.Ctx, "imported")
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = ts.Queries.GetTagBySlug(ts.Ctx, "topic")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestCategoryParentFailureRollsBackImport(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.DB.ExecContext(ts.Ctx, `
		CREATE TRIGGER fail_category_parent
		BEFORE UPDATE OF parent_id ON categories WHEN NEW.parent_id IS NOT NULL
		BEGIN SELECT RAISE(FAIL, 'forced category parent failure'); END;
	`)
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Categories: []ExportCategory{
		{ID: 1, Name: "Parent", Slug: "parent", LanguageCode: "en"},
		{ID: 2, Name: "Child", Slug: "child", ParentSlug: "parent", LanguageCode: "en"},
	}}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportCategories: true,
	})
	require.ErrorContains(t, err, "forced category parent failure")
	require.False(t, result.Success)
	_, err = ts.Queries.GetCategoryBySlug(ts.Ctx, "parent")
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = ts.Queries.GetCategoryBySlug(ts.Ctx, "child")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestConfigConflictStrategiesMatchDryRunAndRealImport(t *testing.T) {
	for _, tt := range []struct {
		strategy ConflictStrategy
		value    string
		created  int
		updated  int
		skipped  int
	}{
		{strategy: ConflictSkip, value: "before", skipped: 1},
		{strategy: ConflictOverwrite, value: "after", updated: 1},
		{strategy: ConflictRename, value: "before", skipped: 1},
	} {
		t.Run(string(tt.strategy), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			const key = "transfer_contract_setting"
			_, err := ts.DB.ExecContext(ts.Ctx, `
				INSERT INTO config (key, value, type, description, language_code, updated_at)
				VALUES (?, 'before', 'string', '', 'en', ?)`, key, ts.Now)
			require.NoError(t, err)
			data := &ExportData{Version: ExportVersion, Config: map[string]string{key: "after"}}
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			dry, err := importer.Import(ts.Ctx, data, ImportOptions{
				DryRun: true, ConflictStrategy: tt.strategy, ImportConfig: true,
			})
			require.NoError(t, err)
			actual, err := importer.Import(ts.Ctx, data, ImportOptions{
				ConflictStrategy: tt.strategy, ImportConfig: true,
			})
			require.NoError(t, err)
			assert.Equal(t, dry.Created["config"], actual.Created["config"])
			assert.Equal(t, dry.Updated["config"], actual.Updated["config"])
			assert.Equal(t, dry.Skipped["config"], actual.Skipped["config"])
			assert.Equal(t, tt.created, actual.Created["config"])
			assert.Equal(t, tt.updated, actual.Updated["config"])
			assert.Equal(t, tt.skipped, actual.Skipped["config"])
			config, err := ts.Queries.GetConfigByKey(ts.Ctx, key)
			require.NoError(t, err)
			assert.Equal(t, tt.value, config.Value)
		})
	}
}

func TestPreviewReportsConfigConflicts(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	const key = "site_name"
	_, err := ts.Queries.UpsertConfig(ts.Ctx, store.UpsertConfigParams{
		Key: key, Value: "Before", UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	validation, err := NewImporter(ts.Queries, ts.DB, slog.Default()).ValidateData(ts.Ctx, &ExportData{
		Version: ExportVersion,
		Config:  map[string]string{key: "After"},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, "preview errors: %v", validation.Errors)
	require.Equal(t, []string{key}, validation.Conflicts["config"])
}

func TestArchiveDuplicateGlobalSlugIsRejectedForEveryStrategy(t *testing.T) {
	for _, strategy := range []ConflictStrategy{ConflictSkip, ConflictOverwrite, ConflictRename} {
		for _, dryRun := range []bool{true, false} {
			t.Run(string(strategy)+"/dry="+strconv.FormatBool(dryRun), func(t *testing.T) {
				ts := setupTest(t)
				defer ts.Cleanup()
				_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
					Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
					Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
				})
				require.NoError(t, err)
				data := &ExportData{Version: ExportVersion, Pages: []ExportPage{
					{ID: 1, Title: "English", Slug: "same", Status: "draft", LanguageCode: "en", AuthorEmail: ts.User.Email},
					{ID: 2, Title: "French", Slug: "same", Status: "draft", LanguageCode: "fr", AuthorEmail: ts.User.Email},
				}}
				_, err = NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
					DryRun: dryRun, ConflictStrategy: strategy, ImportPages: true,
				})
				require.ErrorContains(t, err, "duplicate archive page slug")
				var count int
				require.NoError(t, ts.DB.QueryRowContext(ts.Ctx,
					`SELECT COUNT(*) FROM pages WHERE slug = 'same'`).Scan(&count))
				assert.Zero(t, count)
			})
		}
	}
}

func TestArchiveDuplicateEntityIDIsRejectedBeforeMapping(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{
		{ID: 7, Title: "One", Slug: "one", Status: "draft", LanguageCode: "en", AuthorEmail: ts.User.Email},
		{ID: 7, Title: "Two", Slug: "two", Status: "draft", LanguageCode: "en", AuthorEmail: ts.User.Email},
	}}
	_, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
		DryRun: true, ConflictStrategy: ConflictRename, ImportPages: true,
	})
	require.ErrorContains(t, err, "duplicate archive page ID 7")
}

func TestArchiveDuplicateNaturalIdentitiesAreRejectedBeforeDryRun(t *testing.T) {
	tests := []struct {
		name    string
		data    ExportData
		opts    ImportOptions
		message string
	}{
		{
			name: "languages", data: ExportData{Languages: []ExportLanguage{
				{Code: "fr", Name: "French", IsActive: true, IsDefault: true},
				{Code: "fr", Name: "Français", IsActive: true},
			}}, opts: ImportOptions{ImportLanguages: true}, message: "duplicate archive language identity",
		},
		{
			name: "users", data: ExportData{Users: []ExportUser{
				{Email: "same@example.com", Name: "One", Role: "admin"},
				{Email: "same@example.com", Name: "Two", Role: "editor"},
			}}, opts: ImportOptions{ImportUsers: true}, message: "duplicate archive user identity",
		},
		{
			name: "media", data: ExportData{Media: []ExportMedia{
				{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Filename: "one.jpg"},
				{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Filename: "two.jpg"},
			}}, opts: ImportOptions{ImportMedia: true}, message: "duplicate archive media identity",
		},
		{
			name: "menus", data: ExportData{Menus: []ExportMenu{
				{ID: 1, Name: "One", Slug: "main", LanguageCode: "en"},
				{ID: 2, Name: "Two", Slug: "main", LanguageCode: "en"},
			}}, opts: ImportOptions{ImportMenus: true}, message: "duplicate archive menu identity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			tt.data.Version = ExportVersion
			tt.opts.DryRun = true
			tt.opts.ConflictStrategy = ConflictRename
			_, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, &tt.data, tt.opts)
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestTranslationGraphRejectsSelfAndSameLanguageEdges(t *testing.T) {
	known := map[string]struct{}{"en": {}, "fr": {}}
	tests := []struct {
		name string
		data ExportData
		opts ImportOptions
	}{
		{
			name: "page", data: ExportData{Pages: []ExportPage{
				{ID: 1, Slug: "one", LanguageCode: "en", Translations: map[string]int64{"en": 2}},
				{ID: 2, Slug: "two", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
			}}, opts: ImportOptions{ImportPages: true},
		},
		{
			name: "category", data: ExportData{Categories: []ExportCategory{
				{ID: 1, Slug: "one", LanguageCode: "en", Translations: map[string]int64{"en": 2}},
				{ID: 2, Slug: "two", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
			}}, opts: ImportOptions{ImportCategories: true},
		},
		{
			name: "tag", data: ExportData{Tags: []ExportTag{
				{ID: 1, Slug: "one", LanguageCode: "en", Translations: map[string]int64{"en": 2}},
				{ID: 2, Slug: "two", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
			}}, opts: ImportOptions{ImportTags: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTranslationGraph(&tt.data, tt.opts, "en", known)
			require.Len(t, errs, 2)
			assert.Contains(t, errs[0].Message, "source language")
			assert.Contains(t, errs[1].Message, "source entity")
		})
	}
}

func TestTranslationGraphRejectsDuplicateLanguageInComponent(t *testing.T) {
	known := map[string]struct{}{"en": {}, "fr": {}}
	tests := []struct {
		name string
		data ExportData
		opts ImportOptions
	}{
		{
			name: "page", data: ExportData{Pages: []ExportPage{
				{ID: 1, Slug: "page-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Slug: "page-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Slug: "page-en-two", LanguageCode: "en"},
			}}, opts: ImportOptions{ImportPages: true},
		},
		{
			name: "category", data: ExportData{Categories: []ExportCategory{
				{ID: 1, Slug: "category-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Slug: "category-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Slug: "category-en-two", LanguageCode: "en"},
			}}, opts: ImportOptions{ImportCategories: true},
		},
		{
			name: "tag", data: ExportData{Tags: []ExportTag{
				{ID: 1, Slug: "tag-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Slug: "tag-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Slug: "tag-en-two", LanguageCode: "en"},
			}}, opts: ImportOptions{ImportTags: true},
		},
		{
			name: "form", data: ExportData{Forms: []ExportForm{
				{ID: 1, Slug: "form-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Slug: "form-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Slug: "form-en-two", LanguageCode: "en"},
			}}, opts: ImportOptions{ImportForms: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTranslationGraph(&tt.data, tt.opts, "en", known)
			require.NotEmpty(t, errs)
			assert.Contains(t, errs[len(errs)-1].Message, "multiple "+tt.name+" entities for language \"en\"")
		})
	}
}

func TestImportRejectsDuplicateLanguageComponentsInDryAndRealModes(t *testing.T) {
	tests := []struct {
		name string
		data ExportData
		opts ImportOptions
	}{
		{
			name: "page",
			data: ExportData{Pages: []ExportPage{
				{ID: 1, Title: "English one", Slug: "page-en-one", Status: "draft", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Title: "French", Slug: "page-fr", Status: "draft", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Title: "English two", Slug: "page-en-two", Status: "draft", LanguageCode: "en"},
			}},
			opts: ImportOptions{ImportPages: true},
		},
		{
			name: "category",
			data: ExportData{Categories: []ExportCategory{
				{ID: 1, Name: "English one", Slug: "category-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Name: "French", Slug: "category-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Name: "English two", Slug: "category-en-two", LanguageCode: "en"},
			}},
			opts: ImportOptions{ImportCategories: true},
		},
		{
			name: "tag",
			data: ExportData{Tags: []ExportTag{
				{ID: 1, Name: "English one", Slug: "tag-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Name: "French", Slug: "tag-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Name: "English two", Slug: "tag-en-two", LanguageCode: "en"},
			}},
			opts: ImportOptions{ImportTags: true},
		},
		{
			name: "form",
			data: ExportData{Forms: []ExportForm{
				{ID: 1, Name: "English one", Title: "English one", Slug: "form-en-one", LanguageCode: "en", Translations: map[string]int64{"fr": 2}},
				{ID: 2, Name: "French", Title: "French", Slug: "form-fr", LanguageCode: "fr", Translations: map[string]int64{"en": 3}},
				{ID: 3, Name: "English two", Title: "English two", Slug: "form-en-two", LanguageCode: "en"},
			}},
			opts: ImportOptions{ImportForms: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.data.Version = ExportVersion
			ts := setupTest(t)
			defer ts.Cleanup()
			_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
				Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
				Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			require.NoError(t, err)
			for _, dryRun := range []bool{false, true} {
				opts := tt.opts
				opts.DryRun = dryRun
				opts.ConflictStrategy = ConflictRename
				result, importErr := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, &tt.data, opts)
				require.Error(t, importErr)
				require.NotNil(t, result)
				require.Condition(t, func() bool {
					for _, item := range result.Errors {
						if item.Entity == tt.name && strings.Contains(item.Message, "translation component contains multiple") {
							return true
						}
					}
					return false
				}, "errors: %+v", result.Errors)
			}
		})
	}
}

func TestConflictSkipRejectsMergingDestinationTranslationComponents(t *testing.T) {
	for _, entityType := range []string{"page", "category", "tag", "form"} {
		t.Run(entityType, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
				Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
				Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			require.NoError(t, err)

			var sourceID, targetID, existingSourceID int64
			data := ExportData{Version: ExportVersion}
			opts := ImportOptions{ConflictStrategy: ConflictSkip}
			switch entityType {
			case "page":
				existingSource, createErr := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
					Title: "Existing source", Slug: "existing-source", Status: "draft", AuthorID: ts.User.ID,
					LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
				})
				require.NoError(t, createErr)
				source, createErr := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
					Title: "Archive source", Slug: "archive-source", Status: "draft", AuthorID: ts.User.ID,
					LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
				})
				require.NoError(t, createErr)
				target, createErr := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
					Title: "French target", Slug: "french-target", Status: "draft", AuthorID: ts.User.ID,
					LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
				})
				require.NoError(t, createErr)
				existingSourceID, sourceID, targetID = existingSource.ID, source.ID, target.ID
				data.Pages = []ExportPage{
					{ID: 10, Title: source.Title, Slug: source.Slug, Status: source.Status, AuthorEmail: ts.User.Email, LanguageCode: "en", Translations: map[string]int64{"fr": 20}},
					{ID: 20, Title: target.Title, Slug: target.Slug, Status: target.Status, AuthorEmail: ts.User.Email, LanguageCode: "fr"},
				}
				opts.ImportPages = true
			case "category":
				existingSource, createErr := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{Name: "Existing source", Slug: "existing-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				source, createErr := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{Name: "Archive source", Slug: "archive-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				target, createErr := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{Name: "French target", Slug: "french-target", LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				existingSourceID, sourceID, targetID = existingSource.ID, source.ID, target.ID
				data.Categories = []ExportCategory{
					{ID: 10, Name: source.Name, Slug: source.Slug, LanguageCode: "en", Translations: map[string]int64{"fr": 20}},
					{ID: 20, Name: target.Name, Slug: target.Slug, LanguageCode: "fr"},
				}
				opts.ImportCategories = true
			case "tag":
				existingSource, createErr := ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{Name: "Existing source", Slug: "existing-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				source, createErr := ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{Name: "Archive source", Slug: "archive-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				target, createErr := ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{Name: "French target", Slug: "french-target", LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				existingSourceID, sourceID, targetID = existingSource.ID, source.ID, target.ID
				data.Tags = []ExportTag{
					{ID: 10, Name: source.Name, Slug: source.Slug, LanguageCode: "en", Translations: map[string]int64{"fr": 20}},
					{ID: 20, Name: target.Name, Slug: target.Slug, LanguageCode: "fr"},
				}
				opts.ImportTags = true
			case "form":
				existingSource, createErr := ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{Name: "Existing source", Title: "Existing source", Slug: "existing-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				source, createErr := ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{Name: "Archive source", Title: "Archive source", Slug: "archive-source", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				target, createErr := ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{Name: "French target", Title: "French target", Slug: "french-target", LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
				require.NoError(t, createErr)
				existingSourceID, sourceID, targetID = existingSource.ID, source.ID, target.ID
				data.Forms = []ExportForm{
					{ID: 10, Name: source.Name, Title: source.Title, Slug: source.Slug, LanguageCode: "en", Translations: map[string]int64{"fr": 20}},
					{ID: 20, Name: target.Name, Title: target.Title, Slug: target.Slug, LanguageCode: "fr"},
				}
				opts.ImportForms = true
			}

			_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{
				EntityType: entityType, EntityID: existingSourceID, LanguageID: fr.ID,
				TranslationID: targetID, CreatedAt: ts.Now,
			})
			require.NoError(t, err)
			require.NotZero(t, sourceID)

			for _, dryRun := range []bool{true, false} {
				attempt := opts
				attempt.DryRun = dryRun
				result, importErr := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, &data, attempt)
				require.ErrorContains(t, importErr, "language contract validation failed")
				require.NotNil(t, result)
				require.Condition(t, func() bool {
					for _, item := range result.Errors {
						if strings.Contains(item.Message, "joining translation components would create multiple "+entityType+" entities") {
							return true
						}
					}
					return false
				})
			}
		})
	}
}

func TestConflictSkipPreflightAggregatesAllDestinationComponents(t *testing.T) {
	t.Run("new source conflicts with target component", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
			Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
			Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)
		existingEN, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
			Title: "Existing English", Slug: "existing-english", Status: "draft", AuthorID: ts.User.ID,
			LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)
		targetFR, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
			Title: "French target", Slug: "french-target", Status: "draft", AuthorID: ts.User.ID,
			LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		require.NoError(t, err)
		_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{
			EntityType: "page", EntityID: existingEN.ID, LanguageID: fr.ID,
			TranslationID: targetFR.ID, CreatedAt: ts.Now,
		})
		require.NoError(t, err)
		data := &ExportData{Version: ExportVersion, Pages: []ExportPage{
			{ID: 10, Title: "New English", Slug: "new-english", Status: "draft", AuthorEmail: ts.User.Email, LanguageCode: "en", Translations: map[string]int64{"fr": 20}},
			{ID: 20, Title: targetFR.Title, Slug: targetFR.Slug, Status: targetFR.Status, AuthorEmail: ts.User.Email, LanguageCode: "fr"},
		}}
		for _, dryRun := range []bool{true, false} {
			result, importErr := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
			})
			require.ErrorContains(t, importErr, "language contract validation failed")
			require.NotNil(t, result)
			require.NotEmpty(t, result.Errors)
			require.Contains(t, result.Errors[0].Message, "multiple page entities for language \"en\"")
		}
	})

	t.Run("pairwise safe edges are jointly unsafe", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{Code: "fr", Name: "French", NativeName: "Français", IsActive: true, Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
		require.NoError(t, err)
		de, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{Code: "de", Name: "German", NativeName: "Deutsch", IsActive: true, Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
		require.NoError(t, err)
		es, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{Code: "es", Name: "Spanish", NativeName: "Español", IsActive: true, Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now})
		require.NoError(t, err)
		createPage := func(title, slug, language string) store.Page {
			page, createErr := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
				Title: title, Slug: slug, Status: "draft", AuthorID: ts.User.ID,
				LanguageCode: language, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			require.NoError(t, createErr)
			return page
		}
		a := createPage("English A", "english-a", "en")
		b := createPage("French B", "french-b", "fr")
		c := createPage("German C", "german-c", "de")
		x := createPage("German X", "german-x", "de")
		y := createPage("Spanish Y", "spanish-y", "es")
		_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{EntityType: "page", EntityID: b.ID, LanguageID: de.ID, TranslationID: x.ID, CreatedAt: ts.Now})
		require.NoError(t, err)
		_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{EntityType: "page", EntityID: c.ID, LanguageID: es.ID, TranslationID: y.ID, CreatedAt: ts.Now})
		require.NoError(t, err)
		data := &ExportData{Version: ExportVersion, Pages: []ExportPage{
			{ID: 10, Title: a.Title, Slug: a.Slug, Status: a.Status, AuthorEmail: ts.User.Email, LanguageCode: "en", Translations: map[string]int64{"fr": 20, "de": 30}},
			{ID: 20, Title: b.Title, Slug: b.Slug, Status: b.Status, AuthorEmail: ts.User.Email, LanguageCode: "fr"},
			{ID: 30, Title: c.Title, Slug: c.Slug, Status: c.Status, AuthorEmail: ts.User.Email, LanguageCode: "de"},
		}}
		for _, dryRun := range []bool{true, false} {
			result, importErr := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true})
			require.ErrorContains(t, importErr, "language contract validation failed")
			require.NotNil(t, result)
			require.NotEmpty(t, result.Errors)
			require.Contains(t, result.Errors[0].Message, "multiple page entities for language \"de\"")
		}
	})
}

func TestPreviewAllowsConflictsResolvableByRenameOrOverwrite(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "English", Slug: "about", Status: "published", AuthorID: ts.User.ID,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
		ID: 1, Title: "French", Slug: "about", Status: "published",
		LanguageCode: "fr", AuthorEmail: ts.User.Email,
	}}}
	validation, err := NewImporter(ts.Queries, ts.DB, slog.Default()).ValidateData(ts.Ctx, data)
	require.NoError(t, err)
	require.True(t, validation.Valid, "preview errors: %v", validation.Errors)
	require.Equal(t, []string{"about"}, validation.Conflicts["pages"])
}

func TestFormSlugConflictsAreScopedByLanguage(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	_, err = ts.Queries.CreateForm(ts.Ctx, store.CreateFormParams{
		Name: "Contact", Slug: "contact", Title: "Contact", IsActive: true,
		LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	require.NoError(t, err)
	data := &ExportData{Version: ExportVersion, Forms: []ExportForm{{
		ID: 1, Name: "Contact FR", Slug: "contact", Title: "Contact FR",
		IsActive: true, LanguageCode: "fr",
	}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	result, err := importer.Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportForms: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Created["forms"])
	assert.Zero(t, result.Skipped["forms"])
	created, err := ts.Queries.GetFormBySlugAndLanguage(ts.Ctx, store.GetFormBySlugAndLanguageParams{
		Slug: "contact", LanguageCode: "fr",
	})
	require.NoError(t, err)
	assert.Equal(t, "Contact FR", created.Name)

	result, err = importer.Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictRename, ImportForms: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Created["forms"])
	renamed, err := ts.Queries.GetFormBySlugAndLanguage(ts.Ctx, store.GetFormBySlugAndLanguageParams{
		Slug: "contact-2", LanguageCode: "fr",
	})
	require.NoError(t, err)
	assert.Equal(t, "fr", renamed.LanguageCode)
}

func TestImportUsersDoesNotCreateAfterOperationalLookupFailure(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	require.NoError(t, func() error {
		_, err := ts.DB.ExecContext(ts.Ctx, `DROP TABLE users`)
		return err
	}())
	result := NewImportResult(false)
	NewImporter(ts.Queries, ts.DB, slog.Default()).importUsers(ts.Ctx, ts.Queries,
		[]ExportUser{{Email: "new@example.com", Name: "New", Role: "admin"}},
		ImportOptions{ConflictStrategy: ConflictSkip}, result)
	require.Len(t, result.Errors, 1)
	require.Contains(t, result.Errors[0].Message, "failed to check for existing user")
	require.Zero(t, result.Created["users"])
}

func TestTransferRoundTripPreservesLanguagesAndTranslations(t *testing.T) {
	source := setupTest(t)
	defer source.Cleanup()

	en, err := source.Queries.GetDefaultLanguage(source.Ctx)
	require.NoError(t, err)
	fr, err := source.Queries.CreateLanguage(source.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)

	enCategory, err := source.Queries.CreateCategory(source.Ctx, store.CreateCategoryParams{
		Name: "News", Slug: "news", LanguageCode: en.Code,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	frCategory, err := source.Queries.CreateCategory(source.Ctx, store.CreateCategoryParams{
		Name: "Actualités", Slug: "actualites", LanguageCode: fr.Code,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	enTag, err := source.Queries.CreateTag(source.Ctx, store.CreateTagParams{
		Name: "Go", Slug: "go", LanguageCode: en.Code,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	frTag, err := source.Queries.CreateTag(source.Ctx, store.CreateTagParams{
		Name: "Golang FR", Slug: "golang-fr", LanguageCode: fr.Code,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)

	enPage, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title: "Hello", Slug: "hello", Status: "published", AuthorID: source.User.ID,
		LanguageCode: en.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	frPage, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title: "Bonjour", Slug: "bonjour", Status: "published", AuthorID: source.User.ID,
		LanguageCode: fr.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)

	enForm, err := source.Queries.CreateForm(source.Ctx, store.CreateFormParams{
		Name: "Contact", Slug: "contact", Title: "Contact", IsActive: true,
		LanguageCode: en.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	frForm, err := source.Queries.CreateForm(source.Ctx, store.CreateFormParams{
		Name: "Contact FR", Slug: "contact", Title: "Contact FR", IsActive: true,
		LanguageCode: fr.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)

	for _, link := range []store.CreateTranslationParams{
		{EntityType: "page", EntityID: enPage.ID, LanguageID: fr.ID, TranslationID: frPage.ID, CreatedAt: source.Now},
		{EntityType: "category", EntityID: enCategory.ID, LanguageID: fr.ID, TranslationID: frCategory.ID, CreatedAt: source.Now},
		{EntityType: "tag", EntityID: enTag.ID, LanguageID: fr.ID, TranslationID: frTag.ID, CreatedAt: source.Now},
		{EntityType: "form", EntityID: enForm.ID, LanguageID: fr.ID, TranslationID: frForm.ID, CreatedAt: source.Now},
	} {
		_, err := source.Queries.CreateTranslation(source.Ctx, link)
		require.NoError(t, err)
	}

	mediaUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, err = source.Queries.CreateMedia(source.Ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "photo.jpg", MimeType: "image/jpeg", UploadedBy: source.User.ID,
		LanguageCode: fr.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	_, err = source.Queries.CreateMenu(source.Ctx, store.CreateMenuParams{
		Name: "Principal", Slug: "main", LanguageCode: fr.Code,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	require.NoError(t, err)
	exporter := NewExporter(source.Queries, slog.Default())
	archive, err := exporter.Export(source.Ctx, DefaultExportOptions())
	require.NoError(t, err)
	require.Len(t, archive.Categories, 2)
	require.Len(t, archive.Tags, 2)
	require.Len(t, archive.Media, 1)
	require.Len(t, archive.Menus, 1)
	require.Len(t, archive.Forms, 2)
	assert.Equal(t, fr.Code, archive.Media[0].LanguageCode)
	assert.Equal(t, fr.Code, archive.Menus[0].LanguageCode)
	formTranslations := make(map[string]map[string]int64, len(archive.Forms))
	for _, form := range archive.Forms {
		formTranslations[form.LanguageCode] = form.Translations
	}
	assert.Equal(t, frForm.ID, formTranslations[en.Code][fr.Code])

	destination := setupTest(t)
	defer destination.Cleanup()
	importer := NewImporter(destination.Queries, destination.DB, slog.Default())
	result, err := importer.Import(destination.Ctx, archive, DefaultImportOptions())
	require.NoError(t, err)
	require.True(t, result.Success, "import errors: %v", result.Errors)

	gotCategory, err := destination.Queries.GetCategoryBySlug(destination.Ctx, frCategory.Slug)
	require.NoError(t, err)
	assert.Equal(t, fr.Code, gotCategory.LanguageCode)
	gotTag, err := destination.Queries.GetTagBySlug(destination.Ctx, frTag.Slug)
	require.NoError(t, err)
	assert.Equal(t, fr.Code, gotTag.LanguageCode)
	gotMedia, err := destination.Queries.GetMediaByUUID(destination.Ctx, mediaUUID)
	require.NoError(t, err)
	assert.Equal(t, fr.Code, gotMedia.LanguageCode)
	gotMenu, err := destination.Queries.GetMenuBySlugAndLanguage(destination.Ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: fr.Code,
	})
	require.NoError(t, err)
	assert.Equal(t, fr.Code, gotMenu.LanguageCode)
	gotENForm, err := destination.Queries.GetFormBySlugAndLanguage(destination.Ctx, store.GetFormBySlugAndLanguageParams{
		Slug: "contact", LanguageCode: en.Code,
	})
	require.NoError(t, err)
	gotFRForm, err := destination.Queries.GetFormBySlugAndLanguage(destination.Ctx, store.GetFormBySlugAndLanguageParams{
		Slug: "contact", LanguageCode: fr.Code,
	})
	require.NoError(t, err)
	assert.Equal(t, fr.Code, gotFRForm.LanguageCode)

	gotENPage, err := destination.Queries.GetPageBySlug(destination.Ctx, enPage.Slug)
	require.NoError(t, err)
	gotFRPage, err := destination.Queries.GetPageBySlug(destination.Ctx, frPage.Slug)
	require.NoError(t, err)
	gotENCategory, err := destination.Queries.GetCategoryBySlug(destination.Ctx, enCategory.Slug)
	require.NoError(t, err)
	gotFRCategory, err := destination.Queries.GetCategoryBySlug(destination.Ctx, frCategory.Slug)
	require.NoError(t, err)
	gotENTag, err := destination.Queries.GetTagBySlug(destination.Ctx, enTag.Slug)
	require.NoError(t, err)
	gotFRTag, err := destination.Queries.GetTagBySlug(destination.Ctx, frTag.Slug)
	require.NoError(t, err)
	destinationFR, err := destination.Queries.GetLanguageByCode(destination.Ctx, fr.Code)
	require.NoError(t, err)

	for _, want := range []struct {
		entityType string
		sourceID   int64
		targetID   int64
	}{
		{entityType: "page", sourceID: gotENPage.ID, targetID: gotFRPage.ID},
		{entityType: "category", sourceID: gotENCategory.ID, targetID: gotFRCategory.ID},
		{entityType: "tag", sourceID: gotENTag.ID, targetID: gotFRTag.ID},
		{entityType: "form", sourceID: gotENForm.ID, targetID: gotFRForm.ID},
	} {
		link, err := destination.Queries.GetTranslation(destination.Ctx, store.GetTranslationParams{
			EntityType: want.entityType, EntityID: want.sourceID, LanguageID: destinationFR.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, want.targetID, link.TranslationID)
	}
}

// TestImportRejectsPageSlugShadowedByLanguagePrefix stops an archive importing
// a page that no URL can reach.
//
// The language middleware strips a first path segment matching an active
// language code before the frontend router runs, so a page slug equal to one
// of those codes is answered by the language homepage forever. Preflight
// catches it because the shadowing language can arrive in the same archive:
// nothing about the page alone is wrong, only the pair.
func TestImportRejectsPageSlugShadowedByLanguagePrefix(t *testing.T) {
	for name, importLanguages := range map[string]bool{
		"language already in the destination": false,
		"language carried by the archive":     true,
	} {
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			data := ExportData{Version: ExportVersion, Pages: []ExportPage{
				{ID: 1, Title: "Engineering", Slug: "eng", Status: "draft", LanguageCode: "en"},
			}}
			opts := ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true}
			if importLanguages {
				opts.ImportLanguages = true
				data.Languages = []ExportLanguage{
					{Code: "en", Name: "English", NativeName: "English", IsDefault: true, IsActive: true, Direction: "ltr"},
					{Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true, Direction: "ltr"},
				}
			} else {
				_, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
					Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true,
					Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
				})
				require.NoError(t, err)
			}

			result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, &data, opts)
			require.ErrorContains(t, err, "language contract validation failed")
			require.NotNil(t, result)
			require.Condition(t, func() bool {
				for _, item := range result.Errors {
					if strings.Contains(item.Message, `page slug "eng" is the URL prefix of active language`) {
						return true
					}
				}
				return false
			})
			_, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, "eng")
			assert.ErrorIs(t, lookupErr, sql.ErrNoRows)
		})
	}
}
