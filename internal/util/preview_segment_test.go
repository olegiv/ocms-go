// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "testing"

// TestShadowsPreviewRoute pins which page paths collide with the preview
// routes: preview as the first segment, or the second after a
// language-shaped segment. Deeper segments are ordinary content, so an
// existing alias such as blog/preview or a/b/preview/c keeps saving
// (2026-11-03 review of the C-3 fix).
func TestShadowsPreviewRoute(t *testing.T) {
	for path, want := range map[string]bool{
		"preview":                  true,
		"preview/share":            true,
		"blog/preview/x":           false,
		"blog/preview":             false,
		"docs/preview/share/guide": true,
		"ru/preview":               true,
		"a/b/preview/c":            false,
		"docs/guide/preview":       false,
		"pt-br/preview/x":          true,
		"/ru/preview/":             true,
		"":                         false,
		"previews":                 false,
		"preview-notes":            false,
		"docs/previews/x":          false,
		"about/preview-guide":      false,
		"Preview/x":                false,
		"blog/post/55":             false,
	} {
		if got := ShadowsPreviewRoute(path); got != want {
			t.Errorf("ShadowsPreviewRoute(%q) = %v, want %v", path, got, want)
		}
	}
}
