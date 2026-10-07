// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type feedFixture struct {
	db       *sql.DB
	handler  *FrontendHandler
	router   http.Handler
	authorID int64
}

func newFeedFixture(t *testing.T) *feedFixture {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "ru", true)
	createTestLanguage(t, db, "zh-hans", true)
	createTestLanguage(t, db, "de", false)
	f := &feedFixture{db: db, authorID: admin.ID}
	f.exec(t, `INSERT INTO config (key, value, type, language_code) VALUES
		('site_url', 'https://feeds.example/', 'string', 'en'),
		('site_name', 'News & Луна', 'string', 'en'), ('site_description', 'Public news', 'string', 'en')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	f.exec(t, `INSERT INTO config_translations (config_key, language_id, value)
		SELECT 'site_name', id, 'Новости Луны' FROM languages WHERE code = 'ru'`)
	f.exec(t, `INSERT INTO categories (name, slug, language_code) VALUES
		('Technology', 'tech', 'en'), ('Технологии', 'tech-ru', 'ru')`)
	f.exec(t, `INSERT INTO tags (name, slug, language_code) VALUES
		('Go', 'go', 'en'), ('Го', 'go-ru', 'ru')`)
	f.handler = NewFrontendHandler(db, testThemeManager(), nil, testutil.TestLoggerSilent(), nil, nil)
	root, frontend := chi.NewRouter(), chi.NewRouter()
	frontend.Use(middleware.Language(db))
	for _, format := range []struct {
		name string
		fn   http.HandlerFunc
	}{{"rss.xml", f.handler.RSSFeed}, {"atom.xml", f.handler.AtomFeed}} {
		for _, path := range []string{"/" + format.name, "/{taxonomy:category|tag}/{slug}/" + format.name} {
			frontend.Get(path, format.fn)
			frontend.Head(path, format.fn)
		}
	}
	frontend.Get("/", f.handler.Home)
	frontend.Get("/category/{slug}", f.handler.Category)
	frontend.Get("/tag/{slug}", f.handler.Tag)
	frontend.Get("/{slug}", f.handler.Page)
	frontend.NotFound(f.handler.NotFound)
	root.Mount("/", frontend)
	f.router = root
	return f
}

func (f *feedFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}

func (f *feedFixture) post(t *testing.T, slug, language string) int64 {
	t.Helper()
	result, err := f.db.Exec(`INSERT INTO pages
		(title, slug, body, summary, status, page_type, author_id, language_code, published_at, updated_at)
		VALUES (?, ?, '<p>Public body</p>', 'Saved summary', 'published', 'post', ?, ?,
		'2026-10-01 12:00:00', '2026-10-02 12:00:00')`, slug, slug, f.authorID, language)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *feedFixture) request(method, path, etag string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("If-None-Match", etag)
	// Preferences must never override the canonical language of a feed URL.
	req.Header.Set("Accept-Language", "ru")
	req.AddCookie(&http.Cookie{Name: middleware.LanguageCookieName, Value: "ru"})
	req.Host = "untrusted.example"
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

type feedTestEntry struct {
	ID        string `xml:"id"`
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Link struct {
		URL string `xml:"href,attr"`
	} `xml:"link"`
}

func decodeFeedEntries(t *testing.T, w *httptest.ResponseRecorder) []feedTestEntry {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("feed status = %d: %s", w.Code, w.Body.String())
	}
	var doc struct {
		Entries []feedTestEntry `xml:"entry"`
		Channel struct {
			Items []struct {
				ID      string `xml:"guid"`
				Title   string `xml:"title"`
				Summary string `xml:"description"`
				URL     string `xml:"link"`
				Author  string `xml:"http://purl.org/dc/elements/1.1/ creator"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid feed XML: %v\n%s", err, w.Body.String())
	}
	for _, item := range doc.Channel.Items {
		entry := feedTestEntry{ID: item.ID, Title: item.Title, Summary: item.Summary}
		entry.Author.Name, entry.Link.URL = item.Author, item.URL
		doc.Entries = append(doc.Entries, entry)
	}
	return doc.Entries
}

