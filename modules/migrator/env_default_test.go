// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"slices"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// TestAllowedEnvsExcludesProduction pins the declaration itself.
func TestAllowedEnvsExcludesProduction(t *testing.T) {
	envs := New().AllowedEnvs()
	if len(envs) == 0 {
		t.Fatal("AllowedEnvs returned nothing; the registry would treat every " +
			"environment as disallowed")
	}
	if !slices.Contains(envs, "development") {
		t.Errorf("AllowedEnvs = %v, want it to contain \"development\"", envs)
	}
	if slices.Contains(envs, "production") {
		t.Errorf("AllowedEnvs = %v, must not contain \"production\": the migrator "+
			"dials external databases and takes source credentials", envs)
	}
}

// TestMigratorRegistersInactiveInProduction proves the declaration is wired to
// an effect, not just present. A registry loading a database with no modules
// row must insert the migrator inactive under production and active under
// development.
//
// This is the guard on the outage: the migrator registering itself active in
// production is what put an unrestricted importer on the public demo and then
// refused startup once the DB-host allowlist audit landed.
//
// Bug state: delete AllowedEnvs from module.go and the production case flips to
// active.
func TestMigratorRegistersInactiveInProduction(t *testing.T) {
	tests := map[string]bool{
		"production":  false,
		"development": true,
	}
	for env, wantActive := range tests {
		t.Run(env, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)

			registry := module.NewRegistry(testutil.TestLogger())
			if err := registry.Register(New()); err != nil {
				t.Fatalf("Register: %v", err)
			}

			ctx, _ := moduleutil.TestModuleContext(t, db)
			ctx.Config.Env = env
			if err := registry.InitAll(ctx); err != nil {
				t.Fatalf("InitAll in %s: %v", env, err)
			}

			if got := registry.IsActive("migrator"); got != wantActive {
				t.Errorf("IsActive(migrator) in %s = %v, want %v", env, got, wantActive)
			}
		})
	}
}
