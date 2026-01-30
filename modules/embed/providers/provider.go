// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package providers defines the Provider interface and common types for embed providers.
package providers

import "html/template"

// SettingField defines a configuration field for a provider.
type SettingField struct {
	// ID is the unique identifier for the field (used in JSON storage)
	ID string

	// Name is the display name for the field
	Name string

	// Description provides additional context for the field
	Description string

	// Type is the field type: "text", "textarea", "url", "color", "select", "checkbox"
	Type string

	// Options are the available choices for "select" type fields
	Options []SelectOption

	// Required indicates if the field must have a value when enabled
	Required bool

	// Default is the default value for the field
	Default string

	// Placeholder is the placeholder text for input fields
	Placeholder string
}

// SelectOption represents an option for select fields.
type SelectOption struct {
	Value string
	Label string
}

// Provider defines the interface for embed providers.
// Each provider implements a specific third-party service integration.
type Provider interface {
	// ID returns the unique identifier for the provider (e.g., "dify", "youtube")
	ID() string

	// Name returns the display name for the provider
	Name() string

	// Description returns a brief description of the provider
	Description() string

	// SettingsSchema returns the configuration fields for the provider
	SettingsSchema() []SettingField

	// Validate validates the provider settings
	// Returns nil if valid, or an error describing the issue
	Validate(settings map[string]string) error

	// RenderHead returns HTML to inject in the <head> section
	RenderHead(settings map[string]string) template.HTML

	// RenderBody returns HTML to inject before </body>
	RenderBody(settings map[string]string) template.HTML
}
