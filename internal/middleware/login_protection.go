// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
)

// LoginProtection provides combined IP rate limiting and account lockout protection.
type LoginProtection struct {
	// IP-based rate limiting (uses limiterCache from api.go)
	ipLimiters *limiterCache[string]

	// Account-based lockout tracking
	failedAttempts map[string]*loginAttempt
	attemptsMu     sync.RWMutex

	// Configuration
	maxFailedAttempts int           // Lock account after this many failures
	lockoutDuration   time.Duration // Base lockout duration (doubles with each lockout)
	attemptWindow     time.Duration // Window to count failed attempts
}

// loginAttempt tracks failed login attempts for an account.
type loginAttempt struct {
	count       int
	firstFailed time.Time
	lockedUntil time.Time
	lockouts    int // Number of times account has been locked (for exponential backoff)
}

var (
	trustedProxiesMu     sync.RWMutex
	trustedProxyPrefixes []netip.Prefix
)

// LoginProtectionConfig holds configuration for login protection.
type LoginProtectionConfig struct {
	// IPRateLimit is requests per second per IP (default: 0.5 = 1 request per 2 seconds)
	IPRateLimit float64
	// IPBurst is the maximum burst size for IP rate limiting (default: 5)
	IPBurst int
	// MaxFailedAttempts before account lockout (default: 5)
	MaxFailedAttempts int
	// LockoutDuration is base lockout time, doubles with each lockout (default: 15 minutes)
	LockoutDuration time.Duration
	// AttemptWindow is the time window for counting failed attempts (default: 15 minutes)
	AttemptWindow time.Duration
}

// DefaultLoginProtectionConfig returns sensible defaults.
func DefaultLoginProtectionConfig() LoginProtectionConfig {
	return LoginProtectionConfig{
		IPRateLimit:       0.5,              // 1 request per 2 seconds
		IPBurst:           5,                // Allow burst of 5 requests
		MaxFailedAttempts: 5,                // Lock after 5 failed attempts
		LockoutDuration:   15 * time.Minute, // 15 minute base lockout
		AttemptWindow:     15 * time.Minute, // 15 minute window
	}
}

// NewLoginProtection creates a new login protection instance.
func NewLoginProtection(cfg LoginProtectionConfig) *LoginProtection {
	if cfg.IPRateLimit <= 0 {
		cfg.IPRateLimit = 0.5
	}
	if cfg.IPBurst <= 0 {
		cfg.IPBurst = 5
	}
	if cfg.MaxFailedAttempts <= 0 {
		cfg.MaxFailedAttempts = 5
	}
	if cfg.LockoutDuration <= 0 {
		cfg.LockoutDuration = 15 * time.Minute
	}
	if cfg.AttemptWindow <= 0 {
		cfg.AttemptWindow = 15 * time.Minute
	}

	lp := &LoginProtection{
		ipLimiters:        newLimiterCache[string](cfg.IPRateLimit, cfg.IPBurst),
		failedAttempts:    make(map[string]*loginAttempt),
		maxFailedAttempts: cfg.MaxFailedAttempts,
		lockoutDuration:   cfg.LockoutDuration,
		attemptWindow:     cfg.AttemptWindow,
	}

	// Start cleanup goroutine
	go lp.cleanup()

	return lp
}

// CheckIPRateLimit checks if the IP is rate limited.
// Returns true if the request should be allowed.
func (lp *LoginProtection) CheckIPRateLimit(ip string) bool {
	return lp.ipLimiters.get(ip).Allow()
}

// IsAccountLocked checks if an account is currently locked.
// Returns (locked, remainingTime).
func (lp *LoginProtection) IsAccountLocked(email string) (bool, time.Duration) {
	lp.attemptsMu.RLock()
	attempt, exists := lp.failedAttempts[email]
	lp.attemptsMu.RUnlock()

	if !exists {
		return false, 0
	}

	if time.Now().Before(attempt.lockedUntil) {
		return true, time.Until(attempt.lockedUntil)
	}

	return false, 0
}

