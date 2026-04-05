// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package video

import (
	"strings"
	"testing"
)

func TestYouTubeProviderMatch(t *testing.T) {
	p := &YouTubeProvider{}

	matchCases := []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com/watch?v=dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ",
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"http://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}

	for _, tc := range matchCases {
		if !p.Match(tc) {
			t.Errorf("expected Match(%q) = true", tc)
		}
	}

	noMatchCases := []string{
		"https://vimeo.com/123456789",
		"https://example.com/watch?v=dQw4w9WgXcQ",
		"https://notyoutube.com/watch?v=dQw4w9WgXcQ",
		"not-a-url",
		"",
	}

	for _, tc := range noMatchCases {
		if p.Match(tc) {
			t.Errorf("expected Match(%q) = false", tc)
		}
	}
}

func TestYouTubeProviderExtractID(t *testing.T) {
	p := &YouTubeProvider{}
	expectedID := "dQw4w9WgXcQ"

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"standard watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", expectedID},
		{"watch with extra params", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s&list=PLx", expectedID},
		{"short url", "https://youtu.be/dQw4w9WgXcQ", expectedID},
		{"short url with params", "https://youtu.be/dQw4w9WgXcQ?t=42", expectedID},
		{"embed url", "https://www.youtube.com/embed/dQw4w9WgXcQ", expectedID},
		{"embed with params", "https://www.youtube.com/embed/dQw4w9WgXcQ?autoplay=1", expectedID},
		{"shorts url", "https://www.youtube.com/shorts/dQw4w9WgXcQ", expectedID},
		{"mobile watch", "https://m.youtube.com/watch?v=dQw4w9WgXcQ", expectedID},
		{"nocookie embed", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", expectedID},
		{"http scheme", "http://www.youtube.com/watch?v=dQw4w9WgXcQ", expectedID},
		{"no www", "https://youtube.com/watch?v=dQw4w9WgXcQ", expectedID},
		{"no v param", "https://www.youtube.com/watch", ""},
		{"invalid id too short", "https://www.youtube.com/watch?v=abc", ""},
		{"invalid id too long", "https://www.youtube.com/watch?v=dQw4w9WgXcQextra", ""},
		{"empty path youtu.be", "https://youtu.be/", ""},
		{"invalid url", "not-a-url://bad", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.ExtractID(tt.url)
			if got != tt.want {
				t.Errorf("ExtractID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestYouTubeProviderEmbedHTML(t *testing.T) {
	p := &YouTubeProvider{}
	html := p.EmbedHTML("dQw4w9WgXcQ")

	if !strings.Contains(string(html), "youtube-nocookie.com/embed/dQw4w9WgXcQ") {
		t.Error("embed HTML should use youtube-nocookie.com")
	}
	if !strings.Contains(string(html), "allowfullscreen") {
		t.Error("embed HTML should include allowfullscreen")
	}
	if !strings.Contains(string(html), `referrerpolicy="strict-origin-when-cross-origin"`) {
		t.Error("embed HTML should include referrer policy")
	}
	if !strings.Contains(string(html), "<iframe") {
		t.Error("embed HTML should be an iframe")
	}
}

func TestYouTubeProviderEmbedHTMLInvalidID(t *testing.T) {
	p := &YouTubeProvider{}
	html := p.EmbedHTML("invalid<script>")
	if html != "" {
		t.Error("embed HTML should be empty for invalid video ID")
	}
}

func TestRegistryEmbedHTML(t *testing.T) {
	r := NewRegistry()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		want    string
	}{
		{"valid youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", false, "youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"empty url", "", false, ""},
		{"unsupported provider", "https://vimeo.com/123456789", true, ""},
		{"youtube bad id", "https://www.youtube.com/watch?v=bad", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.EmbedHTML(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("EmbedHTML(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if tt.want != "" && !strings.Contains(string(got), tt.want) {
				t.Errorf("EmbedHTML(%q) = %q, want containing %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestRegistryValidateURL(t *testing.T) {
	r := NewRegistry()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty url", "", false},
		{"valid youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", false},
		{"valid youtu.be", "https://youtu.be/dQw4w9WgXcQ", false},
		{"valid shorts", "https://www.youtube.com/shorts/dQw4w9WgXcQ", false},
		{"unsupported provider", "https://vimeo.com/123456789", true},
		{"not a url", "not-a-url", true},
		{"ftp scheme", "ftp://youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"bad youtube id", "https://www.youtube.com/watch?v=bad", true},
		{"too long url", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&" + strings.Repeat("x", MaxVideoURLLength), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ValidateURL(tt.url)
			if (got != "") != tt.wantErr {
				t.Errorf("ValidateURL(%q) = %q, wantErr %v", tt.url, got, tt.wantErr)
			}
		})
	}
}

func TestYouTubeEmbedHTMLNoXSS(t *testing.T) {
	p := &YouTubeProvider{}

	// Attempt XSS via video ID - should be rejected by pattern
	maliciousIDs := []string{
		`"><script>alert(1)</script>`,
		`' onload='alert(1)`,
		`../../../etc/passwd`,
		`dQw4w9WgXcQ" onclick="alert(1)`,
	}

	for _, id := range maliciousIDs {
		html := p.EmbedHTML(id)
		if html != "" {
			t.Errorf("EmbedHTML(%q) should return empty for malicious ID, got %q", id, html)
		}
	}
}
