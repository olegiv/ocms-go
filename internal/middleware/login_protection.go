// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/store"
)

// LoginProtection provides combined IP rate limiting and account lockout
// protection. IP rate limiting is in-memory (short-lived, OK to lose on
// restart). Account lockout state is persisted to SQLite so an attacker
// who can trigger process restarts (OOM kill, deploy loop) cannot reset
// the brute-force window — see audit finding FIND-005.
//
// If db is nil the lockout methods no-op, degrading to IP-rate-limit-only
// protection. This keeps the type testable without a DB when only the IP
// limiter is exercised, and makes the handler fail-open on DB outages
// instead of locking legitimate users out.
type LoginProtection struct {
	db *sql.DB

	// IP-based rate limiting (uses limiterCache from api.go)
	ipLimiters *limiterCache[string]

	// Configuration
	maxFailedAttempts int           // Lock account after this many failures
	lockoutDuration   time.Duration // Base lockout duration (doubles with each lockout)
	attemptWindow     time.Duration // Window to count failed attempts
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

// NewLoginProtection creates a new login protection instance backed by the
// given SQLite database for persistent lockout state. Passing db=nil
// disables persistent lockout tracking; the IP rate limiter still works.
func NewLoginProtection(db *sql.DB, cfg LoginProtectionConfig) *LoginProtection {
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
		db:                db,
		ipLimiters:        newLimiterCache[string](cfg.IPRateLimit, cfg.IPBurst),
		maxFailedAttempts: cfg.MaxFailedAttempts,
		lockoutDuration:   cfg.LockoutDuration,
		attemptWindow:     cfg.AttemptWindow,
	}

	// Start cleanup goroutine only when persistent storage is wired.
	if db != nil {
		go lp.cleanup()
	}

	return lp
}

// CheckIPRateLimit checks if the IP is rate limited.
// Returns true if the request should be allowed.
func (lp *LoginProtection) CheckIPRateLimit(ip string) bool {
	return lp.ipLimiters.get(ip).Allow()
}

// IsAccountLocked checks if an account is currently locked.
// Returns (locked, remainingTime). DB errors fail-open (return not-locked)
// so a transient database outage does not lock out legitimate users.
func (lp *LoginProtection) IsAccountLocked(email string) (bool, time.Duration) {
	if lp.db == nil {
		return false, 0
	}
	rec, err := store.New(lp.db).GetLoginProtection(context.Background(), email)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("login_protection: get", "error", err, "email", email)
		}
		return false, 0
	}
	if !rec.LockedUntil.Valid || !time.Now().Before(rec.LockedUntil.Time) {
		return false, 0
	}
	return true, time.Until(rec.LockedUntil.Time)
}

// RecordFailedAttempt records a failed login attempt.
// Returns (locked, lockDuration) if the account is now locked.
//
// DB writes are best-effort: on error we log and return (false, 0) so the
// handler still surfaces "invalid credentials" rather than a 5xx. A
// sustained DB outage degrades to Argon2-only defense (~20ms per guess),
// which is what the pre-persistence code already produced on restart.
func (lp *LoginProtection) RecordFailedAttempt(email string) (bool, time.Duration) {
	if lp.db == nil {
		return false, 0
	}
	ctx := context.Background()
	q := store.New(lp.db)
	now := time.Now()

	rec, err := q.GetLoginProtection(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		// First recorded failure for this email.
		if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
			Email:         email,
			AttemptCount:  1,
			FirstFailedAt: now,
			LockedUntil:   sql.NullTime{},
			LockoutCount:  0,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: upsert initial", "error", err, "email", email)
		} else {
			slog.Debug("login attempt recorded", "email", email, "count", 1)
		}
		return false, 0
	}
	if err != nil {
		slog.Error("login_protection: get on record", "error", err, "email", email)
		return false, 0
	}

	// Attempt window has passed: start a fresh counter but keep the
	// historical lockout_count so exponential backoff still stiffens.
	if now.Sub(rec.FirstFailedAt) > lp.attemptWindow {
		if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
			Email:         email,
			AttemptCount:  1,
			FirstFailedAt: now,
			LockedUntil:   sql.NullTime{},
			LockoutCount:  rec.LockoutCount,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: reset window", "error", err, "email", email)
		} else {
			slog.Debug("login attempt window reset", "email", email, "count", 1)
		}
		return false, 0
	}

	newCount := rec.AttemptCount + 1
	slog.Debug("login attempt recorded", "email", email, "count", newCount)

	// Lock the account if we've crossed the threshold.
	if int(newCount) >= lp.maxFailedAttempts {
		lockDuration := lp.lockoutDuration
		for range rec.LockoutCount {
			lockDuration *= 2
			if lockDuration > 24*time.Hour {
				lockDuration = 24 * time.Hour
				break
			}
		}
		lockedUntil := now.Add(lockDuration)
		if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
			Email:         email,
			AttemptCount:  0, // Reset count on lockout; next failure starts over.
			FirstFailedAt: rec.FirstFailedAt,
			LockedUntil:   sql.NullTime{Time: lockedUntil, Valid: true},
			LockoutCount:  rec.LockoutCount + 1,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: lock", "error", err, "email", email)
			return false, 0
		}
		slog.Warn("account locked due to failed attempts",
			"email", email,
			"lockouts", rec.LockoutCount+1,
			"duration", lockDuration,
		)
		return true, lockDuration
	}

	// Under the threshold: just increment.
	if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
		Email:         email,
		AttemptCount:  newCount,
		FirstFailedAt: rec.FirstFailedAt,
		LockedUntil:   rec.LockedUntil,
		LockoutCount:  rec.LockoutCount,
		UpdatedAt:     now,
	}); err != nil {
		slog.Error("login_protection: increment", "error", err, "email", email)
	}
	return false, 0
}

