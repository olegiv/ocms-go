// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/transfer"
)

func TestCommittedLanguageImportRefreshesRuntimeI18nState(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Init(nil) })
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	ru, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsDefault: true, IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE languages SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END,
		 is_active = CASE WHEN id = ? THEN 1 ELSE 0 END`, ru.ID, ru.ID); err != nil {
		t.Fatal(err)
	}

	h := &ImportExportHandler{queries: queries, logger: slog.Default()}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	h.invalidateCachesAfterImport(canceledCtx, &transfer.ImportResult{DryRun: false})
	if got := i18n.GetDefaultLanguage(); got != "ru" {
		t.Fatalf("runtime default = %q, want ru", got)
	}
	if got := i18n.MatchLanguage("en"); got != "ru" {
		t.Fatalf("runtime active matcher selected %q for en, want sole active ru", got)
	}
}

func TestDryRunLanguageImportLeavesRuntimeI18nStateUnchanged(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	i18n.ConfigureLanguages([]string{"en"}, "en")
	t.Cleanup(func() { _ = i18n.Init(nil) })
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	ru, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsDefault: true, IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE languages SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END,
		 is_active = CASE WHEN id = ? THEN 1 ELSE 0 END`, ru.ID, ru.ID); err != nil {
		t.Fatal(err)
	}

	h := &ImportExportHandler{queries: queries, logger: slog.Default()}
	h.invalidateCachesAfterImport(ctx, &transfer.ImportResult{DryRun: true})
	if got := i18n.GetDefaultLanguage(); got != "en" {
		t.Fatalf("dry-run changed runtime default to %q, want en", got)
	}
	if got := i18n.MatchLanguage("ru"); got != "en" {
		t.Fatalf("dry-run changed active matcher: MatchLanguage(ru) = %q, want en", got)
	}
}

func TestCommittedLanguageMutationRefreshesRuntimeWithCanceledRequest(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Init(nil) })
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	ru, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsDefault: true, IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE languages SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END`, ru.ID); err != nil {
		t.Fatal(err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	(&LanguagesHandler{queries: queries}).invalidateLanguageCaches(canceledCtx)
	if got := i18n.GetDefaultLanguage(); got != "ru" {
		t.Fatalf("runtime default = %q, want ru after canceled post-commit request", got)
	}
}

func TestSetDefaultLanguageRefreshesRuntimeI18nState(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Init(nil) })
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	ru, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &LanguagesHandler{db: db, queries: queries}
	if err := h.setDefaultLanguage(ctx, ru.ID); err != nil {
		t.Fatal(err)
	}
	if got := i18n.GetDefaultLanguage(); got != "ru" {
		t.Fatalf("runtime default = %q, want ru", got)
	}
}
