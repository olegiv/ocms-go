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
	"unicode"

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

	// attemptsMu serializes the read-modify-write sequence in
	// RecordFailedAttempt so concurrent failed logins for the same
	// lockout key cannot race and undercount (both reading count=N, both
	// writing count=N+1 when the true answer is N+2). oCMS's default
	// deployment is single-instance; this mutex is sufficient for
	// that model. A multi-instance deployment with a shared SQLite
	// file would need DB-level coordination (BEGIN IMMEDIATE + retry
	// on SQLITE_BUSY) instead; not implemented here.
	attemptsMu sync.Mutex

	// Configuration
	maxFailedAttempts int           // Lock the email+IP pair after this many failures; the account locks at accountLockoutMultiplier times this
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

// accountLockoutMultiplier scales the per-(email, IP) threshold up to the
// account-wide one. A single attacker who knows an email address can no
// longer lock its owner out with a handful of guesses from one address: the
// usual threshold locks only that email+IP pair, and the account itself locks
// for everyone only after this many times as many failures, which still puts
// a hard stop on a distributed guessing campaign (finding L-7, 2026-10-11).
const accountLockoutMultiplier = 5

// lockoutKeySeparator joins the scope name, the email and the client IP in
// a login_protection key. The email is raw form input, so control characters
// are stripped from it before the key is built; together with the scope
// prefix that makes it impossible for a crafted email to produce another
// scope's key.
const lockoutKeySeparator = "\x1f"

// lockoutScope is one row of login_protection: the key it is stored under
// and the failure count that locks it.
type lockoutScope struct {
	name      string
	key       string
	threshold int
}

// lockoutScopes returns the pair scope first and the account scope second.
func (lp *LoginProtection) lockoutScopes(email, ip string) [2]lockoutScope {
	email = stripControlCharacters(email)
	ip = stripControlCharacters(ip)
	return [2]lockoutScope{
		{name: "email+ip", key: "pair" + lockoutKeySeparator + email + lockoutKeySeparator + ip, threshold: lp.maxFailedAttempts},
		{name: "account", key: "account" + lockoutKeySeparator + email, threshold: lp.maxFailedAttempts * accountLockoutMultiplier},
	}
}

// stripControlCharacters removes every control character, including the
// key separator, from a value that goes into a lockout key.
func stripControlCharacters(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// IsAccountLocked reports whether logins for email from ip are currently
// refused: either the email+IP pair or the whole account is locked.
// Returns (locked, remainingTime). DB errors fail-open (return not-locked)
// so a transient database outage does not lock out legitimate users.
func (lp *LoginProtection) IsAccountLocked(email, ip string) (bool, time.Duration) {
	if lp.db == nil {
		return false, 0
	}
	var remaining time.Duration
	locked := false
	for _, scope := range lp.lockoutScopes(email, ip) {
		if ok, left := lp.scopeLocked(scope, email); ok {
			locked = true
			remaining = max(remaining, left)
		}
	}
	return locked, remaining
}

// scopeLocked reports whether one lockout key is currently locked and for
// how much longer.
func (lp *LoginProtection) scopeLocked(scope lockoutScope, email string) (bool, time.Duration) {
	rec, err := store.New(lp.db).GetLoginProtection(context.Background(), scope.key)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("login_protection: get", "error", err, "email", email, "scope", scope.name)
		}
		return false, 0
	}
	if !rec.LockedUntil.Valid || !time.Now().Before(rec.LockedUntil.Time) {
		return false, 0
	}
	return true, time.Until(rec.LockedUntil.Time)
}

// RecordFailedAttempt records a failed login attempt for email from ip in
// both scopes. Returns (locked, lockDuration) if either scope is now locked.
//
// DB writes are best-effort: on error we log and return (false, 0) so the
// handler still surfaces "invalid credentials" rather than a 5xx. A
// sustained DB outage degrades to Argon2-only defense (~20ms per guess),
// which is what the pre-persistence code already produced on restart.
func (lp *LoginProtection) RecordFailedAttempt(email, ip string) (bool, time.Duration) {
	if lp.db == nil {
		return false, 0
	}
	// Serialize the read-modify-write so two concurrent failed logins
	// for the same key do not both read count=N and both write count=N+1
	// (losing the second increment). See attemptsMu comment on the struct.
	lp.attemptsMu.Lock()
	defer lp.attemptsMu.Unlock()

	var duration time.Duration
	locked := false
	for _, scope := range lp.lockoutScopes(email, ip) {
		if ok, d := lp.recordScopeFailure(scope, email, ip); ok {
			locked = true
			duration = max(duration, d)
		}
	}
	return locked, duration
}

