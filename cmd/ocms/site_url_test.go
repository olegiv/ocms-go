// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestApplySiteURLOverrideWritesConfig covers the reason the override exists: a
// containerized deployment has no way to set site_url, because it is absent
// from store.DefaultConfig and only the admin UI writes it. Until it is set the
// sitemap and the agent-discovery documents answer 503.
func TestApplySiteURLOverrideWritesConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := context.Background()
	queries := store.New(db)

	// Migration 20260130235529_add_seo_config_keys inserts the row with an
	// empty value, which is the state the Fly demo was in: present but blank,
	// so the sitemap answered 503.
	before, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("reading site_url in a fresh database: %v", err)
	}
	if before.Value != "" {
		t.Fatalf("site_url = %q in a fresh database, want empty", before.Value)
	}

	const want = "https://ocms-demo.fly.dev"
	if err := applySiteURLOverride(ctx, db, want); err != nil {
		t.Fatalf("applySiteURLOverride: %v", err)
	}

	got, gerr := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if gerr != nil {
		t.Fatalf("reading site_url after override: %v", gerr)
	}
	if got.Value != want {
		t.Errorf("site_url = %q, want %q", got.Value, want)
	}
	if got.Type != model.ConfigTypeString {
		t.Errorf("site_url type = %q, want %q", got.Type, model.ConfigTypeString)
	}
}

// TestApplySiteURLOverrideEmptyLeavesConfigAlone pins that an unset
// OCMS_SITE_URL does not clobber a value an administrator set in the UI.
func TestApplySiteURLOverrideEmptyLeavesConfigAlone(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := context.Background()
	queries := store.New(db)

	const adminChoice = "https://set-in-the-admin-ui.example"
	if err := applySiteURLOverride(ctx, db, adminChoice); err != nil {
		t.Fatalf("seeding the admin value: %v", err)
	}

	for _, raw := range []string{"", "   "} {
		if err := applySiteURLOverride(ctx, db, raw); err != nil {
			t.Fatalf("applySiteURLOverride(%q) = %v, want nil", raw, err)
		}
	}

	got, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("reading site_url: %v", err)
	}
	if got.Value != adminChoice {
		t.Errorf("site_url = %q, want the admin value %q preserved", got.Value, adminChoice)
	}
}

// TestApplySiteURLOverrideReplacesAndIsIdempotent covers the override half of
// the contract — a changed environment value wins on the next boot — and the
// no-op path that keeps a restart from rewriting an unchanged row.
func TestApplySiteURLOverrideReplacesAndIsIdempotent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := context.Background()
	queries := store.New(db)

	if err := applySiteURLOverride(ctx, db, "https://old.example"); err != nil {
		t.Fatalf("first override: %v", err)
	}
	if err := applySiteURLOverride(ctx, db, "https://new.example"); err != nil {
		t.Fatalf("second override: %v", err)
	}
	got, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("reading site_url: %v", err)
	}
	if got.Value != "https://new.example" {
		t.Fatalf("site_url = %q, want the environment value to win", got.Value)
	}

	// Re-applying the same value must not touch the row.
	before := got.UpdatedAt
	if err := applySiteURLOverride(ctx, db, "https://new.example"); err != nil {
		t.Fatalf("repeat override: %v", err)
	}
	after, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("re-reading site_url: %v", err)
	}
	if !after.UpdatedAt.Equal(before) {
		t.Errorf("unchanged value rewrote the row (updated_at %v -> %v); every "+
			"restart would churn the config cache", before, after.UpdatedAt)
	}
}

// TestApplySiteURLOverrideRejectsUnusableValues fails startup loudly rather
// than writing a site_url that would render broken canonical links, or a
// scripting scheme into markup.
func TestApplySiteURLOverrideRejectsUnusableValues(t *testing.T) {
	tests := map[string]string{
		"scripting scheme": "javascript:alert(1)",
		"non-http scheme":  "ftp://example.com",
		"no host":          "https://",
		"bare hostname":    "ocms-demo.fly.dev",
		"control bytes":    "https://exa\x7fmple.com",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()
			ctx := context.Background()

			if err := applySiteURLOverride(ctx, db, raw); err == nil {
				t.Fatalf("applySiteURLOverride(%q) = nil, want an error", raw)
			}
			got, gerr := store.New(db).GetConfigByKey(ctx, model.ConfigKeySiteURL)
			if gerr != nil {
				t.Fatalf("reading site_url: %v", gerr)
			}
			if got.Value != "" {
				t.Errorf("a rejected value was written to site_url: %q", got.Value)
			}
		})
	}
}
