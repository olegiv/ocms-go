// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestDemoHomepageWalkthrough(t *testing.T) {
	// Demo mode is configured once at startup.
	mode := os.Getenv("OCMS_TEST_HOME_DEMO")
	if mode == "" {
		for _, mode := range []string{"true", "false"} {
			t.Run("demo="+mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestDemoHomepageWalkthrough$")
				cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE="+mode, "OCMS_TEST_HOME_DEMO="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	const backOffice = "Try the back office"
	const articleLabel = "Read “Language Page”"
	demo := mode == "true"
	for _, renderer := range []string{"fallback", "default", "default-html", "developer", "starter"} {
		t.Run(renderer, func(t *testing.T) {
			f := newStructuredDataFixture(t, renderer)
			id := createPublishedLanguagePage(t, f.db, store.DemoArticleSlug, "en", f.authorID)
			get := func(path string) *html.Node {
				t.Helper()
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("GET %s: HTTP %d", path, w.Code)
				}
				doc, err := html.Parse(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}

			articleURL := "/" + store.DemoArticleSlug
			article := get(articleURL)
			assertDemoWalkthroughLink(t, article, "header", backOffice, "/admin/", demo)
			assertDemoWalkthroughLink(t, get("/ru/"), "header", "В панель управления", "/admin/", demo)

			for _, published := range []bool{true, false} {
				if !published {
					f.exec(t, "UPDATE pages SET status = 'draft' WHERE id = ?", id)
				}
				home := get("/")
				assertDemoWalkthroughLink(t, home, "header", backOffice, "/admin/", demo)
				assertDemoWalkthroughLink(t, home, "main", backOffice, "/admin/", demo)
				assertDemoWalkthroughLink(t, home, "main", "View on GitHub", "https://github.com/olegiv/ocms-go", demo)
				assertDemoWalkthroughLink(t, home, "main", articleLabel, articleURL, demo && published)
			}
		})
	}
}

// Scope each assertion to the first matching landmark: the hero and header
// deliberately offer the same call to action.
func assertDemoWalkthroughLink(t *testing.T, doc *html.Node, landmark, label, href string, want bool) {
	t.Helper()
	for region := range doc.Descendants() {
		if region.Type != html.ElementNode || region.Data != landmark {
			continue
		}
		found := false
		for link := range region.Descendants() {
			if link.Type != html.ElementNode || link.Data != "a" {
				continue
			}
			var labelText strings.Builder
			for child := range link.Descendants() {
				if child.Type == html.TextNode {
					labelText.WriteString(child.Data)
				}
			}
			if strings.TrimSpace(labelText.String()) == label {
				found = true
				if got := htmlAttribute(link, "href"); got != href {
					t.Errorf("%s link %q points to %q, want %q", landmark, label, got, href)
				}
			}
		}
		if found != want {
			t.Errorf("%s link %q present=%t, want %t", landmark, label, found, want)
		}
		return
	}
	t.Fatalf("page has no %s landmark", landmark)
}
