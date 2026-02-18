// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "testing"

func TestCSPNonceAttr(t *testing.T) {
	if got := CSPNonceAttr(""); got != "" {
		t.Fatalf("CSPNonceAttr(empty) = %q, want empty", got)
	}
	if got := CSPNonceAttr("abc123"); got != ` nonce="abc123"` {
		t.Fatalf("CSPNonceAttr(abc123) = %q", got)
	}
}

func TestAddNonceToScriptTags(t *testing.T) {
	input := `<script src="/a.js"></script><script nonce="x">ok</script><script>inline()</script>`
	got := AddNonceToScriptTags(input, "noncev")
	want := `<script nonce="noncev" src="/a.js"></script><script nonce="x">ok</script><script nonce="noncev">inline()</script>`
	if got != want {
		t.Fatalf("AddNonceToScriptTags() = %q, want %q", got, want)
	}
}
