// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/modules/embed/providers"
)

func TestDifyProvider_ID(t *testing.T) {
	p := providers.NewDify()
	if p.ID() != "dify" {
		t.Errorf("expected ID 'dify', got %q", p.ID())
	}
}

func TestDifyProvider_Name(t *testing.T) {
	p := providers.NewDify()
	if p.Name() != "Dify AI Chat" {
		t.Errorf("expected Name 'Dify AI Chat', got %q", p.Name())
	}
}

func TestDifyProvider_SettingsSchema(t *testing.T) {
	p := providers.NewDify()
	schema := p.SettingsSchema()

	if len(schema) == 0 {
		t.Fatal("expected non-empty settings schema")
	}

	// Check required fields exist
	fieldIDs := make(map[string]bool)
	for _, f := range schema {
		fieldIDs[f.ID] = true
	}

	required := []string{"api_endpoint", "api_key", "bot_name", "welcome_message", "primary_color", "position", "opener_questions", "show_suggested"}
	for _, r := range required {
		if !fieldIDs[r] {
			t.Errorf("expected field %q in schema", r)
		}
	}
}

func TestDifyProvider_Validate(t *testing.T) {
	p := providers.NewDify()

	tests := []struct {
		name     string
		settings map[string]string
		wantErr  bool
	}{
		{
			name:     "empty settings",
			settings: map[string]string{},
			wantErr:  true,
		},
		{
			name: "missing api_key",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
			},
			wantErr: true,
		},
		{
			name: "missing api_endpoint",
			settings: map[string]string{
				"api_key": "app-test-key",
			},
			wantErr: true,
		},
		{
			name: "invalid api_endpoint (no protocol)",
			settings: map[string]string{
				"api_endpoint": "api.dify.ai/v1",
				"api_key":      "app-test-key",
			},
			wantErr: true,
		},
		{
			name: "valid settings with https",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key",
			},
			wantErr: false,
		},
		{
			name: "valid settings with http (localhost)",
			settings: map[string]string{
				"api_endpoint": "http://localhost:8080/v1",
				"api_key":      "app-test-key",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.Validate(tt.settings)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDifyProvider_RenderHead(t *testing.T) {
	p := providers.NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-test-key",
	}

	result := p.RenderHead(settings)
	if result != "" {
		t.Errorf("expected empty head, got %q", result)
	}
}

func TestDifyProvider_RenderBody(t *testing.T) {
	p := providers.NewDify()

	tests := []struct {
		name     string
		settings map[string]string
		contains []string
		excludes []string
	}{
		{
			name:     "empty settings returns empty",
			settings: map[string]string{},
			contains: nil,
		},
		{
			name: "basic settings",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key-123",
			},
			contains: []string{
				"dify-chat-widget",
				"dify-chat-toggle",
				"dify-chat-window",
				"dify-chat-messages",
				"API='https://api.dify.ai/v1'",
				"KEY='app-test-key-123'",
				"/chat-messages",
				"AI Assistant", // default bot name
				"#1C64F2",      // default primary color
			},
		},
		{
			name: "custom bot name",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key",
				"bot_name":     "Custom Bot",
			},
			contains: []string{
				"Custom Bot",
			},
		},
		{
			name: "custom welcome message",
			settings: map[string]string{
				"api_endpoint":    "https://api.dify.ai/v1",
				"api_key":         "app-test-key",
				"welcome_message": "Welcome to our chat!",
			},
			contains: []string{
				"Welcome to our chat!",
			},
		},
		{
			name: "custom primary color",
			settings: map[string]string{
				"api_endpoint":  "https://api.dify.ai/v1",
				"api_key":       "app-test-key",
				"primary_color": "#FF5733",
			},
			contains: []string{
				"#FF5733",
			},
		},
		{
			name: "bottom-left position",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key",
				"position":     "bottom-left",
			},
			contains: []string{
				"left: 20px",
			},
		},
		{
			name: "opener questions",
			settings: map[string]string{
				"api_endpoint":     "https://api.dify.ai/v1",
				"api_key":          "app-test-key",
				"opener_questions": "What can you help with?\nTell me about pricing\nHow do I get started?",
			},
			contains: []string{
				"OPENERS=['What can you help with?','Tell me about pricing','How do I get started?']",
				"dify-openers",
				"dify-opener",
				"showOpeners",
				"hideOpeners",
			},
		},
		{
			name: "empty opener questions",
			settings: map[string]string{
				"api_endpoint":     "https://api.dify.ai/v1",
				"api_key":          "app-test-key",
				"opener_questions": "",
			},
			contains: []string{
				"OPENERS=[]",
			},
		},
		{
			name: "show_suggested disabled by default",
			settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key",
			},
			contains: []string{
				"SHOW_SUGGESTED=false",
				"showSuggestions",
				"hideSuggestions",
			},
		},
		{
			name: "show_suggested enabled",
			settings: map[string]string{
				"api_endpoint":   "https://api.dify.ai/v1",
				"api_key":        "app-test-key",
				"show_suggested": "1",
			},
			contains: []string{
				"SHOW_SUGGESTED=true",
				"/suggested?user=",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(p.RenderBody(tt.settings))

			for _, c := range tt.contains {
				if !strings.Contains(result, c) {
					t.Errorf("expected result to contain %q", c)
				}
			}

			for _, e := range tt.excludes {
				if strings.Contains(result, e) {
					t.Errorf("expected result NOT to contain %q", e)
				}
			}
		})
	}
}

