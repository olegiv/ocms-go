// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"html/template"
	"testing"
)

func TestHcaptchaEnabled_Default(t *testing.T) {
	r := &Renderer{}
	if r.HcaptchaEnabled() {
		t.Error("HcaptchaEnabled() should return false for empty renderer")
	}
}

func TestHcaptchaWidgetHTML_Default(t *testing.T) {
	r := &Renderer{}
	if got := r.HcaptchaWidgetHTML(); got != "" {
		t.Errorf("HcaptchaWidgetHTML() = %q; want empty string", got)
	}
}

func TestHcaptchaEnabled_WithFunc(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"hcaptchaEnabled": func() bool { return true },
	})

	if !r.HcaptchaEnabled() {
		t.Error("HcaptchaEnabled() should return true when func returns true")
	}
}

func TestHcaptchaEnabled_FuncReturnsFalse(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"hcaptchaEnabled": func() bool { return false },
	})

	if r.HcaptchaEnabled() {
		t.Error("HcaptchaEnabled() should return false when func returns false")
	}
}

func TestHcaptchaWidgetHTML_WithFunc(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"hcaptchaWidget": func() template.HTML {
			return template.HTML(`<div class="h-captcha" data-sitekey="test"></div>`)
		},
	})

	got := r.HcaptchaWidgetHTML()
	want := `<div class="h-captcha" data-sitekey="test"></div>`
	if got != want {
		t.Errorf("HcaptchaWidgetHTML() = %q; want %q", got, want)
	}
}

func TestHcaptchaWidgetHTML_EmptyWhenDisabled(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"hcaptchaWidget": func() template.HTML { return "" },
	})

	got := r.HcaptchaWidgetHTML()
	if got != "" {
		t.Errorf("HcaptchaWidgetHTML() = %q; want empty string", got)
	}
}

func TestAdminLangOptions_Default(t *testing.T) {
	r := &Renderer{}
	opts := r.AdminLangOptions()
	if len(opts) != 1 {
		t.Fatalf("AdminLangOptions() returned %d options; want 1", len(opts))
	}
	if opts[0].Code != "en" || opts[0].Name != "English" {
		t.Errorf("AdminLangOptions()[0] = {%q, %q}; want {\"en\", \"English\"}", opts[0].Code, opts[0].Name)
	}
}

func TestAdminLangOptions_WrongFuncType(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"adminLangOptions": func() string { return "wrong" },
	})

	opts := r.AdminLangOptions()
	// Should fall back to default
	if len(opts) != 1 {
		t.Fatalf("AdminLangOptions() returned %d options; want 1 (fallback)", len(opts))
	}
	if opts[0].Code != "en" {
		t.Errorf("opts[0].Code = %q; want \"en\" (fallback)", opts[0].Code)
	}
}

func TestSentinelIsActive_Default(t *testing.T) {
	r := &Renderer{}
	if r.SentinelIsActive() {
		t.Error("SentinelIsActive() should return false for empty renderer")
	}
}

func TestSentinelIsIPBanned_Default(t *testing.T) {
	r := &Renderer{}
	if r.SentinelIsIPBanned("1.2.3.4") {
		t.Error("SentinelIsIPBanned() should return false for empty renderer")
	}
}

func TestSentinelIsIPWhitelisted_Default(t *testing.T) {
	r := &Renderer{}
	if r.SentinelIsIPWhitelisted("1.2.3.4") {
		t.Error("SentinelIsIPWhitelisted() should return false for empty renderer")
	}
}

func TestSentinelIsActive_WithFunc(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"sentinelIsActive": func() bool { return true },
	})

	if !r.SentinelIsActive() {
		t.Error("SentinelIsActive() should return true when func returns true")
	}
}

func TestSentinelIsIPBanned_WithFunc(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"sentinelIsIPBanned": func(ip string) bool { return ip == "10.0.0.1" },
	})

	if !r.SentinelIsIPBanned("10.0.0.1") {
		t.Error("SentinelIsIPBanned(\"10.0.0.1\") should return true")
	}
	if r.SentinelIsIPBanned("10.0.0.2") {
		t.Error("SentinelIsIPBanned(\"10.0.0.2\") should return false")
	}
}

func TestSentinelIsIPWhitelisted_WithFunc(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"sentinelIsIPWhitelisted": func(ip string) bool { return ip == "127.0.0.1" },
	})

	if !r.SentinelIsIPWhitelisted("127.0.0.1") {
		t.Error("SentinelIsIPWhitelisted(\"127.0.0.1\") should return true")
	}
	if r.SentinelIsIPWhitelisted("10.0.0.1") {
		t.Error("SentinelIsIPWhitelisted(\"10.0.0.1\") should return false")
	}
}

func TestListSidebarModules_Default(t *testing.T) {
	r := &Renderer{}
	modules := r.ListSidebarModules()
	if modules != nil {
		t.Errorf("ListSidebarModules() = %v; want nil", modules)
	}
}
