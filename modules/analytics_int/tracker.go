// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/util"
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
		// If no Content-Type set, default to plain text to prevent XSS
		if rw.ResponseWriter.Header().Get("Content-Type") == "" {
			rw.ResponseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
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

// requestIdentity holds anonymized visitor identification extracted from a request.
type requestIdentity struct {
	IP          string
	UA          ParsedUA
	VisitorHash string
	SessionHash string
}

// extractIdentity extracts anonymized visitor identity from a request.
// Returns nil if the request should be skipped (excluded IP or bot).
func (m *Module) extractIdentity(r *http.Request) *requestIdentity {
	ip := getRealIP(r)

	if excludedIPs := m.getExcludedIPs(); len(excludedIPs) > 0 && util.MatchesIPList(ip, excludedIPs) {
		return nil
	}

	userAgent := r.UserAgent()
	ua := parseUserAgent(userAgent)
	if ua.DeviceType == "bot" {
		return nil
	}

	return &requestIdentity{
		IP:          ip,
		UA:          ua,
		VisitorHash: m.CreateVisitorHash(ip, userAgent),
		SessionHash: m.CreateSessionHash(ip, userAgent),
	}
}

// trackPageView records a page view.
func (m *Module) trackPageView(r *http.Request) {
	id := m.extractIdentity(r)
	if id == nil {
		return
	}

	// Get country from IP (before we lose the IP)
	countryCode := m.geoIP.LookupCountry(id.IP)

	// Extract referrer domain
	referrerDomain := extractReferrerDomain(r.Referer())

	// Filter self-referrals using site domain from config
	if siteDomain := m.getSiteDomain(); siteDomain != "" && matchesSiteDomain(referrerDomain, siteDomain) {
		referrerDomain = ""
	}

	// Get primary language from Accept-Language
	language := parseAcceptLanguage(r.Header.Get("Accept-Language"))

	// Build page view record
	view := &PageView{
		VisitorHash:    id.VisitorHash,
		Path:           r.URL.Path,
		ReferrerDomain: referrerDomain,
		CountryCode:    countryCode,
		Browser:        id.UA.Browser,
		OS:             id.UA.OS,
		DeviceType:     id.UA.DeviceType,
		Language:       language,
		SessionHash:    id.SessionHash,
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

// getRealIP extracts the client IP using shared trusted-proxy-aware logic.
func getRealIP(r *http.Request) string {
	return middleware.GetClientIP(r)
}

// matchesSiteDomain checks if a referrer domain matches the site domain.
// Handles www. prefix and case-insensitive comparison.
// e.g., "www.it-digest.info" matches "it-digest.info" and vice versa.
func matchesSiteDomain(referrerDomain, siteDomain string) bool {
	if referrerDomain == "" || siteDomain == "" {
		return false
	}

	// Normalize: lowercase and strip www. prefix
	ref := strings.TrimPrefix(strings.ToLower(referrerDomain), "www.")
	site := strings.TrimPrefix(strings.ToLower(siteDomain), "www.")

	return ref == site
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

// ReadRequest represents a read tracking beacon request from the frontend.
type ReadRequest struct {
	Path        string `json:"path"`
	ScrollDepth int    `json:"scroll_depth"`
	TimeOnPage  int    `json:"time_on_page"`
}

// recordRead processes a read beacon request and inserts a read event.
// Returns true if the read was recorded, false if it was a duplicate or invalid.
func (m *Module) recordRead(r *http.Request, req *ReadRequest) bool {
	id := m.extractIdentity(r)
	if id == nil {
		return false
	}
	return m.recordReadWithIdentity(id, req)
}

// recordReadWithIdentity inserts a read event using pre-extracted identity.
// This avoids capturing *http.Request in goroutines after the response completes.
func (m *Module) recordReadWithIdentity(id *requestIdentity, req *ReadRequest) bool {
	read := &PageRead{
		VisitorHash: id.VisitorHash,
		Path:        req.Path,
		SessionHash: id.SessionHash,
		ScrollDepth: req.ScrollDepth,
		TimeOnPage:  req.TimeOnPage,
		CreatedAt:   timeNow(),
	}

	if err := m.insertPageRead(read); err != nil {
		// Unique constraint violation means duplicate — not an error
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return false
		}
		m.ctx.Logger.Error("failed to insert page read", "error", err, "path", read.Path)
		return false
	}
	return true
}

// insertPageRead inserts a read event into the database.
// The unique index on (session_hash, path) prevents duplicate reads.
func (m *Module) insertPageRead(read *PageRead) error {
	createdAtStr := read.CreatedAt.Format("2006-01-02 15:04:05")
	_, err := m.ctx.DB.Exec(`
		INSERT INTO page_analytics_reads (
			visitor_hash, path, page_id, session_hash,
			scroll_depth, time_on_page, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, read.VisitorHash, read.Path, read.PageID,
		read.SessionHash, read.ScrollDepth, read.TimeOnPage, createdAtStr)
	return err
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
