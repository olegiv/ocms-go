// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewConfigHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewConfigHandler(db, nil, sm, nil)
	if h == nil {
		t.Fatal("NewConfigHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestConfigGet(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// The test helper creates site_name config
	config, err := queries.GetConfigByKey(context.Background(), "site_name")
	if err != nil {
		t.Fatalf("GetConfigByKey failed: %v", err)
	}

	if config.Key != "site_name" {
		t.Errorf("Key = %q, want %q", config.Key, "site_name")
	}
	if config.Value != "Test Site" {
		t.Errorf("Value = %q, want %q", config.Value, "Test Site")
	}
}

func TestConfigUpsert(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	t.Run("create new config", func(t *testing.T) {
		_, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
			Key:   "new_key",
			Value: "new_value",
		})
		if err != nil {
			t.Fatalf("UpsertConfig failed: %v", err)
		}

		config, err := queries.GetConfigByKey(context.Background(), "new_key")
		if err != nil {
			t.Fatalf("GetConfigByKey failed: %v", err)
		}
		if config.Value != "new_value" {
			t.Errorf("Value = %q, want %q", config.Value, "new_value")
		}
	})

	t.Run("update existing config", func(t *testing.T) {
		_, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
			Key:   "site_name",
			Value: "Updated Site",
		})
		if err != nil {
			t.Fatalf("UpsertConfig failed: %v", err)
		}

		config, err := queries.GetConfigByKey(context.Background(), "site_name")
		if err != nil {
			t.Fatalf("GetConfigByKey failed: %v", err)
		}
		if config.Value != "Updated Site" {
			t.Errorf("Value = %q, want %q", config.Value, "Updated Site")
		}
	})
}

func TestConfigList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create additional configs
	configs := []struct {
		key   string
		value string
	}{
		{"theme", "default"},
		{"locale", "en"},
	}
	for _, c := range configs {
		_, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
			Key:   c.key,
			Value: c.value,
		})
		if err != nil {
			t.Fatalf("UpsertConfig failed: %v", err)
		}
	}

	result, err := queries.ListConfig(context.Background())
	if err != nil {
		t.Fatalf("ListConfigs failed: %v", err)
	}

	// Should have at least 3 configs (site_name from test helper + 2 we created)
	if len(result) < 3 {
		t.Errorf("got %d configs, want at least 3", len(result))
	}
}

func TestConfigDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	_, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
		Key:   "to_delete",
		Value: "value",
	})
	if err != nil {
		t.Fatalf("UpsertConfig failed: %v", err)
	}

	if err := queries.DeleteConfig(context.Background(), "to_delete"); err != nil {
		t.Fatalf("DeleteConfig failed: %v", err)
	}

	_, err = queries.GetConfigByKey(context.Background(), "to_delete")
	if err == nil {
		t.Error("expected error when getting deleted config")
	}
}
