// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

func TestNewLanguagesHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewLanguagesHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewLanguagesHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestLanguagesListData(t *testing.T) {
	data := adminviews.LanguagesListData{
		Languages:      []adminviews.LanguageListItem{},
		TotalLanguages: 0,
	}

	if data.Languages == nil {
		t.Error("Languages should not be nil")
	}
	if data.TotalLanguages != 0 {
		t.Error("TotalLanguages should be 0")
	}
}

func TestLanguageFormInput(t *testing.T) {
	input := languageFormInput{
		Code:       "fr",
		Name:       "French",
		NativeName: "Français",
		Direction:  "ltr",
		IsActive:   true,
		Position:   "1",
	}

	formValues := input.toFormValues()

	if formValues["code"] != "fr" {
		t.Errorf("code = %q, want %q", formValues["code"], "fr")
	}
	if formValues["name"] != "French" {
		t.Errorf("name = %q, want %q", formValues["name"], "French")
	}
	if formValues["native_name"] != "Français" {
		t.Errorf("native_name = %q, want %q", formValues["native_name"], "Français")
	}
	if formValues["direction"] != "ltr" {
		t.Errorf("direction = %q, want %q", formValues["direction"], "ltr")
	}
	if formValues["is_active"] != "1" {
		t.Errorf("is_active = %q, want %q", formValues["is_active"], "1")
	}
	if formValues["position"] != "1" {
		t.Errorf("position = %q, want %q", formValues["position"], "1")
	}
}

func TestLanguageFormInputInactive(t *testing.T) {
	input := languageFormInput{
		Code:     "de",
		IsActive: false,
	}

	formValues := input.toFormValues()

	// When inactive, is_active should not be in form values
	if _, exists := formValues["is_active"]; exists {
		t.Error("is_active should not exist when inactive")
	}
}

func TestValidateLanguageCodeForSave(t *testing.T) {
	tests := []struct {
		name         string
		code         string
		isActive     bool
		existingCode string
		wantError    bool
	}{
		{name: "two character code", code: "en", isActive: true},
		{name: "ten character code", code: "abcdefghij", isActive: true},
		{name: "hyphenated code", code: "zh-hans", isActive: true},
		{name: "alphanumeric code", code: "a1-b2", isActive: true},
		{name: "one character code", code: "x", isActive: true, wantError: true},
		{name: "uppercase code", code: "EN", isActive: true, wantError: true},
		{name: "new active reserved code", code: "blog", isActive: true, wantError: true},
		{name: "new inactive reserved code", code: "blog", wantError: true},
		{name: "activate legacy reserved code", code: "blog", isActive: true, existingCode: "blog", wantError: true},
		{name: "deactivate legacy reserved code", code: "blog", existingCode: "blog"},
		{name: "rename legacy reserved code", code: "fr", isActive: true, existingCode: "blog"},
		{name: "cannot rename another code to reserved", code: "api", existingCode: "fr", wantError: true},
		{name: "deactivate legacy invalid code", code: "x", existingCode: "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMessage := validateLanguageCodeForSave(tt.code, tt.isActive, tt.existingCode)
			if (errMessage != "") != tt.wantError {
				t.Errorf("validateLanguageCodeForSave(%q, %v, %q) = %q, wantError %v", tt.code, tt.isActive, tt.existingCode, errMessage, tt.wantError)
			}
		})
	}
}

func TestContentLanguageChoicesIgnoreLegacyUnsafeRows(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()
	for _, language := range []store.CreateLanguageParams{
		{Code: "admin", Name: "Legacy reserved", NativeName: "Legacy reserved", IsActive: true, Direction: "ltr", Position: 10, CreatedAt: now, UpdatedAt: now},
		{Code: "x", Name: "Legacy invalid", NativeName: "Legacy invalid", IsActive: true, Direction: "ltr", Position: 11, CreatedAt: now, UpdatedAt: now},
		{Code: "de", Name: "Inactive", NativeName: "Inactive", IsActive: false, Direction: "ltr", Position: 12, CreatedAt: now, UpdatedAt: now},
	} {
		_, err := queries.CreateLanguage(context.Background(), language)
		if err != nil {
			t.Fatalf("CreateLanguage(%q): %v", language.Code, err)
		}
	}

	choices := ListActiveLanguagesWithFallback(context.Background(), queries)
	if len(choices) != 1 || choices[0].Code != "en" {
		t.Fatalf("content language choices = %#v, want only en", choices)
	}
	for _, code := range []string{"admin", "x", "de"} {
		if _, err := getRoutableContentLanguage(context.Background(), queries, code); err == nil {
			t.Errorf("getRoutableContentLanguage(%q) succeeded; want rejection", code)
		}
	}
	if language, err := getRoutableContentLanguage(context.Background(), queries, "en"); err != nil || language.Code != "en" {
		t.Fatalf("getRoutableContentLanguage(en) = %#v, %v", language, err)
	}
}

func TestLanguageCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	now := time.Now()
	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "fr",
		Name:       "French",
		NativeName: "Français",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if lang.Code != "fr" {
		t.Errorf("Code = %q, want %q", lang.Code, "fr")
	}
	if lang.Name != "French" {
		t.Errorf("Name = %q, want %q", lang.Name, "French")
	}
	if lang.NativeName != "Français" {
		t.Errorf("NativeName = %q, want %q", lang.NativeName, "Français")
	}
}

func TestLanguageList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// English is already created by test helper
	// Create additional test languages
	languages := []store.CreateLanguageParams{
		{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr", IsActive: true, Position: 1, CreatedAt: now, UpdatedAt: now},
		{Code: "de", Name: "German", NativeName: "Deutsch", Direction: "ltr", IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now},
	}
	for _, lang := range languages {
		if _, err := queries.CreateLanguage(context.Background(), lang); err != nil {
			t.Fatalf("CreateLanguage failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		result, err := queries.ListLanguages(context.Background())
		if err != nil {
			t.Fatalf("ListLanguages failed: %v", err)
		}
		if len(result) != 3 { // en + fr + de
			t.Errorf("got %d languages, want 3", len(result))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountLanguages(context.Background())
		if err != nil {
			t.Fatalf("CountLanguages failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})

	t.Run("list active", func(t *testing.T) {
		result, err := queries.ListActiveLanguages(context.Background())
		if err != nil {
			t.Fatalf("ListActiveLanguages failed: %v", err)
		}
		if len(result) < 1 {
			t.Error("should have at least 1 active language")
		}
	})
}

func TestLanguageGetByCode(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// English is already created by test helper
	lang, err := queries.GetLanguageByCode(context.Background(), "en")
	if err != nil {
		t.Fatalf("GetLanguageByCode failed: %v", err)
	}

	if lang.Code != "en" {
		t.Errorf("Code = %q, want %q", lang.Code, "en")
	}
	if !lang.IsDefault {
		t.Error("English should be the default language")
	}
}

func TestLanguageUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "es",
		Name:       "Spanish",
		NativeName: "Español",
		Direction:  "ltr",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	_, err = queries.UpdateLanguage(context.Background(), store.UpdateLanguageParams{
		ID:         lang.ID,
		Code:       "es",
		Name:       "Spanish Updated",
		NativeName: "Español",
		IsDefault:  false,
		IsActive:   false,
		Direction:  "ltr",
		Position:   2,
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateLanguage failed: %v", err)
	}

	updated, err := queries.GetLanguageByID(context.Background(), lang.ID)
	if err != nil {
		t.Fatalf("GetLanguageByID failed: %v", err)
	}

	if updated.Name != "Spanish Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Spanish Updated")
	}
	if updated.IsActive {
		t.Error("IsActive should be false")
	}
	if updated.Position != 2 {
		t.Errorf("Position = %d, want 2", updated.Position)
	}
}

func TestLegacyLanguageRenamePropagatesDenormalizedCodes(t *testing.T) {
	db, sm := testHandlerSetup(t)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()

	legacy, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "blog", Name: "Legacy", NativeName: "Legacy", Direction: "ltr",
		IsActive: false, Position: 2, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "language-rename@example.com", PasswordHash: "x", Role: "admin", Name: "Admin",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO pages (title, slug, author_id, language_code) VALUES ('Legacy page', 'legacy-page', ?, 'blog')`, []any{user.ID}},
		{`INSERT INTO tags (name, slug, language_code) VALUES ('Legacy tag', 'legacy-tag', 'blog')`, nil},
		{`INSERT INTO categories (name, slug, language_code) VALUES ('Legacy category', 'legacy-category', 'blog')`, nil},
		{`INSERT INTO menus (name, slug, language_code) VALUES ('Legacy menu', 'legacy-menu', 'blog')`, nil},
		{`INSERT INTO media (uuid, filename, mime_type, size, uploaded_by, language_code) VALUES ('aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee', 'legacy.pdf', 'application/pdf', 1, ?, 'blog')`, []any{user.ID}},
	} {
		if _, err := db.ExecContext(ctx, seed.statement, seed.args...); err != nil {
			t.Fatalf("seed language-owned row with %q: %v", seed.statement, err)
		}
	}

	h := NewLanguagesHandler(db, nil, sm)
	if err := h.updateLanguage(ctx, store.UpdateLanguageParams{
		ID: legacy.ID, Code: "fr", Name: "French", NativeName: "Français",
		IsActive: true, Direction: "ltr", Position: legacy.Position, UpdatedAt: now,
	}, legacy.Code); err != nil {
		t.Fatalf("updateLanguage: %v", err)
	}

	for table, query := range map[string]string{
		"pages":      `SELECT COUNT(*) FROM pages WHERE language_code = 'fr'`,
		"tags":       `SELECT COUNT(*) FROM tags WHERE language_code = 'fr'`,
		"categories": `SELECT COUNT(*) FROM categories WHERE language_code = 'fr'`,
		"menus":      `SELECT COUNT(*) FROM menus WHERE language_code = 'fr'`,
		"media":      `SELECT COUNT(*) FROM media WHERE language_code = 'fr'`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Errorf("%s rows after rename = %d, err=%v; want one fr row", table, count, err)
		}
	}
	if _, err := queries.GetLanguageByCode(ctx, "blog"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("legacy language code still exists: %v", err)
	}
	if renamed, err := queries.GetLanguageByCode(ctx, "fr"); err != nil || renamed.ID != legacy.ID {
		t.Errorf("renamed language = %+v, err=%v", renamed, err)
	}
}

func TestLanguageDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "it",
		Name:       "Italian",
		NativeName: "Italiano",
		Direction:  "ltr",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if err := queries.DeleteLanguage(context.Background(), lang.ID); err != nil {
		t.Fatalf("DeleteLanguage failed: %v", err)
	}

	_, err = queries.GetLanguageByID(context.Background(), lang.ID)
	if err == nil {
		t.Error("expected error when getting deleted language")
	}
}

func TestLanguageDeleteRefreshesRuntimeStateAndCaches(t *testing.T) {
	db, sm := testHandlerSetup(t)
	if _, err := db.Exec(`
		CREATE TABLE config_translations (
			config_key TEXT NOT NULL,
			language_id INTEGER NOT NULL,
			value TEXT NOT NULL
		);
		CREATE TABLE media_translations (
			media_id INTEGER NOT NULL,
			language_id INTEGER NOT NULL,
			alt TEXT NOT NULL DEFAULT '',
			caption TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatal(err)
	}
	queries := store.New(db)
	now := time.Now()
	language, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	i18n.ConfigureLanguages([]string{"en", "ru"}, "en")
	t.Cleanup(func() { i18n.ConfigureLanguages([]string{"en"}, "en") })
	if got := i18n.MatchLanguage("ru"); got != "ru" {
		t.Fatalf("runtime language before delete = %q, want ru", got)
	}

	cacheManager := cache.NewManager(queries)
	cacheManager.General.Set("language-delete-sentinel", "stale")
	h := NewLanguagesHandler(db, nil, sm, cacheManager)
	req := httptest.NewRequest(http.MethodDelete, "/admin/languages/1", nil)
	req.Header.Set("HX-Request", "true")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(language.ID, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body = %s", w.Code, w.Body.String())
	}
	if got := i18n.MatchLanguage("ru"); got == "ru" {
		t.Fatalf("deleted runtime language still matches: %q", got)
	}
	if _, ok := cacheManager.General.Get("language-delete-sentinel"); ok {
		t.Fatal("language delete did not invalidate general cache")
	}
}

func TestDeleteLanguageProtectsEveryLanguageOwnedTableAndInvalidatesCaches(t *testing.T) {
	db, sm := testHandlerSetup(t)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	language, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 2, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE config_translations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, config_key TEXT NOT NULL,
			language_id INTEGER NOT NULL, value TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE media_translations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, media_id INTEGER NOT NULL,
			language_id INTEGER NOT NULL, alt TEXT NOT NULL DEFAULT '', caption TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatal(err)
	}
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "language-owner@example.com", PasswordHash: "x", Role: "admin",
		Name: "Owner", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		fmt.Sprintf(`INSERT INTO pages (title, slug, author_id, language_code) VALUES ('FR page','fr-page',%d,'fr')`, user.ID),
		`INSERT INTO tags (name, slug, language_code) VALUES ('FR tag','fr-tag','fr')`,
		`INSERT INTO categories (name, slug, language_code) VALUES ('FR category','fr-category','fr')`,
		`INSERT INTO menus (name, slug, language_code) VALUES ('FR menu','fr-menu','fr')`,
		`INSERT INTO forms (name, slug, title, language_code) VALUES ('FR form','fr-form','FR form','fr')`,
		`INSERT INTO form_fields (form_id, type, name, label, language_code) VALUES ((SELECT id FROM forms WHERE slug='fr-form'),'text','name','Name','fr')`,
		`INSERT INTO form_submissions (form_id, data, language_code) VALUES ((SELECT id FROM forms WHERE slug='fr-form'),'{}','fr')`,
		`INSERT INTO widgets (theme, area, widget_type, language_code) VALUES ('default','sidebar','text','fr')`,
		fmt.Sprintf(`INSERT INTO media (uuid, filename, mime_type, size, uploaded_by, language_code) VALUES ('aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee','fr.pdf','application/pdf',1,%d,'fr')`, user.ID),
		`INSERT INTO config (key, value, language_code) VALUES ('fr.setting','value','fr')`,
		fmt.Sprintf(`INSERT INTO translations (entity_type, entity_id, language_id, translation_id) VALUES ('page',1,%d,2)`, language.ID),
		fmt.Sprintf(`INSERT INTO config_translations (config_key, language_id, value) VALUES ('site_name',%d,'Site FR')`, language.ID),
		fmt.Sprintf(`INSERT INTO media_translations (media_id, language_id, alt, caption) VALUES ((SELECT id FROM media WHERE uuid='aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'),%d,'Alt','Caption')`, language.ID),
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed language reference with %q: %v", statement, err)
		}
	}

	cacheManager := cache.NewManager(queries)
	h := NewLanguagesHandler(db, nil, sm, cacheManager)
	err = h.deleteLanguageIfUnused(ctx, language)
	var inUse *languageInUseError
	if !errors.As(err, &inUse) || inUse.count != 13 {
		t.Fatalf("deleteLanguageIfUnused() error = %v, want 13 protected references", err)
	}
	if _, err := queries.GetLanguageByID(ctx, language.ID); err != nil {
		t.Fatalf("referenced language was deleted: %v", err)
	}

	for _, statement := range []string{
		`DELETE FROM media_translations`, `DELETE FROM config_translations`, `DELETE FROM translations`,
		`DELETE FROM form_submissions`, `DELETE FROM form_fields`, `DELETE FROM forms`,
		`DELETE FROM pages`, `DELETE FROM tags`, `DELETE FROM categories`, `DELETE FROM menus`,
		`DELETE FROM widgets`, `DELETE FROM media`, `DELETE FROM config WHERE language_code='fr'`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	cacheManager.General.Set("language-delete-sentinel", "stale")
	if err := h.deleteLanguageIfUnused(ctx, language); err != nil {
		t.Fatalf("delete unused language: %v", err)
	}
	if _, err := queries.GetLanguageByID(ctx, language.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unused language lookup = %v, want sql.ErrNoRows", err)
	}
	if _, ok := cacheManager.General.Get("language-delete-sentinel"); ok {
		t.Fatal("successful language deletion left derived caches populated")
	}
}

func TestLanguageCodeExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	t.Run("exists", func(t *testing.T) {
		// "en" is created by test helper
		count, err := queries.LanguageCodeExists(context.Background(), "en")
		if err != nil {
			t.Fatalf("LanguageCodeExists failed: %v", err)
		}
		if count == 0 {
			t.Error("expected code to exist")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		count, err := queries.LanguageCodeExists(context.Background(), "zz")
		if err != nil {
			t.Fatalf("LanguageCodeExists failed: %v", err)
		}
		if count != 0 {
			t.Error("expected code to not exist")
		}
	})
}

func TestLanguageRTL(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "ar",
		Name:       "Arabic",
		NativeName: "العربية",
		Direction:  "rtl",
		IsActive:   true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	if lang.Direction != "rtl" {
		t.Errorf("Direction = %q, want %q", lang.Direction, "rtl")
	}
}

func TestLanguageSetDefault(t *testing.T) {
	db, sm := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create a new language
	lang, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code:       "pt",
		Name:       "Portuguese",
		NativeName: "Português",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	cacheManager := cache.NewManager(queries)
	cacheManager.General.Set("language-route-sentinel", "stale")
	h := NewLanguagesHandler(db, nil, sm, cacheManager)
	if err := h.setDefaultLanguage(context.Background(), lang.ID); err != nil {
		t.Fatalf("setDefaultLanguage failed: %v", err)
	}

	updated, err := queries.GetLanguageByID(context.Background(), lang.ID)
	if err != nil {
		t.Fatalf("GetLanguageByID failed: %v", err)
	}

	if !updated.IsDefault {
		t.Error("IsDefault should be true after SetDefaultLanguage")
	}
	if _, ok := cacheManager.General.Get("language-route-sentinel"); ok {
		t.Fatal("default-language switch left derived routing caches populated")
	}
	var defaults int
	if err := db.QueryRow(`SELECT COUNT(*) FROM languages WHERE is_default = 1`).Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if defaults != 1 {
		t.Fatalf("default language count = %d, want 1", defaults)
	}
}

func TestLanguageSetDefaultRollsBackWhenTargetUpdateFails(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()
	target, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code: "pt", Name: "Portuguese", NativeName: "Português", IsActive: true,
		Direction: "ltr", Position: 2, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_default_switch
		BEFORE UPDATE OF is_default ON languages
		WHEN NEW.id = %d AND NEW.is_default = 1
		BEGIN SELECT RAISE(FAIL, 'forced default switch failure'); END`, target.ID)); err != nil {
		t.Fatal(err)
	}

	h := NewLanguagesHandler(db, nil, sm)
	if err := h.setDefaultLanguage(context.Background(), target.ID); err == nil {
		t.Fatal("setDefaultLanguage succeeded despite forced second-update failure")
	}
	current, err := queries.GetDefaultLanguage(context.Background())
	if err != nil {
		t.Fatalf("previous default was cleared despite rollback: %v", err)
	}
	if current.Code != "en" {
		t.Fatalf("default after rollback = %q, want en", current.Code)
	}
}

func TestFindDefaultLanguage(t *testing.T) {
	languages := []store.Language{
		{ID: 1, Code: "en", Name: "English", IsDefault: false},
		{ID: 2, Code: "fr", Name: "French", IsDefault: true},
		{ID: 3, Code: "de", Name: "German", IsDefault: false},
	}

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang == nil {
		t.Fatal("expected to find default language")
	}
	if defaultLang.Code != "fr" {
		t.Errorf("Code = %q, want %q", defaultLang.Code, "fr")
	}
}

func TestFindDefaultLanguageNone(t *testing.T) {
	languages := []store.Language{
		{ID: 1, Code: "en", Name: "English", IsDefault: false},
		{ID: 2, Code: "fr", Name: "French", IsDefault: false},
	}

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang != nil {
		t.Error("expected nil when no default language")
	}
}

func TestFindDefaultLanguageMultiple(t *testing.T) {
	languages := []store.Language{
		{ID: 1, Code: "en", Name: "English", IsDefault: true},
		{ID: 2, Code: "fr", Name: "French", IsDefault: true},
	}
	if defaultLang := FindDefaultLanguage(languages); defaultLang != nil {
		t.Fatalf("FindDefaultLanguage() = %+v, want nil for ambiguous defaults", defaultLang)
	}
}

func TestFindDefaultLanguageEmpty(t *testing.T) {
	var languages []store.Language

	defaultLang := FindDefaultLanguage(languages)
	if defaultLang != nil {
		t.Error("expected nil for empty slice")
	}
}

func TestLanguageToFormValues(t *testing.T) {
	lang := &store.Language{
		Code:       "ja",
		Name:       "Japanese",
		NativeName: "日本語",
		Direction:  "ltr",
		Position:   5,
		IsActive:   true,
	}

	formValues := languageToFormValues(lang)

	if formValues["code"] != "ja" {
		t.Errorf("code = %q, want %q", formValues["code"], "ja")
	}
	if formValues["name"] != "Japanese" {
		t.Errorf("name = %q, want %q", formValues["name"], "Japanese")
	}
	if formValues["native_name"] != "日本語" {
		t.Errorf("native_name = %q, want %q", formValues["native_name"], "日本語")
	}
	if formValues["direction"] != "ltr" {
		t.Errorf("direction = %q, want %q", formValues["direction"], "ltr")
	}
	if formValues["position"] != "5" {
		t.Errorf("position = %q, want %q", formValues["position"], "5")
	}
	if formValues["is_active"] != "1" {
		t.Errorf("is_active = %q, want %q", formValues["is_active"], "1")
	}
}

func TestLanguageToFormValuesInactive(t *testing.T) {
	lang := &store.Language{
		Code:     "ko",
		IsActive: false,
	}

	formValues := languageToFormValues(lang)

	if _, exists := formValues["is_active"]; exists {
		t.Error("is_active should not exist when inactive")
	}
}

func TestLanguageGetMaxPosition(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create languages with different positions
	positions := []int64{5, 10, 3}
	for i, pos := range positions {
		_, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
			Code:       string(rune('a' + i)),
			Name:       "Lang " + string(rune('A'+i)),
			NativeName: "Lang " + string(rune('A'+i)),
			Direction:  "ltr",
			IsActive:   true,
			Position:   pos,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			t.Fatalf("CreateLanguage failed: %v", err)
		}
	}

	maxPos, err := queries.GetMaxLanguagePosition(context.Background())
	if err != nil {
		t.Fatalf("GetMaxLanguagePosition failed: %v", err)
	}

	// Expect max to be 10
	if maxPos == nil {
		t.Fatal("maxPos should not be nil")
	}
}

// TestLanguagePrefixCannotShadowExistingPageRoute covers the gap between two
// namespaces that are validated apart. Page slugs are checked against pages
// and aliases; language codes are checked against the application's reserved
// routes. Neither consults the other, so an active language whose code matches
// a page's URL used to take that URL over — the middleware strips the segment
// and the language homepage answers, leaving the page unreachable with no
// error anywhere.
func TestLanguagePrefixCannotShadowExistingPageRoute(t *testing.T) {
	db, _ := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	queries := store.New(db)
	ctx := context.Background()

	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Engineering", Slug: "eng", Body: "Content", Status: "published", AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}
	if _, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
		PageID: page.ID, Alias: "esp/equipo", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreatePageAlias failed: %v", err)
	}

	for name, tc := range map[string]struct {
		code        string
		isActive    bool
		wantMessage string
	}{
		"slug taken":                 {code: "eng", isActive: true, wantMessage: "languages.error_code_page_conflict"},
		"alias parent taken":         {code: "esp", isActive: true, wantMessage: "languages.error_code_page_conflict"},
		"free prefix":                {code: "fra", isActive: true, wantMessage: ""},
		"inactive never routes":      {code: "eng", isActive: false, wantMessage: ""},
		"reserved handled elsewhere": {code: "admin", isActive: true, wantMessage: ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := validateLanguagePrefixAgainstPages(ctx, queries, tc.code, tc.isActive)
			if err != nil {
				t.Fatalf("validateLanguagePrefixAgainstPages() error = %v", err)
			}
			if got != tc.wantMessage {
				t.Fatalf("validateLanguagePrefixAgainstPages(%q, active=%v) = %q, want %q",
					tc.code, tc.isActive, got, tc.wantMessage)
			}
		})
	}
}

// TestSetDefaultLanguageRejectsPageShadowingPrefix proves the transactional
// path refuses the switch too, not just the form validation ahead of it.
func TestSetDefaultLanguageRejectsPageShadowingPrefix(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	if _, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Engineering", Slug: "eng", Body: "Content", Status: "published", AuthorID: user.ID,
	}); err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}
	lang, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true,
		Direction: "ltr", Position: 3, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage failed: %v", err)
	}

	h := NewLanguagesHandler(db, nil, sm, cache.NewManager(queries))
	err = h.setDefaultLanguage(ctx, lang.ID)
	if err == nil {
		t.Fatal("setDefaultLanguage() accepted a code that shadows an existing page")
	}
	updated, getErr := queries.GetLanguageByID(ctx, lang.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if updated.IsDefault {
		t.Fatal("rejected default-language switch was committed anyway")
	}
}
