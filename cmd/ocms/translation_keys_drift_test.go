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

// templateTranslationCall matches a literal key passed to T or TTheme in an
// html/template file, capturing the function and the key: {{T .AdminLang
// "key"}}, (T .Lang "key") or {{TTheme .LangCode "key"}}. Pipelines such as
// {{"key" | T .Lang}} are not matched.
var templateTranslationCall = regexp.MustCompile(`(?:\{\{-?|\()\s*(T|TTheme)\s+[$.\w]+\s+"([^"]+)"`)

// translationKeyShape matches a string shaped like a translation key.
var translationKeyShape = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$`)

// notTranslationKeys are string literals in Go code that look like keys of
// a catalog namespace but are not translation keys, with what they are.
var notTranslationKeys = map[string]string{
	"auth.before_login": "module hook name",
	"auth.login_widget": "module hook name",
	"docs.html":         "template name",
	"export.json":       "file name",
	"media.deleted":     "webhook event",
}

// TestTranslationKeysExist fails when code uses a translation key that a
// language of the core catalog does not define. A key missing from English
// renders as the key itself; one missing from Russian shows the English
// text. The module view tests cannot notice either: their packages load no
// translations, so every key renders as itself there (review of the
// 2026-10-17 audit fixes).
//
// Keys are read from Go calls to i18n.T, formT and T methods such as
// PageContext.T; from every key-shaped string literal in Go code whose first
// segment is a namespace of the global catalog, which covers keys held in
// constants, struct fields and lists; from templ components through their
// generated _templ.go files; and from {{T}} and {{TTheme}} calls in html
// templates. Keys are looked up in the global catalog (internal/i18n/locales
// plus every module's locales), except that {{TTheme}} in a theme template
// looks in that theme's locales first, as TTheme does; {{T}} reads only the
// global catalog, in a theme too. Every language of internal/i18n/locales
// must define every key. Keys built at run time ("migrator.imported_" +
// kind), keys in a namespace the catalog does not have, and keys passed
// through variables are not checked: this is a best-effort scan. A literal
// that only looks like a key goes in notTranslationKeys.
//
// Bug state: rename the warning_settings_unreadable key in
// modules/hcaptcha/views.templ, regenerate, and this names it.
func TestTranslationKeysExist(t *testing.T) {
	scan := newTranslationScan()
	err := walkRepoFiles(filepath.Join("..", ".."), func(rel string) bool {
		if strings.Contains(rel, "/testdata/") || strings.HasSuffix(rel, "_test.go") {
			return false
		}
		return strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".html") || isCatalogFile(rel)
	}, func(file, rel string) error {
		src, err := os.ReadFile(file) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		switch {
		case isCatalogFile(rel):
			return scan.addCatalog(rel, src)
		case strings.HasSuffix(rel, ".html"):
			scan.addTemplate(rel, src)
			return nil
		default:
			return scan.addGo(rel, src)
		}
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if len(scan.coreLangs) < 2 || len(scan.calls) < 500 || len(scan.literals) < 500 || len(scan.themeKeys) == 0 {
		t.Fatalf("found %d core languages, %d keys in calls, %d key-shaped literals and %d themes using keys; the scan is broken and this test is vacuous",
			len(scan.coreLangs), len(scan.calls), len(scan.literals), len(scan.themeKeys))
	}
	for _, problem := range scan.problems() {
		t.Error(problem)
	}
}

// translationScan collects the translation catalogs and the keys the code
// uses. Its maps are filled by the add methods and read by problems.
type translationScan struct {
	global    map[string]map[string]bool            // language → keys
	themes    map[string]map[string]map[string]bool // theme dir → language → keys
	coreLangs map[string]bool                       // languages of internal/i18n/locales
	calls     map[string][]string                   // key passed to a translation call → positions
	literals  map[string][]string                   // key-shaped Go string literal → positions
	themeKeys map[string]map[string][]string        // theme dir → key → positions
	fset      *token.FileSet
}

// newTranslationScan returns an empty scan.
func newTranslationScan() *translationScan {
	return &translationScan{
		global:    map[string]map[string]bool{},
		themes:    map[string]map[string]map[string]bool{},
		coreLangs: map[string]bool{},
		calls:     map[string][]string{},
		literals:  map[string][]string{},
		themeKeys: map[string]map[string][]string{},
		fset:      token.NewFileSet(),
	}
}

// addCatalog records the keys of a messages file.
func (s *translationScan) addCatalog(rel string, src []byte) error {
	var messages struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(src, &messages); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	lang := path.Base(path.Dir(rel))
	catalog := s.global
	if theme := themeDir(rel); theme != "" {
		if s.themes[theme] == nil {
			s.themes[theme] = map[string]map[string]bool{}
		}
		catalog = s.themes[theme]
	} else if strings.HasPrefix(rel, "internal/i18n/locales/") {
		s.coreLangs[lang] = true
	}
	if catalog[lang] == nil {
		catalog[lang] = map[string]bool{}
	}
	for _, m := range messages.Messages {
		catalog[lang][m.ID] = true
	}
	return nil
}

// addTemplate records the keys an html template passes to T or TTheme. A
// theme's TTheme keys are kept per theme; T keys are global everywhere.
func (s *translationScan) addTemplate(rel string, src []byte) {
	theme := themeDir(rel)
	for _, match := range templateTranslationCall.FindAllSubmatchIndex(src, -1) {
		function, key := string(src[match[2]:match[3]]), string(src[match[4]:match[5]])
		pos := fmt.Sprintf("%s:%d", rel, 1+strings.Count(string(src[:match[0]]), "\n"))
		if theme == "" || function != "TTheme" {
			s.calls[key] = append(s.calls[key], pos)
			continue
		}
		if s.themeKeys[theme] == nil {
			s.themeKeys[theme] = map[string][]string{}
		}
		s.themeKeys[theme][key] = append(s.themeKeys[theme][key], pos)
	}
}

// addGo records the keys a Go file passes to translation calls and its
// key-shaped string literals.
func (s *translationScan) addGo(rel string, src []byte) error {
	parsed, err := parser.ParseFile(s.fset, rel, src, 0)
	if err != nil {
		return err
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if key, ok := literalTranslationKey(node); ok {
				s.calls[key] = append(s.calls[key], s.fset.Position(node.Pos()).String())
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				break
			}
			if value, err := strconv.Unquote(node.Value); err == nil && translationKeyShape.MatchString(value) {
				s.literals[value] = append(s.literals[value], s.fset.Position(node.Pos()).String())
			}
		}
		return true
	})
	return nil
}

// problems lists every key missing from a core language, sorted.
func (s *translationScan) problems() []string {
	langs := make([]string, 0, len(s.coreLangs))
	for lang := range s.coreLangs {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	namespaces := map[string]bool{}
	for _, keys := range s.global {
		for key := range keys {
			namespaces[strings.SplitN(key, ".", 2)[0]] = true
		}
	}
	var problems []string
	missing := func(pos, key, hint string, defined func(lang string) bool) {
		for _, lang := range langs {
			if !defined(lang) {
				problems = append(problems, fmt.Sprintf("%s uses translation key %q, which the %s catalog does not define%s", pos, key, lang, hint))
			}
		}
	}
	for key, positions := range s.calls {
		missing(positions[0], key, "", func(lang string) bool { return s.global[lang][key] })
	}
	for key, positions := range s.literals {
		_, listed := notTranslationKeys[key]
		if _, called := s.calls[key]; called || listed || strings.HasSuffix(key, "_") || !namespaces[strings.SplitN(key, ".", 2)[0]] {
			continue // checked as a call, not a key, a run-time prefix, or another namespace
		}
		missing(positions[0], key, " (if this string is not a translation key, add it to notTranslationKeys)",
			func(lang string) bool { return s.global[lang][key] })
	}
	for key, what := range notTranslationKeys {
		if _, used := s.literals[key]; !used {
			problems = append(problems, fmt.Sprintf("notTranslationKeys lists %q (%s), which no Go code uses any more; remove it", key, what))
		}
	}
	for theme, keys := range s.themeKeys {
		for key, positions := range keys {
			missing(positions[0], key, "", func(lang string) bool { return s.themes[theme][lang][key] || s.global[lang][key] })
		}
	}
	sort.Strings(problems)
	return problems
}

// isCatalogFile reports whether rel is a messages file of a translation
// catalog: <dir>/locales/<lang>/messages.json.
func isCatalogFile(rel string) bool {
	return path.Base(rel) == "messages.json" && path.Base(path.Dir(path.Dir(rel))) == "locales"
}

// themeDir returns the theme directory rel belongs to, such as
// internal/themes/default, or "" outside a theme.
func themeDir(rel string) string {
	i := strings.Index(rel, "themes/")
	if i < 0 || (i > 0 && rel[i-1] != '/') {
		return ""
	}
	name, _, found := strings.Cut(rel[i+len("themes/"):], "/")
	if !found {
		return ""
	}
	return rel[:i+len("themes/")] + name
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
	if arg < 0 || len(call.Args) <= arg {
		return "", false
	}
	lit, ok := call.Args[arg].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	key, err := strconv.Unquote(lit.Value)
	return key, err == nil
}
