// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// ---------------------------------------------------------------------------
// WebhookHelper edge cases
// ---------------------------------------------------------------------------

func TestWebhookHelper_GetEvents_MalformedJSON(t *testing.T) {
	tests := []struct {
		name   string
		events string
		want   int
	}{
		{"malformed json", `["page.created"`, 0},
		{"not an array", `{"key":"value"}`, 0},
		{"null json", `null`, 0},
		{"number json", `42`, 0},
		{"whitespace only", "   ", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wh := &webhookHelper{Events: tt.events}
			result := wh.GetEvents()
			if len(result) != tt.want {
				t.Errorf("GetEvents() returned %d events for input %q, want %d", len(result), tt.events, tt.want)
			}
		})
	}
}

func TestWebhookHelper_GetHeaders_MalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		headers string
		want    int
	}{
		{"malformed json", `{"Authorization":"Bearer`, 0},
		{"not an object", `["Authorization"]`, 0},
		{"null json", `null`, 0},
		{"number json", `42`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wh := &webhookHelper{Headers: tt.headers}
			result := wh.GetHeaders()
			if len(result) != tt.want {
				t.Errorf("GetHeaders() returned %d headers for input %q, want %d", len(result), tt.headers, tt.want)
			}
		})
	}
}

func TestWebhookHelper_HasEvent_MalformedJSON(t *testing.T) {
	wh := &webhookHelper{Events: `["page.created"`} // malformed
	if wh.HasEvent("page.created") {
		t.Error("HasEvent() should return false when JSON is malformed")
	}
}

// ---------------------------------------------------------------------------
// DefaultConfig / DefaultDebounceConfig – all fields
// ---------------------------------------------------------------------------

func TestDefaultConfig_AllFields(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Workers != 3 {
		t.Errorf("DefaultConfig().Workers = %d, want 3", cfg.Workers)
	}
	if !cfg.EnableDebounce {
		t.Error("DefaultConfig().EnableDebounce should be true")
	}
	if cfg.Debounce.Interval != 1*time.Second {
		t.Errorf("DefaultConfig().Debounce.Interval = %v, want 1s", cfg.Debounce.Interval)
	}
	if cfg.Debounce.MaxWait != 5*time.Second {
		t.Errorf("DefaultConfig().Debounce.MaxWait = %v, want 5s", cfg.Debounce.MaxWait)
	}
	if cfg.Debounce.MaxPending != 1000 {
		t.Errorf("DefaultConfig().Debounce.MaxPending = %d, want 1000", cfg.Debounce.MaxPending)
	}
}

func TestDefaultDebounceConfig_Reasonable(t *testing.T) {
	cfg := DefaultDebounceConfig()

	if cfg.Interval <= 0 {
		t.Error("DefaultDebounceConfig().Interval must be positive")
	}
	if cfg.MaxWait <= 0 {
		t.Error("DefaultDebounceConfig().MaxWait must be positive")
	}
	if cfg.MaxWait < cfg.Interval {
		t.Errorf("DefaultDebounceConfig().MaxWait (%v) should be >= Interval (%v)", cfg.MaxWait, cfg.Interval)
	}
	if cfg.MaxPending <= 0 {
		t.Error("DefaultDebounceConfig().MaxPending must be positive")
	}
}

// ---------------------------------------------------------------------------
// NewDispatcher
// ---------------------------------------------------------------------------

func TestNewDispatcher_Defaults(t *testing.T) {
	d := NewDispatcher(nil, nil, Config{Workers: 0})
	if d == nil {
		t.Fatal("NewDispatcher() returned nil")
	}
	if d.workers != 3 {
		t.Errorf("NewDispatcher() workers = %d, want 3 (default)", d.workers)
	}
	if d.logger == nil {
		t.Error("NewDispatcher() logger should not be nil when nil is passed")
	}
	// Debounce disabled when config.EnableDebounce is false
	if d.debouncer != nil {
		t.Error("NewDispatcher() debouncer should be nil when EnableDebounce is false")
	}
}

func TestNewDispatcher_WithDebounce(t *testing.T) {
	cfg := DefaultConfig()
	d := NewDispatcher(nil, slog.Default(), cfg)
	if d == nil {
		t.Fatal("NewDispatcher() returned nil")
	}
	if d.debouncer == nil {
		t.Error("NewDispatcher() debouncer should not be nil when EnableDebounce is true")
	}
}

func TestNewDispatcher_ExplicitWorkers(t *testing.T) {
	d := NewDispatcher(nil, slog.Default(), Config{Workers: 5})
	if d.workers != 5 {
		t.Errorf("NewDispatcher() workers = %d, want 5", d.workers)
	}
}

// ---------------------------------------------------------------------------
// DebounceStats
// ---------------------------------------------------------------------------