func TestFeedsScopesAndLanguages(t *testing.T) {
	f := newFeedFixture(t)
	en := f.post(t, "english-post", "en")
	ru := f.post(t, "russian-post", "ru")
	f.post(t, "chinese-post", "zh-hans")
	for _, post := range []struct {
		id   int64
		lang string
	}{{en, "en"}, {ru, "ru"}} {
		f.exec(t, `INSERT INTO page_categories (page_id, category_id)
			SELECT ?, id FROM categories WHERE language_code = ?`, post.id, post.lang)
		f.exec(t, `INSERT INTO page_tags (page_id, tag_id)
			SELECT ?, id FROM tags WHERE language_code = ?`, post.id, post.lang)
	}
	for _, format := range []string{"rss.xml", "atom.xml"} {
		for _, tc := range []struct{ path, title, link string }{
			{"/", "english-post", "https://feeds.example/english-post"},
			{"/en/", "english-post", "https://feeds.example/english-post"},
			{"/category/tech/", "english-post", "https://feeds.example/english-post"},
			{"/tag/go/", "english-post", "https://feeds.example/english-post"},
			{"/ru/", "russian-post", "https://feeds.example/ru/russian-post"},
			{"/ru/category/tech-ru/", "russian-post", "https://feeds.example/ru/russian-post"},
			{"/ru/tag/go-ru/", "russian-post", "https://feeds.example/ru/russian-post"},
			{"/zh-hans/", "chinese-post", "https://feeds.example/zh-hans/chinese-post"},
		} {
			t.Run(tc.path+format, func(t *testing.T) {
				w := f.request(http.MethodGet, tc.path+format+"?lang=ru", "")
				entries := decodeFeedEntries(t, w)
				if len(entries) != 1 || entries[0].Title != tc.title || entries[0].Link.URL != tc.link || entries[0].Summary != "Saved summary" {
					t.Fatalf("unexpected subscription: %+v", entries)
				}
				if entries[0].Author.Name == "" || strings.Contains(w.Body.String(), "admin@example.com") || strings.Contains(w.Body.String(), "untrusted.example") {
					t.Error("feed must expose public author names and configured-origin links only")
				}
				if strings.HasPrefix(tc.path, "/ru/") && !strings.Contains(w.Body.String(), "Новости Луны") {
					t.Error("translated site name missing")
				}
			})
		}
		for _, path := range []string{
			"/zz/", "/de/", "/category/missing/", "/tag/missing/",
			"/category/tech-ru/", "/tag/go-ru/", "/ru/category/tech/", "/ru/tag/go/",
		} {
			if w := f.request(http.MethodGet, path+format, ""); w.Code != http.StatusNotFound {
				t.Errorf("%s%s status = %d; want 404", path, format, w.Code)
			}
		}
	}
}

func TestFeedsVisibilityAndLimit(t *testing.T) {
	f := newFeedFixture(t)
	var enIDs []int64
	for i := range 25 {
		id := f.post(t, fmt.Sprintf("english-%02d", i), "en")
		enIDs = append(enIDs, id)
		f.exec(t, `INSERT INTO page_categories (page_id, category_id) SELECT ?, id FROM categories WHERE slug = 'tech'`, id)
		if i < 3 {
			f.exec(t, `INSERT INTO page_tags (page_id, tag_id) SELECT ?, id FROM tags WHERE slug = 'go'`, id)
		}
		f.post(t, fmt.Sprintf("russian-%02d", i), "ru")
	}
	for _, slug := range []string{"draft", "scheduled", "excluded", "static-page"} {
		id := f.post(t, slug, "en")
		f.exec(t, `INSERT INTO page_categories (page_id, category_id) SELECT ?, id FROM categories WHERE slug = 'tech'`, id)
		f.exec(t, `INSERT INTO page_tags (page_id, tag_id) SELECT ?, id FROM tags WHERE slug = 'go'`, id)
	}
	f.exec(t, `UPDATE pages SET status = 'draft' WHERE slug IN ('draft', 'scheduled')`)
	f.exec(t, `UPDATE pages SET scheduled_at = '2099-01-01' WHERE slug = 'scheduled'`)
	f.exec(t, `UPDATE pages SET exclude_from_lists = 1 WHERE slug = 'excluded'`)
	f.exec(t, `UPDATE pages SET page_type = 'page' WHERE slug = 'static-page'`)
	f.exec(t, `UPDATE pages SET no_index = 1 WHERE id = ?`, enIDs[24])
	for _, format := range []string{"rss.xml", "atom.xml"} {
		for _, scope := range []string{"/", "/category/tech/"} {
			entries := decodeFeedEntries(t, f.request(http.MethodGet, scope+format, ""))
			if len(entries) != 20 {
				t.Fatalf("%s%s: got %d entries, want 20", scope, format, len(entries))
			}
			for i, entry := range entries {
				if want := fmt.Sprintf("english-%02d", 24-i); entry.Title != want {
					t.Errorf("%s%s entry %d = %q; want %q", scope, format, i, entry.Title, want)
				}
			}
		}
		entries := decodeFeedEntries(t, f.request(http.MethodGet, "/tag/go/"+format, ""))
		if len(entries) != 3 || entries[0].Title != "english-02" || entries[2].Title != "english-00" {
			t.Errorf("taxonomy must filter before limiting: %+v", entries)
		}
	}
	// A newer publication date wins even when the post has a lower ID.
	f.exec(t, `UPDATE pages SET published_at = '2026-10-03 12:00:00' WHERE id = ?`, enIDs[0])
	for _, format := range []string{"rss.xml", "atom.xml"} {
		for _, scope := range []string{"/", "/category/tech/", "/tag/go/"} {
			entries := decodeFeedEntries(t, f.request(http.MethodGet, scope+format, ""))
			if entries[0].Title != "english-00" {
				t.Errorf("%s%s: publication date must precede the ID tie-breaker", scope, format)
			}
		}
	}
}

