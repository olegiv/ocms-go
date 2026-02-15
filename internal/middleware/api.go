// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// ContextKeyAPIKey is the context key for API key data.
const ContextKeyAPIKey ContextKey = "api_key"

// APIError represents a JSON error response for the API.
type APIError struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details,omitempty"`
	} `json:"error"`
}

// WriteAPIError writes a JSON error response.
func WriteAPIError(w http.ResponseWriter, statusCode int, code, message string, details map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	apiErr := APIError{}
	apiErr.Error.Code = code
	apiErr.Error.Message = message
	apiErr.Error.Details = details

	_ = json.NewEncoder(w).Encode(apiErr)
}

// validateAPIKey parses the Authorization header and validates the API key.
// Returns the API key if valid, or nil if not.
// If required is true and validation fails, writes an error response and returns (nil, true).
// The second return value indicates if an error response was written.
func validateAPIKey(w http.ResponseWriter, r *http.Request, queries *store.Queries, required bool) (*store.ApiKey, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Missing Authorization header", nil)
			return nil, true
		}
		return nil, false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Invalid Authorization header format. Use: Bearer <api_key>", nil)
			return nil, true
		}
		return nil, false
	}

	rawKey := parts[1]
	if rawKey == "" {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key is empty", nil)
			return nil, true
		}
		return nil, false
	}

	// Look up API keys by prefix (since Argon2 hashes are salted, we can't query by hash)
	prefix := model.ExtractAPIKeyPrefix(rawKey)
	apiKeys, err := queries.GetAPIKeysByPrefix(r.Context(), prefix)
	if err != nil {
		if required {
			slog.Error("failed to query API keys by prefix", "error", err)
			WriteAPIError(w, http.StatusInternalServerError, "internal_error", "Failed to validate API key", nil)
			return nil, true
		}
		return nil, false
	}

	if len(apiKeys) == 0 {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Invalid API key", nil)
			return nil, true
		}
		return nil, false
	}

	// Find matching key by verifying hash
	var matchedKey *store.ApiKey
	for i := range apiKeys {
		if model.CheckAPIKeyHash(rawKey, apiKeys[i].KeyHash) {
			matchedKey = &apiKeys[i]
			break
		}
	}

	if matchedKey == nil {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Invalid API key", nil)
			return nil, true
		}
		return nil, false
	}

	if !matchedKey.IsActive {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key is inactive", nil)
			return nil, true
		}
		return nil, false
	}

	if matchedKey.ExpiresAt.Valid && time.Now().After(matchedKey.ExpiresAt.Time) {
		if required {
			WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key has expired", nil)
			return nil, true
		}
		return nil, false
	}

	return matchedKey, false
}

// APIKeyAuth creates middleware that validates API key authentication.
// It checks the Authorization header for a Bearer token.
func APIKeyAuth(db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, errorWritten := validateAPIKey(w, r, queries, true)
			if errorWritten {
				return
			}

			updateAPIKeyLastUsed(queries, apiKey.ID)
			addAPIKeyToContext(next, w, r, *apiKey)
		})
	}
}

// GetAPIKey retrieves the API key from the request context.
// Returns nil if no API key is in context.
func GetAPIKey(r *http.Request) *store.ApiKey {
	apiKey, ok := r.Context().Value(ContextKeyAPIKey).(store.ApiKey)
	if !ok {
		return nil
	}
	return &apiKey
}

// ParseAPIKeyPermissions parses the JSON permissions string from an API key.
// Returns an empty slice if the permissions string is empty or invalid.
func ParseAPIKeyPermissions(apiKey *store.ApiKey) []string {
	if apiKey == nil || apiKey.Permissions == "" || apiKey.Permissions == "[]" {
		return nil
	}
	var permissions []string
	_ = json.Unmarshal([]byte(apiKey.Permissions), &permissions)
	return permissions
}

// updateAPIKeyLastUsed updates the last used timestamp in a background goroutine.
func updateAPIKeyLastUsed(queries *store.Queries, keyID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = queries.UpdateAPIKeyLastUsed(ctx, store.UpdateAPIKeyLastUsedParams{
			LastUsedAt: sql.NullTime{Time: time.Now(), Valid: true},
			ID:         keyID,
		})
	}()
}

// addAPIKeyToContext adds the API key to context and serves the next handler.
func addAPIKeyToContext(next http.Handler, w http.ResponseWriter, r *http.Request, apiKey store.ApiKey) {
	ctx := context.WithValue(r.Context(), ContextKeyAPIKey, apiKey)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// OptionalAPIKeyAuth creates middleware that optionally validates API key authentication.
// Unlike APIKeyAuth, this middleware does not require authentication - it simply
// adds the API key to context if a valid one is provided.
func OptionalAPIKeyAuth(db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, _ := validateAPIKey(w, r, queries, false)
			if apiKey == nil {
				next.ServeHTTP(w, r)
				return
			}

			updateAPIKeyLastUsed(queries, apiKey.ID)
			addAPIKeyToContext(next, w, r, *apiKey)
		})
	}
}

