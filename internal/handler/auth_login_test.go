// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
)

func TestLoginRoleRedirect(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	const password = "Login-test-password!"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		role string
		want string
	}{
		{model.RoleAdmin, "/admin"},
		{model.RoleEditor, "/admin"},
		{model.RolePublic, "/account"},
		{"unknown", "/"},
	} {
		t.Run(tt.role, func(t *testing.T) {
			h, db, sm := newAuthTestHandler(t)
			user := createTestUser(t, db, testUser{
				Email: "login@example.com", Name: "Login user", Role: tt.role, PasswordHash: passwordHash,
			})
			form := url.Values{"email": {user.Email}, "password": {password}, "redirect": {"https://evil.example/"}}
			req := httptest.NewRequest(http.MethodPost, "/login?next=//evil.example/", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = requestWithSession(sm, req)
			rr := httptest.NewRecorder()
			h.Login(rr, req)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Errorf("login = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
			if got := sm.GetInt64(req.Context(), middleware.SessionKeyUserID); got != user.ID {
				t.Errorf("session user = %d, want %d", got, user.ID)
			}
			rr = httptest.NewRecorder()
			h.LoginForm(rr, req)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Errorf("authenticated login form = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
		})
	}
}

func newAuthTestHandler(t *testing.T) (*AuthHandler, *sql.DB, *scs.SessionManager) {
	t.Helper()
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	middleware.SetSessionManager(sm)
	t.Cleanup(func() { middleware.SetSessionManager(nil) })
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthHandler(db, renderer, sm, nil, nil), db, sm
}

func authTestRouter(h *AuthHandler, db *sql.DB, sm *scs.SessionManager) http.Handler {
	r := chi.NewRouter()
	r.Use(sm.LoadAndSave)
	r.Use(middleware.CSRF(middleware.DefaultCSRFConfig([]byte("test-secret-key-32-bytes-long!!!"), true)))
	r.Get("/login", h.LoginForm)
	r.Post("/login", h.Login)
	r.Post("/logout", h.Logout)
	r.Post("/language", h.SetLanguage)
	r.Get("/anonymous", func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), "anonymous", true)
		w.WriteHeader(http.StatusNoContent)
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(sm), middleware.LoadUser(sm, db), middleware.LoadSiteConfig(db, nil))
		r.Get("/account", h.Account)
		r.With(middleware.RequireEditor()).Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		r.With(middleware.RequireAdmin()).Get("/admin/users", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	return r
}

func authRequest(router http.Handler, method, target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder, sm *scs.SessionManager) *http.Cookie {
	t.Helper()
	response := rr.Result()
	defer func() { _ = response.Body.Close() }()
	for _, cookie := range response.Cookies() {
		if cookie.Name == sm.Cookie.Name {
			return cookie
		}
	}
	t.Fatal("response did not set a session cookie")
	return nil
}

func TestLoginSessionAndAuthorization(t *testing.T) {
	const password = "Login-test-password!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{model.RoleAdmin, model.RoleEditor, model.RolePublic, "unknown"} {
		t.Run(role, func(t *testing.T) {
			h, db, sm := newAuthTestHandler(t)
			user := createTestUser(t, db, testUser{Email: "user@example.com", Name: "Account user", Role: role, PasswordHash: hash})
			router := authTestRouter(h, db, sm)
			anonymous := sessionCookie(t, authRequest(router, http.MethodGet, "/anonymous", nil), sm)
			login := authRequest(router, http.MethodPost, "/login", url.Values{"email": {user.Email}, "password": {password}}, anonymous)
			cookie := sessionCookie(t, login, sm)
			if cookie.Value == anonymous.Value {
				t.Fatal("successful login did not rotate session token")
			}
			oldSession := authRequest(router, http.MethodGet, "/account", nil, anonymous)
			if oldSession.Header().Get("Location") != "/login" {
				t.Errorf("old session retained access: %d", oldSession.Code)
			}
			for _, target := range []string{"/account", "/admin", "/admin/users"} {
				want := http.StatusForbidden
				switch target {
				case "/account":
					if role != "unknown" {
						want = http.StatusOK
					}
				case "/admin":
					if role == model.RoleAdmin || role == model.RoleEditor {
						want = http.StatusNoContent
					}
				case "/admin/users":
					if role == model.RoleAdmin {
						want = http.StatusNoContent
					}
				}
				rr := authRequest(router, http.MethodGet, target, nil, cookie)
				if rr.Code != want {
					t.Errorf("%s = %d, want %d", target, rr.Code, want)
				}
				if target == "/account" && rr.Code == http.StatusOK {
					body := rr.Body.String()
					if !strings.Contains(body, `class="admin-layout"`) || strings.Contains(body, `href="/admin`) || strings.Contains(body, `action="/admin`) {
						t.Error("account did not use the shared shell with account-only navigation")
					}
				}
			}
			logout := authRequest(router, http.MethodPost, "/logout", nil, cookie)
			if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/login" {
				t.Errorf("logout = %d %q", logout.Code, logout.Header().Get("Location"))
			}
			if rr := authRequest(router, http.MethodGet, "/account", nil, cookie); rr.Header().Get("Location") != "/login" {
				t.Error("logged-out session retained account access")
			}
		})
	}
}

