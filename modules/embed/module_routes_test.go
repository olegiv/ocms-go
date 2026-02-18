// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestRegisterAdminRoutes_EditorForbidden(t *testing.T) {
	m := New()
	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/embed", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{
		ID:    1,
		Email: "editor@example.com",
		Role:  "editor",
		Name:  "Editor",
	})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want %d", w.Code, http.StatusForbidden)
	}
}
