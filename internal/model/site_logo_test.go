// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"errors"
	"testing"
)

func TestNormalizeSiteLogoURL(t *testing.T) {
	const png = "data:image/png;base64,iVBORw0KGgo="
	for _, tc := range []struct {
		name, raw, want string
		valid           bool
	}{
		{"empty", "", "", true},
		{"blank", "  ", "", true},
		{"media path", "/uploads/originals/logo.png", "/uploads/originals/logo.png", true},
		{"protocol-relative", "//cdn.example.com/logo.png", "//cdn.example.com/logo.png", true},
		{"https", "https://cdn.example.com/logo.webp", "https://cdn.example.com/logo.webp", true},
		{"uppercase scheme", "HTTPS://cdn.example.com/logo.png", "HTTPS://cdn.example.com/logo.png", true},
		{"surrounding space", "  /uploads/logo.png ", "/uploads/logo.png", true},
		{"png data URI", png, png, true},
		{"uppercase data URI", "DATA:IMAGE/PNG;BASE64,iVBORw0KGgo=", "DATA:IMAGE/PNG;BASE64,iVBORw0KGgo=", true},
		{"jpg data URI", "data:image/jpg;base64,/9j/4AAQ", "data:image/jpg;base64,/9j/4AAQ", true},
		{"svg data URI", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", true},
		{"path without slash", "uploads/logo.png", "", false},
		{"http without host", "http:/logo.png", "", false},
		{"mailto", "mailto:a@example.com", "", false},
		{"tel", "tel:+123", "", false},
		{"ftp", "ftp://example.com/logo.png", "", false},
		{"html data URI", "data:text/html;base64,PHNjcmlwdD4=", "", false},
		{"unencoded data URI", "data:image/png,raw", "", false},
		{"data URI with markup", "data:image/png;base64,AAAA\"><script>", "", false},
		{"wrapped base64", "data:image/png;base64,iVBO\nRw0K", "", false},
		{"javascript", "javascript:alert(1)", "", false},
		{"javascript after space", " javascript:alert(1)", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeSiteLogoURL(tc.raw)
			if tc.valid && (err != nil || got != tc.want) {
				t.Errorf("NormalizeSiteLogoURL(%q) = (%q, %v), want (%q, nil)", tc.raw, got, err, tc.want)
			}
			if !tc.valid && (!errors.Is(err, ErrInvalidSiteLogoURL) || got != "") {
				t.Errorf("NormalizeSiteLogoURL(%q) = (%q, %v), want ErrInvalidSiteLogoURL", tc.raw, got, err)
			}
		})
	}
}

func TestIsRenderableMenuURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"/about", true},
		{"about", true},
		{"#section:2", true},
		{"?t=10:30", true},
		{"https://example.com/", true},
		{"MAILTO:team@example.com", true},
		{"tel:+123", true},
		{"javascript:alert(1)", false},
		{"JaVaScRiPt:alert(1)", false},
		{" javascript:alert(1)", false},
		{"java\tscript:alert(1)", false},
		{"data:text/html,<script>", false},
		{"vbscript:msgbox", false},
		{"ftp://example.com/", false},
	} {
		if got := IsRenderableMenuURL(tc.raw); got != tc.want {
			t.Errorf("IsRenderableMenuURL(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