// RecordSuccessfulLogin clears failed attempt tracking for an account.
func (lp *LoginProtection) RecordSuccessfulLogin(email string) {
	if lp.db == nil {
		return
	}
	if err := store.New(lp.db).DeleteLoginProtection(context.Background(), email); err != nil {
		slog.Error("login_protection: delete", "error", err, "email", email)
		return
	}
	slog.Debug("login attempts cleared", "email", email)
}

// GetRemainingAttempts returns the number of remaining attempts before lockout.
func (lp *LoginProtection) GetRemainingAttempts(email string) int {
	if lp.db == nil {
		return lp.maxFailedAttempts
	}
	rec, err := store.New(lp.db).GetLoginProtection(context.Background(), email)
	if err != nil {
		return lp.maxFailedAttempts
	}
	if time.Since(rec.FirstFailedAt) > lp.attemptWindow {
		return lp.maxFailedAttempts
	}
	remaining := lp.maxFailedAttempts - int(rec.AttemptCount)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// cleanup periodically removes stale rows from login_protection.
func (lp *LoginProtection) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		lp.cleanupStaleEntries()
	}
}

func (lp *LoginProtection) cleanupStaleEntries() {
	if lp.db == nil {
		return
	}
	now := time.Now()
	err := store.New(lp.db).CleanupStaleLoginProtection(context.Background(), store.CleanupStaleLoginProtectionParams{
		LockedUntil:   sql.NullTime{Time: now, Valid: true},
		FirstFailedAt: now.Add(-lp.attemptWindow),
	})
	if err != nil {
		slog.Error("login_protection cleanup", "error", err)
	}
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

// IsRequestFromTrustedProxy reports whether the immediate peer (r.RemoteAddr)
// is in the configured trusted-proxy CIDR list. Callers use this to decide
// whether forwarding headers like X-Forwarded-Host or X-Forwarded-Proto on
// the request can be trusted as authoritative.
func IsRequestFromTrustedProxy(r *http.Request) bool {
	if r == nil {
		return false
	}
	remoteIP, _ := parseRemoteAddrIP(r.RemoteAddr)
	return isTrustedProxy(remoteIP)
}

type trustedProxyCtxKey struct{}

// AnnotateTrustedProxy records whether the original r.RemoteAddr belongs to a
// configured trusted proxy. It MUST run before any middleware that rewrites
// RemoteAddr (e.g. RealIP) so that downstream code can call
// WasFromTrustedProxy instead of re-checking the (now-rewritten) RemoteAddr.
func AnnotateTrustedProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), trustedProxyCtxKey{}, IsRequestFromTrustedProxy(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WasFromTrustedProxy reports whether the request originally arrived from a
// trusted proxy, as recorded by AnnotateTrustedProxy. Falls back to checking
// r.RemoteAddr directly if the middleware has not run.
func WasFromTrustedProxy(r *http.Request) bool {
	if v, ok := r.Context().Value(trustedProxyCtxKey{}).(bool); ok {
		return v
	}
	return IsRequestFromTrustedProxy(r)
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
	// If header is present but unusable, fail closed to remote peer IP.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, ok := firstUntrustedXFFIP(xff); ok {
			return ip.String()
		}
		return remoteIPString
	}

	// Check X-Real-IP header only when X-Forwarded-For is absent.
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
