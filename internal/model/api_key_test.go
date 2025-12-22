package model

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestGenerateAPIKey(t *testing.T) {
	rawKey, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// Check raw key is not empty and has reasonable length
	if len(rawKey) < 32 {
		t.Errorf("GenerateAPIKey() rawKey length = %d, want >= 32", len(rawKey))
	}

	// Check prefix is 8 characters
	if len(prefix) != 8 {
		t.Errorf("GenerateAPIKey() prefix length = %d, want 8", len(prefix))
	}

	// Check prefix matches start of raw key
	if !strings.HasPrefix(rawKey, prefix) {
		t.Errorf("GenerateAPIKey() prefix %q is not prefix of rawKey %q", prefix, rawKey)
	}

	// Generate another key to ensure uniqueness
	rawKey2, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() second call error = %v", err)
	}
	if rawKey == rawKey2 {
		t.Error("GenerateAPIKey() generated identical keys")
	}
}

func TestHashAPIKey(t *testing.T) {
	key := "test-api-key-12345"
	hash := HashAPIKey(key)

	// Hash should be 64 characters (SHA-256 hex)
	if len(hash) != 64 {
		t.Errorf("HashAPIKey() length = %d, want 64", len(hash))
	}

	// Same input should produce same hash
	hash2 := HashAPIKey(key)
	if hash != hash2 {
		t.Error("HashAPIKey() is not deterministic")
	}

	// Different input should produce different hash
	hash3 := HashAPIKey("different-key")
	if hash == hash3 {
		t.Error("HashAPIKey() produced same hash for different inputs")
	}
}

func TestAPIKeyGetPermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions string
		want        []string
	}{
		{
			name:        "empty string",
			permissions: "",
			want:        []string{},
		},
		{
			name:        "empty array",
			permissions: "[]",
			want:        []string{},
		},
		{
			name:        "single permission",
			permissions: `["pages:read"]`,
			want:        []string{"pages:read"},
		},
		{
			name:        "multiple permissions",
			permissions: `["pages:read","pages:write","media:read"]`,
			want:        []string{"pages:read", "pages:write", "media:read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &APIKey{Permissions: tt.permissions}
			got := k.GetPermissions()
			if len(got) != len(tt.want) {
				t.Errorf("GetPermissions() = %v, want %v", got, tt.want)
				return
			}
			for i, p := range got {
				if p != tt.want[i] {
					t.Errorf("GetPermissions()[%d] = %q, want %q", i, p, tt.want[i])
				}
			}
		})
	}
}

func TestAPIKeyHasPermission(t *testing.T) {
	key := &APIKey{
		Permissions: `["pages:read","pages:write"]`,
	}

	tests := []struct {
		perm string
		want bool
	}{
		{"pages:read", true},
		{"pages:write", true},
		{"media:read", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.perm, func(t *testing.T) {
			if got := key.HasPermission(tt.perm); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.perm, got, tt.want)
			}
		})
	}
}

func TestAPIKeyHasAnyPermission(t *testing.T) {
	key := &APIKey{
		Permissions: `["pages:read","media:write"]`,
	}

	tests := []struct {
		name  string
		perms []string
		want  bool
	}{
		{
			name:  "has first permission",
			perms: []string{"pages:read", "unknown"},
			want:  true,
		},
		{
			name:  "has second permission",
			perms: []string{"unknown", "media:write"},
			want:  true,
		},
		{
			name:  "has none",
			perms: []string{"pages:write", "media:read"},
			want:  false,
		},
		{
			name:  "empty perms",
			perms: []string{},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := key.HasAnyPermission(tt.perms...); got != tt.want {
				t.Errorf("HasAnyPermission(%v) = %v, want %v", tt.perms, got, tt.want)
			}
		})
	}
}

func TestAPIKeyIsExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt sql.NullTime
		want      bool
	}{
		{
			name:      "no expiration",
			expiresAt: sql.NullTime{Valid: false},
			want:      false,
		},
		{
			name:      "expired yesterday",
			expiresAt: sql.NullTime{Time: now.Add(-24 * time.Hour), Valid: true},
			want:      true,
		},
		{
			name:      "expires tomorrow",
			expiresAt: sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &APIKey{ExpiresAt: tt.expiresAt}
			if got := k.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKeyIsValid(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		isActive  bool
		expiresAt sql.NullTime
		want      bool
	}{
		{
			name:      "active, not expired",
			isActive:  true,
			expiresAt: sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
			want:      true,
		},
		{
			name:      "active, no expiration",
			isActive:  true,
			expiresAt: sql.NullTime{Valid: false},
			want:      true,
		},
		{
			name:      "inactive, not expired",
			isActive:  false,
			expiresAt: sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
			want:      false,
		},
		{
			name:      "active, expired",
			isActive:  true,
			expiresAt: sql.NullTime{Time: now.Add(-24 * time.Hour), Valid: true},
			want:      false,
		},
		{
			name:      "inactive, expired",
			isActive:  false,
			expiresAt: sql.NullTime{Time: now.Add(-24 * time.Hour), Valid: true},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &APIKey{IsActive: tt.isActive, ExpiresAt: tt.expiresAt}
			if got := k.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPermissionsToJSON(t *testing.T) {
	tests := []struct {
		name  string
		perms []string
		want  string
	}{
		{
			name:  "empty",
			perms: []string{},
			want:  "[]",
		},
		{
			name:  "nil",
			perms: nil,
			want:  "[]",
		},
		{
			name:  "single",
			perms: []string{"pages:read"},
			want:  `["pages:read"]`,
		},
		{
			name:  "multiple",
			perms: []string{"pages:read", "pages:write"},
			want:  `["pages:read","pages:write"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PermissionsToJSON(tt.perms); got != tt.want {
				t.Errorf("PermissionsToJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllPermissions(t *testing.T) {
	perms := AllPermissions()

	// Should return all defined permissions
	expected := []string{
		PermissionPagesRead,
		PermissionPagesWrite,
		PermissionMediaRead,
		PermissionMediaWrite,
		PermissionTaxonomyRead,
		PermissionTaxonomyWrite,
	}

	if len(perms) != len(expected) {
		t.Errorf("AllPermissions() returned %d permissions, want %d", len(perms), len(expected))
	}

	for i, p := range perms {
		if p != expected[i] {
			t.Errorf("AllPermissions()[%d] = %q, want %q", i, p, expected[i])
		}
	}
}