func TestLoginFailureDoesNotAuthenticate(t *testing.T) {
	h, db, sm := newAuthTestHandler(t)
	hash, err := auth.HashPassword("Correct-password!")
	if err != nil {
		t.Fatal(err)
	}
	createTestUser(t, db, testUser{Email: "user@example.com", Name: "User", Role: model.RolePublic, PasswordHash: hash})
	router := authTestRouter(h, db, sm)
	for _, email := range []string{"user@example.com", "missing@example.com", ""} {
		rr := authRequest(router, http.MethodPost, "/login", url.Values{"email": {email}, "password": {"Wrong-password!"}})
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Errorf("failed login = %d %q", rr.Code, rr.Header().Get("Location"))
		}
		cookie := sessionCookie(t, rr, sm)
		if rr := authRequest(router, http.MethodGet, "/account", nil, cookie); rr.Header().Get("Location") != "/login" {
			t.Error("failed login created an authenticated session")
		}
	}
}

func TestAccountSessionChanges(t *testing.T) {
	for _, change := range []string{"demoted", "deleted", "stale"} {
		t.Run(change, func(t *testing.T) {
			h, db, sm := newAuthTestHandler(t)
			user := createTestUser(t, db, testUser{Email: "editor@example.com", Name: "Editor", Role: model.RoleEditor})
			router := authTestRouter(h, db, sm)
			ctx, err := sm.Load(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			sm.Put(ctx, middleware.SessionKeyUserID, user.ID)
			token, _, err := sm.Commit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cookie := &http.Cookie{Name: sm.Cookie.Name, Value: token}
			var query string
			switch change {
			case "demoted":
				query = "UPDATE users SET role = 'public' WHERE id = ?"
			case "deleted":
				query = "DELETE FROM users WHERE id = ?"
			case "stale":
				query = "UPDATE users SET session_version = session_version + 1 WHERE id = ?"
			}
			if _, err := db.Exec(query, user.ID); err != nil {
				t.Fatal(err)
			}
			account := authRequest(router, http.MethodGet, "/account", nil, cookie)
			admin := authRequest(router, http.MethodGet, "/admin", nil, cookie)
			if change == "demoted" {
				login := authRequest(router, http.MethodGet, "/login", nil, cookie)
				if account.Code != http.StatusOK || admin.Code != http.StatusForbidden || login.Header().Get("Location") != "/account" {
					t.Errorf("demotion: account=%d admin=%d login=%q", account.Code, admin.Code, login.Header().Get("Location"))
				}
			} else if account.Header().Get("Location") != "/login" || admin.Header().Get("Location") != "/login" {
				t.Errorf("invalidated session: account=%d admin=%d", account.Code, admin.Code)
			}
		})
	}
}

func TestAccountOverview(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		t.Run(lang, func(t *testing.T) {
			h, db, sm := newAuthTestHandler(t)
			user := createTestUser(t, db, testUser{Email: "owner@example.com", Name: "<script>alert('name')</script>", Role: model.RolePublic})
			other := createTestUser(t, db, testUser{Email: "other@example.com", Name: "Other private user", Role: model.RoleAdmin})
			req := requestWithSession(sm, httptest.NewRequest(http.MethodGet, "/account?user_id=2", nil))
			req.Header.Set("Accept-Language", lang)
			req = addUserToContext(req, &user)
			sm.Put(req.Context(), "flash", "<img src=x onerror=alert('flash')>")
			for _, lastLogin := range []bool{false, true} {
				user.LastLoginAt = sql.NullTime{Time: time.Date(2026, time.October, 10, 9, 30, 0, 0, time.UTC), Valid: lastLogin}
				req = addUserToContext(req, &user)
				rr := httptest.NewRecorder()
				h.Account(rr, req)
				body := rr.Body.String()
				if rr.Code != http.StatusOK || !strings.Contains(body, i18n.T(lang, "account.title")) || !strings.Contains(body, `lang="`+lang+`"`) {
					t.Errorf("account language or status: %d %s", rr.Code, body)
				}
				date := i18n.T(lang, "account.no_login")
				if lastLogin {
					date = h.renderer.FormatDateTimeLocale(user.LastLoginAt.Time, lang)
				}
				for _, want := range []string{user.Email, "&lt;script&gt;", date, h.renderer.FormatDateTimeLocale(user.CreatedAt, lang), `action="/logout" method="POST"`, `content="noindex, nofollow"`} {
					if !strings.Contains(body, want) {
						t.Errorf("account missing %q", want)
					}
				}
				for _, secret := range []string{user.Name, user.PasswordHash, other.Email, other.Name, `href="/admin`, `action="/admin`, "<img src=x"} {
					if strings.Contains(body, secret) {
						t.Errorf("account leaked or failed to escape %q", secret)
					}
				}
				if !lastLogin && !strings.Contains(body, "&lt;img") {
					t.Error("success flash was not rendered and escaped")
				}
				if lastLogin && strings.Contains(body, "&lt;img") {
					t.Error("flash was displayed more than once")
				}
				if rr.Header().Get("Cache-Control") != "private, no-store" || rr.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
					t.Error("missing private account headers")
				}
			}
		})
	}
}

func TestAccountAnonymousAndLogoutCSRF(t *testing.T) {
	h, db, sm := newAuthTestHandler(t)
	router := authTestRouter(h, db, sm)
	if rr := authRequest(router, http.MethodGet, "/account", nil); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Errorf("anonymous account access = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if rr := authRequest(router, http.MethodGet, "/logout", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET logout = %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-site logout = %d", rr.Code)
	}
}

func TestAccountLanguagePreference(t *testing.T) {
	h, db, sm := newAuthTestHandler(t)
	user := createTestUser(t, db, testUser{Email: "language@example.com", Name: "Language user", Role: model.RolePublic})
	req := requestWithSession(sm, httptest.NewRequest(http.MethodGet, "/account", nil))
	req.Header.Set("Accept-Language", "en")
	req = addUserToContext(req, &user)
	sm.Put(req.Context(), middleware.SessionKeyAdminLang, "ru")
	rr := httptest.NewRecorder()
	h.Account(rr, req)
	if !strings.Contains(rr.Body.String(), `lang="ru"`) || !strings.Contains(rr.Body.String(), "Моя учётная запись") {
		t.Error("saved language preference did not override Accept-Language")
	}
}
