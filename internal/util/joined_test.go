// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "testing"

// TestJoinedValueHas checks that a checkbox option counts as checked only for
// a whole stored value: "Post" is not checked by "Postcard", and an option
// that contains ", " still matches. The templ form and the themes' form
// templates (checkedOption) both use it.
func TestJoinedValueHas(t *testing.T) {
	for _, tc := range []struct {
		value, option string
		want          bool
	}{
		{"Post", "Post", true},
		{"Postcard", "Post", false},
		{"Email, Postcard", "Post", false},
		{"Post, Email", "Post", true},
		{"Email, Post", "Post", true},
		{"Email, Post, Phone", "Post", true},
		{"Yes, please", "Yes, please", true},
		{"No, Yes, please", "Yes, please", true},
		{"", "Post", false},
	} {
		if got := JoinedValueHas(tc.value, tc.option); got != tc.want {
			t.Errorf("JoinedValueHas(%q, %q) = %t, want %t", tc.value, tc.option, got, tc.want)
		}
	}
}
