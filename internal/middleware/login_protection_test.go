// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// testLoginProtectionConfig returns a config suitable for fast testing.
func testLoginProtectionConfig(maxAttempts int, lockoutDuration, attemptWindow time.Duration) LoginProtectionConfig {
	return LoginProtectionConfig{
		IPRateLimit:       10,  // High rate for testing
		IPBurst:           100, // High burst for testing
		MaxFailedAttempts: maxAttempts,
		LockoutDuration:   lockoutDuration,
		AttemptWindow:     attemptWindow,
	}
}

// testLoginProtectionDB opens a per-test in-memory SQLite DB with a
// shared cache so concurrent goroutines using the same *sql.DB handle
// all see the same table. Without `cache=shared`, each connection Go's
// pool opens would get its own empty `:memory:` DB and the tests would
// see "no such table" errors. The test-specific `&test=<name>` segment
// isolates databases per-test so parallel tests do not collide.
func testLoginProtectionDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE login_protection (
			email            TEXT PRIMARY KEY,
			attempt_count    INTEGER  NOT NULL DEFAULT 0,
			first_failed_at  DATETIME NOT NULL,
			locked_until     DATETIME,
			lockout_count    INTEGER  NOT NULL DEFAULT 0,
			updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func setTrustedProxiesForTest(t *testing.T, entries ...string) {
	t.Helper()
	if err := SetTrustedProxies(entries); err != nil {
		t.Fatalf("SetTrustedProxies() error: %v", err)
	}
	t.Cleanup(func() {
		if err := SetTrustedProxies(nil); err != nil {
			t.Fatalf("reset SetTrustedProxies() error: %v", err)
		}
	})
}

func TestDefaultLoginProtectionConfig(t *testing.T) {
	cfg := DefaultLoginProtectionConfig()

	if cfg.IPRateLimit != 0.5 {
		t.Errorf("IPRateLimit = %v, want 0.5", cfg.IPRateLimit)
	}
	if cfg.IPBurst != 5 {
		t.Errorf("IPBurst = %d, want 5", cfg.IPBurst)
	}
	if cfg.MaxFailedAttempts != 5 {
		t.Errorf("MaxFailedAttempts = %d, want 5", cfg.MaxFailedAttempts)
	}
	if cfg.LockoutDuration != 15*time.Minute {
		t.Errorf("LockoutDuration = %v, want 15m", cfg.LockoutDuration)
	}
	if cfg.AttemptWindow != 15*time.Minute {
		t.Errorf("AttemptWindow = %v, want 15m", cfg.AttemptWindow)
	}
}

func TestNewLoginProtection(t *testing.T) {
	cfg := DefaultLoginProtectionConfig()
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)

	if lp == nil {
		t.Fatal("NewLoginProtection() returned nil")
	}
	if lp.maxFailedAttempts != cfg.MaxFailedAttempts {
		t.Errorf("maxFailedAttempts = %d, want %d", lp.maxFailedAttempts, cfg.MaxFailedAttempts)
	}
}

func TestNewLoginProtectionDefaultValues(t *testing.T) {
	// Test with zero config values - should use defaults
	cfg := LoginProtectionConfig{}
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)

	if lp.maxFailedAttempts != 5 {
		t.Errorf("maxFailedAttempts = %d, want 5 (default)", lp.maxFailedAttempts)
	}
	if lp.lockoutDuration != 15*time.Minute {
		t.Errorf("lockoutDuration = %v, want 15m (default)", lp.lockoutDuration)
	}
}

