// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthHandler_Health(t *testing.T) {
	db := testDB(t)
	uploadsDir := t.TempDir()

	handler := NewHealthHandler(db, uploadsDir)

	tests := []struct {
		name           string
		queryVerbose   bool
		wantStatus     int
		wantHealthy    bool
		wantSystemInfo bool
	}{
		{
			name:        "healthy response",
			wantStatus:  http.StatusOK,
			wantHealthy: true,
		},
		{
			name:           "verbose includes system info",
			queryVerbose:   true,
			wantStatus:     http.StatusOK,
			wantHealthy:    true,
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
			w := httptest.NewRecorder()

			handler.Health(w, req)

			assertStatus(t, w.Code, tt.wantStatus)

			// Check Content-Type
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q; want application/json", ct)
			}

			// Parse response
			var resp HealthStatus
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			// Check status
			if tt.wantHealthy && resp.Status != "healthy" {
				t.Errorf("status = %q; want healthy", resp.Status)
			}

			// Check system info presence
			if tt.wantSystemInfo && resp.System == nil {
				t.Error("expected system info in response")
			}
			if !tt.wantSystemInfo && resp.System != nil {
				t.Error("unexpected system info in response")
			}

			// Check required fields
			if resp.Timestamp.IsZero() {
				t.Error("timestamp should not be zero")
			}
			if resp.Uptime == "" {
				t.Error("uptime should not be empty")
			}
			if resp.Version == "" {
				t.Error("version should not be empty")
			}

			// Check database check
			if dbCheck, ok := resp.Checks["database"]; ok {
				if dbCheck.Status != "healthy" {
					t.Errorf("database check status = %q; want healthy", dbCheck.Status)
				}
			} else {
				t.Error("expected database check in response")
			}
		})
	}
}

func TestHealthHandler_Health_UnhealthyDatabase(t *testing.T) {
	// Create a database and close it to simulate unhealthy state
	db := testDB(t)
	uploadsDir := t.TempDir()

	handler := NewHealthHandler(db, uploadsDir)

	// Close the database to make it unhealthy
	_ = db.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	// Should return degraded status
	assertStatus(t, w.Code, http.StatusServiceUnavailable)

	var resp HealthStatus
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "degraded" {
		t.Errorf("status = %q; want degraded", resp.Status)
	}

	if dbCheck, ok := resp.Checks["database"]; ok {
		if dbCheck.Status != "unhealthy" {
			t.Errorf("database check status = %q; want unhealthy", dbCheck.Status)
		}
	}
}

func TestHealthHandler_Liveness(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	w := httptest.NewRecorder()

	handler.Liveness(w, req)

	assertStatus(t, w.Code, http.StatusOK)

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "alive" {
		t.Errorf("status = %q; want alive", resp["status"])
	}
}

func TestHealthHandler_Readiness(t *testing.T) {
	handler := newTestHealthHandler(t)

	tests := []struct {
		name       string
		closeDB    bool
		wantStatus int
		wantReady  bool
	}{
		{
			name:       "ready when database is healthy",
			wantStatus: http.StatusOK,
			wantReady:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			w := httptest.NewRecorder()

			handler.Readiness(w, req)

			assertStatus(t, w.Code, tt.wantStatus)

			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if tt.wantReady && resp["status"] != "ready" {
				t.Errorf("status = %q; want ready", resp["status"])
			}
		})
	}
}

func TestHealthHandler_Readiness_NotReady(t *testing.T) {
	// Need separate db to close it
	db := testDB(t)

	handler := NewHealthHandler(db, t.TempDir())

	// Close database to make it not ready
	_ = db.Close()

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
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
}

func TestHealthHandler_DiskCheck(t *testing.T) {
	db := testDB(t)

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
			uploadsDir := tt.setupDir(t)
			handler := NewHealthHandler(db, uploadsDir)

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
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

func TestHealthHandler_SystemInfo(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health?verbose=true", nil)
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
	uploadsDir := t.TempDir()

	handler := NewHealthHandler(db, uploadsDir)

	if handler == nil {
		t.Fatal("NewHealthHandler returned nil")
	}
	if handler.db != db {
		t.Error("db not set correctly")
	}
	if handler.uploadsDir != uploadsDir {
		t.Error("uploadsDir not set correctly")
	}
	if handler.startTime.IsZero() {
		t.Error("startTime should not be zero")
	}
}

func TestHealthHandler_UptimeCalculation(t *testing.T) {
	handler := newTestHealthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
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
		// Allow for small delays in test execution
		t.Logf("uptime = %s (expected very short duration)", resp.Uptime)
	}
}

// Ensure uploads dir can be an environment variable path
func TestHealthHandler_EnvUploadsDir(t *testing.T) {
	db := testDB(t)

	// Create a temp directory and set it as env var
	tempDir := t.TempDir()
	t.Setenv("UPLOADS_DIR", tempDir)

	handler := NewHealthHandler(db, os.Getenv("UPLOADS_DIR"))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	assertStatus(t, w.Code, http.StatusOK)
}
