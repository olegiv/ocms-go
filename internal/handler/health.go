// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"syscall"
	"time"
)

// HealthHandler handles health check requests.
type HealthHandler struct {
	db         *sql.DB
	uploadsDir string
	startTime  time.Time
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(db *sql.DB, uploadsDir string) *HealthHandler {
	return &HealthHandler{
		db:         db,
		uploadsDir: uploadsDir,
		startTime:  time.Now(),
	}
}

// HealthStatus represents the overall health status.
type HealthStatus struct {
	Status    string           `json:"status"`
	Timestamp time.Time        `json:"timestamp"`
	Uptime    string           `json:"uptime"`
	Version   string           `json:"version"`
	Checks    map[string]Check `json:"checks"`
	System    *SystemInfo      `json:"system,omitempty"`
}

// Check represents a single health check result.
type Check struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// SystemInfo contains system-level information.
type SystemInfo struct {
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutines"`
	NumCPU       int    `json:"num_cpus"`
	MemAlloc     string `json:"mem_alloc"`
	MemSys       string `json:"mem_sys"`
}

// Health handles GET /health requests.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	status := HealthStatus{
		Timestamp: time.Now().UTC(),
		Uptime:    time.Since(h.startTime).Round(time.Second).String(),
		Version:   "1.0.0",
		Checks:    make(map[string]Check),
	}

	// Check database connectivity
	dbCheck := h.checkDatabase()
	status.Checks["database"] = dbCheck

	// Check disk space for uploads
	diskCheck := h.checkDiskSpace()
	status.Checks["disk"] = diskCheck

	// Determine overall status
	allHealthy := true
	for _, check := range status.Checks {
		if check.Status != "healthy" {
			allHealthy = false
			break
		}
	}

	if allHealthy {
		status.Status = "healthy"
	} else {
		status.Status = "degraded"
	}

	// Include system info if requested via query param
	if r.URL.Query().Get("verbose") == "true" {
		status.System = h.getSystemInfo()
	}

	w.Header().Set("Content-Type", "application/json")

	if status.Status != "healthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	_ = json.NewEncoder(w).Encode(status)
}

// Liveness handles GET /health/live - simple liveness check.
func (h *HealthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "alive",
	})
}

// Readiness handles GET /health/ready - checks if the service is ready to accept traffic.
func (h *HealthHandler) Readiness(w http.ResponseWriter, _ *http.Request) {
	// Check database
	dbCheck := h.checkDatabase()

	w.Header().Set("Content-Type", "application/json")

	if dbCheck.Status == "healthy" {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "not_ready",
			"message": dbCheck.Message,
		})
	}
}

// checkDatabase verifies database connectivity.
func (h *HealthHandler) checkDatabase() Check {
	start := time.Now()

	err := h.db.Ping()
	latency := time.Since(start)

	if err != nil {
		return Check{
			Status:  "unhealthy",
			Message: err.Error(),
			Latency: latency.String(),
		}
	}

	return Check{
		Status:  "healthy",
		Message: "Connected",
		Latency: latency.String(),
	}
}

// checkDiskSpace checks available disk space in the uploads directory.
func (h *HealthHandler) checkDiskSpace() Check {
	// Ensure uploads directory exists
	if _, err := os.Stat(h.uploadsDir); os.IsNotExist(err) {
		// Directory doesn't exist, but that's okay - it will be created when needed
		return Check{
			Status:  "healthy",
			Message: "Uploads directory does not exist yet",
		}
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.uploadsDir, &stat); err != nil {
		return Check{
			Status:  "unhealthy",
			Message: "Failed to check disk space: " + err.Error(),
		}
	}

	// Calculate available space in bytes
	availableBytes := stat.Bavail * uint64(stat.Bsize)

	// Convert to human-readable format
	available := formatBytes(availableBytes)

	// Warn if less than 100MB available
	const minSpace = 100 * 1024 * 1024 // 100MB
	if availableBytes < minSpace {
		return Check{
			Status:  "degraded",
			Message: "Low disk space: " + available + " available",
		}
	}

	return Check{
		Status:  "healthy",
		Message: available + " available",
	}
}

// getSystemInfo returns system-level metrics.
func (h *HealthHandler) getSystemInfo() *SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &SystemInfo{
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		NumCPU:       runtime.NumCPU(),
		MemAlloc:     formatBytes(m.Alloc),
		MemSys:       formatBytes(m.Sys),
	}
}

// formatBytes converts bytes to a human-readable string.
func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
