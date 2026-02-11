// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"testing"
)

// resetCustomModules clears the global custom module registry between tests.
func resetCustomModules() {
	customMu.Lock()
	defer customMu.Unlock()
	customModules = nil
}

func TestRegisterCustomModule(t *testing.T) {
	resetCustomModules()
	defer resetCustomModules()

	m := &BaseModule{name: "test-mod", version: "1.0.0", description: "Test module"}
	RegisterCustomModule(m)

	modules := CustomModules()
	if len(modules) != 1 {
		t.Fatalf("CustomModules() returned %d modules, want 1", len(modules))
	}
	if modules[0].Name() != "test-mod" {
		t.Errorf("module name = %q, want test-mod", modules[0].Name())
	}
}

func TestRegisterMultipleCustomModules(t *testing.T) {
	resetCustomModules()
	defer resetCustomModules()

	m1 := &BaseModule{name: "mod-a", version: "1.0.0", description: "Module A"}
	m2 := &BaseModule{name: "mod-b", version: "2.0.0", description: "Module B"}
	m3 := &BaseModule{name: "mod-c", version: "0.1.0", description: "Module C"}

	RegisterCustomModule(m1)
	RegisterCustomModule(m2)
	RegisterCustomModule(m3)

	modules := CustomModules()
	if len(modules) != 3 {
		t.Fatalf("CustomModules() returned %d modules, want 3", len(modules))
	}

	// Verify order is preserved (registration order)
	expected := []string{"mod-a", "mod-b", "mod-c"}
	for i, name := range expected {
		if modules[i].Name() != name {
			t.Errorf("modules[%d].Name() = %q, want %q", i, modules[i].Name(), name)
		}
	}
}

func TestCustomModulesEmpty(t *testing.T) {
	resetCustomModules()
	defer resetCustomModules()

	modules := CustomModules()
	if len(modules) != 0 {
		t.Errorf("CustomModules() returned %d modules, want 0", len(modules))
	}
	if modules == nil {
		t.Error("CustomModules() should return non-nil empty slice, not nil")
	}
}

func TestCustomModulesReturnsCopy(t *testing.T) {
	resetCustomModules()
	defer resetCustomModules()

	m := &BaseModule{name: "original", version: "1.0.0", description: "Original"}
	RegisterCustomModule(m)

	// Get the slice and modify it
	modules1 := CustomModules()
	modules1[0] = &BaseModule{name: "tampered", version: "0.0.0", description: "Tampered"}

	// Get again — should still have original
	modules2 := CustomModules()
	if modules2[0].Name() != "original" {
		t.Errorf("CustomModules() returned mutated data: got %q, want original", modules2[0].Name())
	}
}

func TestCustomModulesPreservesMetadata(t *testing.T) {
	resetCustomModules()
	defer resetCustomModules()

	m := &BaseModule{name: "meta-test", version: "3.2.1", description: "Metadata test"}
	RegisterCustomModule(m)

	modules := CustomModules()
	if modules[0].Version() != "3.2.1" {
		t.Errorf("Version() = %q, want 3.2.1", modules[0].Version())
	}
	if modules[0].Description() != "Metadata test" {
		t.Errorf("Description() = %q, want Metadata test", modules[0].Description())
	}
}