// RecordFailedAttempt records a failed login attempt.
// Returns (locked, lockDuration) if the account is now locked.
func (lp *LoginProtection) RecordFailedAttempt(email string) (bool, time.Duration) {
	lp.attemptsMu.Lock()
	defer lp.attemptsMu.Unlock()

	now := time.Now()
	attempt, exists := lp.failedAttempts[email]

	if !exists {
		attempt = &loginAttempt{
			count:       1,
			firstFailed: now,
		}
		lp.failedAttempts[email] = attempt
		slog.Debug("login attempt recorded", "email", email, "count", 1)
		return false, 0
	}

	// If the attempt window has passed, reset the counter
	if now.Sub(attempt.firstFailed) > lp.attemptWindow {
		attempt.count = 1
		attempt.firstFailed = now
		slog.Debug("login attempt window reset", "email", email, "count", 1)
		return false, 0
	}

	// Increment counter
	attempt.count++
	slog.Debug("login attempt recorded", "email", email, "count", attempt.count)

	// Check if we should lock the account
	if attempt.count >= lp.maxFailedAttempts {
		// Calculate lockout duration with exponential backoff
		lockDuration := lp.lockoutDuration
		for i := 0; i < attempt.lockouts; i++ {
			lockDuration *= 2
			// Cap at 24 hours
			if lockDuration > 24*time.Hour {
				lockDuration = 24 * time.Hour
				break
			}
		}

		attempt.lockedUntil = now.Add(lockDuration)
		attempt.lockouts++
		attempt.count = 0 // Reset count after lockout

		slog.Warn("account locked due to failed attempts",
			"email", email,
			"lockouts", attempt.lockouts,
			"duration", lockDuration,
		)

		return true, lockDuration
	}

	return false, 0
}

// RecordSuccessfulLogin clears failed attempt tracking for an account.
func (lp *LoginProtection) RecordSuccessfulLogin(email string) {
	lp.attemptsMu.Lock()
	defer lp.attemptsMu.Unlock()

	delete(lp.failedAttempts, email)
	slog.Debug("login attempts cleared", "email", email)
}

// GetRemainingAttempts returns the number of remaining attempts before lockout.
func (lp *LoginProtection) GetRemainingAttempts(email string) int {
	lp.attemptsMu.RLock()
	attempt, exists := lp.failedAttempts[email]
	lp.attemptsMu.RUnlock()

	if !exists {
		return lp.maxFailedAttempts
	}

	// Check if window has passed
	if time.Since(attempt.firstFailed) > lp.attemptWindow {
		return lp.maxFailedAttempts
	}

	remaining := lp.maxFailedAttempts - attempt.count
	if remaining < 0 {
		return 0
	}
	return remaining
}

// cleanup periodically removes stale entries.
func (lp *LoginProtection) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		lp.cleanupStaleEntries()
	}
}

func (lp *LoginProtection) cleanupStaleEntries() {
	now := time.Now()

	// IP rate limiters are cleaned up automatically via TTL-based eviction

	// Cleanup old login attempts
	lp.attemptsMu.Lock()
	for email, attempt := range lp.failedAttempts {
		// Remove if lockout has expired and no recent attempts
		if now.After(attempt.lockedUntil) &&
			now.Sub(attempt.firstFailed) > lp.attemptWindow {
			delete(lp.failedAttempts, email)
		}
	}
	lp.attemptsMu.Unlock()
}

