// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/internal/views/utils"
)

func newStructuredDataFixture(t *testing.T, renderer string) *feedFixture {
	t.Helper()
	f := newFeedFixture(t)
	if renderer != "fallback" {
		name := strings.TrimSuffix(renderer, "-html")
		f.handler.themeManager = loadedFrontendThemeManager(t, name)
		if renderer == "default-html" {
			f.handler.themeManager.GetActiveTheme().Config.Engine = theme.EngineHTML
		}
	}
	root, frontend := chi.NewRouter(), chi.NewRouter()
	root.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	frontend.Use(middleware.Language(f.db))
	frontend.Get("/", f.handler.Home)
	frontend.Get("/blog", f.handler.Blog)
	frontend.Get("/search", f.handler.Search)
	frontend.Get("/category/{slug}", f.handler.Category)
	frontend.Get("/tag/{slug}", f.handler.Tag)
	frontend.Get("/{slug}", f.handler.Page)
	frontend.NotFound(f.handler.NotFound)
	root.Mount("/", frontend)
	f.router = root
	return f
}

func htmlAttribute(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func walkHTML(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, visit)
	}
}

func decodeStructuredResponse(t *testing.T, w *httptest.ResponseRecorder) (map[string]json.RawMessage, *html.Node) {
	t.Helper()
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Fatalf("response status %d: %s", w.Code, w.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]json.RawMessage)
	scriptCount := 0
	walkHTML(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "script" || htmlAttribute(n, "type") != "application/ld+json" {
			return
		}
		scriptCount++
		nonce := htmlAttribute(n, "nonce")
		if nonce == "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "'nonce-"+nonce+"'") {
			t.Error("JSON-LD script nonce does not match the CSP header")
		}
		var content strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			content.WriteString(child.Data)
		}
		raw := []byte(strings.TrimSpace(content.String()))
		if !json.Valid(raw) {
			t.Fatalf("invalid rendered JSON-LD: %s", raw)
		}
		values := []json.RawMessage{raw}
		if raw[0] == '[' {
			if err := json.Unmarshal(raw, &values); err != nil {
				t.Fatal(err)
			}
		}
		for _, value := range values {
			var identity struct {
				Type string `json:"@type"`
			}
			if err := json.Unmarshal(value, &identity); err != nil {
				t.Fatal(err)
			}
			if _, exists := schemas[identity.Type]; exists {
				t.Errorf("duplicate %s schema", identity.Type)
			}
			schemas[identity.Type] = value
		}
	})
	if scriptCount > 1 {
		t.Errorf("got %d JSON-LD scripts, want at most one", scriptCount)
	}
	return schemas, doc
}

func assertRenderedBreadcrumbs(t *testing.T, doc *html.Node, schema json.RawMessage, names, urls []string) {
	t.Helper()
	var trail seo.BreadcrumbSchema
	if err := json.Unmarshal(schema, &trail); err != nil {
		t.Fatalf("breadcrumb JSON-LD: %v", err)
	}
	if len(trail.ItemList) != len(names) {
		t.Fatalf("trail = %+v, want %v", trail, names)
	}
	for i, crumb := range trail.ItemList {
		if crumb.Name != names[i] || crumb.Item != urls[i] || crumb.Position != i+1 || crumb.Type != "ListItem" {
			t.Errorf("breadcrumb %d = %+v; want %q, %q", i, crumb, names[i], urls[i])
		}
	}
	var visible []string
	var links []string
	current := 0
	navCount := 0
	walkHTML(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "nav" ||
			!slices.Contains([]string{"fe-breadcrumbs", "frontend-breadcrumbs"}, htmlAttribute(n, "class")) {
			return
		}
		navCount++
		if htmlAttribute(n, "aria-label") == "" {
			t.Error("breadcrumb navigation has no accessible label")
		}
		walkHTML(n, func(child *html.Node) {
			if child.Type != html.ElementNode {
				return
			}
			if child.Data == "a" {
				links = append(links, htmlAttribute(child, "href"))
				if child.FirstChild != nil {
					visible = append(visible, child.FirstChild.Data)
				}
			}
			if htmlAttribute(child, "aria-current") == "page" {
				current++
				if child.Data != "span" {
					t.Error("current breadcrumb must be plain text")
				}
				if child.FirstChild != nil {
					visible = append(visible, child.FirstChild.Data)
				}
			}
		})
	})
	if navCount != 1 || current != 1 || !slices.Equal(visible, names) || len(links) != len(names)-1 {
		t.Fatalf("visible trail = %v, links = %v, current = %d, navs = %d; want %v", visible, links, current, navCount, names)
	}
	for i, link := range links {
		if "https://feeds.example"+link != urls[i] {
			t.Errorf("visible link %q differs from schema destination %q", link, urls[i])
		}
	}
}