func TestDifyProvider_RenderBody_XSSPrevention(t *testing.T) {
	p := providers.NewDify()

	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-<script>alert('xss')</script>",
	}

	result := string(p.RenderBody(settings))

	// Should not contain unescaped script tags in the API key
	if strings.Contains(result, "<script>alert") {
		t.Error("XSS: unescaped script tag in api_key")
	}
}

func TestDifyProvider_RenderBody_OpenerQuestionsXSS(t *testing.T) {
	p := providers.NewDify()

	settings := map[string]string{
		"api_endpoint":     "https://api.dify.ai/v1",
		"api_key":          "app-test-key",
		"opener_questions": "Normal question\n<script>alert('xss')</script>\nAnother question",
	}

	result := string(p.RenderBody(settings))

	// Should not contain unescaped script tags in opener questions
	if strings.Contains(result, "<script>alert") {
		t.Error("XSS: unescaped script tag in opener_questions")
	}

	// Should contain escaped version (uppercase hex)
	if !strings.Contains(result, `\u003Cscript\u003E`) {
		t.Error("Expected JS-escaped script tag in opener questions")
	}
}

func TestNewModule(t *testing.T) {
	m := New()

	if m.Name() != "embed" {
		t.Errorf("expected Name 'embed', got %q", m.Name())
	}

	if m.Version() != "1.0.0" {
		t.Errorf("expected Version '1.0.0', got %q", m.Version())
	}

	if len(m.providers) == 0 {
		t.Error("expected at least one provider registered")
	}
}

func TestModule_TemplateFuncs(t *testing.T) {
	m := New()
	funcs := m.TemplateFuncs()

	if _, ok := funcs["embedHead"]; !ok {
		t.Error("expected 'embedHead' template function")
	}

	if _, ok := funcs["embedBody"]; !ok {
		t.Error("expected 'embedBody' template function")
	}
}

func TestModule_AdminURL(t *testing.T) {
	m := New()
	if m.AdminURL() != "/admin/embed" {
		t.Errorf("expected AdminURL '/admin/embed', got %q", m.AdminURL())
	}
}

func TestModule_Migrations(t *testing.T) {
	m := New()
	migrations := m.Migrations()

	if len(migrations) == 0 {
		t.Error("expected at least one migration")
	}

	if migrations[0].Version != 1 {
		t.Errorf("expected first migration version 1, got %d", migrations[0].Version)
	}
}
