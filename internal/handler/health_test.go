// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// createTestAPIKey creates an API key in the test DB and returns the raw key string.
func createTestAPIKey(t *testing.T, h *HealthHandler) string {
	t.Helper()

	rawKey, keyPrefix, err := model.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey failed: %v", err)
	}

	keyHash, err := model.HashAPIKey(rawKey)
	if err != nil {
		t.Fatalf("HashAPIKey failed: %v", err)
	}

	_, err = h.queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Health Test Key",
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Permissions: `["pages:read"]`,
		IsActive:    true,
		CreatedBy:   1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	return rawKey
}

// addAPIKeyAuth adds a Bearer token to the request for authenticated health checks.
func addAPIKeyAuth(r *http.Request, rawKey string) {
	r.Header.Set("Authorization", "Bearer "+rawKey)
}

func TestHealthHandler_Health_Public(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	assertStatus(t, w.Code, http.StatusOK)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}

	// Public response should be minimal
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("status = %v; want healthy", resp["status"])
	}

	// Should NOT contain detailed fields
	if _, ok := resp["uptime"]; ok {
		t.Error("public response should not contain uptime")
	}
	if _, ok := resp["version"]; ok {
		t.Error("public response should not contain version")
	}
	if _, ok := resp["checks"]; ok {
		t.Error("public response should not contain checks")
	}
	if _, ok := resp["timestamp"]; ok {
		t.Error("public response should not contain timestamp")
	}
}

func TestHealthHandler_Health_Public_Verbose(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health?verbose=true", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	// Public response should still be minimal even with verbose=true
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if _, ok := resp["system"]; ok {
		t.Error("public response should not contain system info even with verbose=true")
	}
}

func TestHealthHandler_Health_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	tests := []struct {
		name           string
		queryVerbose   bool
		wantSystemInfo bool
	}{
		{
			name: "full details without verbose",
		},
		{
			name:           "full details with verbose",
			queryVerbose:   true,
			wantSystemInfo: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/health"
			if tt.queryVerbose {
				path += "?verbose=true"
			}

			req := httptest.NewRequest(http.MethodGet, path, nil)
			addAPIKeyAuth(req, rawKey)
			w := httptest.NewRecorder()

			handler.Health(w, req)

			assertStatus(t, w.Code, http.StatusOK)

			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q; want application/json", ct)
			}

			var resp HealthStatus
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if resp.Status != "healthy" {
				t.Errorf("status = %q; want healthy", resp.Status)
			}

			// Authenticated response should include detailed fields
			if resp.Timestamp.IsZero() {
				t.Error("timestamp should not be zero")
			}
			if resp.Uptime == "" {
				t.Error("uptime should not be empty")
			}
			if resp.Version == "" {
				t.Error("version should not be empty")
			}

			if dbCheck, ok := resp.Checks["database"]; ok {
				if dbCheck.Status != "healthy" {
					t.Errorf("database check status = %q; want healthy", dbCheck.Status)
				}
			} else {
				t.Error("expected database check in response")
			}

			if tt.wantSystemInfo && resp.System == nil {
				t.Error("expected system info in response")
			}
			if !tt.wantSystemInfo && resp.System != nil {
				t.Error("unexpected system info in response")
			}
		})
	}
}

func TestHealthHandler_Health_UnhealthyDatabase_Public(t *testing.T) {
	db := testDB(t)
	handler := NewHealthHandler(db, nil, t.TempDir())

	_ = db.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	assertStatus(t, w.Code, http.StatusServiceUnavailable)

	// Public response should be minimal even when degraded
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "degraded" {
		t.Errorf("status = %v; want degraded", resp["status"])
	}

	// Should NOT expose check details
	if _, ok := resp["checks"]; ok {
		t.Error("public degraded response should not contain checks")
	}
}

func TestHealthHandler_Health_UnhealthyDatabase_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	// Close DB after creating the API key
	_ = handler.db.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	addAPIKeyAuth(req, rawKey)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	assertStatus(t, w.Code, http.StatusServiceUnavailable)

	// Authenticated callers can't auth when DB is closed, so they get public response
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "degraded" {
		t.Errorf("status = %v; want degraded", resp["status"])
	}
}

// testHealthProbe tests a health probe endpoint for expected status response.
func testHealthProbe(t *testing.T, path string, handlerFn func(http.ResponseWriter, *http.Request), expectedStatus string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()

	handlerFn(w, req)

	assertStatus(t, w.Code, http.StatusOK)

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != expectedStatus {
		t.Errorf("status = %q; want %s", resp["status"], expectedStatus)
	}
}

// testNotReadyProbe tests the readiness probe with a closed database and returns the response.
func testNotReadyProbe(t *testing.T, handler *HealthHandler, rawKey string) map[string]string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	if rawKey != "" {
		addAPIKeyAuth(req, rawKey)
	}
	w := httptest.NewRecorder()

	handler.Readiness(w, req)

	assertStatus(t, w.Code, http.StatusServiceUnavailable)

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "not_ready" {
		t.Errorf("status = %q; want not_ready", resp["status"])
	}

	return resp
}

func TestHealthHandler_Liveness(t *testing.T) {
	handler := newTestHealthHandler(t)
	testHealthProbe(t, "/health/live", handler.Liveness, "alive")
}

