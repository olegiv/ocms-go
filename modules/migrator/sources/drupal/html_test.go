// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// TestRewriteAbsoluteDrupalURLs covers body HTML that references files by their
// full old-site URL rather than by path.
//
// Rewriting only the path suffix left the old origin in place, producing
// "https://old.example/uploads/originals/…" — a URL that does not exist on
// either host, so every such image stayed broken after migration.
func TestRewriteAbsoluteDrupalURLs(t *testing.T) {
	refs := NewMediaRefs()
	refs.ByPath["a.jpg"] = "/uploads/originals/uuid-1/a.jpg"

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "absolute https URL",
			body: `<img src="https://old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "absolute http URL",
			body: `<img src="http://old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "protocol-relative URL",
			body: `<img src="//old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "already relative",
			body: `<img src="/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "unknown file keeps its original URL",
			body: `<img src="https://old.example/sites/default/files/missing.jpg">`,
			want: `<img src="https://old.example/sites/default/files/missing.jpg">`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewritePrefixedURLs(tc.body, "/sites/default/files/", refs); got != tc.want {
				t.Errorf("rewritePrefixedURLs()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestMenuURISchemesMatchMenuValidator ties the Drupal menu-link resolver to the
// schemes oCMS itself accepts for a menu item.
//
// Bug state: the resolver matched only "http://" and "https://", so a Drupal
// menu containing a mailto: or tel: link hit the unsupported-URI branch and
// importMenu dropped the item — silent data loss with no destination-side
// reason for it, since internal/handler/menus.go stores both.
//
// Driving the loop from model.AllowedMenuURLSchemes rather than a list of its
// own is the point: adding a scheme to the validator without teaching the
// importer about it fails here.
func TestMenuURISchemesMatchMenuValidator(t *testing.T) {
	samples := map[string]string{
		"http":   "http://example.com/page",
		"https":  "https://example.com/page",
		"mailto": "mailto:hello@example.com",
		"tel":    "tel:+15551234567",
	}

	for _, scheme := range model.AllowedMenuURLSchemes {
		uri, ok := samples[scheme]
		if !ok {
			t.Fatalf("model.AllowedMenuURLSchemes gained %q with no sample here; "+
				"add one so the importer is actually exercised for it", scheme)
		}
		t.Run(scheme, func(t *testing.T) {
			target, err := ResolveLinkURI(uri)
			if err != nil {
				t.Fatalf("ResolveLinkURI(%q) = error %v, want it accepted: "+
					"oCMS stores this scheme, so dropping the menu item loses data", uri, err)
			}
			if target.NodeID != 0 || target.TermID != 0 {
				t.Errorf("ResolveLinkURI(%q) entity target = %+v, want none", uri, target)
			}
			if target.URL != uri {
				t.Errorf("ResolveLinkURI(%q) url = %q, want it passed through unchanged", uri, target.URL)
			}
		})
	}
}

// TestResolveLinkURIRejectsForeignSchemes keeps the widened scheme match from
// turning into "anything with a colon is an external link".
func TestResolveLinkURIRejectsForeignSchemes(t *testing.T) {
	for _, uri := range []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"route:<front>",
		"ftp://example.com/file",
		"file:///etc/passwd",
	} {
		t.Run(uri, func(t *testing.T) {
			if _, err := ResolveLinkURI(uri); err == nil {
				t.Errorf("ResolveLinkURI(%q) = nil error, want it rejected", uri)
			}
		})
	}
}

// TestFilePrefixesInHandlesMultisite covers a source whose public files are not
// under sites/default.
//
// A multisite install, or any site with a customised file_public_path, had its
// files copied successfully and then kept the source URLs in every body — so
// the media existed in oCMS and the page still pointed at the old host.
func TestFilePrefixesInHandlesMultisite(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{
			name: "stock install",
			body: `<img src="/sites/default/files/a.jpg">`,
			want: []string{"/sites/default/files/", "/system/files/"},
		},
		{
			name: "multisite by domain",
			body: `<img src="/sites/example.com/files/a.jpg">`,
			want: []string{"/sites/example.com/files/", "/system/files/"},
		},
		{
			name: "several sites in one body",
			body: `<img src="/sites/a.example/files/1.jpg"><img src="/sites/b.example/files/2.jpg">`,
			want: []string{"/sites/a.example/files/", "/sites/b.example/files/", "/system/files/"},
		},
		{
			name: "absolute URL",
			body: `<img src="https://old.example/sites/mysite/files/a.jpg">`,
			want: []string{"/sites/mysite/files/", "/system/files/"},
		},
		{
			name: "no file URLs at all",
			body: `<p>plain text</p>`,
			want: []string{"/system/files/"},
		},
		{
			name: "not a files path",
			body: `<a href="/sites/example.com/about">x</a>`,
			want: []string{"/system/files/"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := filePrefixesIn(tc.body, "")
			if len(got) != len(tc.want) {
				t.Fatalf("filePrefixesIn() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("filePrefixesIn() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestNormalizeFilePrefixAndCustomPublicPath covers a source whose
// file_public_path is not a /sites/<x>/files/ path at all.
//
// Drupal's file_public_path can be set to anything — "/assets/", say — and it
// lives in settings.php, not in the database, so nothing in the source can
// reveal it. The operator supplies it; these are the shapes they might type.
func TestNormalizeFilePrefixAndCustomPublicPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"/", ""},
		{"///", ""},
		{"assets", "/assets/"},
		{"/assets", "/assets/"},
		{"/assets/", "/assets/"},
		{"  /assets/  ", "/assets/"},
		{"files/public", "/files/public/"},
		{"https://old.example/assets/", "/assets/"},
		{"http://old.example/sites/x/files", "/sites/x/files/"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := NormalizeFilePrefix(tc.in); got != tc.want {
				t.Errorf("NormalizeFilePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// The configured prefix reaches the rewriter alongside the discovered ones.
	got := filePrefixesIn(`<img src="/assets/photo.jpg">`, "/assets/")
	want := []string{"/assets/", "/system/files/"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("filePrefixesIn() = %v, want %v", got, want)
	}
}

// TestRenderSourceBodyRespectsTextFormat covers Drupal's text formats.
//
// body_value is only HTML for some of them. A plain_text body was fed straight
// to the HTML rewriter and sanitizer, so "2 < 3" became a broken tag and a
// literal <script> example — the kind a documentation page shows on purpose —
// was deleted outright.
func TestRenderSourceBodyRespectsTextFormat(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		format   string
		want     string
		handling BodyFormatHandling
	}{
		{
			name: "plain text is escaped", body: "2 < 3 & 4 > 1", format: "plain_text",
			want: "<p>2 &lt; 3 &amp; 4 &gt; 1</p>", handling: BodyFormatEscaped,
		},
		{
			name: "plain text keeps a literal tag visible", body: "<script>alert(1)</script>", format: "plain_text",
			want: "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>", handling: BodyFormatEscaped,
		},
		{
			name: "plain text paragraphs and line breaks", body: "one\ntwo\n\nthree", format: "plain_text",
			want: "<p>one<br>two</p><p>three</p>", handling: BodyFormatEscaped,
		},
		{
			name: "basic_html passes through", body: "<p>hi</p>", format: "basic_html",
			want: "<p>hi</p>", handling: BodyFormatHTML,
		},
		{
			name: "full_html passes through", body: "<p>hi</p>", format: "full_html",
			want: "<p>hi</p>", handling: BodyFormatHTML,
		},
		{
			name: "empty format is treated as HTML", body: "<p>hi</p>", format: "",
			want: "<p>hi</p>", handling: BodyFormatHTML,
		},
		{
			name: "contrib format is reported, not mangled", body: "# Heading", format: "markdown",
			want: "# Heading", handling: BodyFormatUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, handling := RenderSourceBody(tc.body, tc.format)
			if got != tc.want {
				t.Errorf("RenderSourceBody() = %q, want %q", got, tc.want)
			}
			if handling != tc.handling {
				t.Errorf("handling = %v, want %v", handling, tc.handling)
			}
		})
	}
}
