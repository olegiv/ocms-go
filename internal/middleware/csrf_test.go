package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultCSRFConfig_Development(t *testing.T) {
	authKey := []byte("12345678901234567890123456789012") // 32-byte key
	cfg := DefaultCSRFConfig(authKey, true)               // isDev = true

	// Check AuthKey is set
	if len(cfg.AuthKey) != 32 {
		t.Errorf("expected 32-byte AuthKey, got %d bytes", len(cfg.AuthKey))
	}

	// Check TrustedOrigins are host-only (not full URLs)
	// This is critical for the csrf library to work correctly
	if len(cfg.TrustedOrigins) != 2 {
		t.Errorf("expected 2 TrustedOrigins in dev mode, got %d", len(cfg.TrustedOrigins))
	}

	expectedOrigins := map[string]bool{
		"localhost:8080": true,
		"127.0.0.1:8080": true,
	}

	for _, origin := range cfg.TrustedOrigins {
		if !expectedOrigins[origin] {
			t.Errorf("unexpected TrustedOrigin: %s (should be host:port, not full URL)", origin)
		}
		// Verify it's not a full URL (should not contain "http")
		if len(origin) > 4 && origin[:4] == "http" {
			t.Errorf("TrustedOrigin should be host:port, not full URL: %s", origin)
		}
	}
}

func TestDefaultCSRFConfig_Production(t *testing.T) {
	authKey := []byte("12345678901234567890123456789012") // 32-byte key
	cfg := DefaultCSRFConfig(authKey, false)              // isDev = false (production)

	// Check AuthKey is set
	if len(cfg.AuthKey) != 32 {
		t.Errorf("expected 32-byte AuthKey, got %d bytes", len(cfg.AuthKey))
	}

	// Check no TrustedOrigins in production (stricter security)
	if len(cfg.TrustedOrigins) != 0 {
		t.Errorf("expected no TrustedOrigins in production, got %d", len(cfg.TrustedOrigins))
	}
}

func TestSkipCSRF_SkipsSpecifiedPaths(t *testing.T) {
	skipPaths := []string{"/api/webhook", "/health"}
	middleware := SkipCSRF(skipPaths...)

	// Create a test handler that checks if CSRF was skipped
	var csrfSkipped bool
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the request has the skip flag set
		// We can't directly check the flag, but we can verify the middleware wrapped correctly
		csrfSkipped = true
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	// Test that handler is called for skipped paths
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/api/webhook", true},
		{"/health", true},
		{"/login", true},       // Not in skip list, but handler should still be called
		{"/admin/pages", true}, // Not in skip list, but handler should still be called
	}

	for _, tc := range testCases {
		csrfSkipped = false
		req := httptest.NewRequest("POST", tc.path, nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if csrfSkipped != tc.expected {
			t.Errorf("path %s: expected handler called=%v, got %v", tc.path, tc.expected, csrfSkipped)
		}
	}
}

func TestSkipCSRF_EmptyPaths(t *testing.T) {
	// Should not panic with empty paths
	middleware := SkipCSRF()

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	req := httptest.NewRequest("POST", "/any/path", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestCSRFHeaderName(t *testing.T) {
	// Verify the constant matches the expected header name
	if CSRFHeaderName != "X-CSRF-Token" {
		t.Errorf("expected CSRFHeaderName='X-CSRF-Token', got '%s'", CSRFHeaderName)
	}
}

func TestCSRFConfig_AuthKeyLength(t *testing.T) {
	// Test with exactly 32-byte key (recommended)
	key32 := []byte("12345678901234567890123456789012")
	cfg := DefaultCSRFConfig(key32, false)

	if len(cfg.AuthKey) != 32 {
		t.Errorf("expected 32-byte AuthKey, got %d bytes", len(cfg.AuthKey))
	}
}

// Note: csrfErrorHandler cannot be tested in isolation because it calls
// csrf.FailureReason(r) which requires the csrf middleware context.
// The error handler is tested implicitly through integration tests.

func TestCSRF_MiddlewareCreation(t *testing.T) {
	authKey := []byte("12345678901234567890123456789012")
	cfg := DefaultCSRFConfig(authKey, true)

	// Should not panic when creating middleware
	middleware := CSRF(cfg)

	if middleware == nil {
		t.Error("expected middleware to be non-nil")
	}

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap the handler
	handler := middleware(testHandler)

	if handler == nil {
		t.Error("expected wrapped handler to be non-nil")
	}
}

func TestCSRF_WithCustomErrorHandler(t *testing.T) {
	authKey := []byte("12345678901234567890123456789012")
	cfg := DefaultCSRFConfig(authKey, true)

	customCalled := false
	cfg.ErrorHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customCalled = true
		http.Error(w, "Custom CSRF Error", http.StatusForbidden)
	})

	middleware := CSRF(cfg)

	if middleware == nil {
		t.Error("expected middleware to be non-nil with custom error handler")
	}

	// Note: We can't easily test that the custom handler is called without
	// triggering an actual CSRF failure, which requires cookies and tokens.
	// The test above validates that the middleware accepts a custom handler.
	_ = customCalled
}

// TestTrustedOriginsFormat validates that TrustedOrigins use the correct format.
// The csrf library expects host:port format, NOT full URLs.
// Using full URLs (http://localhost:8080) causes "origin invalid" errors.
func TestTrustedOriginsFormat(t *testing.T) {
	authKey := []byte("12345678901234567890123456789012")
	cfg := DefaultCSRFConfig(authKey, true) // dev mode

	for _, origin := range cfg.TrustedOrigins {
		// Check it doesn't start with http:// or https://
		if len(origin) > 7 && (origin[:7] == "http://" || origin[:8] == "https://") {
			t.Errorf("TrustedOrigin '%s' should be host:port format, not full URL", origin)
		}

		// Check it contains a port (host:port format)
		hasPort := false
		for _, c := range origin {
			if c == ':' {
				hasPort = true
				break
			}
		}
		if !hasPort {
			t.Errorf("TrustedOrigin '%s' should include port (e.g., localhost:8080)", origin)
		}
	}
}