// Middleware returns HTTP middleware for IP rate limiting on login.
// This should be applied to the login POST route.
func (lp *LoginProtection) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only rate limit POST requests
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			ip := GetClientIP(r)

			if !lp.CheckIPRateLimit(ip) {
				slog.Warn("login rate limit exceeded", "ip", ip)
				lang := GetAdminLang(r)
				http.Error(w, i18n.T(lang, "auth.rate_limit"), http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetRequestURL extracts the request URL path from the request.
// Returns the URL path without query string for cleaner logging.
func GetRequestURL(r *http.Request) string {
	return r.URL.Path
}

// GetClientIP extracts the client IP from the request.
// It trusts forwarding headers only when the direct peer is a trusted proxy.
// For X-Forwarded-For chains it returns the first untrusted hop from the right,
// which is more resilient to spoofed left-most values.
func GetClientIP(r *http.Request) string {
	remoteIP, remoteIPString := parseRemoteAddrIP(r.RemoteAddr)

	// Only trust forwarding headers when traffic comes from an explicitly
	// trusted reverse proxy network.
	if !isTrustedProxy(remoteIP) {
		return remoteIPString
	}

	// Check X-Forwarded-For header (can contain multiple IPs).
	// Use the first untrusted entry from the right side of the chain.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, ok := firstUntrustedXFFIP(xff); ok {
			return ip.String()
		}
	}

	// Check X-Real-IP header (set by reverse proxies)
	if ip, ok := parseIP(r.Header.Get("X-Real-IP")); ok {
		return ip.String()
	}

	return remoteIPString
}

func firstUntrustedXFFIP(xff string) (netip.Addr, bool) {
	parts := strings.Split(xff, ",")
	ips := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		if ip, ok := parseIP(part); ok {
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return netip.Addr{}, false
	}

	for i := len(ips) - 1; i >= 0; i-- {
		if !isTrustedProxy(ips[i]) {
			return ips[i], true
		}
	}

	// All entries are trusted proxies; fall back to the left-most address.
	return ips[0], true
}

// ConfigureTrustedProxies updates trusted proxy ranges from a comma-separated
// list of CIDRs/IPs. Empty input disables trust of forwarding headers.
func ConfigureTrustedProxies(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return SetTrustedProxies(nil)
	}

	return SetTrustedProxies(strings.Split(raw, ","))
}

// SetTrustedProxies updates trusted proxy ranges from CIDR/IP entries.
// IPs are treated as /32 (IPv4) or /128 (IPv6).
func SetTrustedProxies(entries []string) error {
	prefixes := make([]netip.Prefix, 0, len(entries))

	for _, entry := range entries {
		value := strings.TrimSpace(entry)
		if value == "" {
			continue
		}

		if strings.Contains(value, "/") {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return fmt.Errorf("invalid trusted proxy CIDR %q: %w", value, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}

		ip, err := netip.ParseAddr(value)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy IP %q: %w", value, err)
		}

		if ip.Is4() {
			prefixes = append(prefixes, netip.PrefixFrom(ip.Unmap(), 32))
		} else {
			prefixes = append(prefixes, netip.PrefixFrom(ip, 128))
		}
	}

	trustedProxiesMu.Lock()
	trustedProxyPrefixes = prefixes
	trustedProxiesMu.Unlock()

	return nil
}

func isTrustedProxy(remoteIP netip.Addr) bool {
	if !remoteIP.IsValid() {
		return false
	}

	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()

	for _, prefix := range trustedProxyPrefixes {
		if prefix.Contains(remoteIP) {
			return true
		}
	}

	return false
}

func parseRemoteAddrIP(remoteAddr string) (netip.Addr, string) {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return netip.Addr{}, ""
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}

	if ip, ok := parseIP(host); ok {
		return ip, ip.String()
	}

	return netip.Addr{}, host
}

func parseIP(value string) (netip.Addr, bool) {
	candidate := strings.TrimSpace(value)
	candidate = strings.TrimPrefix(candidate, "[")
	candidate = strings.TrimSuffix(candidate, "]")
	if candidate == "" {
		return netip.Addr{}, false
	}

	ip, err := netip.ParseAddr(candidate)
	if err != nil {
		return netip.Addr{}, false
	}

	return ip.Unmap(), true
}
