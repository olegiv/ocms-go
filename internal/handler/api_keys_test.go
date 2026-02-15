// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewAPIKeysHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewAPIKeysHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewAPIKeysHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestApplyDefaultAPIKeyExpiry(t *testing.T) {
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	t.Run("preserve explicit expiry", func(t *testing.T) {
		explicit := sql.NullTime{
			Time:  now.Add(24 * time.Hour),
			Valid: true,
		}
		got := applyDefaultAPIKeyExpiry(explicit, now)
		if !got.Valid {
			t.Fatal("expiry should be valid")
		}
		if !got.Time.Equal(explicit.Time) {
			t.Errorf("expiry = %s, want %s", got.Time, explicit.Time)
		}
	})

	t.Run("assign default expiry when missing", func(t *testing.T) {
		got := applyDefaultAPIKeyExpiry(sql.NullTime{}, now)
		if !got.Valid {
			t.Fatal("default expiry should be valid")
		}
		want := now.Add(defaultAPIKeyLifetime)
		if !got.Time.Equal(want) {
			t.Errorf("expiry = %s, want %s", got.Time, want)
		}
	})
}

func TestParseAPIKeyExpiration(t *testing.T) {
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	t.Run("empty", func(t *testing.T) {
		got, errMsg := parseAPIKeyExpirationAt("", true, now)
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
		if got.Valid {
			t.Fatal("expected empty expiration for empty input")
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		_, errMsg := parseAPIKeyExpirationAt("15-02-2026", true, now)
		if errMsg == "" {
			t.Fatal("expected validation error")
		}
	})

	t.Run("past date when future required", func(t *testing.T) {
		_, errMsg := parseAPIKeyExpirationAt("2026-02-14", true, now)
		if errMsg != "Expiration date must be in the future" {
			t.Fatalf("error = %q, want %q", errMsg, "Expiration date must be in the future")
		}
	})

	t.Run("too far in future", func(t *testing.T) {
		_, errMsg := parseAPIKeyExpirationAt("2028-02-15", true, now)
		if errMsg == "" {
			t.Fatal("expected max-lifetime validation error")
		}
	})

	t.Run("valid within max lifetime", func(t *testing.T) {
		got, errMsg := parseAPIKeyExpirationAt("2027-02-14", true, now)
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
		if !got.Valid {
			t.Fatal("expected parsed expiration to be valid")
		}
		want := time.Date(2027, 2, 14, 23, 59, 59, 0, time.UTC)
		if !got.Time.Equal(want) {
			t.Fatalf("expiresAt = %s, want %s", got.Time, want)
		}
	})
}

func TestParseAPIKeySourceCIDRs(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		got, errMsg := parseAPIKeySourceCIDRs("")
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
		if len(got) != 0 {
			t.Fatalf("expected no entries, got %d", len(got))
		}
	})

	t.Run("normalize and deduplicate", func(t *testing.T) {
		got, errMsg := parseAPIKeySourceCIDRs("203.0.113.10,203.0.113.10/32\n2001:db8::1")
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(got))
		}
		if got[0] != "203.0.113.10/32" {
			t.Errorf("first entry = %q, want %q", got[0], "203.0.113.10/32")
		}
		if got[1] != "2001:db8::1/128" {
			t.Errorf("second entry = %q, want %q", got[1], "2001:db8::1/128")
		}
	})

	t.Run("invalid entry", func(t *testing.T) {
		_, errMsg := parseAPIKeySourceCIDRs("not-an-ip")
		if errMsg == "" {
			t.Fatal("expected validation error")
		}
	})
}

func TestValidateAPIKeyForm_RequireSourceCIDRs(t *testing.T) {
	t.Run("reject when source CIDRs are required but missing", func(t *testing.T) {
		_, errs := validateAPIKeyForm(
			"Key With Policy",
			[]string{"pages:read"},
			"",
			"",
			true,
			true,
		)
		if errs["source_cidrs"] == "" {
			t.Fatal("expected source_cidrs validation error")
		}
	})

	t.Run("allow when source CIDRs are present", func(t *testing.T) {
		_, errs := validateAPIKeyForm(
			"Key With CIDRs",
			[]string{"pages:read"},
			"",
			"203.0.113.0/24",
			true,
			true,
		)
		if errs["source_cidrs"] != "" {
			t.Fatalf("unexpected source_cidrs validation error: %s", errs["source_cidrs"])
		}
	})
}

func TestAPIKeyCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	key, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Test API Key",
		KeyHash:     "hash123",
		KeyPrefix:   "test_",
		Permissions: `["pages:read", "pages:write"]`,
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if key.Name != "Test API Key" {
		t.Errorf("Name = %q, want %q", key.Name, "Test API Key")
	}
	if key.KeyPrefix != "test_" {
		t.Errorf("KeyPrefix = %q, want %q", key.KeyPrefix, "test_")
	}
	if !key.IsActive {
		t.Error("IsActive should be true")
	}
}

func TestAPIKeyWithExpiry(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	expiryTime := now.Add(24 * time.Hour)
	key, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Expiring Key",
		KeyHash:     "hash456",
		KeyPrefix:   "exp_",
		Permissions: `[]`,
		ExpiresAt:   sql.NullTime{Time: expiryTime, Valid: true},
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if !key.ExpiresAt.Valid {
		t.Error("ExpiresAt should be valid")
	}
}

func TestAPIKeyList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	// Create test API keys
	for i := 1; i <= 3; i++ {
		_, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
			Name:        "Key " + string(rune('A'+i-1)),
			KeyHash:     "hash" + string(rune('0'+i)),
			KeyPrefix:   "key" + string(rune('0'+i)) + "_",
			Permissions: `[]`,
			IsActive:    true,
			CreatedBy:   user.ID,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			t.Fatalf("CreateAPIKey failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		keys, err := queries.ListAPIKeys(context.Background(), store.ListAPIKeysParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListAPIKeys failed: %v", err)
		}
		if len(keys) != 3 {
			t.Errorf("got %d keys, want 3", len(keys))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountAPIKeys(context.Background())
		if err != nil {
			t.Fatalf("CountAPIKeys failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})
}

func TestAPIKeyGetByPrefix(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	_, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Prefix Test Key",
		KeyHash:     "prefixhash",
		KeyPrefix:   "prefix_test_",
		Permissions: `[]`,
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	key, err := queries.GetAPIKeyByPrefix(context.Background(), "prefix_test_")
	if err != nil {
		t.Fatalf("GetAPIKeyByPrefix failed: %v", err)
	}

	if key.KeyPrefix != "prefix_test_" {
		t.Errorf("KeyPrefix = %q, want %q", key.KeyPrefix, "prefix_test_")
	}
}

func TestAPIKeyUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	key, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Original Key",
		KeyHash:     "originalhash",
		KeyPrefix:   "orig_",
		Permissions: `[]`,
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	_, err = queries.UpdateAPIKey(context.Background(), store.UpdateAPIKeyParams{
		ID:          key.ID,
		Name:        "Updated Key",
		Permissions: `["pages:read"]`,
	})
	if err != nil {
		t.Fatalf("UpdateAPIKey failed: %v", err)
	}

	updated, err := queries.GetAPIKeyByID(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("GetAPIKeyByID failed: %v", err)
	}

	if updated.Name != "Updated Key" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Key")
	}
}

func TestAPIKeyDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	key, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "To Delete Key",
		KeyHash:     "deletehash",
		KeyPrefix:   "del_",
		Permissions: `[]`,
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if err := queries.DeleteAPIKey(context.Background(), key.ID); err != nil {
		t.Fatalf("DeleteAPIKey failed: %v", err)
	}

	_, err = queries.GetAPIKeyByID(context.Background(), key.ID)
	if err == nil {
		t.Error("expected error when getting deleted API key")
	}
}

func TestAPIKeyUpdateLastUsed(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	key, err := queries.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name:        "Last Used Key",
		KeyHash:     "lastused",
		KeyPrefix:   "used_",
		Permissions: `[]`,
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Initially last_used_at should be null
	if key.LastUsedAt.Valid {
		t.Error("LastUsedAt should not be valid initially")
	}

	// Update last used
	if err := queries.UpdateAPIKeyLastUsed(context.Background(), store.UpdateAPIKeyLastUsedParams{
		ID:         key.ID,
		LastUsedAt: sql.NullTime{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatalf("UpdateAPIKeyLastUsed failed: %v", err)
	}

	updated, err := queries.GetAPIKeyByID(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("GetAPIKeyByID failed: %v", err)
	}

	if !updated.LastUsedAt.Valid {
		t.Error("LastUsedAt should be valid after update")
	}
}