// RequirePermission creates middleware that requires a specific API permission.
// This should be used after APIKeyAuth middleware.
func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := GetAPIKey(r)
			if apiKey == nil {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key required", nil)
				return
			}

			permissions := ParseAPIKeyPermissions(apiKey)

			// Check if key has required permission
			hasPermission := false
			for _, p := range permissions {
				if p == permission {
					hasPermission = true
					break
				}
			}

			if !hasPermission {
				WriteAPIError(w, http.StatusForbidden, "forbidden", "API key lacks required permission: "+permission, nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAnyPermission creates middleware that requires any one of the specified permissions.
// This should be used after APIKeyAuth middleware.
func RequireAnyPermission(requiredPerms ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := GetAPIKey(r)
			if apiKey == nil {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key required", nil)
				return
			}

			keyPermissions := ParseAPIKeyPermissions(apiKey)

			// Check if key has any of the required permissions
			hasPermission := false
			for _, required := range requiredPerms {
				for _, kp := range keyPermissions {
					if kp == required {
						hasPermission = true
						break
					}
				}
				if hasPermission {
					break
				}
			}

			if !hasPermission {
				WriteAPIError(w, http.StatusForbidden, "forbidden", "API key lacks required permissions", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// limiterEntry holds a rate limiter and its last access time for TTL eviction.
type limiterEntry struct {
	limiter  *rate.Limiter
	lastUsed time.Time
}

// limiterCacheTTL is the time after which idle entries are evicted.
const limiterCacheTTL = 10 * time.Minute

// limiterCacheCleanupInterval is how often the cleanup goroutine runs.
const limiterCacheCleanupInterval = 5 * time.Minute

// limiterCache is a generic rate limiter cache with TTL-based eviction.
type limiterCache[K comparable] struct {
	entries map[K]*limiterEntry
	mu      sync.RWMutex
	rate    rate.Limit
	burst   int
}

// newLimiterCache creates a new limiter cache with periodic TTL-based cleanup.
func newLimiterCache[K comparable](rps float64, burst int) *limiterCache[K] {
	lc := &limiterCache[K]{
		entries: make(map[K]*limiterEntry),
		rate:    rate.Limit(rps),
		burst:   burst,
	}
	go lc.cleanupLoop()
	return lc
}

// get returns the rate limiter for a specific key, creating one if needed.
func (lc *limiterCache[K]) get(key K) *rate.Limiter {
	now := time.Now()

	lc.mu.RLock()
	entry, exists := lc.entries[key]
	lc.mu.RUnlock()

	if exists {
		lc.mu.Lock()
		entry.lastUsed = now
		lc.mu.Unlock()
		return entry.limiter
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists = lc.entries[key]; exists {
		entry.lastUsed = now
		return entry.limiter
	}

	limiter := rate.NewLimiter(lc.rate, lc.burst)
	lc.entries[key] = &limiterEntry{
		limiter:  limiter,
		lastUsed: now,
	}
	return limiter
}

// cleanupLoop periodically evicts entries that haven't been used within the TTL.
func (lc *limiterCache[K]) cleanupLoop() {
	ticker := time.NewTicker(limiterCacheCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		lc.evictExpired()
	}
}

// evictExpired removes entries older than limiterCacheTTL.
func (lc *limiterCache[K]) evictExpired() {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	cutoff := time.Now().Add(-limiterCacheTTL)
	for key, entry := range lc.entries {
		if entry.lastUsed.Before(cutoff) {
			delete(lc.entries, key)
		}
	}
}

// APIRateLimit creates middleware that rate limits requests per API key.
// rps is requests per second, burst is the maximum burst size.
func APIRateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	cache := newLimiterCache[int64](rps, burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := GetAPIKey(r)
			if apiKey == nil {
				// No API key - skip rate limiting (or apply global limit)
				next.ServeHTTP(w, r)
				return
			}

			if !cache.get(apiKey.ID).Allow() {
				WriteAPIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded. Please slow down.", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GlobalRateLimiter provides a global rate limiter for unauthenticated requests.
type GlobalRateLimiter struct {
	cache *limiterCache[string]
}

// NewGlobalRateLimiter creates a new global rate limiter.
func NewGlobalRateLimiter(rps float64, burst int) *GlobalRateLimiter {
	return &GlobalRateLimiter{
		cache: newLimiterCache[string](rps, burst),
	}
}

// Middleware returns the rate limiting middleware for API routes (returns JSON errors).
func (rl *GlobalRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r)
			if !rl.cache.get(ip).Allow() {
				WriteAPIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded. Please slow down.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HTMLMiddleware returns the rate limiting middleware for public routes (returns plain text errors).
// This is suitable for login and other public HTML form endpoints.
func (rl *GlobalRateLimiter) HTMLMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r)
			if !rl.cache.get(ip).Allow() {
				slog.Warn("public rate limit exceeded", "ip", ip, "path", r.URL.Path)
				http.Error(w, "Too many requests. Please wait a moment and try again.", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
