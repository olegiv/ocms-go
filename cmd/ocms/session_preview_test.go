// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
)

func TestSessionPreviewRefusesNonEditors(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	now := time.Now()
	sessions := scs.New()
	createSession := func(role string) (store.User, *http.Cookie) {
		t.Helper()
		user, err := q.CreateUser(t.Context(), store.CreateUserParams{Email: role + "@example.test", Name: role, Role: role, PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := sessions.Load(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		sessions.Put(ctx, middleware.SessionKeyUserID, user.ID)
		sessions.Put(ctx, middleware.SessionKeyUserSessionVersion, user.SessionVersion)
		sessions.Put(ctx, middleware.SessionKeyMFAVerified, true)
		value, _, err := sessions.Commit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return user, &http.Cookie{Name: sessions.Cookie.Name, Value: value}
	}
	editor, editorSession := createSession(model.RoleEditor)
	reader, readerSession := createSession(model.RolePublic)
	const marker = "Unpublished revision marker"
	page, err := pagewrite.Insert(t.Context(), q, provenance.Principal{UserID: editor.ID, Source: "human"}, store.CreatePageParams{
		Title: "Preview authorization", Slug: "preview-authorization", Body: "<p>" + marker + "</p>", Status: "draft",
		AuthorID: editor.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	logger := testutil.TestLoggerSilent()
	var emptyFS embed.FS
	frontend := handler.NewFrontendHandler(db, theme.NewManager(emptyFS, "", logger), nil, logger, nil, nil)
	router := chi.NewRouter()
	router.Use(sessions.LoadAndSave, middleware.OptionalLoadUser(sessions, db))
	var loadedUser int64
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loadedUser = middleware.GetUserID(r)
			next.ServeHTTP(w, r)
		})
	})
	registerPreviewRoutes(router, frontend)
	replace := strings.NewReplacer("{id}", strconv.FormatInt(page.ID, 10), "{revision}", strconv.FormatInt(page.Revision, 10), "{lang}", "en")
	var routes []string
	if err = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path := replace.Replace(route)
		if preview.IsPath(path) && strings.TrimSuffix(route, "/") != "/preview" && route != preview.SharePath && route != preview.ShareOpenPath {
			if method != http.MethodGet || strings.Contains(path, "{") {
				t.Errorf("session preview route needs an authorization fixture: %s %s", method, route)
				return nil
			}
			routes = append(routes, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(routes) < 2 {
		t.Fatalf("expected both session preview routes, got %v", routes)
	}
	for _, route := range routes {
		for _, tc := range []struct {
			user   store.User
			cookie *http.Cookie
			status int
		}{{reader, readerSession, http.StatusNotFound}, {editor, editorSession, http.StatusOK}} {
			r := httptest.NewRequest(http.MethodGet, route, nil)
			r.AddCookie(tc.cookie)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, r)
			if loadedUser != tc.user.ID {
				t.Fatalf("%s session did not load its current user", tc.user.Role)
			}
			if response.Code != tc.status || strings.Contains(response.Body.String(), marker) != (tc.status == http.StatusOK) {
				t.Errorf("%s session on %s returned %d, want %d with the corresponding revision visibility", tc.user.Role, route, response.Code, tc.status)
			}
		}
	}
}
