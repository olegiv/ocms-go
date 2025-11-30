// Package model defines domain models and types used throughout the application.
package model

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"
)

// API permissions
const (
	PermissionPagesRead     = "pages:read"
	PermissionPagesWrite    = "pages:write"
	PermissionMediaRead     = "media:read"
	PermissionMediaWrite    = "media:write"
	PermissionTaxonomyRead  = "taxonomy:read"
	PermissionTaxonomyWrite = "taxonomy:write"
)

// AllPermissions returns all available API permissions.
func AllPermissions() []string {
	return []string{
		PermissionPagesRead,
		PermissionPagesWrite,
		PermissionMediaRead,
		PermissionMediaWrite,
		PermissionTaxonomyRead,
		PermissionTaxonomyWrite,
	}
}

// APIKey represents an API authentication key.
type APIKey struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	KeyHash     string       `json:"-"` // Never expose hash in JSON
	KeyPrefix   string       `json:"key_prefix"`
	Permissions string       `json:"-"` // JSON array stored as string
	LastUsedAt  sql.NullTime `json:"last_used_at,omitempty"`
	ExpiresAt   sql.NullTime `json:"expires_at,omitempty"`
	IsActive    bool         `json:"is_active"`
	CreatedBy   int64        `json:"created_by"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// GenerateAPIKey generates a new random API key.
// Returns the raw key (to show user once) and the key prefix.
func GenerateAPIKey() (rawKey string, prefix string, err error) {
	// Generate 32 random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}

	// Encode as base64
	rawKey = base64.URLEncoding.EncodeToString(bytes)

	// Get prefix (first 8 characters)
	prefix = rawKey[:8]

	return rawKey, prefix, nil
}

// HashAPIKey creates a SHA-256 hash of the API key for storage.
func HashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// GetPermissions parses the JSON permissions string into a slice.
func (k *APIKey) GetPermissions() []string {
	var perms []string
	if k.Permissions == "" || k.Permissions == "[]" {
		return perms
	}
	_ = json.Unmarshal([]byte(k.Permissions), &perms)
	return perms
}

// HasPermission checks if the API key has a specific permission.
func (k *APIKey) HasPermission(perm string) bool {
	for _, p := range k.GetPermissions() {
		if p == perm {
			return true
		}
	}
	return false
}

// HasAnyPermission checks if the API key has any of the specified permissions.
func (k *APIKey) HasAnyPermission(perms ...string) bool {
	keyPerms := k.GetPermissions()
	for _, perm := range perms {
		for _, kp := range keyPerms {
			if kp == perm {
				return true
			}
		}
	}
	return false
}

// IsExpired checks if the API key has expired.
func (k *APIKey) IsExpired() bool {
	if !k.ExpiresAt.Valid {
		return false // No expiration set
	}
	return time.Now().After(k.ExpiresAt.Time)
}

// IsValid checks if the API key is active and not expired.
func (k *APIKey) IsValid() bool {
	return k.IsActive && !k.IsExpired()
}

// PermissionsToJSON converts a slice of permissions to a JSON string.
func PermissionsToJSON(perms []string) string {
	if len(perms) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(perms)
	return string(data)
}