// recordScopeFailure applies one failure to a single lockout key and reports
// whether that key just locked, and for how long.
func (lp *LoginProtection) recordScopeFailure(scope lockoutScope, email, ip string) (bool, time.Duration) {
	ctx := context.Background()
	q := store.New(lp.db)
	now := time.Now()
	logAttrs := []any{"email", email, "ip", ip, "scope", scope.name}

	rec, err := q.GetLoginProtection(ctx, scope.key)
	if errors.Is(err, sql.ErrNoRows) {
		// First recorded failure for this key.
		if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
			Email:         scope.key,
			AttemptCount:  1,
			FirstFailedAt: now,
			LockedUntil:   sql.NullTime{},
			LockoutCount:  0,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: upsert initial", append(logAttrs, "error", err)...)
		} else {
			slog.Debug("login attempt recorded", append(logAttrs, "count", 1)...)
		}
		return false, 0
	}
	if err != nil {
		slog.Error("login_protection: get on record", append(logAttrs, "error", err)...)
		return false, 0
	}

	// Attempt window has passed: start a fresh counter but keep the
	// historical lockout_count so exponential backoff still stiffens.
	if now.Sub(rec.FirstFailedAt) > lp.attemptWindow {
		if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
			Email:         scope.key,
			AttemptCount:  1,
			FirstFailedAt: now,
			LockedUntil:   sql.NullTime{},
			LockoutCount:  rec.LockoutCount,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: reset window", append(logAttrs, "error", err)...)
		} else {
			slog.Debug("login attempt window reset", append(logAttrs, "count", 1)...)
		}
		return false, 0
	}

	newCount := rec.AttemptCount + 1
	slog.Debug("login attempt recorded", append(logAttrs, "count", newCount)...)

	// Lock the scope if we've crossed its threshold.
	if int(newCount) >= scope.threshold {
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
			Email:         scope.key,
			AttemptCount:  0, // Reset count on lockout; next failure starts over.
			FirstFailedAt: rec.FirstFailedAt,
			LockedUntil:   sql.NullTime{Time: lockedUntil, Valid: true},
			LockoutCount:  rec.LockoutCount + 1,
			UpdatedAt:     now,
		}); err != nil {
			slog.Error("login_protection: lock", append(logAttrs, "error", err)...)
			return false, 0
		}
		slog.Warn("login locked due to failed attempts",
			append(logAttrs, "lockouts", rec.LockoutCount+1, "duration", lockDuration)...)
		return true, lockDuration
	}

	// Under the threshold: just increment.
	if err := q.UpsertLoginProtection(ctx, store.UpsertLoginProtectionParams{
		Email:         scope.key,
		AttemptCount:  newCount,
		FirstFailedAt: rec.FirstFailedAt,
		LockedUntil:   rec.LockedUntil,
		LockoutCount:  rec.LockoutCount,
		UpdatedAt:     now,
	}); err != nil {
		slog.Error("login_protection: increment", append(logAttrs, "error", err)...)
	}
	return false, 0
}

// RecordSuccessfulLogin clears failed attempt tracking for an account in
// both scopes.
//
// Holds the same attemptsMu as RecordFailedAttempt so a concurrent failed
// attempt cannot read state, have it deleted underneath, and then re-upsert
// a stale counter — which would cause "successful login clears attempts"
// to silently regress and potentially re-lock the user. See attemptsMu
// comment on the struct.
func (lp *LoginProtection) RecordSuccessfulLogin(email, ip string) {
	if lp.db == nil {
		return
	}
	lp.attemptsMu.Lock()
	defer lp.attemptsMu.Unlock()

	// The two rows are independent: a failed delete on one must not leave
	// the other's counter in place, so every scope is attempted.
	for _, scope := range lp.lockoutScopes(email, ip) {
		if err := store.New(lp.db).DeleteLoginProtection(context.Background(), scope.key); err != nil {
			slog.Error("login_protection: delete", "error", err, "email", email, "scope", scope.name)
		}
	}
	slog.Debug("login attempts cleared", "email", email, "ip", ip)
}

// GetRemainingAttempts returns the number of remaining attempts before the
// next lockout in either scope.
func (lp *LoginProtection) GetRemainingAttempts(email, ip string) int {
	if lp.db == nil {
		return lp.maxFailedAttempts
	}
	remaining := lp.maxFailedAttempts
	for _, scope := range lp.lockoutScopes(email, ip) {
		rec, err := store.New(lp.db).GetLoginProtection(context.Background(), scope.key)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("login_protection: get remaining", "error", err, "email", email, "scope", scope.name)
			}
			continue
		}
		if time.Since(rec.FirstFailedAt) > lp.attemptWindow {
			continue
		}
		remaining = min(remaining, max(scope.threshold-int(rec.AttemptCount), 0))
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
