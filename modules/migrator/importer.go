// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"sort"
	"sync"

	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// Type aliases for convenience - allows using migrator.Source instead of types.Source
type (
	Source        = types.Source
	ConfigField   = types.ConfigField
	ImportOptions = types.ImportOptions
	ImportResult  = types.ImportResult
	ImportTracker = types.ImportTracker
)

// Source registry

var (
	sources   = make(map[string]Source)
	sourcesMu sync.RWMutex
)

// RegisterSource registers a source with the registry.
func RegisterSource(s Source) {
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	sources[s.Name()] = s
}

// GetSource returns a source by name.
func GetSource(name string) (Source, bool) {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	s, ok := sources[name]
	return s, ok
}

// ListSources returns all registered sources sorted by name.
func ListSources() []Source {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()

	result := make([]Source, 0, len(sources))
	for _, s := range sources {
		result = append(result, s)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})

	return result
}