func TestLoginProtectionIsAccountLocked(t *testing.T) {
	cfg := testLoginProtectionConfig(3, 1*time.Second, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// Initially not locked
	locked, _ := lp.IsAccountLocked(email)
	if locked {
		t.Error("Account should not be locked initially")
	}

	// Record failed attempts until locked
	for i := 0; i < cfg.MaxFailedAttempts; i++ {
		lp.RecordFailedAttempt(email)
	}

	// Now should be locked
	locked, remaining := lp.IsAccountLocked(email)
	if !locked {
		t.Error("Account should be locked after max failed attempts")
	}
	if remaining <= 0 {
		t.Error("Remaining lockout time should be positive")
	}

	// Wait for lockout to expire
	time.Sleep(cfg.LockoutDuration + 100*time.Millisecond)

	// Should be unlocked now
	locked, _ = lp.IsAccountLocked(email)
	if locked {
		t.Error("Account should be unlocked after lockout expires")
	}
}

func TestLoginProtectionRecordFailedAttempt(t *testing.T) {
	cfg := testLoginProtectionConfig(3, 1*time.Second, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// First attempt should not lock
	locked, _ := lp.RecordFailedAttempt(email)
	if locked {
		t.Error("First attempt should not lock account")
	}

	// Second attempt should not lock
	locked, _ = lp.RecordFailedAttempt(email)
	if locked {
		t.Error("Second attempt should not lock account")
	}

	// Third attempt should lock
	locked, duration := lp.RecordFailedAttempt(email)
	if !locked {
		t.Error("Third attempt should lock account")
	}
	if duration <= 0 {
		t.Error("Lock duration should be positive")
	}
}

func TestLoginProtectionRecordSuccessfulLogin(t *testing.T) {
	cfg := testLoginProtectionConfig(3, 1*time.Minute, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// Record some failed attempts
	lp.RecordFailedAttempt(email)
	lp.RecordFailedAttempt(email)

	// Record successful login
	lp.RecordSuccessfulLogin(email)

	// Should have full attempts again
	remaining := lp.GetRemainingAttempts(email)
	if remaining != cfg.MaxFailedAttempts {
		t.Errorf("GetRemainingAttempts() = %d, want %d", remaining, cfg.MaxFailedAttempts)
	}
}

func TestLoginProtectionGetRemainingAttempts(t *testing.T) {
	cfg := testLoginProtectionConfig(5, 1*time.Minute, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// Initial remaining should be max
	remaining := lp.GetRemainingAttempts(email)
	if remaining != 5 {
		t.Errorf("GetRemainingAttempts() = %d, want 5", remaining)
	}

	// After one failed attempt
	lp.RecordFailedAttempt(email)
	remaining = lp.GetRemainingAttempts(email)
	if remaining != 4 {
		t.Errorf("GetRemainingAttempts() = %d, want 4", remaining)
	}

	// After two more failed attempts
	lp.RecordFailedAttempt(email)
	lp.RecordFailedAttempt(email)
	remaining = lp.GetRemainingAttempts(email)
	if remaining != 2 {
		t.Errorf("GetRemainingAttempts() = %d, want 2", remaining)
	}
}

func TestLoginProtectionExponentialBackoff(t *testing.T) {
	cfg := testLoginProtectionConfig(2, 100*time.Millisecond, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// First lockout
	lp.RecordFailedAttempt(email)
	_, duration1 := lp.RecordFailedAttempt(email)

	// Wait for lockout to expire
	time.Sleep(duration1 + 10*time.Millisecond)

	// Second lockout should be longer (exponential backoff)
	lp.RecordFailedAttempt(email)
	_, duration2 := lp.RecordFailedAttempt(email)

	if duration2 <= duration1 {
		t.Errorf("Second lockout duration (%v) should be longer than first (%v)", duration2, duration1)
	}
}

func TestLoginProtectionAttemptWindowReset(t *testing.T) {
	cfg := testLoginProtectionConfig(5, 1*time.Minute, 100*time.Millisecond) // Very short window for testing
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "test@example.com"

	// Record a failed attempt
	lp.RecordFailedAttempt(email)
	remaining := lp.GetRemainingAttempts(email)
	if remaining != 4 {
		t.Errorf("GetRemainingAttempts() = %d, want 4", remaining)
	}

	// Wait for window to expire
	time.Sleep(cfg.AttemptWindow + 50*time.Millisecond)

	// After window expires, should reset to max
	remaining = lp.GetRemainingAttempts(email)
	if remaining != cfg.MaxFailedAttempts {
		t.Errorf("GetRemainingAttempts() after window = %d, want %d", remaining, cfg.MaxFailedAttempts)
	}
}

// TestRecordFailedAttemptIsSerialized is the drift test for the Codex P1
// finding on PR #129: without serialization, concurrent failed logins for
// the same email race in the read-modify-write cycle (both read count=N,
// both compute and write count=N+1) and undercount attempts, weakening
// the lockout control. With the attemptsMu mutex, every increment is
// observed.
//
// The test spawns N concurrent goroutines calling RecordFailedAttempt
// against the same email, all under a per-email window large enough that
// no lockout is triggered. The final attempt_count must equal N exactly.
// Removing the mutex makes this test fail intermittently with count < N.
func TestRecordFailedAttemptIsSerialized(t *testing.T) {
	// Large attempt count with a high lockout threshold so the loop
	// exercises the "increment" path every time, never the "lock" path.
	cfg := testLoginProtectionConfig(1_000_000, 1*time.Minute, 1*time.Minute)
	lp := NewLoginProtection(testLoginProtectionDB(t), cfg)
	email := "race@example.com"

	const parallelism = 50
	var wg sync.WaitGroup
	wg.Add(parallelism)
	for range parallelism {
		go func() {
			defer wg.Done()
			lp.RecordFailedAttempt(email)
		}()
	}
	wg.Wait()

	remaining := lp.GetRemainingAttempts(email)
	got := cfg.MaxFailedAttempts - remaining
	if got != parallelism {
		t.Errorf("attempt_count after %d concurrent failures = %d, want %d (racy read-modify-write lost increments)", parallelism, got, parallelism)
	}
}

// TestLoginProtectionSurvivesProcessRestart is the drift test for audit
// finding FIND-005. The previous in-memory implementation lost all lockout
// state on restart — an attacker who could trigger a deploy/OOM/crash
// could reset the brute-force window. The DB-backed implementation must
// read the same state back after a fresh LoginProtection instance is
// constructed against the same DB.
func TestLoginProtectionSurvivesProcessRestart(t *testing.T) {
	db := testLoginProtectionDB(t)
	cfg := testLoginProtectionConfig(3, 10*time.Minute, 10*time.Minute)
	email := "survivor@example.com"

	// "Before restart" — record failures up to the lockout threshold.
	before := NewLoginProtection(db, cfg)
	for range cfg.MaxFailedAttempts {
		before.RecordFailedAttempt(email)
	}
	locked, remaining := before.IsAccountLocked(email)
	if !locked {
		t.Fatalf("pre-restart: account should be locked after %d attempts", cfg.MaxFailedAttempts)
	}
	if remaining <= 0 {
		t.Fatalf("pre-restart: remaining lockout should be positive, got %v", remaining)
	}

	// "After restart" — fresh LoginProtection, same DB. The old
	// implementation would forget everything here; this one must not.
	after := NewLoginProtection(db, cfg)
	lockedAfter, remainingAfter := after.IsAccountLocked(email)
	if !lockedAfter {
		t.Errorf("post-restart: account MUST still be locked — lockout state did not persist to DB")
	}
	if remainingAfter <= 0 {
		t.Errorf("post-restart: remaining lockout should still be positive, got %v", remainingAfter)
	}
}

func TestGetClientIP(t *testing.T) {
	t.Run("defaults to RemoteAddr when no trusted proxies", func(t *testing.T) {
		setTrustedProxiesForTest(t)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")
		req.Header.Set("X-Real-IP", "10.0.0.5")

		got := GetClientIP(req)
		if got != "192.168.1.1" {
			t.Errorf("GetClientIP() = %q, want %q", got, "192.168.1.1")
		}
	})

	t.Run("uses first untrusted IP from right in XFF chain", func(t *testing.T) {
		setTrustedProxiesForTest(t, "127.0.0.1/32", "10.0.0.0/8")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")
		req.Header.Set("X-Real-IP", "10.0.0.5")

		got := GetClientIP(req)
		if got != "203.0.113.9" {
			t.Errorf("GetClientIP() = %q, want %q", got, "203.0.113.9")
		}
	})

	t.Run("ignores spoofed left-most XFF when trusted hops are present", func(t *testing.T) {
		setTrustedProxiesForTest(t, "127.0.0.1/32", "10.0.0.0/8")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		req.Header.Set("X-Forwarded-For", "198.51.100.200, 198.51.100.9, 10.0.0.7")

		got := GetClientIP(req)
		if got != "198.51.100.9" {
			t.Errorf("GetClientIP() = %q, want %q", got, "198.51.100.9")
		}
	})

	t.Run("fails closed to remote when XFF is present but invalid", func(t *testing.T) {
		setTrustedProxiesForTest(t, "127.0.0.1")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		req.Header.Set("X-Forwarded-For", "invalid, also-invalid")
		req.Header.Set("X-Real-IP", "10.0.0.5")

		got := GetClientIP(req)
		if got != "127.0.0.1" {
			t.Errorf("GetClientIP() = %q, want %q", got, "127.0.0.1")
		}
	})

	t.Run("uses X-Real-IP when XFF is absent", func(t *testing.T) {
		setTrustedProxiesForTest(t, "127.0.0.1")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		req.Header.Set("X-Real-IP", "10.0.0.5")

		got := GetClientIP(req)
		if got != "10.0.0.5" {
			t.Errorf("GetClientIP() = %q, want %q", got, "10.0.0.5")
		}
	})

	t.Run("falls back to remote when proxy headers invalid", func(t *testing.T) {
		setTrustedProxiesForTest(t, "127.0.0.0/8")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		req.Header.Set("X-Forwarded-For", "invalid")
		req.Header.Set("X-Real-IP", "also-invalid")

		got := GetClientIP(req)
		if got != "127.0.0.1" {
			t.Errorf("GetClientIP() = %q, want %q", got, "127.0.0.1")
		}
	})
}

func TestSetTrustedProxies_Validation(t *testing.T) {
	t.Run("accepts CIDR and single IP", func(t *testing.T) {
		if err := SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"}); err != nil {
			t.Fatalf("SetTrustedProxies() error = %v", err)
		}
	})

	t.Run("rejects invalid CIDR", func(t *testing.T) {
		if err := SetTrustedProxies([]string{"10.0.0.0/99"}); err == nil {
			t.Fatal("SetTrustedProxies() expected error, got nil")
		}
	})

	t.Run("rejects invalid IP", func(t *testing.T) {
		if err := SetTrustedProxies([]string{"not-an-ip"}); err == nil {
			t.Fatal("SetTrustedProxies() expected error, got nil")
		}
	})

	if err := SetTrustedProxies(nil); err != nil {
		t.Fatalf("reset SetTrustedProxies() error: %v", err)
	}
}

func TestAnnotateTrustedProxy(t *testing.T) {
	if err := SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	defer SetTrustedProxies(nil) //nolint:errcheck

	t.Run("preserves trust after RemoteAddr rewrite", func(t *testing.T) {
		var got bool
		inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			// Simulate RealIP middleware overwriting RemoteAddr.
			r.RemoteAddr = "194.230.146.14:12345"
			got = WasFromTrustedProxy(r)
		})
		handler := AnnotateTrustedProxy(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if !got {
			t.Error("WasFromTrustedProxy should be true after AnnotateTrustedProxy, even with rewritten RemoteAddr")
		}
	})

	t.Run("untrusted peer stays false", func(t *testing.T) {
		var got bool
		inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = WasFromTrustedProxy(r)
		})
		handler := AnnotateTrustedProxy(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.1:9999"
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if got {
			t.Error("WasFromTrustedProxy should be false for untrusted peer")
		}
	})

	t.Run("fallback without middleware", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		if !WasFromTrustedProxy(req) {
			t.Error("WasFromTrustedProxy should fall back to IsRequestFromTrustedProxy")
		}
	})
}

func TestLoginProtectionMiddleware(t *testing.T) {
	cfg := testLoginProtectionConfig(5, 1*time.Minute, 1*time.Minute)
	// Middleware only exercises the IP rate limiter, no lockout DB needed.
	lp := NewLoginProtection(nil, cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := lp.Middleware()
	wrapped := middleware(handler)

	// GET request should pass through
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET request status = %d, want %d", rr.Code, http.StatusOK)
	}

	// POST request should pass through (within rate limit)
	req = httptest.NewRequest(http.MethodPost, "/login", nil)
	rr = httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("POST request status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestLoginProtectionCheckIPRateLimit(t *testing.T) {
	cfg := LoginProtectionConfig{
		IPRateLimit:       10, // High rate for quick testing
		IPBurst:           5,  // Lower burst to test rate limiting
		MaxFailedAttempts: 5,
		LockoutDuration:   1 * time.Minute,
		AttemptWindow:     1 * time.Minute,
	}
	// IP rate limiter is in-memory only, no lockout DB needed.
	lp := NewLoginProtection(nil, cfg)
	ip := "192.168.1.100"

	// First several requests should be allowed (within burst)
	for i := 0; i < 5; i++ {
		if !lp.CheckIPRateLimit(ip) {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}
}
