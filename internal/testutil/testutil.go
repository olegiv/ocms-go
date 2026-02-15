// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil provides shared test helpers for the oCMS project.
package testutil

import (
	"database/sql"
	"html/template"
	"log/slog"
	"os"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

// TestLogger creates a silent test logger that only outputs warnings and errors.
func TestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
}

// TestLoggerSilent creates a completely silent test logger (error level only).
func TestLoggerSilent() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

// TestDB creates a temporary test database with core migrations applied.
// Returns the database and a cleanup function that should be deferred.
func TestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "ocms-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	return db, func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}
}

// MinimalThemeFuncMap returns the minimal template functions needed to parse theme templates in tests.
// Use this in any test that creates a theme.Manager and calls LoadThemes or SetFuncMap.
func MinimalThemeFuncMap() template.FuncMap {
	return template.FuncMap{
		"safeHTML":              func(s string) string { return s },
		"safeCSS":              func(s string) string { return s },
		"safeURL":              func(s string) string { return s },
		"T":                    func(lang, key string, args ...any) string { return key },
		"TTheme":               func(lang, key string, args ...any) string { return key },
		"defaultLangCode":      func() string { return "en" },
		"buildMenuTree":        func(items any) any { return nil },
		"buildBreadcrumbs":     func(items any, pageID int64) any { return nil },
		"dateFormat":           func(t any, layout string) string { return "" },
		"dateFormatMedium":     func(t any, lang string) string { return "" },
		"truncateHTML":         func(s string, max int) string { return s },
		"raw":                  func(s string) string { return s },
		"nl2br":                func(s string) string { return s },
		"addCacheBuster":       func(s string) string { return s },
		"analyticsHead":        func() string { return "" },
		"analyticsBody":        func() string { return "" },
		"hcaptchaEnabled":      func() bool { return false },
		"hcaptchaHead":         func() string { return "" },
		"hcaptchaWidget":       func() string { return "" },
		"seq":                  func(start, end int) []int { return nil },
		"add":                  func(a, b int) int { return a + b },
		"sub":                  func(a, b int) int { return a - b },
		"mul":                  func(a, b int) int { return a * b },
		"div":                  func(a, b int) int { return a / b },
		"mod":                  func(a, b int) int { return a % b },
		"gt":                   func(a, b int) bool { return a > b },
		"lt":                   func(a, b int) bool { return a < b },
		"gte":                  func(a, b int) bool { return a >= b },
		"lte":                  func(a, b int) bool { return a <= b },
		"eq":                   func(a, b int) bool { return a == b },
		"hasPrefix":            func(s, prefix string) bool { return false },
		"hasSuffix":            func(s, suffix string) bool { return false },
		"contains":             func(s, substr string) bool { return false },
		"replace":              func(s, old, nw string) string { return s },
		"lower":                func(s string) string { return s },
		"upper":                func(s string) string { return s },
		"title":                func(s string) string { return s },
		"join":                 func(a []string, sep string) string { return "" },
		"split":                func(s, sep string) []string { return nil },
		"default":              func(defVal, val any) any { return val },
		"json":                 func(v any) string { return "" },
		"jsonIndent":           func(v any) string { return "" },
		"toJSON":               func(v any) string { return "" },
		"parseJSON":            func(s string) any { return nil },
		"dict":                 func(pairs ...any) map[string]any { return nil },
		"list":                 func(items ...any) []any { return items },
		"mapWidgetsByArea":     func(widgets any) any { return nil },
		"renderWidget":         func(widget any, settings any, lang string) string { return "" },
		"embedHead":            func() string { return "" },
		"embedScripts":         func() string { return "" },
		"analyticsExtHead":     func() string { return "" },
		"analyticsExtBody":     func() string { return "" },
		"embedBody":            func() string { return "" },
		"now":                  func() any { return nil },
		"timeBefore":           func(t1, t2 any) bool { return false },
		"formatDate":           func(t any) string { return "" },
		"formatDateTime":       func(t any) string { return "" },
		"privacyHead":          func() string { return "" },
		"privacyFooterLink":    func() string { return "" },
		"formatDateLocale":     func(t any, lang string) string { return "" },
		"formatDateTimeLocale": func(t any, lang string) string { return "" },
		"truncate":             func(s string, length int) string { return s },
		"safe":                 func(s string) string { return s },
		"multiply":             func(a, b int) int { return a * b },
		"repeat":               func(s string, count int) string { return s },
		"formatBytes":          func(bytes int64) string { return "" },
		"deref":                func(p any) int64 { return 0 },
		"mediaAlt":             func(alt, filename string) string { return alt },
		"mediaURL":             func(u string) string { return u },
		"imageSrc":             func(u string, variant string) string { return u },
		"imageSrcset":          func(u string) string { return "" },
		"informerBar":          func() string { return "" },
	}
}

// TestMemoryDB creates an in-memory SQLite database for testing.
// Useful for tests that don't need persistent storage or migrations.
func TestMemoryDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return db
}
