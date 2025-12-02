package webhook

import (
	"testing"
	"time"
)

func TestGenerateSignature(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		secret   string
		expected string
	}{
		{
			name:     "empty payload",
			payload:  []byte{},
			secret:   "secret",
			expected: "f9e66e179b6747ae54108f82f8ade8b3c25d76fd30afde6c395822c530196169",
		},
		{
			name:     "simple payload",
			payload:  []byte(`{"event":"test"}`),
			secret:   "mysecret",
			expected: "7d073b7b9f70c7f5e2e1fcb74c7e9f76f6e16c47e0d7e22f0b39c2a5c0e55f78",
		},
		{
			name:     "complex payload",
			payload:  []byte(`{"type":"page.created","timestamp":"2024-01-01T00:00:00Z","data":{"id":123,"title":"Test Page"}}`),
			secret:   "webhook-secret-key",
			expected: "0c9d3cde9d5c6b5c5a3e5c1c3b1e0a8f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b",
		},
		{
			name:     "empty secret",
			payload:  []byte(`test`),
			secret:   "",
			expected: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateSignature(tt.payload, tt.secret)
			// Verify it's a valid hex string of 64 characters (SHA256 = 256 bits = 32 bytes = 64 hex chars)
			if len(result) != 64 {
				t.Errorf("GenerateSignature() returned signature with length %d, expected 64", len(result))
			}

			// Verify consistency - same input should always produce same output
			result2 := GenerateSignature(tt.payload, tt.secret)
			if result != result2 {
				t.Errorf("GenerateSignature() not consistent: %s != %s", result, result2)
			}
		})
	}
}

func TestVerifySignature(t *testing.T) {
	tests := []struct {
		name      string
		payload   []byte
		secret    string
		wantValid bool
	}{
		{
			name:      "valid signature",
			payload:   []byte(`{"event":"test"}`),
			secret:    "mysecret",
			wantValid: true,
		},
		{
			name:      "empty payload valid signature",
			payload:   []byte{},
			secret:    "secret",
			wantValid: true,
		},
		{
			name:      "valid with unicode payload",
			payload:   []byte(`{"title":"Тест","content":"日本語"}`),
			secret:    "unicode-secret-ключ",
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate signature with secret
			signature := GenerateSignature(tt.payload, tt.secret)

			// Verify with correct secret
			if valid := VerifySignature(tt.payload, signature, tt.secret); valid != tt.wantValid {
				t.Errorf("VerifySignature() = %v, want %v", valid, tt.wantValid)
			}

			// Verify fails with wrong secret
			if tt.wantValid {
				wrongSig := VerifySignature(tt.payload, signature, "wrong-secret")
				if wrongSig {
					t.Error("VerifySignature() should return false with wrong secret")
				}
			}
		})
	}
}

func TestVerifySignature_InvalidSignature(t *testing.T) {
	payload := []byte(`{"test":"data"}`)
	secret := "mysecret"

	tests := []struct {
		name      string
		signature string
	}{
		{"empty signature", ""},
		{"invalid hex", "not-a-valid-hex-string"},
		{"wrong length", "abc123"},
		{"tampered signature", "0000000000000000000000000000000000000000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if VerifySignature(payload, tt.signature, secret) {
				t.Error("VerifySignature() should return false for invalid signature")
			}
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		name     string
		attempt  int64
		expected time.Duration
	}{
		{"attempt 0", 0, 1 * time.Minute},     // Treated as attempt 1
		{"attempt 1", 1, 1 * time.Minute},     // 1 min * 2^0 = 1 min
		{"attempt 2", 2, 2 * time.Minute},     // 1 min * 2^1 = 2 min
		{"attempt 3", 3, 4 * time.Minute},     // 1 min * 2^2 = 4 min
		{"attempt 4", 4, 8 * time.Minute},     // 1 min * 2^3 = 8 min
		{"attempt 5", 5, 16 * time.Minute},    // 1 min * 2^4 = 16 min
		{"attempt 10", 10, 512 * time.Minute}, // 1 min * 2^9 = 512 min (~8.5 hours)
		{"attempt 15", 15, 24 * time.Hour},    // Would be >24 hours, capped at MaxBackoff
		{"attempt 20", 20, 24 * time.Hour},    // Capped at MaxBackoff
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateBackoff(tt.attempt)
			if result != tt.expected {
				t.Errorf("calculateBackoff(%d) = %v, want %v", tt.attempt, result, tt.expected)
			}
		})
	}
}

func TestCalculateBackoff_NeverExceedsMax(t *testing.T) {
	// Test that no matter how many attempts, backoff never exceeds MaxBackoff
	for attempt := int64(1); attempt <= 100; attempt++ {
		result := calculateBackoff(attempt)
		if result > MaxBackoff {
			t.Errorf("calculateBackoff(%d) = %v, exceeds MaxBackoff %v", attempt, result, MaxBackoff)
		}
	}
}

