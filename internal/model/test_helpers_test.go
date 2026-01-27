// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "testing"

// jsonArrayParseTest defines a test case for parsing JSON arrays.
type jsonArrayParseTest struct {
	name  string
	input string
	want  []string
}

// standardJSONArrayParseTests returns common test cases for JSON array parsing.
func standardJSONArrayParseTests(singleItem, multiItem1, multiItem2, multiItem3 string) []jsonArrayParseTest {
	return []jsonArrayParseTest{
		{name: "empty string", input: "", want: []string{}},
		{name: "empty array", input: "[]", want: []string{}},
		{name: "single item", input: `["` + singleItem + `"]`, want: []string{singleItem}},
		{name: "multiple items", input: `["` + multiItem1 + `","` + multiItem2 + `","` + multiItem3 + `"]`, want: []string{multiItem1, multiItem2, multiItem3}},
	}
}

// assertStringSliceEqual asserts that two string slices are equal.
func assertStringSliceEqual(t *testing.T, testName string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", testName, got, want)
		return
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("%s[%d] = %q, want %q", testName, i, v, want[i])
		}
	}
}

// hasItemTest defines a test case for checking item membership.
type hasItemTest struct {
	item string
	want bool
}

// runHasItemTests runs membership test cases with the provided check function.
func runHasItemTests(t *testing.T, tests []hasItemTest, checkFn func(string) bool) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.item, func(t *testing.T) {
			if got := checkFn(tt.item); got != tt.want {
				t.Errorf("check(%q) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}
}
