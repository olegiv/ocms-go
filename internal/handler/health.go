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
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// HealthHandler handles health check requests.
type HealthHandler struct {
	db         *sql.DB
	queries    *store.Queries
	sm         *scs.SessionManager
	uploadsDir string
	startTime  time.Time
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(db *sql.DB, sm *scs.SessionManager, uploadsDir string) *HealthHandler {
	return &HealthHandler{
		db:         db,
		queries:    store.New(db),
		sm:         sm,
		uploadsDir: uploadsDir,
		startTime:  time.Now(),
	}
}

// StartTime returns when the handler (and application) was started.
func (h *HealthHandler) StartTime() time.Time {
	return h.startTime
}

// HealthStatusPublic is the minimal health response for unauthenticated callers.
type HealthStatusPublic struct {
	Status string `json:"status"`
}

// HealthStatus represents the overall health status (authenticated callers only).
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
// Returns minimal status for unauthenticated callers, full details for authenticated ones.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	dbCheck := h.checkDatabase()
	diskCheck := h.checkDiskSpace()

	allHealthy := dbCheck.Status == "healthy" && diskCheck.Status == "healthy"

	overallStatus := "healthy"
	if !allHealthy {
		overallStatus = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")

	if overallStatus != "healthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	// Unauthenticated callers get minimal response
	if !h.isAuthenticated(r) {
		_ = json.NewEncoder(w).Encode(HealthStatusPublic{
			Status: overallStatus,
		})
		return
	}

	// Authenticated non-admin: basic response without system info or check details
	if !h.isAdmin(r) {
		_ = json.NewEncoder(w).Encode(HealthStatus{
			Status:    overallStatus,
			Timestamp: time.Now().UTC(),
			Uptime:    time.Since(h.startTime).Round(time.Second).String(),
			Version:   "1.0.0",
		})
		return
	}

	// Admin only: full details including checks and optional system info
	status := HealthStatus{
		Status:    overallStatus,
		Timestamp: time.Now().UTC(),
		Uptime:    time.Since(h.startTime).Round(time.Second).String(),
		Version:   "1.0.0",
		Checks: map[string]Check{
			"database": dbCheck,
			"disk":     diskCheck,
		},
	}

	if r.URL.Query().Get("verbose") == "true" {
		status.System = h.getSystemInfo()
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
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	dbCheck := h.checkDatabase()

	w.Header().Set("Content-Type", "application/json")

	if dbCheck.Status == "healthy" {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		resp := map[string]string{
			"status": "not_ready",
		}
		// Only include error details for authenticated callers
		if h.isAuthenticated(r) {
			resp["message"] = dbCheck.Message
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// isAuthenticated checks if the request comes from an authenticated admin/editor
// session or a valid API key holder.
func (h *HealthHandler) isAuthenticated(r *http.Request) bool {
	// Check session-based auth (admin/editor users).
	// SCS panics if session data is not loaded into context, so recover gracefully.
	if h.sm != nil {
		if h.checkSessionAuth(r) {
			return true
		}
	}

	// Check API key auth (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			rawKey := parts[1]
			prefix := model.ExtractAPIKeyPrefix(rawKey)
			apiKeys, err := h.queries.GetAPIKeysByPrefix(r.Context(), prefix)
			if err == nil && len(apiKeys) > 0 {
				for i := range apiKeys {
					if model.CheckAPIKeyHash(rawKey, apiKeys[i].KeyHash) {
						if apiKeys[i].IsActive {
							if !apiKeys[i].ExpiresAt.Valid || !time.Now().After(apiKeys[i].ExpiresAt.Time) {
								return true
							}
						}
						break
					}
				}
			}
		}
	}

	return false
}

// checkSessionAuth checks if the request has a valid admin/editor session.
// Returns false (without panicking) if session data is not loaded into context.
func (h *HealthHandler) checkSessionAuth(r *http.Request) (authenticated bool) {
	defer func() {
		if rec := recover(); rec != nil {
			authenticated = false
		}
	}()

	userID := h.sm.GetInt64(r.Context(), SessionKeyUserID)
	if userID > 0 {
		user, err := h.queries.GetUserByID(r.Context(), userID)
		if err == nil && (user.Role == RoleAdmin || user.Role == RoleEditor) {
			return true
		}
	}
	return false
}

// isAdmin checks if the request comes from an authenticated admin user.
// Returns false for editors, API key holders, and unauthenticated callers.
func (h *HealthHandler) isAdmin(r *http.Request) bool {
	if h.sm == nil {
		return false
	}
	return h.checkSessionRole(r, RoleAdmin)
}

// checkSessionRole checks if the request has a valid session with the given role.
// Returns false (without panicking) if session data is not loaded into context.
func (h *HealthHandler) checkSessionRole(r *http.Request, role string) (hasRole bool) {
	defer func() {
		if rec := recover(); rec != nil {
			hasRole = false
		}
	}()

	userID := h.sm.GetInt64(r.Context(), SessionKeyUserID)
	if userID > 0 {
		user, err := h.queries.GetUserByID(r.Context(), userID)
		if err == nil && user.Role == role {
			return true
		}
	}
	return false
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
