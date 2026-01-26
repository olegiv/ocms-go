// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestGetUser(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		user := GetUser(req)
		if user != nil {
			t.Errorf("GetUser() = %v, want nil", user)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{
			ID:    123,
			Email: "test@example.com",
			Role:  "admin",
			Name:  "Test User",
		}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		user := GetUser(req)
		if user == nil {
			t.Fatal("GetUser() = nil, want user")
		}
		if user.ID != 123 {
			t.Errorf("GetUser().ID = %d, want 123", user.ID)
		}
		if user.Email != "test@example.com" {
			t.Errorf("GetUser().Email = %q, want %q", user.Email, "test@example.com")
		}
	})
}

func TestGetUserID(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		id := GetUserID(req)
		if id != 0 {
			t.Errorf("GetUserID() = %d, want 0", id)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{ID: 456}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		id := GetUserID(req)
		if id != 456 {
			t.Errorf("GetUserID() = %d, want 456", id)
		}
	})
}

func TestGetUserIDPtr(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		idPtr := GetUserIDPtr(req)
		if idPtr != nil {
			t.Errorf("GetUserIDPtr() = %v, want nil", idPtr)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{ID: 789}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		idPtr := GetUserIDPtr(req)
		if idPtr == nil {
			t.Fatal("GetUserIDPtr() = nil, want pointer")
		}
		if *idPtr != 789 {
			t.Errorf("*GetUserIDPtr() = %d, want 789", *idPtr)
		}
	})
}

func TestGetUserEmail(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		email := GetUserEmail(req)
		if email != "" {
			t.Errorf("GetUserEmail() = %q, want empty", email)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{Email: "user@example.com"}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		email := GetUserEmail(req)
		if email != "user@example.com" {
			t.Errorf("GetUserEmail() = %q, want %q", email, "user@example.com")
		}
	})
}

func TestGetSiteName(t *testing.T) {
	t.Run("no site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		name := GetSiteName(req)
		if name != "oCMS" {
			t.Errorf("GetSiteName() = %q, want %q", name, "oCMS")
		}
	})

	t.Run("empty site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), ContextKeySiteName, "")
		req = req.WithContext(ctx)

		name := GetSiteName(req)
		if name != "oCMS" {
			t.Errorf("GetSiteName() = %q, want %q (default)", name, "oCMS")
		}
	})

	t.Run("site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), ContextKeySiteName, "My Site")
		req = req.WithContext(ctx)

		name := GetSiteName(req)
		if name != "My Site" {
			t.Errorf("GetSiteName() = %q, want %q", name, "My Site")
		}
	})
}

func TestRequestPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := GetRequestPath(r.Context())
		_, _ = w.Write([]byte(path))
	})

	wrapped := RequestPath(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/pages", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if body := rr.Body.String(); body != "/admin/pages" {
		t.Errorf("GetRequestPath() = %q, want %q", body, "/admin/pages")
	}
}

func TestGetRequestPath(t *testing.T) {
	t.Run("no path in context", func(t *testing.T) {
		ctx := context.Background()
		path := GetRequestPath(ctx)
		if path != "" {
			t.Errorf("GetRequestPath() = %q, want empty", path)
		}
	})

	t.Run("path in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyRequestPath, "/test/path")
		path := GetRequestPath(ctx)
		if path != "/test/path" {
			t.Errorf("GetRequestPath() = %q, want %q", path, "/test/path")
		}
	})
}