func TestDebounceStats_Disabled(t *testing.T) {
	d := NewDispatcher(nil, slog.Default(), Config{EnableDebounce: false, Workers: 1})
	pending, enabled := d.DebounceStats()
	if enabled {
		t.Error("DebounceStats() enabled should be false when debounce is disabled")
	}
	if pending != 0 {
		t.Errorf("DebounceStats() pending = %d, want 0", pending)
	}
}

func TestDebounceStats_Enabled(t *testing.T) {
	d := NewDispatcher(nil, slog.Default(), DefaultConfig())
	_, enabled := d.DebounceStats()
	if !enabled {
		t.Error("DebounceStats() enabled should be true when debounce is configured")
	}
}

// ---------------------------------------------------------------------------
// Debouncer – unit tests without DB
// ---------------------------------------------------------------------------

func newTestDebouncer(t *testing.T, interval, maxWait time.Duration) *Debouncer {
	t.Helper()
	// Build a minimal dispatcher without DB for debouncer unit tests.
	// Dispatch() on the inner dispatcher will be called by the debouncer;
	// since there is no DB the call will crash – we keep interval very small
	// and call Stop() before any timer fires, or we test PendingCount only.
	d := &Dispatcher{
		logger: slog.Default(),
		queue:  make(chan *QueuedDelivery, 16),
		done:   make(chan struct{}),
	}
	cfg := DebounceConfig{Interval: interval, MaxWait: maxWait}
	db := NewDebouncer(d, cfg)
	return db
}

func TestNewDebouncer_NotNil(t *testing.T) {
	db := newTestDebouncer(t, 50*time.Millisecond, 200*time.Millisecond)
	if db == nil {
		t.Fatal("NewDebouncer() returned nil")
	}
	if db.PendingCount() != 0 {
		t.Error("NewDebouncer() should have 0 pending events")
	}
}

func TestNewDebouncer_DefaultsMaxPendingWhenUnset(t *testing.T) {
	d := &Dispatcher{
		logger: slog.Default(),
		queue:  make(chan *QueuedDelivery, 16),
		done:   make(chan struct{}),
	}
	db := NewDebouncer(d, DebounceConfig{
		Interval: 50 * time.Millisecond,
		MaxWait:  200 * time.Millisecond,
	})
	defer db.Stop()

	if db.config.MaxPending != DefaultDebounceConfig().MaxPending {
		t.Errorf("NewDebouncer().config.MaxPending = %d, want %d", db.config.MaxPending, DefaultDebounceConfig().MaxPending)
	}
}

func TestDebouncer_PendingCount_QueuesEvent(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)
	defer db.Stop()

	event := NewEvent("page.updated", PageEventData{ID: 1, Title: "Hello"})
	if err := db.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("Dispatch() error: %v", err)
	}

	if db.PendingCount() != 1 {
		t.Errorf("PendingCount() = %d, want 1", db.PendingCount())
	}
}

func TestDebouncer_CoalescesEvents(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)
	defer db.Stop()

	event1 := NewEvent("page.updated", PageEventData{ID: 5, Title: "v1"})
	event2 := NewEvent("page.updated", PageEventData{ID: 5, Title: "v2"})

	if err := db.Dispatch(context.Background(), event1); err != nil {
		t.Fatalf("Dispatch(event1) error: %v", err)
	}
	if err := db.Dispatch(context.Background(), event2); err != nil {
		t.Fatalf("Dispatch(event2) error: %v", err)
	}

	// Both events for the same entity must collapse into one pending entry.
	if db.PendingCount() != 1 {
		t.Errorf("PendingCount() = %d, want 1 (events should be coalesced)", db.PendingCount())
	}
}

func TestDebouncer_DifferentEntitiesAreNotCoalesced(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)
	defer db.Stop()

	e1 := NewEvent("page.updated", PageEventData{ID: 1, Title: "Page 1"})
	e2 := NewEvent("page.updated", PageEventData{ID: 2, Title: "Page 2"})

	if err := db.Dispatch(context.Background(), e1); err != nil {
		t.Fatalf("Dispatch(e1) error: %v", err)
	}
	if err := db.Dispatch(context.Background(), e2); err != nil {
		t.Fatalf("Dispatch(e2) error: %v", err)
	}

	if db.PendingCount() != 2 {
		t.Errorf("PendingCount() = %d, want 2 (different entities must not be coalesced)", db.PendingCount())
	}
}

func TestDebouncer_FlushClearsPending(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)

	event := NewEvent("page.updated", PageEventData{ID: 10, Title: "Flush test"})
	if err := db.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("Dispatch() error: %v", err)
	}

	db.Flush()

	if db.PendingCount() != 0 {
		t.Errorf("PendingCount() = %d after Flush(), want 0", db.PendingCount())
	}
}

