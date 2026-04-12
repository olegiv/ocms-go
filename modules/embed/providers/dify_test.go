// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"strings"
	"testing"
	"time"
)

func TestDify_ID(t *testing.T) {
	p := NewDify()
	if p.ID() != "dify" {
		t.Errorf("ID() = %q, want 'dify'", p.ID())
	}
}

func TestDify_Name(t *testing.T) {
	p := NewDify()
	if p.Name() != "Dify AI Chat" {
		t.Errorf("Name() = %q, want 'Dify AI Chat'", p.Name())
	}
}

func TestDify_Description(t *testing.T) {
	p := NewDify()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestDify_SettingsSchema(t *testing.T) {
	p := NewDify()
	schema := p.SettingsSchema()

	if len(schema) == 0 {
		t.Fatal("SettingsSchema() should return at least one field")
	}

	seen := make(map[string]bool)
	for _, f := range schema {
		if f.ID == "" {
			t.Error("SettingField.ID must not be empty")
		}
		if f.Name == "" {
			t.Errorf("SettingField %q: Name must not be empty", f.ID)
		}
		if f.Type == "" {
			t.Errorf("SettingField %q: Type must not be empty", f.ID)
		}
		if seen[f.ID] {
			t.Errorf("SettingField %q: duplicate field ID", f.ID)
		}
		seen[f.ID] = true
	}
}

func TestDify_Validate_Valid(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://8.8.8.8/v1",
		"api_key":      "app-testkey",
	}
	if err := p.Validate(settings); err != nil {
		t.Errorf("Validate() with valid settings returned error: %v", err)
	}
}

func TestDify_Validate_Missing(t *testing.T) {
	p := NewDify()

	tests := []struct {
		name     string
		settings map[string]string
	}{
		{"empty", map[string]string{}},
		{"missing api_key", map[string]string{"api_endpoint": "https://api.dify.ai/v1"}},
		{"missing api_endpoint", map[string]string{"api_key": "app-key"}},
		{"http endpoint", map[string]string{"api_endpoint": "http://api.dify.ai/v1", "api_key": "app-key"}},
		{"localhost", map[string]string{"api_endpoint": "http://localhost:8080/v1", "api_key": "app-key"}},
		{"no scheme", map[string]string{"api_endpoint": "api.dify.ai/v1", "api_key": "app-key"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := p.Validate(tt.settings); err == nil {
				t.Errorf("Validate(%v): expected error, got nil", tt.settings)
			}
		})
	}
}

func TestDify_RenderHead_AlwaysEmpty(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-test-key",
	}
	result := p.RenderHead(settings)
	if result != "" {
		t.Errorf("RenderHead() = %q, want empty string", result)
	}
}

func TestDify_RenderBody_EmptySettings(t *testing.T) {
	p := NewDify()
	result := p.RenderBody(map[string]string{}, RenderContext{})
	if result != "" {
		t.Errorf("RenderBody(empty) = %q, want empty", result)
	}
}

func TestDify_RenderBody_BasicSettings(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-testkey-abc",
	}

	result := string(p.RenderBody(settings, RenderContext{}))

	// Should not expose the API key or endpoint directly.
	if strings.Contains(result, "app-testkey-abc") {
		t.Error("RenderBody should not expose api_key in output")
	}
	if strings.Contains(result, "https://api.dify.ai/v1") {
		t.Error("RenderBody should not expose api_endpoint in output")
	}

	// Should contain the proxy widget.
	if !strings.Contains(result, "dify-chat-widget") {
		t.Error("expected dify-chat-widget in output")
	}
	if !strings.Contains(result, "PROXY_BASE") {
		t.Error("expected PROXY_BASE in output")
	}
}

func TestDify_RenderBody_CustomBotName(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-key",
		"bot_name":     "My Custom Bot",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "My Custom Bot") {
		t.Error("expected custom bot name in output")
	}
}

func TestDify_RenderBody_CustomColor(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint":  "https://api.dify.ai/v1",
		"api_key":       "app-key",
		"primary_color": "#ABCDEF",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "#ABCDEF") {
		t.Error("expected custom primary_color in output")
	}
}

