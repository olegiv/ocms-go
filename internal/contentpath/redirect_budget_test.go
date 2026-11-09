// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath

import (
	"reflect"
	"strings"
	"testing"
)

func TestRedirectWildcardCapturesAndWorkBound(t *testing.T) {
	for _, tc := range []struct {
		path, pattern string
		want          []string
	}{
		{"/a/b/c", "/**/**", []string{"", "a/b/c"}},
		{"/a/b/c", "/**/*/**", []string{"", "a", "b/c"}},
		{"/a/b/c", "/**/b/**", []string{"a", "c"}},
		{"/a", "/a/**/**", []string{"", ""}},
		{"/user/login/foo", "/user/login*", []string{"foo"}},
	} {
		matched, captures := MatchRedirect(tc.path, tc.pattern, true)
		if !matched || !reflect.DeepEqual(captures, tc.want) {
			t.Fatalf("%s against %s = %v, %v; want %v", tc.path, tc.pattern, matched, captures, tc.want)
		}
	}
	long := strings.Repeat("a/", 20000) + "tail"
	if matched, captures := MatchRedirect("/old/"+long, "/old/**", true); !matched || len(captures) != 1 || captures[0] != long {
		t.Fatal("long trailing wildcard did not preserve its capture")
	}
	if matched, _ := MatchRedirect("/"+long, "/**/**/**/absent", true); matched {
		t.Fatal("unmatched repeated wildcard accepted")
	}
	// Large ambiguous patterns must terminate without exploring partitions.
	if matched, _ := MatchRedirect("/"+strings.Repeat("a/", 500), "/"+strings.Repeat("**/", 500)+"missing", true); matched {
		t.Fatal("ambiguous failing pattern matched")
	}
	// Exceeding the public-work budget fails closed, without large allocation.
	if matched, _ := MatchRedirect("/"+strings.Repeat("a/", 2000), "/"+strings.Repeat("**/", 2000), true); matched {
		t.Fatal("over-budget wildcard pattern matched")
	}
}

func BenchmarkRedirectTrailingDoubleWildcard(b *testing.B) {
	path := "/old/" + strings.Repeat("a/", 20000) + "tail"
	b.ReportAllocs()
	for b.Loop() {
		MatchRedirect(path, "/old/**", true)
	}
}
