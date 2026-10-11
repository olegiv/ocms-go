// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package providers defines the Provider interface and common types for embed providers.
package providers

import (
	"html/template"
	"time"
)

// RenderContext carries per-request state into provider render methods.
// Origin is the normalized scheme://host of the page the widget will run on.
// IssueProxyToken mints a signed proxy token bound to Origin (or returns an
// error if the module is not configured to issue one). Providers that do not
// need a token can ignore the closure.
type RenderContext struct {
	Origin          string
	IssueProxyToken func() (token string, expiresAt time.Time, err error)
}

// FieldType names the form control a SettingField renders as.
type FieldType string

// Field types understood by the provider settings form.
const (
	FieldText     FieldType = "text"
	FieldPassword FieldType = "password"
	FieldTextarea FieldType = "textarea"
	FieldURL      FieldType = "url"
	FieldColor    FieldType = "color"
	FieldSelect   FieldType = "select"
	FieldCheckbox FieldType = "checkbox"
)

// SettingField defines a configuration field for a provider.
type SettingField struct {
	// ID is the unique identifier for the field (used in JSON storage)
	ID string

	// Name is the display name for the field
	Name string

	// Description provides additional context for the field
	Description string

	// Type selects the form control. A FieldPassword field is rendered
	// masked, its stored value is never written back into the form, and an
	// empty submission keeps the stored value.
	Type FieldType

	// Options are the available choices for "select" type fields
	Options []SelectOption

	// Required indicates if the field must have a value when enabled
	Required bool

	// Default is the default value for the field. For a "password" field it
	// is always empty when rendered; the stored secret never reaches the view.
	Default string

	// HasStoredValue tells a "password" field that a secret is already saved,
	// so the form can show a placeholder and relax the required attribute.
	HasStoredValue bool

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

	// RenderBody returns HTML to inject before </body>.
	// ctx carries the request origin and a token-issuance closure so providers
	// that need a signed proxy token can mint it at render time instead of
	// relying on a public mint endpoint.
	RenderBody(settings map[string]string, ctx RenderContext) template.HTML
}
