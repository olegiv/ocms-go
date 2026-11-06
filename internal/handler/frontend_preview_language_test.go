// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestPreviewLanguageFailuresAreDistinguished(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	user := createTestAdminUser(t, db)
	createDraftPage(t, db, user.ID)
	q := store.New(db)
	page, err := q.GetPageBySlug(t.Context(), "draft-page")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetPageAIAccess(t.Context(), store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: user.ID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	link, err := (preview.Service{Q: q}).Issue(t.Context(), provenance.Principal{UserID: user.ID, Source: "human"}, page.ID, page.Revision, "https://cms.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(string(link.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := chi.NewRouter()
	router.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware)
	router.Get("/preview/pages/{id}/revisions/{revision}", h.PreviewSession)
	router.Get("/{lang}/preview/pages/{id}/revisions/{revision}", h.PreviewSession)
	router.Post(preview.ShareOpenPath, h.PreviewShare)
	open := func(mode, locale string) *httptest.ResponseRecorder {
		t.Helper()
		var request *http.Request
		if mode == "share" {
			request = postedShareRequest(t, string(link.URL))
		} else {
			prefix := ""
			if mode != "session" {
				prefix = mode
			}
			request = withUser(httptest.NewRequest(http.MethodGet, preview.SessionURL("", prefix, page.ID, page.Revision), nil), user)
		}
		request.Header.Set("Accept-Language", locale)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" ||
			!strings.Contains(response.Header().Get("X-Robots-Tag"), "noindex") {
			t.Error("preview lost privacy headers")
		}
		return response
	}
	for _, mode := range []string{"session", "en", "share"} {
		for _, locale := range []string{"en", "ru"} {
			for _, fault := range []string{"storage", "missing", "inactive"} {
				t.Run(mode+"/"+locale+"/"+fault, func(t *testing.T) {
					h.queries = store.New(&conflictQueryFault{DB: db, query: "-- name: GetLanguageByCode :", missing: fault == "missing"})
					if fault == "inactive" {
						h.queries = q
						if _, err = db.Exec("UPDATE languages SET is_active=0 WHERE code=?", page.LanguageCode); err != nil {
							t.Fatal(err)
						}
					}
					restore := func() {
						h.queries = q
						if fault == "inactive" {
							if _, restoreErr := db.Exec("UPDATE languages SET is_active=1 WHERE code=?", page.LanguageCode); restoreErr != nil {
								t.Fatal(restoreErr)
							}
						}
					}
					t.Cleanup(restore)
					logs := captureLogs(t)
					response := open(mode, locale)
					if fault == "storage" {
						message := i18n.T(locale, "preview.temporarily_unavailable")
						if message == "preview.temporarily_unavailable" || response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != message {
							t.Errorf("language lookup fault: status=%d body=%q, want translated 503", response.Code, response.Body.String())
						}
						for _, field := range []string{"level=ERROR", "preview unavailable", "error=", "unavailable_conflict_data", "path="} {
							if !strings.Contains(logs.String(), field) {
								t.Errorf("language fault log lacks %q: %s", field, logs.String())
							}
						}
						if strings.Count(logs.String(), "preview unavailable") != 1 {
							t.Errorf("expected one preview diagnostic: %s", logs.String())
						}
					} else if response.Code != http.StatusNotFound || strings.Contains(logs.String(), "level=ERROR") {
						t.Errorf("unavailable language: status=%d logs=%q, want quiet 404", response.Code, logs.String())
					}
					if strings.Contains(response.Body.String(), "unavailable_conflict_data") || response.Header().Get("Location") != "" {
						t.Error("language failure exposed an internal cause or redirected")
					}
					for _, private := range []string{parsed.Fragment, "Draft content", page.Title} {
						if strings.Contains(response.Body.String()+logs.String(), private) {
							t.Error("language failure exposed a share token or recorded content")
						}
					}
					restore()
					logs.Reset()
					response = open(mode, locale)
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Draft content") || strings.Contains(logs.String(), "level=ERROR") {
						t.Errorf("preview after repair: status=%d logs=%q", response.Code, logs.String())
					}
					if strings.Contains(response.Body.String()+logs.String(), parsed.Fragment) {
						t.Error("recovered preview exposed its share token")
					}
				})
			}
		}
	}
	t.Run("mismatched route language", func(t *testing.T) {
		logs := captureLogs(t)
		response := open("ru", "en")
		if response.Code != http.StatusNotFound || strings.Contains(logs.String(), "level=ERROR") || strings.Contains(response.Body.String(), "Draft content") {
			t.Errorf("mismatched route language: status=%d logs=%q", response.Code, logs.String())
		}
	})
}

// Bare non-nil error branches must not collapse lookup faults into 404.
// Missing-row guards remain valid; previewFailure already classifies them.
func TestPreviewLookupFaultsDoNotBecomeNotFound(t *testing.T) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, "frontend_preview.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for node := range ast.Preorder(parsed) {
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			continue
		}
		condition, ok := branch.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.NEQ {
			continue
		}
		left, leftOK := condition.X.(*ast.Ident)
		right, rightOK := condition.Y.(*ast.Ident)
		if !leftOK || !rightOK {
			continue
		}
		if left.Name == "nil" {
			left, right = right, left
		}
		if right.Name != "nil" || (left.Name != "err" && !strings.HasSuffix(left.Name, "Err")) {
			continue
		}
		checked++
		for _, statement := range branch.Body.List {
			expression, ok := statement.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := expression.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			selected, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selected.Sel.Name != "NotFound" {
				continue
			}
			if pkg, ok := selected.X.(*ast.Ident); ok && pkg.Name == "http" {
				t.Errorf("%s: non-nil %s must use previewFailure or distinguish missing rows before returning 404", positions.Position(call.Pos()), left.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no preview error branches found; update the fault coverage")
	}
}