func TestFeedsEmptyAndFailures(t *testing.T) {
	for _, format := range []string{"rss.xml", "atom.xml"} {
		t.Run("empty "+format, func(t *testing.T) {
			f := newFeedFixture(t)
			for _, scope := range []string{"/", "/category/tech/", "/tag/go/"} {
				if entries := decodeFeedEntries(t, f.request(http.MethodGet, scope+format, "")); len(entries) != 0 {
					t.Errorf("empty feed contains entries: %+v", entries)
				}
			}
		})
	}
	f := newFeedFixture(t)
	for _, origin := range []string{"", "ftp://example.com", "https://example.com/path", "https://user:secret@example.com", "https://example.com:99999"} {
		f.exec(t, `UPDATE config SET value = ? WHERE key = 'site_url'`, origin)
		for _, format := range []string{"rss.xml", "atom.xml"} {
			w := f.request(http.MethodGet, "/"+format, "*")
			if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret") {
				t.Errorf("invalid origin %q: status %d, headers %v, body %s", origin, w.Code, w.Header(), w.Body.String())
			}
		}
	}
	for _, tc := range []struct{ table, path string }{
		{"pages", "/rss.xml"}, {"config", "/atom.xml"}, {"languages", "/rss.xml"},
		{"config_translations", "/rss.xml"}, {"categories", "/category/tech/rss.xml"}, {"tags", "/tag/go/atom.xml"},
	} {
		t.Run("missing "+tc.table, func(t *testing.T) {
			f := newFeedFixture(t)
			f.exec(t, "PRAGMA foreign_keys = OFF")
			f.exec(t, "DROP TABLE "+tc.table)
			w := f.request(http.MethodGet, tc.path, "*")
			if w.Code != http.StatusInternalServerError || w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("failed query must return uncached 500, got %d %v", w.Code, w.Header())
			}
		})
	}
}

