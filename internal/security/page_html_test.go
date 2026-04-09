// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"strings"
	"testing"
)

func TestSanitizePageHTML(t *testing.T) {
	raw := `<p>Hello</p><script>alert('x')</script><a href="javascript:alert(1)">x</a>`
	got := SanitizePageHTML(raw)

	if strings.Contains(got, "<script") {
		t.Fatalf("expected script tags removed, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Fatalf("expected javascript URLs removed, got %q", got)
	}
	if !strings.Contains(got, "<p>Hello</p>") {
		t.Fatalf("expected safe paragraph preserved, got %q", got)
	}
}

func TestSanitizePageHTML_PreservesFormEmbed(t *testing.T) {
	raw := `<p>Before</p><div data-ocms-form="contact-form" class="ocms-embedded-form">Form: Contact</div><p>After</p>`
	got := SanitizePageHTML(raw)

	if !strings.Contains(got, `data-ocms-form="contact-form"`) {
		t.Fatalf("expected data-ocms-form attribute preserved, got %q", got)
	}
	if !strings.Contains(got, `class="ocms-embedded-form"`) {
		t.Fatalf("expected ocms-embedded-form class preserved, got %q", got)
	}
	if !strings.Contains(got, "<p>Before</p>") {
		t.Fatalf("expected surrounding content preserved, got %q", got)
	}
}

func TestSanitizePageHTML_RejectsInvalidFormSlug(t *testing.T) {
	raw := `<div data-ocms-form="<script>alert(1)</script>" class="ocms-embedded-form">XSS</div>`
	got := SanitizePageHTML(raw)

	if strings.Contains(got, "<script") {
		t.Fatalf("expected script in attribute to be stripped, got %q", got)
	}
}