func TestEventKey(t *testing.T) {
	tests := []struct {
		name     string
		event    *Event
		expected string
	}{
		{
			name: "page event data",
			event: NewEvent("page.created", PageEventData{
				ID:    123,
				Title: "Test Page",
			}),
			expected: "page.created:123",
		},
		{
			name: "page event data pointer",
			event: NewEvent("page.updated", &PageEventData{
				ID:    456,
				Title: "Updated Page",
			}),
			expected: "page.updated:456",
		},
		{
			name: "media event data",
			event: NewEvent("media.uploaded", MediaEventData{
				ID:       789,
				Filename: "test.jpg",
			}),
			expected: "media.uploaded:789",
		},
		{
			name: "user event data",
			event: NewEvent("user.created", UserEventData{
				ID:    100,
				Email: "test@example.com",
			}),
			expected: "user.created:100",
		},
		{
			name: "form event data",
			event: NewEvent("form.submitted", FormEventData{
				SubmissionID: 555,
				FormID:       1,
			}),
			expected: "form.submitted:555",
		},
		{
			name: "map with int64 id",
			event: NewEvent("custom.event", map[string]any{
				"id":   int64(999),
				"name": "Custom",
			}),
			expected: "custom.event:999",
		},
		{
			name: "map with float64 id",
			event: NewEvent("custom.event", map[string]any{
				"id":   float64(888),
				"name": "Custom Float",
			}),
			expected: "custom.event:888",
		},
		{
			name:     "unknown type",
			event:    NewEvent("unknown.event", "string data"),
			expected: "unknown.event",
		},
		{
			name:     "nil data",
			event:    NewEvent("nil.event", nil),
			expected: "nil.event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := eventKey(tt.event)
			if result != tt.expected {
				t.Errorf("eventKey() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestWebhookHelper_GetEvents(t *testing.T) {
	tests := []struct {
		name     string
		events   string
		expected []string
	}{
		{
			name:     "empty string",
			events:   "",
			expected: []string{},
		},
		{
			name:     "empty array",
			events:   "[]",
			expected: []string{},
		},
		{
			name:     "single event",
			events:   `["page.created"]`,
			expected: []string{"page.created"},
		},
		{
			name:     "multiple events",
			events:   `["page.created","page.updated","page.deleted"]`,
			expected: []string{"page.created", "page.updated", "page.deleted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wh := &webhookHelper{Events: tt.events}
			result := wh.GetEvents()
			if len(result) != len(tt.expected) {
				t.Errorf("GetEvents() returned %d events, want %d", len(result), len(tt.expected))
				return
			}
			for i, e := range result {
				if e != tt.expected[i] {
					t.Errorf("GetEvents()[%d] = %q, want %q", i, e, tt.expected[i])
				}
			}
		})
	}
}

func TestWebhookHelper_HasEvent(t *testing.T) {
	wh := &webhookHelper{Events: `["page.created","page.updated","media.uploaded"]`}

	tests := []struct {
		event    string
		expected bool
	}{
		{"page.created", true},
		{"page.updated", true},
		{"media.uploaded", true},
		{"page.deleted", false},
		{"user.created", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			result := wh.HasEvent(tt.event)
			if result != tt.expected {
				t.Errorf("HasEvent(%q) = %v, want %v", tt.event, result, tt.expected)
			}
		})
	}
}

func TestWebhookHelper_GetHeaders(t *testing.T) {
	tests := []struct {
		name     string
		headers  string
		expected map[string]string
	}{
		{
			name:     "empty string",
			headers:  "",
			expected: map[string]string{},
		},
		{
			name:     "empty object",
			headers:  "{}",
			expected: map[string]string{},
		},
		{
			name:    "single header",
			headers: `{"Authorization":"Bearer token123"}`,
			expected: map[string]string{
				"Authorization": "Bearer token123",
			},
		},
		{
			name:    "multiple headers",
			headers: `{"X-Custom-Header":"value1","X-Another":"value2"}`,
			expected: map[string]string{
				"X-Custom-Header": "value1",
				"X-Another":       "value2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wh := &webhookHelper{Headers: tt.headers}
			result := wh.GetHeaders()
			if len(result) != len(tt.expected) {
				t.Errorf("GetHeaders() returned %d headers, want %d", len(result), len(tt.expected))
				return
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("GetHeaders()[%q] = %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Workers != 3 {
		t.Errorf("DefaultConfig().Workers = %d, want 3", cfg.Workers)
	}
	if !cfg.EnableDebounce {
		t.Error("DefaultConfig().EnableDebounce should be true")
	}
}

func TestDefaultDebounceConfig(t *testing.T) {
	cfg := DefaultDebounceConfig()

	if cfg.Interval != 1*time.Second {
		t.Errorf("DefaultDebounceConfig().Interval = %v, want 1s", cfg.Interval)
	}
	if cfg.MaxWait != 5*time.Second {
		t.Errorf("DefaultDebounceConfig().MaxWait = %v, want 5s", cfg.MaxWait)
	}
}

func TestDeliveryResult(t *testing.T) {
	tests := []struct {
		name        string
		result      DeliveryResult
		wantSuccess bool
		wantRetry   bool
	}{
		{
			name: "successful delivery",
			result: DeliveryResult{
				Success:      true,
				StatusCode:   200,
				ResponseBody: "OK",
			},
			wantSuccess: true,
			wantRetry:   false,
		},
		{
			name: "server error should retry",
			result: DeliveryResult{
				Success:     false,
				StatusCode:  500,
				ShouldRetry: true,
			},
			wantSuccess: false,
			wantRetry:   true,
		},
		{
			name: "client error should not retry",
			result: DeliveryResult{
				Success:     false,
				StatusCode:  400,
				ShouldRetry: false,
			},
			wantSuccess: false,
			wantRetry:   false,
		},
		{
			name: "rate limit should retry",
			result: DeliveryResult{
				Success:     false,
				StatusCode:  429,
				ShouldRetry: true,
			},
			wantSuccess: false,
			wantRetry:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Success != tt.wantSuccess {
				t.Errorf("DeliveryResult.Success = %v, want %v", tt.result.Success, tt.wantSuccess)
			}
			if tt.result.ShouldRetry != tt.wantRetry {
				t.Errorf("DeliveryResult.ShouldRetry = %v, want %v", tt.result.ShouldRetry, tt.wantRetry)
			}
		})
	}
}
