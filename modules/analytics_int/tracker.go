// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.status = http.StatusOK
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}

// TrackingMiddleware returns middleware that tracks page views.
func (m *Module) TrackingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Debug: log every request that enters the middleware
			if m.ctx != nil && m.ctx.Logger != nil {
				m.ctx.Logger.Debug("analytics middleware: request received", "path", r.URL.Path, "method", r.Method)
			}

			// Skip if tracking is disabled
			if m.settings == nil || !m.settings.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip non-trackable requests
			if !m.shouldTrack(r) {
				m.ctx.Logger.Debug("analytics: skipping non-trackable request", "path", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}

			// Wrap response writer to capture status
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// Serve the request
			next.ServeHTTP(rw, r)

			// Track only successful HTML responses
			if rw.status == http.StatusOK {
				m.ctx.Logger.Debug("analytics: tracking page view", "path", r.URL.Path)
				// Track asynchronously to not block response
				go m.trackPageView(r)
			} else {
				m.ctx.Logger.Debug("analytics: skipping non-200 response", "path", r.URL.Path, "status", rw.status)
			}
		})
	}
}

// shouldTrack determines if a request should be tracked.
func (m *Module) shouldTrack(r *http.Request) bool {
	// Only track GET requests
	if r.Method != http.MethodGet {
		return false
	}

	path := r.URL.Path

	// Skip static assets
	staticPrefixes := []string{
		"/static/",
		"/assets/",
		"/media/",
		"/uploads/",
		"/favicon.",
		"/robots.txt",
		"/sitemap",
		"/.well-known/",
	}
	for _, prefix := range staticPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}

	// Skip file extensions commonly used for assets
	staticExtensions := []string{
		".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".eot", ".otf",
		".xml", ".json", ".txt", ".pdf",
		".mp3", ".mp4", ".webm", ".ogg", ".wav",
		".zip", ".tar", ".gz", ".rar",
	}
	pathLower := strings.ToLower(path)
	for _, ext := range staticExtensions {
		if strings.HasSuffix(pathLower, ext) {
			return false
		}
	}

	// Skip admin and API routes
	adminAPIPrefixes := []string{
		"/admin",
		"/api/",
		"/health",
	}
	for _, prefix := range adminAPIPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}

	// Skip user-configured excluded paths
	for _, excludePath := range m.settings.ExcludePaths {
		if strings.HasPrefix(path, excludePath) {
			return false
		}
	}

	return true
}

// trackPageView records a page view.
func (m *Module) trackPageView(r *http.Request) {
	// Get real IP (respects X-Real-IP and X-Forwarded-For from chi middleware)
	ip := getRealIP(r)
	userAgent := r.UserAgent()

	// Skip bots (basic detection)
	ua := parseUserAgent(userAgent)
	if ua.DeviceType == "bot" {
		return
	}

	// Create anonymized hashes
	visitorHash := m.CreateVisitorHash(ip, userAgent)
	sessionHash := m.CreateSessionHash(ip, userAgent)

	// Get country from IP (before we lose the IP)
	countryCode := m.geoIP.LookupCountry(ip)

	// Extract referrer domain
	referrerDomain := extractReferrerDomain(r.Referer())

	// Get primary language from Accept-Language
	language := parseAcceptLanguage(r.Header.Get("Accept-Language"))

	// Build page view record
	view := &PageView{
		VisitorHash:    visitorHash,
		Path:           r.URL.Path,
		ReferrerDomain: referrerDomain,
		CountryCode:    countryCode,
		Browser:        ua.Browser,
		OS:             ua.OS,
		DeviceType:     ua.DeviceType,
		Language:       language,
		SessionHash:    sessionHash,
		CreatedAt:      timeNow(),
	}

	// Insert into database
	if err := m.insertPageView(view); err != nil {
		m.ctx.Logger.Error("failed to insert page view", "error", err, "path", view.Path)
	}
}

// insertPageView inserts a page view record into the database.
func (m *Module) insertPageView(view *PageView) error {
	// Format time in SQLite-compatible format (ISO8601 without timezone)
	createdAtStr := view.CreatedAt.Format("2006-01-02 15:04:05")
	_, err := m.ctx.DB.Exec(`
		INSERT INTO page_analytics_views (
			visitor_hash, path, page_id, referrer_domain, country_code,
			browser, os, device_type, language, session_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, view.VisitorHash, view.Path, view.PageID, view.ReferrerDomain,
		view.CountryCode, view.Browser, view.OS, view.DeviceType,
		view.Language, view.SessionHash, createdAtStr)
	return err
}

// getRealIP extracts the real client IP from the request.
// It respects X-Real-IP and X-Forwarded-For headers set by reverse proxies.
func getRealIP(r *http.Request) string {
	// Check X-Real-IP first (set by chi middleware.RealIP)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Check X-Forwarded-For
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list (client IP)
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Fall back to RemoteAddr
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx > 0 {
		ip = ip[:idx]
	}
	// Handle IPv6 addresses in brackets
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")

	return ip
}

// extractReferrerDomain extracts the domain from a referrer URL.
func extractReferrerDomain(referer string) string {
	if referer == "" {
		return ""
	}

	parsed, err := url.Parse(referer)
	if err != nil {
		return ""
	}

	host := parsed.Host
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}

	return host
}

// parseAcceptLanguage extracts the primary language from Accept-Language header.
func parseAcceptLanguage(header string) string {
	if header == "" {
		return ""
	}

	// Accept-Language format: "en-US,en;q=0.9,fr;q=0.8"
	// We want just the primary language code
	parts := strings.Split(header, ",")
	if len(parts) == 0 {
		return ""
	}

	// Get first language preference
	first := strings.TrimSpace(parts[0])

	// Remove quality factor if present
	if idx := strings.Index(first, ";"); idx > 0 {
		first = first[:idx]
	}

	// Get just the language code (e.g., "en" from "en-US")
	if idx := strings.Index(first, "-"); idx > 0 {
		first = first[:idx]
	}

	return strings.ToLower(first)
}

// GetRealTimeVisitorCount returns the number of unique visitors in the last N minutes.
func (m *Module) GetRealTimeVisitorCount(minutes int) int {
	cutoff := time.Now().Add(-time.Duration(minutes) * time.Minute)

	var count int
	err := m.ctx.DB.QueryRow(`
		SELECT COUNT(DISTINCT visitor_hash)
		FROM page_analytics_views
		WHERE created_at >= ?
	`, cutoff).Scan(&count)

	if err != nil {
		return 0
	}
	return count
}
