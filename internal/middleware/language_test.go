// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

func newLanguageMiddlewareTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open language test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE languages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			native_name TEXT NOT NULL,
			is_default BOOLEAN NOT NULL DEFAULT 0,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			direction TEXT NOT NULL DEFAULT 'ltr',
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO languages (code, name, native_name, is_default, is_active, position) VALUES
			('en', 'English', 'English', 1, 1, 0),
			('fr', 'French', 'Français', 0, 1, 1),
			('zh-hans', 'Chinese', '简体中文', 0, 1, 2),
			('abcdefghij', 'Maximum', 'Maximum', 0, 1, 3),
			('a1-b2', 'Alphanumeric', 'Alphanumeric', 0, 1, 4),
			('de', 'German', 'Deutsch', 0, 0, 5),
			('blog', 'Legacy reserved', 'Legacy reserved', 0, 1, 6),
			('x', 'Legacy invalid', 'Legacy invalid', 0, 1, 7);
	`)
	if err != nil {
		t.Fatalf("create language test schema: %v", err)
	}

	return db
}

func newLanguageMiddlewareTestRouter(db *sql.DB) http.Handler {
	root := chi.NewRouter()
	frontend := chi.NewRouter()
	frontend.Use(Language(db))

	writeSelection := func(route string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			langCode := ""
			if lang := GetLanguage(r); lang != nil {
				langCode = lang.Code
			}
			_, _ = fmt.Fprintf(
				w,
				"%s|%s|%s|%s",
				route,
				langCode,
				GetLanguagePrefix(r),
				chi.URLParam(r, "slug"),
			)
		}
	}

	frontend.Get("/", writeSelection("home"))
	frontend.Get("/blog", writeSelection("blog"))
	frontend.Get("/tag/{slug}", writeSelection("tag"))
	frontend.Get("/{slug}", writeSelection("page"))
	root.Mount("/", frontend)
	return root
}

func TestLanguageRouting(t *testing.T) {
	router := newLanguageMiddlewareTestRouter(newLanguageMiddlewareTestDB(t))

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
		wantCookie bool
	}{
		{
			name:       "URL prefix beats query language",
			path:       "/fr/article?lang=en",
			wantStatus: http.StatusOK,
			wantBody:   "page|fr|fr|article",
		},
		{
			name:       "query language is used without prefix",
			path:       "/article?lang=fr",
			wantStatus: http.StatusOK,
			wantBody:   "page|fr||article",
			wantCookie: true,
		},
		{
			name:       "hyphenated active prefix",
			path:       "/zh-hans/tag/news",
			wantStatus: http.StatusOK,
			wantBody:   "tag|zh-hans|zh-hans|news",
		},
		{
			name:       "maximum length active prefix",
			path:       "/abcdefghij",
			wantStatus: http.StatusOK,
			wantBody:   "home|abcdefghij|abcdefghij|",
		},
		{
			name:       "alphanumeric active prefix",
			path:       "/a1-b2/article",
			wantStatus: http.StatusOK,
			wantBody:   "page|a1-b2|a1-b2|article",
		},
		{
			name:       "inactive one segment falls through to page slug",
			path:       "/de",
			wantStatus: http.StatusOK,
			wantBody:   "page|en||de",
		},
		{
			name:       "invalid legacy one segment falls through to page slug",
			path:       "/x",
			wantStatus: http.StatusOK,
			wantBody:   "page|en||x",
		},
		{
			name:       "reserved active code remains core route",
			path:       "/blog",
			wantStatus: http.StatusOK,
			wantBody:   "blog|en||",
		},
		{
			name:       "inactive nested prefix is not routed",
			path:       "/de/article",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown nested prefix is not routed",
			path:       "/zz/article",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "reserved nested prefix is not routed",
			path:       "/blog/article",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantBody != "" && rr.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.wantBody)
			}
			hasCookie := rr.Header().Get("Set-Cookie") != ""
			if hasCookie != tt.wantCookie {
				t.Errorf("Set-Cookie present = %v, want %v", hasCookie, tt.wantCookie)
			}
		})
	}
}

func TestLanguage_InactiveDefaultIsNotInstalled(t *testing.T) {
	db := newLanguageMiddlewareTestDB(t)
	if _, err := db.Exec(`UPDATE languages SET is_active = 0 WHERE is_default = 1`); err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}
	router := newLanguageMiddlewareTestRouter(db)

	tests := []struct {
		name       string
		path       string
		cookieCode string
		wantBody   string
	}{
		{name: "unprefixed route has no language context", path: "/article", wantBody: "page|||article"},
		{name: "active explicit prefix remains available", path: "/fr/article", wantBody: "page|fr|fr|article"},
		{name: "active query selection remains available", path: "/article?lang=fr", wantBody: "page|fr||article"},
		{name: "active homepage preference remains available", path: "/", cookieCode: "fr", wantBody: "home|fr||"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.cookieCode != "" {
				req.AddCookie(&http.Cookie{Name: LanguageCookieName, Value: tt.cookieCode})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusOK)
			}
			if tt.wantBody != "" && w.Body.String() != tt.wantBody {
				t.Errorf("body = %q; want %q", w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestLanguage_AmbiguousDefaultsAreNotInstalled(t *testing.T) {
	db := newLanguageMiddlewareTestDB(t)
	if _, err := db.Exec(`UPDATE languages SET is_default = 1 WHERE code = 'fr'`); err != nil {
		t.Fatalf("add second default language: %v", err)
	}
	router := newLanguageMiddlewareTestRouter(db)

	for _, tt := range []struct {
		name     string
		path     string
		wantCode int
		wantBody string
	}{
		{name: "unprefixed route has no language context", path: "/article", wantCode: http.StatusOK, wantBody: "page|||article"},
		{name: "prefixed route fails closed", path: "/fr/article", wantCode: http.StatusNotFound, wantBody: "404 page not found\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d", w.Code, tt.wantCode)
			}
			if w.Body.String() != tt.wantBody {
				t.Errorf("body = %q; want %q", w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestLanguage_ActiveLegacyDefaultIsIgnored(t *testing.T) {
	for _, code := range []string{"blog", "x"} {
		t.Run(code, func(t *testing.T) {
			db := newLanguageMiddlewareTestDB(t)
			if _, err := db.Exec(`UPDATE languages SET is_default = CASE WHEN code = ? THEN 1 ELSE 0 END`, code); err != nil {
				t.Fatalf("replace default language: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/article", nil)
			w := httptest.NewRecorder()
			newLanguageMiddlewareTestRouter(db).ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusOK)
			}
			if body := w.Body.String(); body != "page|||article" {
				t.Errorf("body = %q; want %q", body, "page|||article")
			}
		})
	}
}

func TestActiveLanguageMapIgnoresAndLogsLegacyCodes(t *testing.T) {
	var logBuffer bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuffer, nil)))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	langMap := activeLanguageMap([]store.Language{
		{ID: 1, Code: "fr"},
		{ID: 2, Code: "blog"},
		{ID: 3, Code: "x"},
	})

	if _, ok := langMap["fr"]; !ok {
		t.Error("valid active language was omitted")
	}
	if _, ok := langMap["blog"]; ok {
		t.Error("reserved active language was not omitted")
	}
	if _, ok := langMap["x"]; ok {
		t.Error("invalid active language was not omitted")
	}

	logs := logBuffer.String()
	for _, message := range []string{"reserved route prefix", "invalid code"} {
		if !strings.Contains(logs, message) {
			t.Errorf("log output %q does not contain %q", logs, message)
		}
	}
}

func TestMatchAcceptLanguage(t *testing.T) {
	langMap := map[string]store.Language{
		"en": {ID: 1, Code: "en", Name: "English", IsDefault: true},
		"ru": {ID: 2, Code: "ru", Name: "Russian"},
		"de": {ID: 3, Code: "de", Name: "German"},
	}

	tests := []struct {
		name       string
		acceptLang string
		wantCode   string
		wantNil    bool
	}{
		{
			name:       "exact match",
			acceptLang: "en",
			wantCode:   "en",
		},
		{
			name:       "exact match with quality",
			acceptLang: "ru;q=0.9",
			wantCode:   "ru",
		},
		{
			name:       "first match wins",
			acceptLang: "de,en;q=0.9,ru;q=0.8",
			wantCode:   "de",
		},
		{
			name:       "primary code match",
			acceptLang: "en-US",
			wantCode:   "en",
		},
		{
			name:       "primary code match with region",
			acceptLang: "de-DE,en;q=0.9",
			wantCode:   "de",
		},
		{
			name:       "no match",
			acceptLang: "fr,es",
			wantNil:    true,
		},
		{
			name:       "case insensitive",
			acceptLang: "EN-US",
			wantCode:   "en",
		},
		{
			name:       "multiple with spaces",
			acceptLang: " ru , en;q=0.8 ",
			wantCode:   "ru",
		},
		{
			name:       "empty string",
			acceptLang: "",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchAcceptLanguage(tt.acceptLang, langMap)

			if tt.wantNil {
				if got != nil {
					t.Errorf("matchAcceptLanguage() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("matchAcceptLanguage() = nil, want language")
			}
			if got.Code != tt.wantCode {
				t.Errorf("matchAcceptLanguage().Code = %q, want %q", got.Code, tt.wantCode)
			}
		})
	}
}

func TestSetLanguageContext(t *testing.T) {
	lang := store.Language{
		ID:         1,
		Code:       "en",
		Name:       "English",
		NativeName: "English",
		Direction:  "ltr",
		IsDefault:  true,
	}

	ctx := context.Background()
	newCtx := setLanguageContext(ctx, lang)

	// Check LanguageInfo
	info, ok := newCtx.Value(ContextKeyLanguage).(LanguageInfo)
	if !ok {
		t.Fatal("ContextKeyLanguage not set")
	}
	if info.ID != 1 {
		t.Errorf("LanguageInfo.ID = %d, want 1", info.ID)
	}
	if info.Code != "en" {
		t.Errorf("LanguageInfo.Code = %q, want %q", info.Code, "en")
	}
	if info.Name != "English" {
		t.Errorf("LanguageInfo.Name = %q, want %q", info.Name, "English")
	}
	if info.Direction != "ltr" {
		t.Errorf("LanguageInfo.Direction = %q, want %q", info.Direction, "ltr")
	}
	if !info.IsDefault {
		t.Error("LanguageInfo.IsDefault = false, want true")
	}

	// Check language code
	code, ok := newCtx.Value(ContextKeyLanguageCode).(string)
	if !ok {
		t.Fatal("ContextKeyLanguageCode not set")
	}
	if code != "en" {
		t.Errorf("ContextKeyLanguageCode = %q, want %q", code, "en")
	}
}

func TestGetLanguage(t *testing.T) {
	t.Run("no language in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		lang := GetLanguage(req)
		if lang != nil {
			t.Errorf("GetLanguage() = %v, want nil", lang)
		}
	})

	t.Run("language in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		langInfo := LanguageInfo{
			ID:         2,
			Code:       "ru",
			Name:       "Russian",
			NativeName: "Русский",
			Direction:  "ltr",
			IsDefault:  false,
		}
		ctx := context.WithValue(req.Context(), ContextKeyLanguage, langInfo)
		req = req.WithContext(ctx)

		lang := GetLanguage(req)
		if lang == nil {
			t.Fatal("GetLanguage() = nil, want language")
		}
		if lang.ID != 2 {
			t.Errorf("GetLanguage().ID = %d, want 2", lang.ID)
		}
		if lang.Code != "ru" {
			t.Errorf("GetLanguage().Code = %q, want %q", lang.Code, "ru")
		}
		if lang.NativeName != "Русский" {
			t.Errorf("GetLanguage().NativeName = %q, want %q", lang.NativeName, "Русский")
		}
	})
}

func TestSetLanguageCookie(t *testing.T) {
	t.Run("production mode (secure)", func(t *testing.T) {
		// Reset to production mode (default)
		InitLanguageCookies(false)

		rr := httptest.NewRecorder()
		SetLanguageCookie(rr, "ru")

		cookies := rr.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("Expected 1 cookie, got %d", len(cookies))
		}

		cookie := cookies[0]
		if cookie.Name != LanguageCookieName {
			t.Errorf("Cookie name = %q, want %q", cookie.Name, LanguageCookieName)
		}
		if cookie.Value != "ru" {
			t.Errorf("Cookie value = %q, want %q", cookie.Value, "ru")
		}
		if cookie.Path != "/" {
			t.Errorf("Cookie path = %q, want %q", cookie.Path, "/")
		}
		if !cookie.HttpOnly {
			t.Error("Cookie should be HttpOnly")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("Cookie SameSite = %v, want %v", cookie.SameSite, http.SameSiteLaxMode)
		}
		if cookie.MaxAge <= 0 {
			t.Error("Cookie MaxAge should be positive (1 year)")
		}
		if !cookie.Secure {
			t.Error("Cookie should be Secure in production mode")
		}
	})

	t.Run("development mode (not secure)", func(t *testing.T) {
		// Set to development mode
		InitLanguageCookies(true)

		rr := httptest.NewRecorder()
		SetLanguageCookie(rr, "en")

		cookies := rr.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("Expected 1 cookie, got %d", len(cookies))
		}

		cookie := cookies[0]
		if cookie.Secure {
			t.Error("Cookie should NOT be Secure in development mode")
		}

		// Reset to production mode for other tests
		InitLanguageCookies(false)
	})
}
