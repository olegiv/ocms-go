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
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSharedPreviewShowsOnlyTheIssuersAuthorBox keeps another user's profile
// out of an anonymous share. The link pins an old revision whose recorded
// author_id can be someone else than the user who issued the link (authorship
// was reassigned since); the page shown to anyone holding the link then
// carried that user's name, avatar, bio and links (security audit 2026-11-03,
// C-8). Signed-in editor previews keep showing the recorded author.
//
// Bug state: pass true instead of snapshot.Page.AuthorID == issuer in
// FrontendHandler.PreviewShare and the reassigned share shows the profile.
func TestSharedPreviewShowsOnlyTheIssuersAuthorBox(t *testing.T) {
	const profileName, profileBio, profileSite = "Recorded Author Zed", "Distinctive biography of Zed", "https://zed.example.test/about"
	for _, tc := range []struct {
		name string
		// reassigned: revision 1 was recorded under the profile owner, then the
		// page moved to another editor, who issues the link.
		reassigned       bool
		shareShowsAuthor bool
		htmlTheme        string // an HTML theme to render with; "" renders the built-in templ layout
	}{
		{"recorded author issued the link", false, true, ""},
		{"authorship was reassigned before the link was issued", true, false, ""},
		{"recorded author issued the link, html theme", false, true, "developer"},
		{"authorship was reassigned before the link was issued, html theme", true, false, "developer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()
			owner := createTestUser(t, db, testUser{Email: "owner@example.test", Name: profileName, Role: "editor"})
			other := createTestUser(t, db, testUser{Email: "other@example.test", Name: "Issuer Alice", Role: "editor"})
			if _, err := db.Exec("UPDATE users SET bio=?, website_url=? WHERE id=?", profileBio, profileSite, owner.ID); err != nil {
				t.Fatal(err)
			}
			issuer := owner
			if tc.reassigned {
				issuer = other
			}
			createDraftPage(t, db, owner.ID)
			q := store.New(db)
			page, err := q.GetPageBySlug(t.Context(), "draft-page")
			if err != nil {
				t.Fatal(err)
			}
			if err = q.SetPageAIAccess(t.Context(), store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: owner.ID, SetAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if tc.reassigned {
				// Moves the page without a new revision: revision 1 still records the owner.
				if _, err = db.Exec("UPDATE pages SET author_id=? WHERE id=?", issuer.ID, page.ID); err != nil {
					t.Fatal(err)
				}
			}
			link, err := (preview.Service{Q: q}).Issue(t.Context(), provenance.Principal{UserID: issuer.ID, Source: "human"}, page.ID, page.Revision, "https://cms.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			// The built-in layout shows the name and bio; an HTML theme adds the links.
			manager, fields := testThemeManager(), []string{profileName, profileBio}
			if tc.htmlTheme != "" {
				manager, fields = loadedFrontendThemeManager(t, tc.htmlTheme), []string{profileName, profileBio, profileSite}
			}
			h := NewFrontendHandler(db, manager, nil, slog.Default(), nil, nil)
			router := chi.NewRouter()
			router.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware)
			router.Post(preview.ShareOpenPath, h.PreviewShare)
			router.Get("/preview/pages/{id}/revisions/{revision}", h.PreviewSession)

			shared := httptest.NewRecorder()
			router.ServeHTTP(shared, postedShareRequest(t, link.URL.Reveal()))
			session := httptest.NewRecorder()
			router.ServeHTTP(session, withUser(httptest.NewRequest(http.MethodGet, preview.SessionURL("", "", page.ID, page.Revision), nil), issuer))
			shownIn := func(response *httptest.ResponseRecorder) bool {
				t.Helper()
				body := response.Body.String()
				if response.Code != http.StatusOK || !strings.Contains(body, "Draft content") {
					t.Fatalf("status=%d, want the recorded draft", response.Code)
				}
				shown := 0
				for _, field := range fields {
					if strings.Contains(body, field) {
						shown++
					}
				}
				if shown != 0 && shown != len(fields) {
					t.Fatalf("the author box is half rendered (%d of %d profile fields)", shown, len(fields))
				}
				return shown == len(fields)
			}
			if got := shownIn(shared); got != tc.shareShowsAuthor {
				t.Errorf("anonymous share shows the recorded author's profile = %v, want %v", got, tc.shareShowsAuthor)
			}
			// A signed-in editor keeps the recorded author's box in both cases.
			if !shownIn(session) {
				t.Error("signed-in session preview lost the recorded author's box")
			}
		})
	}
}

// TestOnlySessionPreviewsPassAuthorBoxTrue fails when a call to
// renderCapturedPreview passes a constant true from a handler that does not
// require a signed-in user. The recorded author's profile may appear for
// editors; an anonymous reader gets it only when the recorded author is the
// issuer (security audit 2026-11-03, C-8).
//
// Bug state: change the argument in PreviewShare to true and this names it.
func TestOnlySessionPreviewsPassAuthorBoxTrue(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "frontend_preview.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		requiresUser, constantTrue := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "GetUser":
				requiresUser = true
			case "renderCapturedPreview":
				calls++
				if last, ok := call.Args[len(call.Args)-1].(*ast.Ident); ok && last.Name == "true" {
					constantTrue = true
				}
			}
			return true
		})
		if constantTrue && !requiresUser {
			t.Errorf("%s passes showAuthor=true to renderCapturedPreview without requiring a signed-in user", fn.Name.Name)
		}
	}
	if calls < 2 {
		t.Fatalf("found %d renderCapturedPreview calls, want the session and the share render; the scan no longer sees them", calls)
	}
}