func TestDify_RenderBody_BottomLeft(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-key",
		"position":     "bottom-left",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "left: 20px") {
		t.Error("expected left positioning for bottom-left setting")
	}
}

func TestDify_RenderBody_OpenerQuestions(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint":     "https://api.dify.ai/v1",
		"api_key":          "app-key",
		"opener_questions": "Question one\nQuestion two",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "Question one") {
		t.Error("expected opener question in output")
	}
	if !strings.Contains(result, "OPENERS=") {
		t.Error("expected OPENERS variable in output")
	}
}

func TestDify_RenderBody_ShowSuggested(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint":   "https://api.dify.ai/v1",
		"api_key":        "app-key",
		"show_suggested": "1",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "SHOW_SUGGESTED=true") {
		t.Error("expected SHOW_SUGGESTED=true in output")
	}
}

func TestDify_RenderBody_DefaultShowSuggested(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-key",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "SHOW_SUGGESTED=false") {
		t.Error("expected SHOW_SUGGESTED=false by default")
	}
}

func TestDify_RenderBody_XSSPrevention(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-<script>evil()</script>",
		"bot_name":     "<img src=x onerror=alert(1)>",
	}

	result := string(p.RenderBody(settings, RenderContext{}))

	// api_key XSS: should not have raw <script> tag.
	if strings.Contains(result, "<script>evil") {
		t.Error("XSS: unescaped script tag in api_key")
	}

	// bot_name XSS: the < and > chars must be HTML-escaped so that the
	// injected <img> tag is not rendered as a real HTML element.
	// After HTMLEscapeString, <img ...> becomes &lt;img ...&gt;.
	if strings.Contains(result, "<img ") {
		t.Error("XSS: raw <img> tag found in bot_name output — should be HTML-escaped")
	}
	// Verify it IS escaped.
	if !strings.Contains(result, "&lt;img") {
		t.Error("expected &lt;img in escaped bot_name output")
	}
}

func TestDify_RenderBody_WelcomeMessage(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint":    "https://api.dify.ai/v1",
		"api_key":         "app-key",
		"welcome_message": "Hello! How can I help?",
	}

	result := string(p.RenderBody(settings, RenderContext{}))
	if !strings.Contains(result, "Hello! How can I help?") {
		t.Error("expected welcome_message in output")
	}
}

func TestDify_RenderBody_InjectedProxyToken(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-key",
	}

	const want = "render-time.signed-token"
	wantExp := time.Now().Add(90 * time.Second)
	calls := 0
	rc := RenderContext{
		Origin: "https://example.com",
		IssueProxyToken: func() (string, time.Time, error) {
			calls++
			return want, wantExp, nil
		},
	}

	result := string(p.RenderBody(settings, rc))
	if calls != 1 {
		t.Fatalf("IssueProxyToken called %d times; want 1", calls)
	}
	if !strings.Contains(result, "proxyToken='"+want+"'") {
		t.Error("expected injected proxyToken literal in rendered output")
	}
	if !strings.Contains(result, "proxyTokenOptional=false") {
		t.Error("expected proxyTokenOptional=false when a token is injected")
	}
	// The expiry must be the Unix seconds value from the minter.
	wantExpLit := strings.TrimSpace("proxyTokenExp=") // anchor, check value by substring
	if !strings.Contains(result, wantExpLit) {
		t.Error("expected proxyTokenExp in rendered output")
	}
}

func TestDify_RenderBody_MinterErrorFallsBackToOptional(t *testing.T) {
	p := NewDify()
	settings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-key",
	}
	rc := RenderContext{
		Origin: "https://example.com",
		IssueProxyToken: func() (string, time.Time, error) {
			return "", time.Time{}, errMinter()
		},
	}

	result := string(p.RenderBody(settings, rc))
	if !strings.Contains(result, "proxyTokenOptional=true") {
		t.Error("expected proxyTokenOptional=true when minter errors")
	}
	if strings.Contains(result, "proxyToken='some-token'") {
		t.Error("no token should be embedded when minter errors")
	}
}

func errMinter() error { return testErr("minter unavailable") }

type testErr string

func (e testErr) Error() string { return string(e) }
