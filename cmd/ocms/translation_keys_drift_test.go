// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// templateTranslationCall matches a literal key passed to T in an
// html/template file: {{T .AdminLang "key"}} or (T .Lang "key").
var templateTranslationCall = regexp.MustCompile(`(?:\{\{-?|\()\s*T\s+[$.\w]+\s+"([^"]+)"`)

// TestTranslationKeysExist fails when code passes a literal translation key
// that a language of the core catalog does not define. A key missing from
// English renders as the key itself; one missing from Russian shows the
// English text. The module view tests cannot notice either: their packages
// load no translations, so every key renders as itself there (review of the
// 2026-10-17 audit fixes).
//
// Keys are read from Go calls to i18n.T, formT and T methods such as
// PageContext.T, from templ components through their generated _templ.go
// files, and from {{T}} calls in admin html templates. They are looked up in
// the global catalog: internal/i18n/locales plus every module's locales;
// theme locales are separate and not part of it. Every language of
// internal/i18n/locales must define every key. Keys built at run time
// ("migrator.imported_" + kind), held in variables, or passed through other
// wrappers are not checked.
//
// Bug state: rename the warning_settings_unreadable key in
// modules/hcaptcha/views.templ, regenerate, and this names it.
func TestTranslationKeysExist(t *testing.T) {
	root := filepath.Join("..", "..")
	catalog := map[string]map[string]bool{} // language → keys
	coreLangs := map[string]bool{}          // languages of internal/i18n/locales
	used := map[string][]string{}           // key → positions
	fset := token.NewFileSet()
	err := walkRepoFiles(root, func(rel string) bool {
		if strings.Contains(rel, "/testdata/") || strings.HasSuffix(rel, "_test.go") {
			return false
		}
		if strings.HasSuffix(rel, ".html") {
			return !strings.Contains(rel, "themes/")
		}
		return strings.HasSuffix(rel, ".go") || isGlobalCatalogFile(rel)
	}, func(file, rel string) error {
		src, err := os.ReadFile(file) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		if strings.HasSuffix(rel, ".html") {
			for _, match := range templateTranslationCall.FindAllSubmatchIndex(src, -1) {
				line := 1 + strings.Count(string(src[:match[0]]), "\n")
				key := string(src[match[2]:match[3]])
				used[key] = append(used[key], fmt.Sprintf("%s:%d", rel, line))
			}
			return nil
		}
		if strings.HasSuffix(rel, ".json") {
			var messages struct {
				Messages []struct {
					ID string `json:"id"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(src, &messages); err != nil {
				t.Errorf("%s: %v", rel, err)
				return nil
			}
			lang := path.Base(path.Dir(rel))
			if strings.HasPrefix(rel, "internal/i18n/locales/") {
				coreLangs[lang] = true
			}
			if catalog[lang] == nil {
				catalog[lang] = map[string]bool{}
			}
			for _, m := range messages.Messages {
				catalog[lang][m.ID] = true
			}
			return nil
		}
		parsed, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if key, ok := literalTranslationKey(call); ok {
					used[key] = append(used[key], fset.Position(call.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if len(coreLangs) < 2 || len(used) < 500 {
		t.Fatalf("found %d core languages and %d keys in use; the scan is broken and this test is vacuous", len(coreLangs), len(used))
	}
	langs := make([]string, 0, len(coreLangs))
	for lang := range coreLangs {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for key, positions := range used {
		for _, lang := range langs {
			if !catalog[lang][key] {
				t.Errorf("%s uses translation key %q, which the %s catalog does not define", positions[0], key, lang)
			}
		}
	}
}

// isGlobalCatalogFile reports whether rel is a messages file of the global
// translation catalog: <dir>/locales/<lang>/messages.json outside themes.
func isGlobalCatalogFile(rel string) bool {
	return path.Base(rel) == "messages.json" &&
		path.Base(path.Dir(path.Dir(rel))) == "locales" &&
		!strings.Contains(rel, "themes/")
}

// literalTranslationKey returns the key of a translation call with a literal
// key: i18n.T(lang, "key", ...), formT(lang, "key") or a T method such as
// PageContext.T("key").
func literalTranslationKey(call *ast.CallExpr) (string, bool) {
	arg := -1
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if fun.Sel.Name == "T" {
			arg = 0
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "i18n" {
				arg = 1
			}
		}
	case *ast.Ident:
		if fun.Name == "formT" {
			arg = 1
		}
	}
	if arg < 0 {
		return "", false
	}
	if len(call.Args) <= arg {
		return "", false
	}
	lit, ok := call.Args[arg].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	key, err := strconv.Unquote(lit.Value)
	return key, err == nil
}
