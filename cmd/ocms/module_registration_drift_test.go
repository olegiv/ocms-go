// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/sentinel"
)

// TestAllModulesMirrorsRegisterModules fails when a module is registered at
// startup but missing from allModules(), or listed there but never
// registered. allModules() is a hand-kept mirror, and a module missing from it
// would let its template funcs escape the placeholder guard in
// TestEveryModuleTemplateFuncHasRendererPlaceholder.
func TestAllModulesMirrorsRegisterModules(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	registry := module.NewRegistry(testutil.TestLoggerSilent())
	if _, err := registerModules(registry, sentinel.New(), scs.New(), service.NewEventService(db)); err != nil {
		t.Fatalf("registerModules: %v", err)
	}

	registered := make(map[string]bool)
	for _, mod := range registry.List() {
		registered[mod.Name()] = true
	}
	enumerated := make(map[string]bool)
	for _, mod := range allModules() {
		enumerated[mod.Name()] = true
	}

	for name := range registered {
		if !enumerated[name] {
			t.Errorf("module %q is registered by registerModules() but missing from allModules()", name)
		}
	}
	for name := range enumerated {
		if !registered[name] {
			t.Errorf("module %q is listed in allModules() but registerModules() never registers it", name)
		}
	}
	if !registered["mcp"] {
		t.Error("the MCP module must be registered at startup")
	}
}
