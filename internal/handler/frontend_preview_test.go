// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func postedShareRequest(t *testing.T, link string) *http.Request {
	t.Helper()
	u, err := neturl.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	body := neturl.Values{"token": {u.Fragment}}.Encode()
	u.Fragment = ""
	u.Path = preview.ShareOpenPath
	r := httptest.NewRequest(http.MethodPost, u.String(), strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestRecordedPreviewHTTPIsolationAndMissingSnapshot(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	user := createTestAdminUser(t, db)
	createDraftPage(t, db, user.ID)
	q := store.New(db)
	p, err := q.GetPageBySlug(t.Context(), "draft-page")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetPageAIAccess(t.Context(), store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: user.ID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	link, err := (preview.Service{Q: q}).Issue(t.Context(), provenance.Principal{UserID: user.ID, Source: "human"}, p.ID, p.Revision, "https://cms.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := p.Revision
	_, err = pagewrite.InTx(t.Context(), db, provenance.Principal{UserID: user.ID, Source: "human"}, func(tx *pagewrite.Tx) error {
		current, e := tx.Track(t.Context(), p.ID, &base, true)
		if e != nil {
			return e
		}
		params := pagewrite.ParamsFor(current)
		params.Title, params.Body = "Later private title", "<p>Later private body</p>"
		_, e = tx.Q.UpdatePage(t.Context(), params)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := chi.NewRouter()
	router.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware)
	router.Get(preview.SharePath, h.PreviewShareLanding)
	router.Post(preview.ShareOpenPath, h.PreviewShare)
	router.Get("/preview/pages/{id}/revisions/{revision}", h.PreviewSession)
	sessionURL := preview.SessionURL("", "", p.ID, p.Revision)
	for _, tc := range []struct {
		name, url     string
		authenticated bool
		status        int
	}{
		{"anonymous session", sessionURL, false, 404},
		{"editor session", sessionURL, true, 200},
		{"anonymous explicit share", link.URL, false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.url, nil)
			if tc.name == "anonymous explicit share" {
				r = postedShareRequest(t, tc.url)
			}
			if tc.authenticated {
				r = withUser(r, user)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("privacy headers missing")
			}
			if tc.status == 200 {
				body := w.Body.String()
				if !strings.Contains(body, "Draft content") || strings.Contains(body, "Later private") || strings.Contains(body, "/preview/share/") || strings.Contains(body, "<script") {
					t.Fatal("preview was not isolated and pinned")
				}
			}
		})
	}
	u, _ := neturl.Parse(link.URL)
	secret := u.Fragment
	u.Fragment = ""
	t.Run("resource-free landing", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, u.String(), nil))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "Draft content") || strings.Contains(w.Body.String(), "src=") {
			t.Fatal("landing disclosed content or a credential, or loaded another resource")
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") || !strings.Contains(w.Body.String(), `nonce="`) || !strings.Contains(w.Body.String(), "history.replaceState") || !strings.Contains(w.Body.String(), "requestSubmit") {
			t.Fatal("landing lost its isolated fragment-to-POST bootstrap")
		}
		if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
			t.Fatal("landing introduced a cookie or redirect")
		}
	})
	for _, tc := range []struct{ name, method, target, body, contentType string }{
		{"legacy path", "GET", preview.SharePath + "/" + secret, "", ""},
		{"encoded legacy path", "GET", "/preview/%73hare/" + secret, "", ""},
		{"encoded endpoint", "POST", "/preview/%73hare/open", "token=" + secret, "application/x-www-form-urlencoded"},
		{"query only", "POST", preview.ShareOpenPath + "?token=" + secret, "", "application/x-www-form-urlencoded"},
		{"query and body", "POST", preview.ShareOpenPath + "?token=" + secret, "token=" + secret, "application/x-www-form-urlencoded"},
		{"duplicate body", "POST", preview.ShareOpenPath, "token=" + secret + "&token=" + secret, "application/x-www-form-urlencoded"},
		{"extra body field", "POST", preview.ShareOpenPath, "token=" + secret + "&other=value", "application/x-www-form-urlencoded"},
		{"malformed body", "POST", preview.ShareOpenPath, "token=" + secret + "&bad=%", "application/x-www-form-urlencoded"},
		{"oversized body", "POST", preview.ShareOpenPath, "token=" + secret + strings.Repeat("&x=1", 100), "application/x-www-form-urlencoded"},
		{"json body", "POST", preview.ShareOpenPath, `{"token":"` + secret + `"}`, "application/json"},
		{"empty body", "POST", preview.ShareOpenPath, "", "application/x-www-form-urlencoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Draft content") || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Header().Get("Location"), secret) {
				t.Fatal("unsafe share transport was accepted or reflected")
			}
		})
	}
	h.themeManager = loadedFrontendThemeManager(t, "developer")
	h.themeManager.GetActiveTheme().Templates = template.New("broken theme")
	failed := httptest.NewRecorder()
	router.ServeHTTP(failed, postedShareRequest(t, link.URL))
	if failed.Code != 503 || strings.Contains(failed.Body.String(), "Draft content") || strings.Contains(failed.Body.String(), "Later private") {
		t.Fatal("theme failure fell back to content")
	}
	h.themeManager = testThemeManager()
	if _, err = db.Exec("DELETE FROM page_versions WHERE page_id=? AND revision=?", p.ID, p.Revision); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, postedShareRequest(t, link.URL))
	if w.Code != 404 || strings.Contains(w.Body.String(), "Later private") {
		t.Fatal("missing snapshot fell back to live content")
	}
}

type previewModuleFuncs struct{ calls *int }

func (p previewModuleFuncs) AllTemplateFuncs() template.FuncMap {
	return template.FuncMap{"privateModule": func() string { *p.calls++; return "PRIVATE MODULE" }}
}

func TestPreviewMenusAndThemeHelpersExcludeOtherDrafts(t *testing.T) {
	db, _ := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	createDraftPage(t, db, user.ID)
	publicID := createPublishedAliasPage(t, db, "public-page", "en", "public-alias", user.ID)
	privateID := createPublishedAliasPage(t, db, "private-page", "en", "private-alias", user.ID)
	if _, err := db.Exec("UPDATE pages SET status='draft' WHERE id=?", privateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO config(key,value,type) VALUES('site_url','https://cms.test','string') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		t.Fatal(err)
	}
	q := store.New(db)
	menu, err := q.CreateMenu(t.Context(), store.CreateMenuParams{Name: "Main", Slug: "main", LanguageCode: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title, url string, page, parent int64) int64 {
		t.Helper()
		item, e := q.CreateMenuItem(t.Context(), store.CreateMenuItemParams{MenuID: menu.ID, Title: title, Url: sql.NullString{String: url, Valid: true}, Target: sql.NullString{String: "_self", Valid: true}, CssClass: sql.NullString{Valid: true}, PageID: sql.NullInt64{Int64: page, Valid: page > 0}, ParentID: sql.NullInt64{Int64: parent, Valid: parent > 0}, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		if e != nil {
			t.Fatal(e)
		}
		return item.ID
	}
	parent := add("PUBLIC PARENT", "", publicID, 0)
	add("PRIVATE ID", "", privateID, parent)
	add("PRIVATE ALIAS", "/private-alias", 0, 0)
	add("PUBLIC ALIAS", "/public-alias", 0, 0)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	items := h.previewMenu(t.Context(), "main", "en")
	if len(items) != 2 || len(items[0].Children) != 0 {
		t.Fatal("private menu titles or children leaked")
	}
	for _, tc := range []struct {
		url    string
		public bool
	}{
		{"/draft-page", false}, {"/private-alias", false}, {"/en/private-page", false}, {"https://cms.test:443/private-alias", false}, {"//cms.test/private-page", false}, {"/admin/pages", false}, {"/preview/share/anything", false},
		{"/public-alias", true}, {"/en/public-page", true}, {"/page/" + strconv.FormatInt(publicID, 10), true}, {"https://elsewhere.test/article", true},
	} {
		if got := h.publicPreviewMenuURL(t.Context(), tc.url); got != tc.public {
			t.Errorf("visibility %s=%t want=%t", tc.url, got, tc.public)
		}
	}
	calls := 0
	h.SetModuleTemplateFuncsProvider(previewModuleFuncs{calls: &calls})
	funcs := h.previewTemplateFuncs(t.Context(), "en")
	tpl, err := template.New("preview").Funcs(funcs).Parse(`{{range getMenu "main"}}{{.Title}}{{range .Children}}{{.Title}}{{end}}{{end}}{{privateModule}}{{renderWidget "private"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = tpl.Execute(&output, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || strings.Contains(output.String(), "PRIVATE") || !strings.Contains(output.String(), "PUBLIC") {
		t.Fatal("theme helpers bypassed preview isolation")
	}
}