func TestFrontendStructuredDataLayouts(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, renderer := range []string{"default", "default-html", "developer", "starter", "fallback"} {
		t.Run(renderer, func(t *testing.T) {
			f := newStructuredDataFixture(t, renderer)
			f.post(t, "public-post", "en")
			pageID := f.post(t, "about", "en")
			f.exec(t, `UPDATE pages SET page_type = 'page', canonical_url = 'https://canonical.example/about' WHERE id = ?`, pageID)
			ruID := f.post(t, "luna-ru", "ru")
			const title = `Луна </script><script id="injected">alert(1)</script> & Go`
			f.exec(t, `UPDATE pages SET title = ? WHERE id = ?`, title, ruID)
			for _, homepage := range []struct{ path, name string }{{"/en", "News & Луна"}, {"/ru", "Новости Луны"}} {
				w := f.request(http.MethodGet, homepage.path, "")
				schemas, _ := decodeStructuredResponse(t, w)
				var website seo.WebSiteSchema
				if err := json.Unmarshal(schemas["WebSite"], &website); err != nil {
					t.Fatal(err)
				}
				if len(schemas) != 1 || website.Name != homepage.name || website.URL != "https://feeds.example/" || website.SearchAction != nil {
					t.Fatalf("%s website = %+v, schemas = %v", homepage.path, website, schemas)
				}
			}
			for _, tc := range []struct {
				path         string
				names, paths []string
				article      bool
			}{
				{"/public-post", []string{"Home", "Blog", "public-post"}, []string{"/", "/blog", "/public-post"}, true},
				{"/about", []string{"Home", "about"}, []string{"/", "https://canonical.example/about"}, true},
				{"/en/blog?page=2&utm_source=test", []string{"Home", "Blog"}, []string{"/", "/blog?page=2"}, false},
				{"/en/category/tech?page=2", []string{"Home", "Technology"}, []string{"/", "/category/tech?page=2"}, false},
				{"/en/tag/go", []string{"Home", "Go"}, []string{"/", "/tag/go"}, false},
				{"/ru/luna-ru", []string{"Главная", "Блог", title}, []string{"/ru", "/ru/blog", "/ru/luna-ru"}, true},
				{"/ru/blog", []string{"Главная", "Блог"}, []string{"/ru", "/ru/blog"}, false},
				{"/ru/category/tech-ru", []string{"Главная", "Технологии"}, []string{"/ru", "/ru/category/tech-ru"}, false},
				{"/ru/tag/go-ru?page=2", []string{"Главная", "Го"}, []string{"/ru", "/ru/tag/go-ru?page=2"}, false},
			} {
				t.Run(tc.path, func(t *testing.T) {
					w := f.request(http.MethodGet, tc.path, "")
					schemas, doc := decodeStructuredResponse(t, w)
					urls := make([]string, len(tc.paths))
					for i, path := range tc.paths {
						urls[i] = path
						if strings.HasPrefix(path, "/") {
							urls[i] = "https://feeds.example" + path
						}
					}
					assertRenderedBreadcrumbs(t, doc, schemas["BreadcrumbList"], tc.names, urls)
					_, hasArticle := schemas["Article"]
					if hasArticle != tc.article {
						t.Errorf("Article present = %v, want %v", hasArticle, tc.article)
					}
					if tc.article && len(schemas) != 2 {
						t.Errorf("want Article and BreadcrumbList, got %v", schemas)
					}
					if strings.Contains(w.Body.String(), `<script id="injected">`) {
						t.Error("title escaped its HTML/JSON context")
					}
				})
			}
			for _, path := range []string{"/en/search?q=Go", "/en/missing-page"} {
				schemas, _ := decodeStructuredResponse(t, f.request(http.MethodGet, path, ""))
				if len(schemas) != 0 {
					t.Errorf("%s emitted unexpected markup: %v", path, schemas)
				}
			}
			f.exec(t, `UPDATE pages SET status = 'draft' WHERE id = ?`, ruID)
			user, err := f.handler.queries.GetUserByID(t.Context(), f.authorID)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, withUser(httptest.NewRequest(http.MethodGet, "/ru/luna-ru", nil), user))
			schemas, _ := decodeStructuredResponse(t, w)
			if len(schemas) != 1 || schemas["Article"] == nil {
				t.Errorf("draft must retain only existing Article markup: %v", schemas)
			}
			for _, origin := range []string{"", "javascript:alert(1)", "https://feeds.example/subsite"} {
				f.exec(t, `UPDATE config SET value = ? WHERE key = 'site_url'`, origin)
				for _, path := range []string{"/en", "/en/blog", "/en/category/tech", "/public-post"} {
					schemas, _ := decodeStructuredResponse(t, f.request(http.MethodGet, path, ""))
					if schemas["WebSite"] != nil || schemas["BreadcrumbList"] != nil {
						t.Errorf("%s with origin %q emits new schemas", path, origin)
					}
				}
			}
		})
	}
}

