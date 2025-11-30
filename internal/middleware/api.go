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

	"ocms-go/internal/model"
	"ocms-go/internal/store"
)

// Context keys for API data.
const (
	ContextKeyAPIKey ContextKey = "api_key"
)

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

// APIKeyAuth creates middleware that validates API key authentication.
// It checks the Authorization header for a Bearer token.
func APIKeyAuth(db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Missing Authorization header", nil)
				return
			}

			// Expect "Bearer <key>" format
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Invalid Authorization header format. Use: Bearer <api_key>", nil)
				return
			}

			rawKey := parts[1]
			if rawKey == "" {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key is empty", nil)
				return
			}

			// Hash the key and look it up
			keyHash := model.HashAPIKey(rawKey)

			apiKey, err := queries.GetAPIKeyByHash(r.Context(), keyHash)
			if err != nil {
				if err == sql.ErrNoRows {
					WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "Invalid API key", nil)
				} else {
					slog.Error("failed to validate API key", "error", err)
					WriteAPIError(w, http.StatusInternalServerError, "internal_error", "Failed to validate API key", nil)
				}
				return
			}

			// Check if key is active
			if !apiKey.IsActive {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key is inactive", nil)
				return
			}

			// Check if key is expired
			if apiKey.ExpiresAt.Valid && time.Now().After(apiKey.ExpiresAt.Time) {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key has expired", nil)
				return
			}

			// Update last used timestamp (fire and forget)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = queries.UpdateAPIKeyLastUsed(ctx, store.UpdateAPIKeyLastUsedParams{
					LastUsedAt: sql.NullTime{Time: time.Now(), Valid: true},
					ID:         apiKey.ID,
				})
			}()

			// Add API key to context
			ctx := context.WithValue(r.Context(), ContextKeyAPIKey, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
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

			// Parse permissions from JSON
			var permissions []string
			if apiKey.Permissions != "" && apiKey.Permissions != "[]" {
				_ = json.Unmarshal([]byte(apiKey.Permissions), &permissions)
			}

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
func RequireAnyPermission(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := GetAPIKey(r)
			if apiKey == nil {
				WriteAPIError(w, http.StatusUnauthorized, "unauthorized", "API key required", nil)
				return
			}

			// Parse permissions from JSON
			var keyPermissions []string
			if apiKey.Permissions != "" && apiKey.Permissions != "[]" {
				_ = json.Unmarshal([]byte(apiKey.Permissions), &keyPermissions)
			}

			// Check if key has any of the required permissions
			hasPermission := false
			for _, required := range permissions {
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

// rateLimiter holds rate limiters per API key.
type rateLimiter struct {
	limiters map[int64]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

// newRateLimiter creates a new rate limiter manager.
func newRateLimiter(rps float64, burst int) *rateLimiter {
	return &rateLimiter{
		limiters: make(map[int64]*rate.Limiter),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
}

// getLimiter returns the rate limiter for a specific API key.
func (rl *rateLimiter) getLimiter(keyID int64) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[keyID]
	rl.mu.RUnlock()

	if exists {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = rl.limiters[keyID]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[keyID] = limiter
	return limiter
}

// APIRateLimit creates middleware that rate limits requests per API key.
// rps is requests per second, burst is the maximum burst size.
func APIRateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	rl := newRateLimiter(rps, burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := GetAPIKey(r)
			if apiKey == nil {
				// No API key - skip rate limiting (or apply global limit)
				next.ServeHTTP(w, r)
				return
			}

			limiter := rl.getLimiter(apiKey.ID)
			if !limiter.Allow() {
				WriteAPIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded. Please slow down.", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GlobalRateLimiter provides a global rate limiter for unauthenticated requests.
type GlobalRateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

// NewGlobalRateLimiter creates a new global rate limiter.
func NewGlobalRateLimiter(rps float64, burst int) *GlobalRateLimiter {
	return &GlobalRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
}

// getLimiter returns the rate limiter for a specific IP.
func (rl *GlobalRateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[ip]
	rl.mu.RUnlock()

	if exists {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = rl.limiters[ip]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[ip] = limiter
	return limiter
}

// Middleware returns the rate limiting middleware.
func (rl *GlobalRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Use X-Real-IP or RemoteAddr for rate limiting
			ip := r.Header.Get("X-Real-IP")
			if ip == "" {
				ip = r.Header.Get("X-Forwarded-For")
				if ip == "" {
					ip = r.RemoteAddr
				}
			}

			limiter := rl.getLimiter(ip)
			if !limiter.Allow() {
				WriteAPIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded. Please slow down.", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
