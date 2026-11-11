// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"strings"
	"testing"
)

const testLogoDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg=="

// TestFrontendNavRendersSiteLogo renders the header with each logo value: an
// accepted one appears as the image source, and any other falls back to the
// site name rather than a broken image or one templ has blocked. The full
// value policy is tested in model.TestNormalizeSiteLogoURL.
func TestFrontendNavRendersSiteLogo(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string // want "" means the site name is shown
	}{
		{"empty", "", ""},
		{"media path", "/uploads/originals/logo.png", "/uploads/originals/logo.png"},
		{"https", "https://cdn.example.com/logo.webp", "https://cdn.example.com/logo.webp"},
		{"surrounding space", "  /uploads/logo.png ", "/uploads/logo.png"},
		{"png data URI", testLogoDataURI, testLogoDataURI},
		{"svg data URI", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="},
		{"uppercase data URI", "DATA:IMAGE/PNG;BASE64,iVBORw0KGgo=", "DATA:IMAGE/PNG;BASE64,iVBORw0KGgo="},
		{"mailto", "mailto:a@example.com", ""},
		{"path without slash", "uploads/logo.png", ""},
		{"html data URI", "data:text/html;base64,PHNjcmlwdD4=", ""},
		{"data URI with markup", "data:image/png;base64,AAAA\"><script>", ""},
		{"javascript", "javascript:alert(1)", ""},
	} {
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
