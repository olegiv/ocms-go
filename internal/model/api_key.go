// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package model defines domain models and types used throughout the application.
package model

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
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

// APIKeyPrefixLength is the number of characters used for the key prefix.
// Reduced from 8 to 4 to preserve more entropy in the key.
const APIKeyPrefixLength = 4

// GenerateAPIKey generates a new random API key.
// Returns the raw key (to show user once) and the key prefix.
func GenerateAPIKey() (rawKey string, prefix string, err error) {
	// Generate 32 random bytes (256 bits of entropy)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}

	// Encode as base64 URL-safe string
	rawKey = base64.URLEncoding.EncodeToString(bytes)

	// Use first 4 characters as prefix (reduced from 8 to preserve entropy)
	prefix = rawKey[:APIKeyPrefixLength]

	return rawKey, prefix, nil
}

// HashAPIKey creates an Argon2id hash of the API key for storage.
// Returns encoded hash string in format: $argon2id$v=19$m=65536,t=1,p=4$salt$hash
func HashAPIKey(key string) (string, error) {
	return auth.HashArgon2(key)
}

// CheckAPIKeyHash verifies an API key against its Argon2id hash.
// Uses constant-time comparison to prevent timing attacks.
func CheckAPIKeyHash(key, encodedHash string) bool {
	ok, _ := auth.VerifyArgon2(key, encodedHash)
	return ok
}

// ExtractAPIKeyPrefix extracts the prefix from a raw API key.
func ExtractAPIKeyPrefix(rawKey string) string {
	if len(rawKey) < APIKeyPrefixLength {
		return rawKey
	}
	return rawKey[:APIKeyPrefixLength]
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