func TestFeedsValidatorsAndMutations(t *testing.T) {
	for _, format := range []string{"rss.xml", "atom.xml"} {
		t.Run(format, func(t *testing.T) {
			f := newFeedFixture(t)
			id := f.post(t, "original-post", "en")
			path := "/" + format
			initial := f.request(http.MethodGet, path, "")
			originalID := decodeFeedEntries(t, initial)[0].ID
			etag := initial.Header().Get("ETag")
			if etag == "" || initial.Header().Get("Cache-Control") != "public, no-cache" {
				t.Fatal("missing revalidation headers")
			}
			for _, validator := range []string{etag, strings.TrimPrefix(etag, "W/"), `"other", ` + etag, "*"} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					w := f.request(method, path, validator)
					if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
						t.Errorf("%s %q should yield bodyless 304: %d", method, validator, w.Code)
					}
				}
			}
			head := f.request(http.MethodHead, path, "")
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(initial.Body.Len()) {
				t.Errorf("HEAD must retain GET headers without its body: %d %v", head.Code, head.Header())
			}
			f.exec(t, `UPDATE pages SET slug = 'renamed-post', title = 'Renamed', summary = 'New summary' WHERE id = ?`, id)
			edited := f.request(http.MethodGet, path, etag)
			entry := decodeFeedEntries(t, edited)[0]
			if entry.ID != originalID || entry.Link.URL != "https://feeds.example/renamed-post" || entry.Summary != "New summary" || edited.Header().Get("ETag") == etag {
				t.Errorf("edit must update XML while preserving identity: %+v", entry)
			}
			etag = edited.Header().Get("ETag")
			for _, scope := range []string{"category/tech", "tag/go"} {
				parts := strings.Split(scope, "/")
				table := "page_categories"
				entity, column := "categories", "category_id"
				if parts[0] == "tag" {
					table, entity, column = "page_tags", "tags", "tag_id"
				}
				feedPath := "/" + scope + "/" + format
				empty := f.request(http.MethodGet, feedPath, "")
				f.exec(t, fmt.Sprintf("INSERT INTO %s (page_id, %s) SELECT ?, id FROM %s WHERE slug = ?", table, column, entity), id, parts[1])
				populated := f.request(http.MethodGet, feedPath, empty.Header().Get("ETag"))
				if got := decodeFeedEntries(t, populated); len(got) != 1 {
					t.Fatal("taxonomy association must update its subscription")
				}
				f.exec(t, "DELETE FROM "+table+" WHERE page_id = ?", id)
				if got := decodeFeedEntries(t, f.request(http.MethodGet, feedPath, populated.Header().Get("ETag"))); len(got) != 0 {
					t.Fatal("taxonomy removal must update its subscription")
				}
			}
			f.exec(t, `UPDATE pages SET status = 'draft' WHERE id = ?`, id)
			unpublished := f.request(http.MethodGet, path, etag)
			if got := decodeFeedEntries(t, unpublished); len(got) != 0 {
				t.Fatal("unpublishing must remove entries immediately")
			}
			f.exec(t, `UPDATE pages SET status = 'published' WHERE id = ?`, id)
			republished := f.request(http.MethodGet, path, unpublished.Header().Get("ETag"))
			if got := decodeFeedEntries(t, republished); len(got) != 1 || got[0].ID != originalID {
				t.Fatal("republishing must restore the same entry identity")
			}
			f.exec(t, `DELETE FROM pages WHERE id = ?`, id)
			if got := decodeFeedEntries(t, f.request(http.MethodGet, path, republished.Header().Get("ETag"))); len(got) != 0 {
				t.Fatal("deleting must remove entries immediately")
			}
		})
	}
}

func TestFeedDiscoveryLayouts(t *testing.T) {
	for _, renderer := range []string{"default", "developer", "starter", "fallback"} {
		t.Run(renderer, func(t *testing.T) {
			f := newFeedFixture(t)
			if renderer != "fallback" {
				f.handler.themeManager = loadedFrontendThemeManager(t, renderer)
			}
			f.post(t, "public-post", "en")
			for _, tc := range []struct {
				path   string
				prefix string
				scope  string
			}{
				{"/en", "", ""}, {"/ru", "/ru", ""},
				{"/category/tech", "", "/category/tech"}, {"/tag/go", "", "/tag/go"},
				{"/ru/category/tech-ru", "/ru", "/category/tech-ru"}, {"/ru/tag/go-ru", "/ru", "/tag/go-ru"},
			} {
				w := f.request(http.MethodGet, tc.path, "")
				if w.Code != http.StatusOK {
					t.Fatalf("%s renders %d: %s", tc.path, w.Code, w.Body.String())
				}
				links := renderedFeedLinks(w.Body.String())
				want := []string{"https://feeds.example" + tc.prefix + "/rss.xml", "https://feeds.example" + tc.prefix + "/atom.xml"}
				if tc.scope != "" {
					want = append(want, "https://feeds.example"+tc.prefix+tc.scope+"/rss.xml", "https://feeds.example"+tc.prefix+tc.scope+"/atom.xml")
				}
				if !slices.Equal(links, want) {
					t.Errorf("%s feed links = %v, want %v", tc.path, links, want)
				}
			}
			f.exec(t, `UPDATE config SET value = '' WHERE key = 'site_url'`)
			if links := renderedFeedLinks(f.request(http.MethodGet, "/en", "").Body.String()); len(links) != 0 {
				t.Error("unconfigured feeds must not be advertised")
			}
		})
	}
}

func renderedFeedLinks(body string) []string {
	z := html.NewTokenizer(strings.NewReader(body))
	var links []string
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			return links
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		if token.Data != "link" {
			continue
		}
		attrs := make(map[string]string)
		for _, attr := range token.Attr {
			attrs[attr.Key] = attr.Val
		}
		if attrs["rel"] == "alternate" && attrs["title"] != "" &&
			(attrs["type"] == seo.RSSMediaType || attrs["type"] == seo.AtomMediaType) {
			links = append(links, attrs["href"])
		}
	}
}
