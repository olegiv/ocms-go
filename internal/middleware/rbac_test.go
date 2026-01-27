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

func TestRoleLevel(t *testing.T) {
	tests := []struct {
		role     string
		expected int
	}{
		{"admin", 2},
		{"editor", 1},
		{"public", 0},  // Public users have no admin access
		{"unknown", 0}, // Unknown roles have no admin access
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := roleLevel(tt.role)
			if got != tt.expected {
				t.Errorf("roleLevel(%q) = %d, want %d", tt.role, got, tt.expected)
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	// Create a test handler that returns 200 OK
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name           string
		minRole        string
		userRole       string
		expectRedirect bool
		expectForbid   bool
	}{
		// Admin tests
		{"admin can access admin route", "admin", "admin", false, false},
		{"editor cannot access admin route", "admin", "editor", false, true},
		{"public cannot access admin route", "admin", "public", false, true},
		{"unknown role cannot access admin route", "admin", "unknown", false, true},

		// Editor tests
		{"admin can access editor route", "editor", "admin", false, false},
		{"editor can access editor route", "editor", "editor", false, false},
		{"public cannot access editor route", "editor", "public", false, true},

		// No user (should redirect to login)
		{"no user redirects to login", "editor", "", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create middleware
			mw := RequireRole(tt.minRole)

			// Create request
			req := httptest.NewRequest("GET", "/admin/test", nil)

			// Add user to context if role is set
			if tt.userRole != "" {
				user := store.User{ID: 1, Role: tt.userRole}
				ctx := req.Context()
				req = req.WithContext(context.WithValue(ctx, ContextKeyUser, user))
			}

			// Create response recorder
			rr := httptest.NewRecorder()

			// Execute middleware
			mw(handler).ServeHTTP(rr, req)

			// Check expectations
			switch {
			case tt.expectRedirect:
				if rr.Code != http.StatusSeeOther {
					t.Errorf("expected redirect (303), got %d", rr.Code)
				}
				location := rr.Header().Get("Location")
				if location != "/login" {
					t.Errorf("expected redirect to /login, got %s", location)
				}
			case tt.expectForbid:
				if rr.Code != http.StatusForbidden {
					t.Errorf("expected forbidden (403), got %d", rr.Code)
				}
			default:
				if rr.Code != http.StatusOK {
					t.Errorf("expected OK (200), got %d", rr.Code)
				}
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireAdmin()

	// Admin should pass
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: "admin"}))
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("RequireAdmin: admin should pass, got %d", rr.Code)
	}

	// Editor should be forbidden
	req = httptest.NewRequest("GET", "/admin/users", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 2, Role: "editor"}))
	rr = httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("RequireAdmin: editor should be forbidden, got %d", rr.Code)
	}
}

func TestRequireEditor(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireEditor()

	// Admin should pass
	req := httptest.NewRequest("GET", "/admin/pages", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: "admin"}))
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("RequireEditor: admin should pass, got %d", rr.Code)
	}

	// Editor should pass
	req = httptest.NewRequest("GET", "/admin/pages", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 2, Role: "editor"}))
	rr = httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("RequireEditor: editor should pass, got %d", rr.Code)
	}

	// Public user should be forbidden
	req = httptest.NewRequest("GET", "/admin/pages", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 3, Role: "public"}))
	rr = httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("RequireEditor: public user should be forbidden, got %d", rr.Code)
	}
}

func TestRequireRole_ForbiddenMessage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireAdmin()
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: "editor"}))
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	// Check response body contains meaningful error message
	body := rr.Body.String()
	if body == "" {
		t.Error("expected non-empty error message in response body")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", rr.Code)
	}
}

func TestRequireAdmin_AllRoles(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireAdmin()

	tests := []struct {
		name       string
		role       string
		expectCode int
	}{
		{"admin allowed", "admin", http.StatusOK},
		{"editor forbidden", "editor", http.StatusForbidden},
		{"public forbidden", "public", http.StatusForbidden},
		{"empty role forbidden", "", http.StatusForbidden},
		{"unknown role forbidden", "superuser", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/users", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: tt.role}))
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			if rr.Code != tt.expectCode {
				t.Errorf("role %q: got %d, want %d", tt.role, rr.Code, tt.expectCode)
			}
		})
	}
}

func TestRequireEditor_AllRoles(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireEditor()

	tests := []struct {
		name       string
		role       string
		expectCode int
	}{
		{"admin allowed", "admin", http.StatusOK},
		{"editor allowed", "editor", http.StatusOK},
		{"public forbidden", "public", http.StatusForbidden},
		{"empty role forbidden", "", http.StatusForbidden},
		{"unknown role forbidden", "guest", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/pages", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: tt.role}))
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			if rr.Code != tt.expectCode {
				t.Errorf("role %q: got %d, want %d", tt.role, rr.Code, tt.expectCode)
			}
		})
	}
}

func TestRequireRole_NoUserInContext(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name    string
		minRole string
	}{
		{"admin route", "admin"},
		{"editor route", "editor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := RequireRole(tt.minRole)
			req := httptest.NewRequest("GET", "/admin/test", nil)
			// No user in context
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Errorf("expected redirect (303), got %d", rr.Code)
			}
			location := rr.Header().Get("Location")
			if location != "/login" {
				t.Errorf("expected redirect to /login, got %s", location)
			}
		})
	}
}

func TestRequireRole_CaseSensitivity(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireAdmin()

	// Role names should be case-sensitive
	tests := []struct {
		role       string
		expectCode int
	}{
		{"admin", http.StatusOK},
		{"Admin", http.StatusForbidden},
		{"ADMIN", http.StatusForbidden},
		{"aDmIn", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/users", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: tt.role}))
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			if rr.Code != tt.expectCode {
				t.Errorf("role %q: got %d, want %d", tt.role, rr.Code, tt.expectCode)
			}
		})
	}
}

func TestRequireRole_DifferentHTTPMethods(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RequireEditor()
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		t.Run(method+"_editor", func(t *testing.T) {
			req := httptest.NewRequest(method, "/admin/pages", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: "editor"}))
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("method %s with editor: got %d, want %d", method, rr.Code, http.StatusOK)
			}
		})

		t.Run(method+"_public", func(t *testing.T) {
			req := httptest.NewRequest(method, "/admin/pages", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, store.User{ID: 1, Role: "public"}))
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("method %s with public: got %d, want %d", method, rr.Code, http.StatusForbidden)
			}
		})
	}
}

func TestRoleLevel_Hierarchy(t *testing.T) {
	// Admin should have higher level than editor
	if roleLevel("admin") <= roleLevel("editor") {
		t.Error("admin should have higher level than editor")
	}

	// Editor should have higher level than public
	if roleLevel("editor") <= roleLevel("public") {
		t.Error("editor should have higher level than public")
	}

	// Public should have same level as unknown roles
	if roleLevel("public") != roleLevel("unknown") {
		t.Error("public should have same level as unknown roles")
	}

	// All unknown roles should have level 0
	unknownRoles := []string{"guest", "viewer", "moderator", "superadmin", ""}
	for _, role := range unknownRoles {
		if roleLevel(role) != 0 {
			t.Errorf("unknown role %q should have level 0, got %d", role, roleLevel(role))
		}
	}
}
