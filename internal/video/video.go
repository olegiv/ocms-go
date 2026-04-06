// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package video

import (
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"
)

// MaxVideoURLLength is the maximum allowed length for a video URL.
const MaxVideoURLLength = 2048

// Provider defines the interface for video hosting providers.
// Implementations must safely extract video IDs and generate embed HTML.
type Provider interface {
	// Name returns the provider name (e.g., "YouTube").
	Name() string
	// Match returns true if the URL belongs to this provider.
	Match(rawURL string) bool
	// ExtractID extracts the video ID from a URL. Returns empty string if not found.
	ExtractID(rawURL string) string
	// EmbedHTML generates safe iframe HTML for the given video ID.
	EmbedHTML(videoID string) template.HTML
}

// Registry holds registered video providers and dispatches URL parsing.
type Registry struct {
	providers []Provider
}

// NewRegistry creates a Registry with the default set of providers.
func NewRegistry() *Registry {
	return &Registry{
		providers: []Provider{
			&YouTubeProvider{},
		},
	}
}

// EmbedHTML parses the URL, identifies the provider, and returns safe embed HTML.
// Returns empty HTML for empty URLs. Returns an error for unrecognized URLs.
func (r *Registry) EmbedHTML(rawURL string) (template.HTML, error) {
	if rawURL == "" {
		return "", nil
	}

	for _, p := range r.providers {
		if p.Match(rawURL) {
			videoID := p.ExtractID(rawURL)
			if videoID == "" {
				return "", fmt.Errorf("could not extract video ID from %s URL", p.Name())
			}
			return p.EmbedHTML(videoID), nil
		}
	}

	return "", fmt.Errorf("unsupported video provider")
}

// ValidateURL checks if a video URL is recognized by a registered provider.
// Returns an error message string, or empty string if valid.
func (r *Registry) ValidateURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	if len(rawURL) > MaxVideoURLLength {
		return "Video URL is too long"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "Video URL must start with http:// or https://"
	}

	for _, p := range r.providers {
		if p.Match(rawURL) {
			if id := p.ExtractID(rawURL); id != "" {
				return ""
			}
			return fmt.Sprintf("Could not extract video ID from %s URL", p.Name())
		}
	}

	return "Unsupported video provider. Currently supported: YouTube"
}

// --- YouTube Provider ---

// youtubeIDPattern validates YouTube video IDs (11 alphanumeric chars, hyphens, underscores).
var youtubeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)

// YouTubeProvider handles YouTube video URLs.
type YouTubeProvider struct{}

// Name returns "YouTube".
func (y *YouTubeProvider) Name() string { return "YouTube" }

// Match returns true if the URL is a YouTube or youtu.be URL.
func (y *YouTubeProvider) Match(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "www.youtube.com" ||
		host == "youtube.com" ||
		host == "m.youtube.com" ||
		host == "youtu.be" ||
		host == "www.youtube-nocookie.com"
}

// ExtractID extracts the video ID from various YouTube URL formats:
//   - https://www.youtube.com/watch?v=VIDEO_ID
//   - https://youtu.be/VIDEO_ID
//   - https://www.youtube.com/embed/VIDEO_ID
//   - https://www.youtube.com/shorts/VIDEO_ID
//   - https://m.youtube.com/watch?v=VIDEO_ID
func (y *YouTubeProvider) ExtractID(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := strings.ToLower(parsed.Hostname())
	path := parsed.Path

	// youtu.be/VIDEO_ID
	if host == "youtu.be" {
		id := strings.TrimPrefix(path, "/")
		// Strip anything after the ID (e.g., query params already handled, but path might have extra segments)
		if idx := strings.Index(id, "/"); idx != -1 {
			id = id[:idx]
		}
		if youtubeIDPattern.MatchString(id) {
			return id
		}
		return ""
	}

	// youtube.com/watch?v=VIDEO_ID
	if strings.HasPrefix(path, "/watch") {
		id := parsed.Query().Get("v")
		if youtubeIDPattern.MatchString(id) {
			return id
		}
		return ""
	}

	// youtube.com/embed/VIDEO_ID or youtube.com/shorts/VIDEO_ID
	for _, prefix := range []string{"/embed/", "/shorts/"} {
		if strings.HasPrefix(path, prefix) {
			id := strings.TrimPrefix(path, prefix)
			if idx := strings.Index(id, "/"); idx != -1 {
				id = id[:idx]
			}
			// Strip query string fragments from path
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			if youtubeIDPattern.MatchString(id) {
				return id
			}
			return ""
		}
	}

	return ""
}

// EmbedHTML generates a privacy-enhanced YouTube iframe embed.
// Uses youtube-nocookie.com to avoid tracking cookies.
func (y *YouTubeProvider) EmbedHTML(videoID string) template.HTML {
	if !youtubeIDPattern.MatchString(videoID) {
		return ""
	}

	// #nosec G203 - videoID is validated against strict alphanumeric pattern
	return template.HTML(fmt.Sprintf(
		`<iframe src="https://www.youtube-nocookie.com/embed/%s" `+
			`title="YouTube video player" `+
			`frameborder="0" `+
			`allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" `+
			`referrerpolicy="strict-origin-when-cross-origin" `+
			`allowfullscreen></iframe>`,
		videoID,
	))
}
