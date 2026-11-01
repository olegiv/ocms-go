// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Headers applies even to denied previews. No reporting endpoint receives a bearer URL.
func Headers(h http.Header) {
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; script-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Del("Link")
	h.Del("ETag")
	h.Del("Last-Modified")
}

// IsPath also recognizes prefixed requests before language middleware runs.
func IsPath(p string) bool {
	return strings.HasPrefix(p, "/preview/") || strings.Contains(p, "/preview/")
}

type tokenKey struct{}

// Token exists only in request context, never in request URLs seen by integrations/loggers.
func Token(ctx context.Context) string { token, _ := ctx.Value(tokenKey{}).(string); return token }

// RedactRequest runs before loggers and integrations. Routing needs only the
// share route's shape; the handler resolves the original secret from context.
func RedactRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if index := strings.Index(r.URL.Path, "/preview/share/"); index >= 0 {
			prefix := r.URL.Path[:index] + "/preview/share/"
			ctx := context.WithValue(r.Context(), tokenKey{}, strings.TrimPrefix(r.URL.Path, prefix))
			clean := r.Clone(ctx)
			u := *r.URL
			u.Path = prefix + "redacted"
			u.RawPath = ""
			u.RawQuery = ""
			clean.URL = &u
			clean.RequestURI = u.EscapedPath()
			r = clean
		}
		next.ServeHTTP(w, r)
	})
}

// Middleware precedes integrations, redirects and failures in the public stack.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsPath(r.URL.Path) {
			Headers(w.Header())
			r = r.WithContext(Context(r.Context()))
		}
		next.ServeHTTP(w, r)
	})
}

// SanitizeTheme retains local theme styling while removing executable and external resources.
// CSP is a second boundary for resources nested in CSS or trusted custom-theme markup.
func SanitizeTheme(raw []byte, banner, placeholder string) ([]byte, error) {
	if len(raw) > 8<<20 {
		return nil, errors.New("preview theme output exceeds limit")
	}
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var clean func(*html.Node)
	clean = func(parent *html.Node) {
		for n := parent.FirstChild; n != nil; {
			next := n.NextSibling
			if n.Type == html.ElementNode {
				switch n.Data {
				case "script", "base", "meta", "svg", "math", "input", "button", "select", "textarea":
					parent.RemoveChild(n)
					n = next
					continue
				case "iframe", "object", "embed", "form", "video", "audio", "source", "track":
					p := &html.Node{Type: html.ElementNode, Data: "p", Attr: []html.Attribute{{Key: "class", Val: "preview-placeholder"}}}
					p.AppendChild(&html.Node{Type: html.TextNode, Data: placeholder})
					parent.InsertBefore(p, n)
					parent.RemoveChild(n)
					n = next
					continue
				case "link":
					if !hasAttr(n, "rel", "stylesheet") || !localAsset(attr(n, "href"), false) {
						parent.RemoveChild(n)
						n = next
						continue
					}
				case "img":
					if !localAsset(attr(n, "src"), true) {
						parent.RemoveChild(n)
						n = next
						continue
					}
				}
				attrs := n.Attr[:0]
				for _, a := range n.Attr {
					key := strings.ToLower(a.Key)
					if strings.HasPrefix(key, "on") || strings.HasPrefix(key, "x-") || strings.HasPrefix(key, "@") || strings.HasPrefix(key, "hx-") || strings.HasPrefix(key, "data-hx-") || key == "srcset" || key == "srcdoc" || key == "ping" || key == "nonce" || key == "autofocus" || key == "formaction" {
						continue
					}
					if key == "rel" && n.Data == "a" {
						continue
					}
					if key == "href" && n.Data == "a" {
						u, e := url.Parse(a.Val)
						if e != nil || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
							continue
						}
					}
					attrs = append(attrs, a)
				}
				n.Attr = attrs
				if n.Data == "a" {
					n.Attr = append(n.Attr, html.Attribute{Key: "rel", Val: "noopener noreferrer nofollow"})
				}
				if n.Data == "body" {
					aside := &html.Node{Type: html.ElementNode, Data: "aside", Attr: []html.Attribute{{Key: "role", Val: "note"}, {Key: "style", Val: "padding:1rem;background:#fff3cd;color:#222;border-bottom:2px solid #856404;font:600 16px system-ui"}}}
					aside.AppendChild(&html.Node{Type: html.TextNode, Data: banner})
					n.InsertBefore(aside, n.FirstChild)
				}
			}
			clean(n)
			n = next
		}
	}
	clean(doc)
	var out bytes.Buffer
	if err = html.Render(&out, doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key, value string) bool { return strings.EqualFold(attr(n, key), value) }

// PrepareBody replaces integrations before the ordinary UGC sanitizer strips
// their markup. The preview explains the omission without loading the embed.
func PrepareBody(raw, placeholder string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return "", err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var walk func(*html.Node)
	walk = func(parent *html.Node) {
		for n := parent.FirstChild; n != nil; {
			next := n.NextSibling
			omit := false
			if n.Type == html.ElementNode {
				switch n.Data {
				case "iframe", "object", "embed", "form", "video", "audio":
					omit = true
				case "img":
					omit = !localAsset(attr(n, "src"), true)
				}
			}
			if omit {
				p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
				p.AppendChild(&html.Node{Type: html.TextNode, Data: placeholder})
				parent.InsertBefore(p, n)
				parent.RemoveChild(n)
			} else {
				walk(n)
			}
			n = next
		}
	}
	walk(root)
	var out bytes.Buffer
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if err = html.Render(&out, n); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

func localAsset(raw string, image bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || !assetQuery(u) || u.Fragment != "" || strings.ContainsAny(raw, "\\\r\n\x00") || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || path.Clean(u.Path) != u.Path {
		return false
	}
	if image {
		return strings.HasPrefix(u.Path, "/uploads/") || strings.HasPrefix(u.Path, "/themes/") || strings.HasPrefix(u.Path, "/static/")
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".css") && (strings.HasPrefix(u.Path, "/themes/") || strings.HasPrefix(u.Path, "/static/"))
}

// Theme cache-busters do not turn an asset URL into an arbitrary request.
func assetQuery(u *url.URL) bool {
	if u.RawQuery == "" {
		return true
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values) != 1 || len(values["v"]) != 1 || len(values["v"][0]) > 128 {
		return false
	}
	for _, r := range values["v"][0] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
