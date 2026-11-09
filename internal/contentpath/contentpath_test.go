// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath_test

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestPureLanguageAliasAndRedirectRules(t *testing.T) {
	languages := contentpath.ActiveLanguages([]store.Language{{Code: "en", IsActive: true}, {Code: "ru", IsActive: true}, {Code: "admin", IsActive: true}, {Code: "RU", IsActive: true}})
	if len(languages) != 2 {
		t.Fatalf("unsafe language prefixes exposed: %+v", languages)
	}
	for _, tc := range []struct {
		path, code, rest string
		ok               bool
	}{{"/ru/a", "ru", "/a", true}, {"/en/a", "en", "/a", true}, {"/ru", "ru", "/", true}, {"/RU/a", "", "", false}, {"/admin/a", "", "", false}} {
		lang, rest, ok := contentpath.MatchLanguagePrefix(tc.path, languages)
		if ok != tc.ok || lang.Code != tc.code || rest != tc.rest {
			t.Fatalf("prefix %s: %+v %q %v", tc.path, lang, rest, ok)
		}
	}
	if contentpath.AliasPath("/ru/old/about", "ru") != "old/about" {
		t.Fatal("alias prefix not removed")
	}
	if _, valid := contentpath.ExplicitLanguage("ru", "en"); valid {
		t.Fatal("disagreeing explicit context accepted")
	}
	redirects := []store.Redirect{{SourcePath: "/old/**", TargetUrl: "/new/**", StatusCode: 301, IsWildcard: true}, {SourcePath: "/old/exact", TargetUrl: "/other", StatusCode: 302}}
	target, status, matched, err := contentpath.FirstRedirect(redirects, "/old/exact", "q=1")
	if err != nil || !matched || status != 301 || target != "/new/exact?q=1" {
		t.Fatalf("ordering/captures/query changed: %s %d %v %v", target, status, matched, err)
	}
	if _, _, matched, err = contentpath.FirstRedirect([]store.Redirect{{SourcePath: "/api/**", TargetUrl: "/x", IsWildcard: true}}, "/api/test", ""); err != nil || matched {
		t.Fatal("private paths redirected")
	}
	if _, _, matched, err = contentpath.FirstRedirect([]store.Redirect{{SourcePath: "/x", TargetUrl: "//outside.test/x"}}, "/x", ""); !matched || err == nil {
		t.Fatal("external redirect allowed")
	}
}