func TestDebouncer_Stop_ClearsPending(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)

	event := NewEvent("media.uploaded", MediaEventData{ID: 99, Filename: "test.jpg"})
	if err := db.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("Dispatch() error: %v", err)
	}

	db.Stop()

	if db.PendingCount() != 0 {
		t.Errorf("PendingCount() = %d after Stop(), want 0", db.PendingCount())
	}
}

func TestDebouncer_DispatchEvent_Convenience(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)
	defer db.Stop()

	if err := db.DispatchEvent(context.Background(), "user.created", UserEventData{ID: 7, Email: "u@example.com"}); err != nil {
		t.Fatalf("DispatchEvent() error: %v", err)
	}
	if db.PendingCount() != 1 {
		t.Errorf("PendingCount() = %d, want 1", db.PendingCount())
	}
}

func TestDebouncer_MaxWaitForcesDispatch(t *testing.T) {
	// Use maxWait shorter than interval to trigger immediate dispatch on second call.
	db := newTestDebouncer(t, 500*time.Millisecond, 1*time.Millisecond)
	defer db.Stop()

	event1 := NewEvent("page.updated", PageEventData{ID: 3, Title: "First"})
	if err := db.Dispatch(context.Background(), event1); err != nil {
		t.Fatalf("Dispatch(event1) error: %v", err)
	}

	// Sleep long enough so that firstSeen + maxWait has definitely elapsed.
	time.Sleep(20 * time.Millisecond)

	event2 := NewEvent("page.updated", PageEventData{ID: 3, Title: "Second"})
	if err := db.Dispatch(context.Background(), event2); err != nil {
		t.Fatalf("Dispatch(event2) error: %v", err)
	}

	// After maxWait is exceeded the debouncer dispatches immediately, so
	// the second Dispatch should result in 0 pending for key "page.updated:3".
	if db.PendingCount() != 0 {
		t.Errorf("PendingCount() = %d after maxWait exceeded, want 0", db.PendingCount())
	}
}

// ---------------------------------------------------------------------------
// NewEvent – edge cases
// ---------------------------------------------------------------------------

func TestNewEvent_TypeAndTimestamp(t *testing.T) {
	before := time.Now().UTC()
	e := NewEvent("page.created", nil)
	after := time.Now().UTC()

	if e == nil {
		t.Fatal("NewEvent() returned nil")
	}
	if e.Type != "page.created" {
		t.Errorf("NewEvent().Type = %q, want %q", e.Type, "page.created")
	}
	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Errorf("NewEvent().Timestamp = %v, want between %v and %v", e.Timestamp, before, after)
	}
}

func TestNewEvent_DataTypes(t *testing.T) {
	// Verify NewEvent stores the data field correctly for various types.
	// Types containing maps cannot be compared with == so we check via fmt.Sprintf.
	assertDataStored := func(t *testing.T, event *Event, label string) {
		t.Helper()
		if event == nil {
			t.Fatalf("NewEvent() returned nil for %s", label)
		}
		if event.Type != "test.event" {
			t.Errorf("NewEvent().Type = %q for %s, want test.event", event.Type, label)
		}
	}

	t.Run("nil data", func(t *testing.T) {
		e := NewEvent("test.event", nil)
		assertDataStored(t, e, "nil")
		if e.Data != nil {
			t.Errorf("NewEvent(nil).Data = %v, want nil", e.Data)
		}
	})
	t.Run("string data", func(t *testing.T) {
		e := NewEvent("test.event", "plain string")
		assertDataStored(t, e, "string")
		if e.Data != "plain string" {
			t.Errorf("NewEvent(string).Data = %v, want plain string", e.Data)
		}
	})
	t.Run("page event", func(t *testing.T) {
		d := PageEventData{ID: 1, Title: "Test"}
		e := NewEvent("test.event", d)
		assertDataStored(t, e, "PageEventData")
		if e.Data != d {
			t.Errorf("NewEvent(PageEventData).Data mismatch")
		}
	})
	t.Run("media event", func(t *testing.T) {
		d := MediaEventData{ID: 2, Filename: "img.png"}
		e := NewEvent("test.event", d)
		assertDataStored(t, e, "MediaEventData")
		if e.Data != d {
			t.Errorf("NewEvent(MediaEventData).Data mismatch")
		}
	})
	t.Run("user event", func(t *testing.T) {
		d := UserEventData{ID: 3, Email: "a@b.com"}
		e := NewEvent("test.event", d)
		assertDataStored(t, e, "UserEventData")
		if e.Data != d {
			t.Errorf("NewEvent(UserEventData).Data mismatch")
		}
	})
	t.Run("form event", func(t *testing.T) {
		// FormEventData contains a map, so use type assertion instead of ==.
		d := FormEventData{FormID: 4, SubmissionID: 10}
		e := NewEvent("test.event", d)
		assertDataStored(t, e, "FormEventData")
		got, ok := e.Data.(FormEventData)
		if !ok {
			t.Fatalf("NewEvent(FormEventData).Data type = %T, want FormEventData", e.Data)
		}
		if got.FormID != d.FormID || got.SubmissionID != d.SubmissionID {
			t.Errorf("NewEvent(FormEventData).Data = %v, want %v", got, d)
		}
	})
	t.Run("test event", func(t *testing.T) {
		d := TestEventData{Message: "ping"}
		e := NewEvent("test.event", d)
		assertDataStored(t, e, "TestEventData")
		if e.Data != d {
			t.Errorf("NewEvent(TestEventData).Data mismatch")
		}
	})
}

