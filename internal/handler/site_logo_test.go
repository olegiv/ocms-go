// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"strings"
	"testing"
)

const testLogoDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg=="

// siteLogoCases lists configured logo values and the URL the frontend layout
// should render for each, "" meaning the site name is shown instead.
var siteLogoCases = []struct {
	name, raw, want string
}{
	{"empty", "", ""},
	{"media path", "/uploads/originals/logo.png", "/uploads/originals/logo.png"},
	{"https", "https://cdn.example.com/logo.webp", "https://cdn.example.com/logo.webp"},
	{"surrounding space", "  /uploads/logo.png ", "/uploads/logo.png"},
	{"png data URI", testLogoDataURI, testLogoDataURI},
	{"jpeg data URI", "data:image/jpeg;base64,/9j/4AAQ", "data:image/jpeg;base64,/9j/4AAQ"},
	{"svg data URI", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", ""},
	{"html data URI", "data:text/html;base64,PHNjcmlwdD4=", ""},
	{"unencoded data URI", "data:image/png,raw", ""},
	{"data URI with markup", "data:image/png;base64,AAAA\"><script>", ""},
	{"javascript", "javascript:alert(1)", ""},
}

func TestSiteLogoURL(t *testing.T) {
	for _, tc := range siteLogoCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(siteLogoURL(tc.raw)); got != tc.want {
				t.Errorf("siteLogoURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestFrontendNavRendersSiteLogo renders the header with each logo value: an
// accepted one appears as the image source, and any other falls back to the
// site name rather than an image templ has blocked.
func TestFrontendNavRendersSiteLogo(t *testing.T) {
	for _, tc := range siteLogoCases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			base := BaseTemplateData{SiteName: "Example Site", HomeURL: "/", SiteLogo: tc.raw}
			if err := frontendNav(base).Render(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			if strings.Contains(html, "TemplFailedSanitizationURL") {
				t.Fatalf("header renders a blocked logo URL:\n%s", html)
			}
			hasImage := strings.Contains(html, `class="fe-logo-img`)
			if tc.want == "" {
				if hasImage || !strings.Contains(html, "Example Site") {
					t.Errorf("logo %q: want the site name and no image, got:\n%s", tc.raw, html)
				}
				return
			}
			if !strings.Contains(html, `<img src="`+tc.want+`"`) {
				t.Errorf("logo %q: want <img src=%q>, got:\n%s", tc.raw, tc.want, html)
			}
		})
	}
}