func TestFrontendStylesheetCacheInvalidation(t *testing.T) {
	originalVersion := utils.ScriptVersion
	t.Cleanup(func() { utils.ScriptVersion = originalVersion })
	for _, renderer := range []string{"default", "default-html", "developer", "starter", "fallback"} {
		t.Run(renderer, func(t *testing.T) {
			f := newStructuredDataFixture(t, renderer)
			activeTheme := f.handler.themeManager.GetActiveTheme()
			stylesheet := "/static/dist/main.css"
			isHTML := activeTheme != nil && activeTheme.RenderEngine() == theme.EngineHTML
			if isHTML {
				stylesheet = "/themes/" + activeTheme.Name + "/static/css/theme.css"
			}
			previousURL := ""
			for _, release := range []struct{ themeVersion, startVersion string }{
				{"1.0.0", "1700000000"}, {"1.0.1", "1700000001"},
			} {
				utils.ScriptVersion = release.startVersion
				if activeTheme != nil {
					activeTheme.Config.Version = release.themeVersion
				}
				wantVersion := release.startVersion
				if isHTML {
					wantVersion = release.themeVersion
				}
				currentURL := ""
				for range 2 {
					w := f.request(http.MethodGet, "/en/blog", "")
					_, doc := decodeStructuredResponse(t, w)
					foundURL := ""
					walkHTML(doc, func(n *html.Node) {
						if n.Type != html.ElementNode || n.Data != "link" || htmlAttribute(n, "rel") != "stylesheet" {
							return
						}
						href := htmlAttribute(n, "href")
						u, err := url.Parse(href)
						if err == nil && u.Path == stylesheet {
							foundURL = href
							if u.Query().Get("v") != wantVersion {
								t.Errorf("stylesheet %q has version %q, want %q", href, u.Query().Get("v"), wantVersion)
							}
						}
					})
					if foundURL == "" || !strings.HasPrefix(foundURL, stylesheet+"?v=") {
						t.Fatalf("missing versioned stylesheet %q", stylesheet)
					}
					if currentURL != "" && currentURL != foundURL {
						t.Errorf("same deployment changed stylesheet URL: %q -> %q", currentURL, foundURL)
					}
					currentURL = foundURL
				}
				if previousURL != "" && currentURL == previousURL {
					t.Errorf("new deployment reused the cached stylesheet URL %q", currentURL)
				}
				t.Logf("theme=%s start=%s stylesheet=%s", release.themeVersion, release.startVersion, currentURL)
				previousURL = currentURL
			}
		})
	}
}