func TestHealthHandler_Readiness(t *testing.T) {
	handler := newTestHealthHandler(t)
	testHealthProbe(t, "/health/ready", handler.Readiness, "ready")
}

func TestHealthHandler_Readiness_NotReady_Public(t *testing.T) {
	db := testDB(t)
	handler := NewHealthHandler(db, nil, t.TempDir())

	_ = db.Close()

	resp := testNotReadyProbe(t, handler, "")

	// Public response should NOT contain error message
	if _, ok := resp["message"]; ok {
		t.Error("public not_ready response should not contain error message")
	}
}

func TestHealthHandler_Readiness_NotReady_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	// Close DB after creating the API key
	_ = handler.db.Close()

	// When DB is closed, API key validation also fails, so auth falls through
	// This is expected: when the DB is down, authentication can't be verified
	_ = testNotReadyProbe(t, handler, rawKey)
}

func TestHealthHandler_DiskCheck_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	tests := []struct {
		name        string
		setupDir    func(t *testing.T) string
		wantHealthy bool
	}{
		{
			name: "healthy with existing directory",
			setupDir: func(t *testing.T) string {
				return t.TempDir()
			},
			wantHealthy: true,
		},
		{
			name: "healthy with non-existent directory",
			setupDir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			wantHealthy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler.uploadsDir = tt.setupDir(t)

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			addAPIKeyAuth(req, rawKey)
			w := httptest.NewRecorder()

			handler.Health(w, req)

			var resp HealthStatus
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			diskCheck, ok := resp.Checks["disk"]
			if !ok {
				t.Fatal("expected disk check in response")
			}

			if tt.wantHealthy && diskCheck.Status != "healthy" {
				t.Errorf("disk check status = %q; want healthy", diskCheck.Status)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1572864, "1.50 MB"},
		{1073741824, "1.00 GB"},
		{1610612736, "1.50 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q; want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestHealthHandler_SystemInfo_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/health?verbose=true", nil)
	addAPIKeyAuth(req, rawKey)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	var resp HealthStatus
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.System == nil {
		t.Fatal("expected system info in response")
	}

	if resp.System.GoVersion == "" {
		t.Error("go_version should not be empty")
	}
	if resp.System.NumGoroutine <= 0 {
		t.Error("num_goroutines should be positive")
	}
	if resp.System.NumCPU <= 0 {
		t.Error("num_cpus should be positive")
	}
	if resp.System.MemAlloc == "" {
		t.Error("mem_alloc should not be empty")
	}
	if resp.System.MemSys == "" {
		t.Error("mem_sys should not be empty")
	}
}

func TestNewHealthHandler(t *testing.T) {
	db := testDB(t)
	sm := testSessionManager(t)
	uploadsDir := t.TempDir()

	handler := NewHealthHandler(db, sm, uploadsDir)

	if handler == nil {
		t.Fatal("NewHealthHandler returned nil")
	}
	if handler.db != db {
		t.Error("db not set correctly")
	}
	if handler.sm != sm {
		t.Error("sm not set correctly")
	}
	if handler.queries == nil {
		t.Error("queries should not be nil")
	}
	if handler.uploadsDir != uploadsDir {
		t.Error("uploadsDir not set correctly")
	}
	if handler.startTime.IsZero() {
		t.Error("startTime should not be zero")
	}
}

func TestNewHealthHandler_NilSessionManager(t *testing.T) {
	db := testDB(t)
	uploadsDir := t.TempDir()

	handler := NewHealthHandler(db, nil, uploadsDir)

	if handler == nil {
		t.Fatal("NewHealthHandler returned nil")
	}
	if handler.sm != nil {
		t.Error("sm should be nil when passed nil")
	}
}

func TestHealthHandler_UptimeCalculation_Authenticated(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	addAPIKeyAuth(req, rawKey)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	var resp HealthStatus
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Uptime == "" {
		t.Error("uptime should not be empty")
	}

	// Uptime should be very short (just created)
	if resp.Uptime != "0s" && len(resp.Uptime) > 10 {
		t.Logf("uptime = %s (expected very short duration)", resp.Uptime)
	}
}

func TestHealthHandler_EnvUploadsDir(t *testing.T) {
	db := testDB(t)

	tempDir := t.TempDir()
	t.Setenv("UPLOADS_DIR", tempDir)

	handler := NewHealthHandler(db, nil, os.Getenv("UPLOADS_DIR"))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	assertStatus(t, w.Code, http.StatusOK)
}

func TestHealthHandler_IsAuthenticated_InvalidBearerToken(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer invalid-key-that-does-not-exist")
	w := httptest.NewRecorder()

	handler.Health(w, req)

	// Should get public (minimal) response since the key is invalid
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if _, ok := resp["uptime"]; ok {
		t.Error("invalid bearer token should not grant access to detailed response")
	}
}

func TestHealthHandler_IsAuthenticated_MalformedAuth(t *testing.T) {
	handler := newTestHealthHandler(t)

	tests := []struct {
		name   string
		header string
	}{
		{name: "empty bearer", header: "Bearer "},
		{name: "basic auth", header: "Basic dXNlcjpwYXNz"},
		{name: "no space", header: "Bearertoken123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Header.Set("Authorization", tt.header)
			w := httptest.NewRecorder()

			handler.Health(w, req)

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if _, ok := resp["uptime"]; ok {
				t.Errorf("malformed auth %q should not grant access to detailed response", tt.header)
			}
		})
	}
}
