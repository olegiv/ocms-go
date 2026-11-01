// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
	"golang.org/x/net/html"
)

func TestPreviewHTMLAndCSPRemoveSideEffects(t *testing.T) {
	raw := `<!doctype html><html><head><link rel="stylesheet" href="/themes/default/static/css/theme.css"><link rel="stylesheet" href="https://external.test/style.css"><link rel="prefetch" href="https://external.test/track"><meta property="og:url" content="https://cms.test/preview/share/secret"><script src="/static/analytics.js"></script><style>@import url(https://external.test/font.css);</style></head><body onload="steal()"><h1>Recorded title</h1><form action="https://external.test"><input name="secret"><button>Submit</button></form><iframe src="https://external.test/embed"></iframe><svg><use href="https://external.test/pixel"></use></svg><img src="https://external.test/pixel"><img src="/uploads/fixture/image.png" srcset="https://external.test/pixel 2x" onerror="steal()"><a href="javascript:steal()">Unsafe</a><a href="https://external.test/article" ping="https://external.test/track" target="_blank" rel="opener">Reference</a><div x-init="steal()" hx-get="/private">Text</div></body></html>`
	result, err := preview.SanitizeTheme([]byte(raw), "Revision 1", "Omitted")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(string(result)))
	if err != nil {
		t.Fatal(err)
	}
	images, styles, banners := 0, 0, 0
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "meta", "iframe", "svg", "form", "input", "button":
				t.Errorf("active/private element retained: %s", n.Data)
			case "img":
				images++
			case "link":
				styles++
			case "aside":
				banners++
			}
			rels := 0
			for _, a := range n.Attr {
				if strings.HasPrefix(a.Key, "on") || strings.HasPrefix(a.Key, "hx-") || strings.HasPrefix(a.Key, "x-") || a.Key == "ping" || a.Key == "srcset" {
					t.Errorf("side-effect attribute retained: %s", a.Key)
				}
				if a.Key == "href" && strings.HasPrefix(a.Val, "javascript:") {
					t.Error("executable URL retained")
				}
				if n.Data == "a" && a.Key == "rel" {
					rels++
					if a.Val != "noopener noreferrer nofollow" {
						t.Error("opener/no-referrer policy lost")
					}
				}
			}
			if n.Data == "a" && rels != 1 {
				t.Error("anchor rel is duplicated or missing")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if images != 1 || styles != 1 || banners != 1 || !strings.Contains(string(result), "Recorded title") {
		t.Fatal("local theme/content fidelity lost")
	}
	h := make(http.Header)
	h.Set("Link", "private metadata")
	preview.Headers(h)
	for _, directive := range []string{"default-src 'none'", "script-src 'none'", "connect-src 'none'", "form-action 'none'", "frame-src 'none'", "img-src 'self' data:", "font-src 'self' data:"} {
		if !strings.Contains(h.Get("Content-Security-Policy"), directive) {
			t.Fatal("CSP no longer blocks preview side effects")
		}
	}
	if h.Get("Cache-Control") != "private, no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Robots-Tag") != "noindex, nofollow" || h.Get("Link") != "" {
		t.Fatal("preview privacy headers lost")
	}
}

func TestPreviewRedactionPrecedesApplicationObservers(t *testing.T) {
	secret := "synthetic-bearer-path-not-a-live-token"
	for _, prefix := range []string{"", "/ru"} {
		request := httptest.NewRequest("GET", "https://cms.test"+prefix+"/preview/share/"+secret+"?also=private", nil)
		h := preview.RedactRequest(preview.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.String(), secret) || strings.Contains(r.RequestURI, secret) || r.URL.RawQuery != "" {
				t.Fatal("application observer saw bearer URL")
			}
			if preview.Token(r.Context()) != secret || !preview.IsRequest(r.Context()) {
				t.Fatal("private handler context lost")
			}
			w.WriteHeader(http.StatusNotFound)
		})))
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 404 || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("denied preview lost privacy headers")
		}
	}
}

func TestPreviewAllowsOnlyLocalVersionedAssetsAndExplainsEmbeds(t *testing.T) {
	raw := `<html><head><link rel="stylesheet" href="/themes/developer/static/css/theme.css?v=1.2-a"><link rel="stylesheet" href="/themes/evil.css?next=https://elsewhere.test"><link rel="stylesheet" href="//elsewhere.test/app.css"></head><body>Text</body></html>`
	out, err := preview.SanitizeTheme([]byte(raw), "Revision 1", "Omitted")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "theme.css?v=1.2-a") || strings.Contains(string(out), "elsewhere.test") || strings.Contains(string(out), "evil.css") {
		t.Fatal("asset policy lost")
	}
	body, err := preview.PrepareBody(`<h2>Article</h2><iframe src="https://elsewhere.test/embed"></iframe><form><input name="x"></form><img src="https://elsewhere.test/pixel"><img src="/uploads/test/a.png">`, "Omitted")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(body, "Omitted") != 3 || strings.Contains(body, "elsewhere.test") || !strings.Contains(body, "/uploads/test/a.png") {
		t.Fatal("embed placeholders lost")
	}
}