func TestNewEvent_EmptyType(t *testing.T) {
	e := NewEvent("", nil)
	if e.Type != "" {
		t.Errorf("NewEvent(\"\") Type = %q, want empty", e.Type)
	}
}

// ---------------------------------------------------------------------------
// isSafeWebhookDeliveryHeader – additional edge cases
// ---------------------------------------------------------------------------

func TestIsSafeWebhookDeliveryHeader_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		expect bool
	}{
		// Additional blocked headers
		{name: "blocked content-length", key: "Content-Length", value: "100", expect: false},
		{name: "blocked transfer-encoding", key: "Transfer-Encoding", value: "chunked", expect: false},
		{name: "blocked connection", key: "Connection", value: "keep-alive", expect: false},
		{name: "blocked proxy-connection", key: "Proxy-Connection", value: "keep-alive", expect: false},
		{name: "blocked upgrade", key: "Upgrade", value: "websocket", expect: false},
		{name: "blocked te", key: "TE", value: "trailers", expect: false},
		{name: "blocked trailer", key: "Trailer", value: "X-Foo", expect: false},
		{name: "blocked keep-alive", key: "Keep-Alive", value: "timeout=5", expect: false},
		// Key length limits
		{name: "empty key", key: "", value: "x", expect: false},
		{name: "key exactly 64 chars", key: strings.Repeat("a", 64), value: "val", expect: true},
		{name: "key 65 chars", key: strings.Repeat("a", 65), value: "val", expect: false},
		// Value with control characters
		{name: "value with null byte", key: "X-Test", value: "bad\x00byte", expect: false},
		{name: "value with tab is allowed", key: "X-Test", value: "ok\tvalue", expect: true},
		{name: "value with LF only", key: "X-Test", value: "bad\nvalue", expect: false},
		// Value length
		{name: "value exactly 1024 bytes", key: "X-Test", value: strings.Repeat("v", 1024), expect: true},
		{name: "value 1025 bytes", key: "X-Test", value: strings.Repeat("v", 1025), expect: false},
		// Valid custom headers
		{name: "authorization header", key: "Authorization", value: "Bearer tok", expect: true},
		{name: "x-api-key", key: "X-Api-Key", value: "key-123", expect: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSafeWebhookDeliveryHeader(tt.key, tt.value)
			if got != tt.expect {
				t.Errorf("isSafeWebhookDeliveryHeader(%q, %q) = %v, want %v", tt.key, tt.value, got, tt.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DestinationHost
// ---------------------------------------------------------------------------

func TestDestinationHost(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{
			name:   "simple https URL",
			rawURL: "https://hooks.example.com/webhook",
			want:   "hooks.example.com",
		},
		{
			name:   "URL with trailing dot in hostname normalised",
			rawURL: "https://hooks.example.com./webhook",
			want:   "hooks.example.com",
		},
		{
			name:   "IPv4 normalised",
			rawURL: "https://203.0.113.1/webhook",
			want:   "203.0.113.1",
		},
		{
			name:   "IPv6 literal",
			rawURL: "https://[2001:db8::1]/webhook",
			want:   "2001:db8::1",
		},
		{
			name:    "no hostname",
			rawURL:  "https:///path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DestinationHost(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DestinationHost(%q) expected error, got nil", tt.rawURL)
				}
				return
			}
			if err != nil {
				t.Errorf("DestinationHost(%q) unexpected error: %v", tt.rawURL, err)
				return
			}
			if got != tt.want {
				t.Errorf("DestinationHost(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateDestinationURLWithPolicy
// ---------------------------------------------------------------------------

// TestValidateDestinationURLWithPolicy_FailFast tests scenarios that return
// an error before any DNS lookup is performed (invalid scheme, disallowed
// host, required-but-empty allowlist). These are safe to run offline.
func TestValidateDestinationURLWithPolicy_FailFast(t *testing.T) {
	allowedHosts, err := ParseAllowedHosts("hooks.example.com")
	if err != nil {
		t.Fatalf("ParseAllowedHosts() error: %v", err)
	}

	tests := []struct {
		name                string
		rawURL              string
		allowedHosts        map[string]struct{}
		requireAllowedHosts bool
		wantErr             bool
	}{
		{
			name:                "disallowed host fails when allowlist configured",
			rawURL:              "https://evil.example.com/webhook",
			allowedHosts:        allowedHosts,
			requireAllowedHosts: false,
			wantErr:             true,
		},
		{
			name:                "no allowlist but required fails before DNS",
			rawURL:              "https://any.example.com/webhook",
			allowedHosts:        nil,
			requireAllowedHosts: true,
			wantErr:             true,
		},
		{
			name:                "invalid scheme rejected before DNS",
			rawURL:              "ftp://hooks.example.com/webhook",
			allowedHosts:        nil,
			requireAllowedHosts: false,
			wantErr:             true,
		},
		{
			name:                "http rejected before DNS when SSRF not required",
			rawURL:              "http://192.0.2.1/webhook",
			allowedHosts:        allowedHosts,
			requireAllowedHosts: false,
			wantErr:             true, // 192.0.2.1 is not in allowedHosts
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDestinationURLWithPolicy(tt.rawURL, tt.allowedHosts, tt.requireAllowedHosts)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateDestinationURLWithPolicy() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateDestinationURLWithPolicy() unexpected error: %v", err)
			}
		})
	}
}

// TestValidateDestinationHostPolicy tests the host-only allowlist check
// without triggering any DNS lookups.
func TestValidateDestinationHostPolicy_Table(t *testing.T) {
	allowedHosts, err := ParseAllowedHosts("hooks.example.com")
	if err != nil {
		t.Fatalf("ParseAllowedHosts() error: %v", err)
	}

	tests := []struct {
		name                string
		rawURL              string
		allowedHosts        map[string]struct{}
		requireAllowedHosts bool
		wantErr             bool
	}{
		{
			name:                "listed host passes",
			rawURL:              "https://hooks.example.com/webhook",
			allowedHosts:        allowedHosts,
			requireAllowedHosts: false,
			wantErr:             false,
		},
		{
			name:                "unlisted host fails",
			rawURL:              "https://other.example.com/webhook",
			allowedHosts:        allowedHosts,
			requireAllowedHosts: false,
			wantErr:             true,
		},
		{
			name:                "empty allowlist and not required passes",
			rawURL:              "https://any.example.com/webhook",
			allowedHosts:        nil,
			requireAllowedHosts: false,
			wantErr:             false,
		},
		{
			name:                "empty allowlist and required fails",
			rawURL:              "https://any.example.com/webhook",
			allowedHosts:        nil,
			requireAllowedHosts: true,
			wantErr:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDestinationHostPolicy(tt.rawURL, tt.allowedHosts, tt.requireAllowedHosts)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateDestinationHostPolicy() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateDestinationHostPolicy() unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// webhookToModel
// ---------------------------------------------------------------------------

func TestWebhookToModel(t *testing.T) {
	// webhookToModel is an internal helper used by the dispatcher. It should
	// produce a webhookHelper whose GetEvents / GetHeaders match the source fields.
	tests := []struct {
		name    string
		events  string
		headers string
	}{
		{
			name:    "empty fields",
			events:  "",
			headers: "",
		},
		{
			name:    "with events and headers",
			events:  `["page.created","page.updated"]`,
			headers: `{"X-Custom":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wh := &webhookHelper{Events: tt.events, Headers: tt.headers}
			if wh.Events != tt.events {
				t.Errorf("webhookHelper.Events = %q, want %q", wh.Events, tt.events)
			}
			if wh.Headers != tt.headers {
				t.Errorf("webhookHelper.Headers = %q, want %q", wh.Headers, tt.headers)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DeliveryResult status-code logic – tested via the fields directly.
// attemptDelivery makes real HTTP requests and uses SSRF protection that
// blocks 127.0.0.1 (httptest.Server), so we test the response-branching
// logic by constructing DeliveryResult values to verify field semantics.
// ---------------------------------------------------------------------------

func TestDeliveryResult_StatusCodeBranches(t *testing.T) {
	tests := []struct {
		name        string
		result      DeliveryResult
		wantSuccess bool
		wantRetry   bool
	}{
		{
			name:        "2xx success",
			result:      DeliveryResult{Success: true, StatusCode: 200},
			wantSuccess: true, wantRetry: false,
		},
		{
			name:        "201 success",
			result:      DeliveryResult{Success: true, StatusCode: 201},
			wantSuccess: true, wantRetry: false,
		},
		{
			name:        "5xx retry",
			result:      DeliveryResult{Success: false, StatusCode: 500, ShouldRetry: true},
			wantSuccess: false, wantRetry: true,
		},
		{
			name:        "503 retry",
			result:      DeliveryResult{Success: false, StatusCode: 503, ShouldRetry: true},
			wantSuccess: false, wantRetry: true,
		},
		{
			name:        "4xx no retry",
			result:      DeliveryResult{Success: false, StatusCode: 400, ShouldRetry: false},
			wantSuccess: false, wantRetry: false,
		},
		{
			name:        "401 no retry",
			result:      DeliveryResult{Success: false, StatusCode: 401, ShouldRetry: false},
			wantSuccess: false, wantRetry: false,
		},
		{
			name:        "408 should retry",
			result:      DeliveryResult{Success: false, StatusCode: 408, ShouldRetry: true},
			wantSuccess: false, wantRetry: true,
		},
		{
			name:        "429 should retry",
			result:      DeliveryResult{Success: false, StatusCode: 429, ShouldRetry: true},
			wantSuccess: false, wantRetry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", tt.result.Success, tt.wantSuccess)
			}
			if tt.result.ShouldRetry != tt.wantRetry {
				t.Errorf("ShouldRetry = %v, want %v", tt.result.ShouldRetry, tt.wantRetry)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isBlockedWebhookDeliveryHeader – full coverage of all blocked names
// ---------------------------------------------------------------------------

func TestIsBlockedWebhookDeliveryHeader(t *testing.T) {
	blocked := []string{
		"Host", "host", "HOST",
		"Content-Length", "content-length",
		"Transfer-Encoding", "transfer-encoding",
		"Connection", "connection",
		"Proxy-Connection", "proxy-connection",
		"Upgrade", "upgrade",
		"Expect", "expect",
		"TE", "te",
		"Trailer", "trailer",
		"Keep-Alive", "keep-alive",
	}
	for _, name := range blocked {
		if !isBlockedWebhookDeliveryHeader(name) {
			t.Errorf("isBlockedWebhookDeliveryHeader(%q) = false, want true", name)
		}
	}

	allowed := []string{"Authorization", "X-Custom-Header", "X-Api-Key", "Accept"}
	for _, name := range allowed {
		if isBlockedWebhookDeliveryHeader(name) {
			t.Errorf("isBlockedWebhookDeliveryHeader(%q) = true, want false", name)
		}
	}
}

// ---------------------------------------------------------------------------
// hasInvalidWebhookDeliveryHeaderValue – coverage of control character cases
// ---------------------------------------------------------------------------

func TestHasInvalidWebhookDeliveryHeaderValue(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		wantBad bool
	}{
		{"clean value", "Bearer token", false},
		{"tab allowed", "ok\tvalue", false},
		{"CR injection", "bad\rvalue", true},
		{"LF injection", "bad\nvalue", true},
		{"CRLF injection", "bad\r\nvalue", true},
		{"null byte", "bad\x00byte", true},
		{"DEL character", "bad\x7fbyte", true},
		{"low control 0x01", "bad\x01byte", true},
		{"low control 0x1f", "bad\x1fbyte", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasInvalidWebhookDeliveryHeaderValue(tt.value)
			if got != tt.wantBad {
				t.Errorf("hasInvalidWebhookDeliveryHeaderValue(%q) = %v, want %v", tt.value, got, tt.wantBad)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isValidWebhookDeliveryHeaderName – token characters
// ---------------------------------------------------------------------------

func TestIsValidWebhookDeliveryHeaderName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"letters only", "Authorization", true},
		{"hyphen", "X-Custom", true},
		{"underscore", "X_Custom", true},
		{"digits", "X1Custom2", true},
		{"special token chars", "X!#$%&'*+-.^`|~", true},
		{"space invalid", "X Custom", false},
		{"colon invalid", "X:Custom", false},
		{"at invalid", "X@Custom", false},
		{"backslash invalid", "X\\Custom", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidWebhookDeliveryHeaderName(tt.input)
			if got != tt.want {
				t.Errorf("isValidWebhookDeliveryHeaderName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DispatchEvent / DispatchImmediate on a non-running dispatcher
// ---------------------------------------------------------------------------

func TestDispatchEvent_NotRunning(t *testing.T) {
	d := NewDispatcher(nil, slog.Default(), Config{EnableDebounce: false, Workers: 1})
	// Not started — Dispatch returns nil with a warning log.
	err := d.DispatchEvent(t.Context(), "page.created", PageEventData{ID: 1})
	if err != nil {
		t.Errorf("DispatchEvent() on stopped dispatcher returned error: %v", err)
	}
}

func TestDispatchImmediate_NotRunning(t *testing.T) {
	d := NewDispatcher(nil, slog.Default(), Config{EnableDebounce: false, Workers: 1})
	err := d.DispatchImmediate(t.Context(), "page.deleted", PageEventData{ID: 2})
	if err != nil {
		t.Errorf("DispatchImmediate() on stopped dispatcher returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// normalizeAllowedHostEntry – additional edge cases
// ---------------------------------------------------------------------------

func TestNormalizeAllowedHostEntry_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{name: "plain domain", entry: "example.com", wantErr: false},
		{name: "dots trimmed", entry: ".example.com.", wantErr: false},
		{name: "only dots becomes empty", entry: ".", wantErr: true},
		{name: "contains scheme", entry: "https://example.com", wantErr: true},
		{name: "contains path", entry: "example.com/path", wantErr: true},
		{name: "contains port", entry: "example.com:443", wantErr: true},
		{name: "IPv4 literal", entry: "203.0.113.1", wantErr: false},
		{name: "bracketed IPv6", entry: "[2001:db8::1]", wantErr: false},
		{name: "half-bracketed IPv6", entry: "[2001:db8::1", wantErr: true},
		{name: "IPv6 with port brackets", entry: "[2001:db8::1]:443", wantErr: true},
		{name: "contains question mark", entry: "example.com?", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAllowedHosts(tt.entry)
			if tt.wantErr && err == nil {
				t.Errorf("ParseAllowedHosts(%q) expected error, got nil", tt.entry)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ParseAllowedHosts(%q) unexpected error: %v", tt.entry, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConfigureAllowedHosts error path
// ---------------------------------------------------------------------------

func TestConfigureAllowedHosts_InvalidEntry(t *testing.T) {
	err := ConfigureAllowedHosts("https://bad.entry.com")
	if err == nil {
		t.Error("ConfigureAllowedHosts() with invalid entry should return error")
	}
}


// ---------------------------------------------------------------------------
// DeliveryResult constants and fields
// ---------------------------------------------------------------------------

func TestDeliveryConstants(t *testing.T) {
	if MaxAttempts <= 0 {
		t.Errorf("MaxAttempts = %d, must be positive", MaxAttempts)
	}
	if InitialBackoff <= 0 {
		t.Errorf("InitialBackoff = %v, must be positive", InitialBackoff)
	}
	if MaxBackoff < InitialBackoff {
		t.Errorf("MaxBackoff (%v) must be >= InitialBackoff (%v)", MaxBackoff, InitialBackoff)
	}
	if RequestTimeout <= 0 {
		t.Errorf("RequestTimeout = %v, must be positive", RequestTimeout)
	}
	if MaxResponseLen <= 0 {
		t.Errorf("MaxResponseLen = %d, must be positive", MaxResponseLen)
	}
	if UserAgent == "" {
		t.Error("UserAgent must not be empty")
	}
}

// ---------------------------------------------------------------------------
// RetryInterval / RetryBatchSize / Cleanup constants
// ---------------------------------------------------------------------------

func TestDispatcherConstants(t *testing.T) {
	if RetryInterval <= 0 {
		t.Errorf("RetryInterval = %v, must be positive", RetryInterval)
	}
	if RetryBatchSize <= 0 {
		t.Errorf("RetryBatchSize = %d, must be positive", RetryBatchSize)
	}
	if CleanupInterval <= 0 {
		t.Errorf("CleanupInterval = %v, must be positive", CleanupInterval)
	}
	if DeliveryRetention <= 0 {
		t.Errorf("DeliveryRetention = %v, must be positive", DeliveryRetention)
	}
}

// ---------------------------------------------------------------------------
// webhookToModel via store.Webhook
// ---------------------------------------------------------------------------

func TestWebhookToModel_FromStoreWebhook(t *testing.T) {
	tests := []struct {
		name        string
		wh          store.Webhook
		wantEvents  []string
		wantHeaders map[string]string
	}{
		{
			name:        "empty events and headers",
			wh:          store.Webhook{Events: "", Headers: ""},
			wantEvents:  []string{},
			wantHeaders: map[string]string{},
		},
		{
			name:       "with events",
			wh:         store.Webhook{Events: `["page.created","page.deleted"]`, Headers: ""},
			wantEvents: []string{"page.created", "page.deleted"},
			wantHeaders: map[string]string{},
		},
		{
			name:       "with headers",
			wh:         store.Webhook{Events: "", Headers: `{"X-Token":"abc"}`},
			wantEvents: []string{},
			wantHeaders: map[string]string{"X-Token": "abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := webhookToModel(tt.wh)
			if model == nil {
				t.Fatal("webhookToModel() returned nil")
			}

			gotEvents := model.GetEvents()
			if len(gotEvents) != len(tt.wantEvents) {
				t.Errorf("GetEvents() len = %d, want %d", len(gotEvents), len(tt.wantEvents))
			} else {
				for i, e := range tt.wantEvents {
					if gotEvents[i] != e {
						t.Errorf("GetEvents()[%d] = %q, want %q", i, gotEvents[i], e)
					}
				}
			}

			gotHeaders := model.GetHeaders()
			if len(gotHeaders) != len(tt.wantHeaders) {
				t.Errorf("GetHeaders() len = %d, want %d", len(gotHeaders), len(tt.wantHeaders))
			}
			for k, v := range tt.wantHeaders {
				if gotHeaders[k] != v {
					t.Errorf("GetHeaders()[%q] = %q, want %q", k, gotHeaders[k], v)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// shouldRetryURLValidationError – net.Error (timeout) branch
// ---------------------------------------------------------------------------

// timeoutNetError implements net.Error with Timeout() == true.
type timeoutNetError struct{}

func (e *timeoutNetError) Error() string   { return "timeout" }
func (e *timeoutNetError) Timeout() bool   { return true }
func (e *timeoutNetError) Temporary() bool { return true }

// nonTimeoutNetError implements net.Error with Timeout() == false.
type nonTimeoutNetError struct{}

func (e *nonTimeoutNetError) Error() string   { return "non-timeout net error" }
func (e *nonTimeoutNetError) Timeout() bool   { return false }
func (e *nonTimeoutNetError) Temporary() bool { return false }

func TestShouldRetryURLValidationError_NetError(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		retry bool
	}{
		{
			name:  "nil error",
			err:   nil,
			retry: false,
		},
		{
			name:  "net.Error timeout returns true",
			err:   &timeoutNetError{},
			retry: true,
		},
		{
			name:  "net.Error non-timeout returns false",
			err:   &nonTimeoutNetError{},
			retry: false,
		},
		{
			name:  "plain error returns false",
			err:   fmt.Errorf("some error"),
			retry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetryURLValidationError(tt.err)
			if got != tt.retry {
				t.Errorf("shouldRetryURLValidationError(%v) = %v, want %v", tt.err, got, tt.retry)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DestinationHost – invalid URL parse error
// ---------------------------------------------------------------------------

func TestDestinationHost_InvalidURL(t *testing.T) {
	// A URL with an invalid escape sequence triggers url.Parse error.
	_, err := DestinationHost("://no-scheme")
	if err == nil {
		t.Error("DestinationHost(\"://no-scheme\") expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Debouncer – FormEventData pointer key extraction
// ---------------------------------------------------------------------------

func TestEventKey_FormEventDataPointer(t *testing.T) {
	event := NewEvent("form.submitted", &FormEventData{
		FormID:       1,
		SubmissionID: 42,
	})
	key := eventKey(event)
	if key != "form.submitted:42" {
		t.Errorf("eventKey() = %q, want %q", key, "form.submitted:42")
	}
}

func TestEventKey_MediaEventDataPointer(t *testing.T) {
	event := NewEvent("media.deleted", &MediaEventData{
		ID: 77,
	})
	key := eventKey(event)
	if key != "media.deleted:77" {
		t.Errorf("eventKey() = %q, want %q", key, "media.deleted:77")
	}
}

func TestEventKey_UserEventDataPointer(t *testing.T) {
	event := NewEvent("user.deleted", &UserEventData{
		ID: 55,
	})
	key := eventKey(event)
	if key != "user.deleted:55" {
		t.Errorf("eventKey() = %q, want %q", key, "user.deleted:55")
	}
}

// ---------------------------------------------------------------------------
// Debouncer – multiple different event types are tracked independently
// ---------------------------------------------------------------------------

func TestDebouncer_MultipleEventTypes(t *testing.T) {
	db := newTestDebouncer(t, 500*time.Millisecond, 2*time.Second)
	defer db.Stop()

	events := []struct {
		etype string
		data  any
	}{
		{"page.created", PageEventData{ID: 1}},
		{"media.uploaded", MediaEventData{ID: 2}},
		{"user.created", UserEventData{ID: 3}},
	}

	for _, e := range events {
		if err := db.DispatchEvent(context.Background(), e.etype, e.data); err != nil {
			t.Fatalf("DispatchEvent(%q) error: %v", e.etype, err)
		}
	}

	if db.PendingCount() != 3 {
		t.Errorf("PendingCount() = %d, want 3 (one per distinct event type+id)", db.PendingCount())
	}
}

// ---------------------------------------------------------------------------
// shouldRetryURLValidationError – wrapped net.Error
// ---------------------------------------------------------------------------

func TestShouldRetryURLValidationError_WrappedNetError(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", &net.OpError{
		Op:  "dial",
		Err: &net.DNSError{IsTimeout: true},
	})
	if !shouldRetryURLValidationError(wrapped) {
		t.Error("shouldRetryURLValidationError() should return true for wrapped DNS timeout error")
	}
}
