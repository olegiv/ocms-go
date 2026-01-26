// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package types defines shared types for the migrator module.
// This package is separate to avoid import cycles between migrator and source implementations.
package types

import (
	"context"
	"database/sql"
)

// ImportTracker tracks imported items for later deletion.
type ImportTracker interface {
	// TrackImportedItem records an imported item.
	TrackImportedItem(ctx context.Context, source, entityType string, entityID int64) error
}

// Source defines the interface that all migration sources must implement.
type Source interface {
	// Name returns the unique identifier for this source (e.g., "elefant", "drupal").
	Name() string

	// DisplayName returns the human-readable name for the UI.
	DisplayName() string

	// Description returns a brief description of what this source imports.
	Description() string

	// ConfigFields returns the configuration fields needed for this source.
	ConfigFields() []ConfigField

	// TestConnection tests the connection using the provided configuration.
	TestConnection(cfg map[string]string) error

	// Import performs the actual import using the provided configuration and options.
	// The tracker can be used to record imported items for later deletion.
	Import(ctx context.Context, db *sql.DB, cfg map[string]string, opts ImportOptions, tracker ImportTracker) (*ImportResult, error)
}

// ConfigField represents a configuration field for a migration source.
type ConfigField struct {
	Name        string // Field name (form key)
	Label       string // Display label
	Type        string // Field type: "text", "password", "number", "path"
	Required    bool   // Whether the field is required
	Default     string // Default value
	Placeholder string // Placeholder text
}

// ImportOptions contains options for the import operation.
type ImportOptions struct {
	ImportTags   bool
	ImportMedia  bool
	ImportPosts  bool
	SkipExisting bool
}

// ImportResult contains the results of an import operation.
type ImportResult struct {
	TagsImported   int
	MediaImported  int
	PostsImported  int
	TagsSkipped    int
	MediaSkipped   int
	PostsSkipped   int
	Errors         []string
}

// TotalImported returns the total number of items imported.
func (r *ImportResult) TotalImported() int {
	return r.TagsImported + r.MediaImported + r.PostsImported
}

// TotalSkipped returns the total number of items skipped.
func (r *ImportResult) TotalSkipped() int {
	return r.TagsSkipped + r.MediaSkipped + r.PostsSkipped
}

// HasErrors returns true if there were any errors during import.
func (r *ImportResult) HasErrors() bool {
	return len(r.Errors) > 0
}
