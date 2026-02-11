// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import "sync"

// customModules holds modules registered via init() from custom module packages.
// This follows the same pattern as database/sql.Register() for database drivers.
var (
	customMu      sync.Mutex
	customModules []Module
)

// RegisterCustomModule registers a custom module for automatic loading.
// Call this from an init() function in your custom module package:
//
//	func init() { module.RegisterCustomModule(New()) }
//
// The module will be registered with the main registry during startup.
func RegisterCustomModule(m Module) {
	customMu.Lock()
	defer customMu.Unlock()
	customModules = append(customModules, m)
}

// CustomModules returns all custom modules registered via RegisterCustomModule.
func CustomModules() []Module {
	customMu.Lock()
	defer customMu.Unlock()
	result := make([]Module, len(customModules))
	copy(result, customModules)
	return result
}
