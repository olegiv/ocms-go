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

	"github.com/olegiv/ocms-go/internal/util"
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

// namespace is the path every preview route sits under, directly or below one
// language-prefix segment (/preview/... and /{lang}/preview/...).
const namespace = "/preview/"

// routeOffset reports whether p is a preview route and where "/preview/"
// begins in it: at 0, or after a single leading language-prefix segment. Both
// middlewares run before the language middleware, so the segment is judged by
// the grammar the router gives language codes (util.IsRoutableLanguageCode:
// well-formed and not a segment an application route owns) and not by the
// active languages. It must not match "/preview/" at any other depth: a page
// served at /blog/preview/x is public content, not a preview, and treating it
// as one hides it behind no-store/noindex headers and a script-blocking CSP.
// New page slugs and aliases cannot occupy these paths. The frontend can
// still resolve an exact stored alias in the unclaimed, prefixed namespace.
func routeOffset(p string) (int, bool) {
	if strings.HasPrefix(p, namespace) {
		return 0, true
	}
	rest, ok := strings.CutPrefix(p, "/")
	if !ok {
		return 0, false
	}
	code, _, found := strings.Cut(rest, "/")
	if !found || !util.IsRoutableLanguageCode(code) {
		return 0, false
	}
	offset := 1 + len(code)
	return offset, strings.HasPrefix(p[offset:], namespace)
}

// IsPath reports whether p is one of the preview routes, which may carry a
// language prefix. See routeOffset for what it does not match.
func IsPath(p string) bool {
	_, ok := routeOffset(p)
	return ok
}

// RewritesSharePath reports whether a request's path is replaced by a
// placeholder. Non-HTTP route lookups use it to match the public router.
func RewritesSharePath(p string) bool {
	index, anchored := routeOffset(p)
	return anchored && rewritesSharePath(p, index)
}

func rewritesSharePath(p string, index int) bool {
	return p != ShareOpenPath && strings.HasPrefix(p[index:], SharePath+"/")
}

type unsafeShareURLKey struct{}
type shareAliasPathKey struct{}

// A context diagnostic must not print the original share path.
type shareAliasPath string

func (shareAliasPath) String() string   { return "[redacted]" }
func (shareAliasPath) GoString() string { return "[redacted]" }

// RedactedShareAliasPath provides the original path only for exact public alias
// lookup. A blank path with ok=true suppresses alias lookup on the scrubbed URL.
func RedactedShareAliasPath(ctx context.Context) (string, bool) {
	alias, ok := ctx.Value(shareAliasPathKey{}).(shareAliasPath)
	return string(alias), ok
}

// ShareRequestAllowed rejects encoded/legacy aliases and query-bearing POSTs
// even after redaction. GET is only a static landing: client-added tracking
// queries confer no access and are cleared before the fixed-path POST.
func ShareRequestAllowed(r *http.Request) bool {
	unsafe, _ := r.Context().Value(unsafeShareURLKey{}).(bool)
	landing := r.Method == http.MethodGet || r.Method == http.MethodHead
	return !unsafe && r.URL.RawPath == "" && r.URL.Fragment == "" &&
		((landing && r.URL.Path == SharePath) ||
			(r.Method == http.MethodPost && r.URL.Path == ShareOpenPath && r.URL.RawQuery == "" && !r.URL.ForceQuery))
}

// RedactRequest runs before loggers and integrations. Legacy URL credentials
// are discarded, never resolved or redirected into the body-only transport.
func RedactRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, anchored := routeOffset(r.URL.Path)
		if tail := r.URL.Path[index:]; anchored && (tail == SharePath || strings.HasPrefix(tail, SharePath+"/")) {
			unsafe := !ShareRequestAllowed(r)
			ctx := context.WithValue(r.Context(), unsafeShareURLKey{}, unsafe)
			u := *r.URL
			if rewritesSharePath(u.Path, index) {
				var alias shareAliasPath
				if index > 0 && u.RawPath == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
					// Only the unclaimed, prefixed namespace can hold a public alias.
					alias = shareAliasPath(strings.TrimSuffix(u.Path, "/"))
				}
				ctx = context.WithValue(ctx, shareAliasPathKey{}, alias)
				u.Path = u.Path[:index] + SharePath + "/redacted"
			}
			clean := r.Clone(ctx)
			u.RawPath = ""
			u.RawQuery = ""
			u.ForceQuery = false
			u.Fragment, u.RawFragment = "", ""
			clean.URL = &u
			clean.RequestURI = u.EscapedPath()
			clean.Header.Del("Referer")
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
