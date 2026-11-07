// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/sqlite3store"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
)

// Kept literal so the regression runs with the old production files.
const testUILanguageCookieName = "ocms_admin_lang"

// TestSetLanguagePreferenceRedirect checks that the language switch returns to
// a same-origin Referer and falls back for one that would leave the site.
// A same-host Referer whose path starts with "//" used to become a
// scheme-relative Location (security audit 2026-10-17, L-1).
func TestSetLanguagePreferenceRedirect(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}

	const fallback = "/admin"
	for _, tt := range []struct {
		name    string
		referer string
		want    string
	}{
		{"same-origin page", "http://example.com/admin/pages?page=2", "/admin/pages?page=2"},
		{"no referer", "", fallback},
		{"cross-origin", "https://evil.example/admin", fallback},
		{"same-origin double-slash path", "http://example.com//evil.example/x", fallback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/language", strings.NewReader("lang=ru"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			req = requestWithSession(sm, req)
			rr := httptest.NewRecorder()

			setLanguagePreference(rr, req, renderer, fallback)

			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Errorf("redirect = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
			if got := renderer.GetAdminLang(req); got != "ru" {
				t.Errorf("saved language = %q, want ru", got)
			}
		})
	}
}

// A public language switch used to mark a new SCS session as modified,
// inserting a SQLite row and sending an authentication session cookie.
func TestLanguageSwitchDoesNotCreateAnonymousSessions(t *testing.T) {
	h, db, sm := newAuthTestHandler(t)
	sm.Store = sqlite3store.NewWithCleanupInterval(db, 0)
	sm.IdleTimeout = 2 * time.Hour
	router := authTestRouter(h, db, sm)

	siteCookie := &http.Cookie{Name: middleware.LanguageCookieName, Value: "en"}
	requestLanguage := func(lang string, cookies ...*http.Cookie) *http.Cookie {
		t.Helper()
		rr := authRequest(router, http.MethodPost, "/language", url.Values{"lang": {lang}}, cookies...)
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("anonymous POST /language created %d session rows", count)
		}
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Fatalf("language redirect = %d %q, want 303 /login", rr.Code, rr.Header().Get("Location"))
		}
		response := rr.Result()
		defer func() { _ = response.Body.Close() }()
		setCookies := response.Cookies()
		if len(setCookies) != 1 || setCookies[0].Name != testUILanguageCookieName {
			t.Fatalf("want only the UI preference cookie, got %v", rr.Header().Values("Set-Cookie"))
		}
		cookie := setCookies[0]
		if cookie.Path != "/" || cookie.Domain != "" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 {
			t.Fatalf("invalid production UI preference cookie attributes: %v", cookie)
		}
		return cookie
	}

	cookie := requestLanguage("ru", siteCookie)
	if cookie.Value != "ru" {
		t.Fatalf("language cookie = %q, want ru", cookie.Value)
	}
	assertLocalizedPage(t, authRequest(router, http.MethodGet, "/login", nil, siteCookie, cookie), "ru", "auth.login")
	// Even a bogus session token cannot turn a preference into a new row.
	cookie = requestLanguage("unsupported", cookie, &http.Cookie{Name: sm.Cookie.Name, Value: "unknown-token"})
	if cookie.Value != "en" {
		t.Fatalf("unsupported language stored as %q, want en", cookie.Value)
	}
	assertLocalizedPage(t, authRequest(router, http.MethodGet, "/login", nil, cookie), "en", "auth.login")
	assertLocalizedPage(t, authRequest(router, http.MethodGet, "/login", nil, &http.Cookie{Name: testUILanguageCookieName, Value: "unsupported"}), "en", "auth.login")
}

func TestLanguageSwitchPreservesPreferenceAcrossSignIn(t *testing.T) {
	h, db, sm := newAuthTestHandler(t)
	router := authTestRouter(h, db, sm)
	const password = "Language-fixture-password!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := createTestUser(t, db, testUser{Email: "language-editor@example.test", Role: model.RoleEditor, Name: "Language editor", PasswordHash: hash})

	// Simulate a visitor with a language stored in an anonymous session
	// before upgrading. A new selection must take effect on that session.
	ctx, err := sm.Load(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	sm.Put(ctx, middleware.SessionKeyAdminLang, "en")
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &http.Cookie{Name: sm.Cookie.Name, Value: token}
	switched := authRequest(router, http.MethodPost, "/language", url.Values{"lang": {"ru"}}, legacy)
	response := switched.Result()
	defer func() { _ = response.Body.Close() }()
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != testUILanguageCookieName {
		t.Fatal("legacy anonymous session did not receive a UI preference cookie")
	}
	anonymousPreference := cookies[0]
	assertLocalizedPage(t, authRequest(router, http.MethodGet, "/login", nil, legacy, anonymousPreference), "ru", "auth.login")

	login := authRequest(router, http.MethodPost, "/login", url.Values{"email": {user.Email}, "password": {password}}, legacy, anonymousPreference)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/admin" {
		t.Fatalf("sign-in redirect = %d %q, want 303 /admin", login.Code, login.Header().Get("Location"))
	}
	signedIn := sessionCookie(t, login, sm)
	assertLocalizedPage(t, authRequest(router, http.MethodGet, "/account", nil, signedIn), "ru", "account.title")

	for _, tt := range []struct {
		path, selected, previous string
	}{
		{"/language", "en", "ru"},
		{"/admin/language", "ru", "en"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			preference := &http.Cookie{Name: testUILanguageCookieName, Value: tt.previous}
			rr := authRequest(router, http.MethodPost, tt.path, url.Values{"lang": {tt.selected}}, signedIn, preference)
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("signed-in language switch = %d, want 303", rr.Code)
			}
			res := rr.Result()
			defer func() { _ = res.Body.Close() }()
			cleared := false
			for _, cookie := range res.Cookies() {
				if cookie.Name == testUILanguageCookieName && cookie.MaxAge < 0 {
					cleared = true
				}
			}
			if !cleared {
				t.Fatal("signed-in change did not clear the anonymous preference")
			}
			signedIn = sessionCookie(t, rr, sm)
			assertLocalizedPage(t, authRequest(router, http.MethodGet, "/account", nil, signedIn), tt.selected, "account.title")
		})
	}
}

func assertLocalizedPage(t *testing.T, rr *httptest.ResponseRecorder, lang, key string) {
	t.Helper()
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, `lang="`+lang+`"`) || !strings.Contains(body, i18n.T(lang, key)) {
		t.Fatalf("page does not render %s in %s (HTTP %d)", key, lang, rr.Code)
	}
}
