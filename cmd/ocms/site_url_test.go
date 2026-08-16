// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"strings"
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

// TestApplySiteURLOverrideNormalizes pins the stored form, not just acceptance.
//
// Consumers append an absolute path to this value. Most trim a trailing slash
// first, but not all: internal/handler/frontend.go:1743 builds the security.txt
// Canonical as siteURL+"/.well-known/security.txt", so a stored "https://x/"
// advertises "https://x//.well-known/security.txt". Storing a canonical base
// makes every consumer correct, including ones written later, which is why this
// asserts the exact value rather than merely that something was written.
func TestApplySiteURLOverrideNormalizes(t *testing.T) {
	tests := map[string]struct{ raw, want string }{
		"host only":         {"https://example.com", "https://example.com"},
		"trailing slash":    {"https://example.com/", "https://example.com"},
		"repeated slashes":  {"https://example.com///", "https://example.com"},
		"explicit port":     {"https://example.com:8443", "https://example.com:8443"},
		"plain http (dev)":  {"http://localhost:8090", "http://localhost:8090"},
		"surrounding space": {"  https://example.com  ", "https://example.com"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()
			ctx := context.Background()

			if err := applySiteURLOverride(ctx, db, tc.raw); err != nil {
				t.Fatalf("applySiteURLOverride(%q) = %v, want it accepted", tc.raw, err)
			}
			got, gerr := store.New(db).GetConfigByKey(ctx, model.ConfigKeySiteURL)
			if gerr != nil {
				t.Fatalf("reading site_url: %v", gerr)
			}
			if got.Value != tc.want {
				t.Errorf("applySiteURLOverride(%q) stored %q, want %q", tc.raw, got.Value, tc.want)
			}
			if strings.HasSuffix(got.Value, "/") {
				t.Errorf("stored %q ends in a slash; every consumer that appends "+
					"an absolute path would emit a doubled separator", got.Value)
			}
		})
	}
}

// TestApplySiteURLOverrideNormalizedFormIsStable checks that spellings which
// normalize to the same base do not rewrite the row, so a restart with a
// differently-spelled value does not churn updated_at and the config cache.
func TestApplySiteURLOverrideNormalizedFormIsStable(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := context.Background()
	queries := store.New(db)

	if err := applySiteURLOverride(ctx, db, "https://example.com"); err != nil {
		t.Fatalf("first override: %v", err)
	}
	before, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("reading site_url: %v", err)
	}

	if err := applySiteURLOverride(ctx, db, "https://example.com/"); err != nil {
		t.Fatalf("second override: %v", err)
	}
	after, err := queries.GetConfigByKey(ctx, model.ConfigKeySiteURL)
	if err != nil {
		t.Fatalf("re-reading site_url: %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("a trailing slash rewrote the row (updated_at %v -> %v)",
			before.UpdatedAt, after.UpdatedAt)
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
		// Consumers concatenate a path onto this value, so a query or fragment
		// swallows the route: "https://example.com?preview=1" + "/about" is a
		// query, not a path. An empty "?" or "#" does the same damage.
		"query string":       "https://example.com?preview=1",
		"empty forced query": "https://example.com?",
		"fragment":           "https://example.com#preview",
		"empty fragment":     "https://example.com#",
		"credentials":        "https://user:pass@example.com",
		// url.Parse only checks that a port is numeric, so an out-of-range one
		// survives into every generated link.
		"port above range": "https://example.com:99999",
		"port zero":        "https://example.com:0",
		// Host is ":443" here, non-empty, but there is no hostname at all.
		"port without host": "https://:443",
		// sitemap.xml, robots.txt, /.well-known/* and /api/v2 are all served
		// from the router root and nothing mounts a base prefix, so a path here
		// would advertise discovery links that 404.
		"subdirectory path": "https://example.com/blog",
		"single segment":    "https://example.com/x",
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
