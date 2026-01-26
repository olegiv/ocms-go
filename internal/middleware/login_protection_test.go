// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	lp := NewLoginProtection(cfg)

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
	lp := NewLoginProtection(cfg)

	if lp.maxFailedAttempts != 5 {
		t.Errorf("maxFailedAttempts = %d, want 5 (default)", lp.maxFailedAttempts)
	}
	if lp.lockoutDuration != 15*time.Minute {
		t.Errorf("lockoutDuration = %v, want 15m (default)", lp.lockoutDuration)
	}
}

func TestLoginProtectionIsAccountLocked(t *testing.T) {
	cfg := testLoginProtectionConfig(3, 1*time.Second, 1*time.Minute)
	lp := NewLoginProtection(cfg)
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
	lp := NewLoginProtection(cfg)
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
	lp := NewLoginProtection(cfg)
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
	lp := NewLoginProtection(cfg)
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
	lp := NewLoginProtection(cfg)
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
	lp := NewLoginProtection(cfg)
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

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xForwarded string
		xRealIP    string
		want       string
	}{
		{
			name:       "simple remote addr",
			remoteAddr: "192.168.1.1:12345",
			want:       "192.168.1.1",
		},
		{
			name:       "X-Forwarded-For single",
			remoteAddr: "127.0.0.1:8080",
			xForwarded: "10.0.0.1",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For multiple",
			remoteAddr: "127.0.0.1:8080",
			xForwarded: "10.0.0.1, 10.0.0.2, 10.0.0.3",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "127.0.0.1:8080",
			xRealIP:    "10.0.0.5",
			want:       "10.0.0.5",
		},
		{
			name:       "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr: "127.0.0.1:8080",
			xForwarded: "10.0.0.1",
			xRealIP:    "10.0.0.5",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For with spaces",
			remoteAddr: "127.0.0.1:8080",
			xForwarded: "  10.0.0.1  ",
			want:       "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xForwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwarded)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}

			got := GetClientIP(req)
			if got != tt.want {
				t.Errorf("GetClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoginProtectionMiddleware(t *testing.T) {
	cfg := testLoginProtectionConfig(5, 1*time.Minute, 1*time.Minute)
	lp := NewLoginProtection(cfg)

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
	lp := NewLoginProtection(cfg)
	ip := "192.168.1.100"

	// First several requests should be allowed (within burst)
	for i := 0; i < 5; i++ {
		if !lp.CheckIPRateLimit(ip) {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}
}
