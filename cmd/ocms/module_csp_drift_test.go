// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
)

// thirdPartyLoads maps each kind of browser load in module source to the CSP
// directive that governs it; each pattern captures the https host. Images are
// not listed: img-src allows every https: host.
var thirdPartyLoads = []struct {
	directive string
	pattern   *regexp.Regexp
}{
	{"script-src", regexp.MustCompile(`<script\b[^>]*\bsrc\s*=\s*["']?https://([a-z0-9.-]+)`)},
	{"script-src", regexp.MustCompile("\\.src\\s*=\\s*[\"'`]https://([a-z0-9.-]+)")}, // script element built in JS
	{"frame-src", regexp.MustCompile(`<iframe\b[^>]*\bsrc\s*=\s*["']?https://([a-z0-9.-]+)`)},
	{"style-src", regexp.MustCompile(`<link\b[^>]*\bhref\s*=\s*["']?https://([a-z0-9.-]+)`)},
	{"connect-src", regexp.MustCompile("fetch\\(\\s*[\"'`]https://([a-z0-9.-]+)")},
}

// TestModuleThirdPartyHostsAreInCSPSources fails when a built-in module loads
// a resource from a literal third-party host that its CSPSources does not
// allow for the directive that load needs (a script under script-src, an
// iframe under frame-src).
//
// The base Content-Security-Policy keeps scripts, connections and (apart
// from Google Fonts and YouTube) styles and frames first-party; each module
// adds the hosts it needs only while it is active (security audit
// 2026-10-17, I-4). A module that loads a host it does not declare is blocked
// by the browser in every environment, which this catches before a release
// does.
//
// Bug state: drop "https://*.hcaptcha.com" from the hcaptcha module's
// CSPSources and this names js.hcaptcha.com.
func TestModuleThirdPartyHostsAreInCSPSources(t *testing.T) {
	const modulePath = "github.com/olegiv/ocms-go/"
	root := filepath.Join("..", "..")
	found := map[string]bool{}
	var problems []string

	for _, mod := range allModules() {
		pkg := reflect.TypeOf(mod).Elem().PkgPath()
		if !strings.HasPrefix(pkg, modulePath+"modules/") {
			continue
		}
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath)))
		var sources map[string][]string
		if sourcer, ok := mod.(module.CSPSourcer); ok {
			sources = sourcer.CSPSources()
		}
		for _, load := range thirdPartyHosts(t, dir) {
			found[load.host] = true
			if !cspAllowsHost(sources[load.directive], load.host) {
				problems = append(problems, mod.Name()+" loads https://"+load.host+" ("+load.directive+")")
			}
		}
	}

	for _, want := range []string{"js.hcaptcha.com", "www.googletagmanager.com"} {
		if !found[want] {
			t.Fatalf("scan did not find %s in module source; the loader pattern changed and this test is vacuous", want)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("these modules load hosts their CSPSources does not allow, so the browser "+
			"blocks them:\n  %s", strings.Join(problems, "\n  "))
	}
}

// thirdPartyLoad is one host that module source loads, with the directive
// that governs the load.
type thirdPartyLoad struct {
	directive string
	host      string
}

// thirdPartyHosts returns the loads that non-test, non-generated source files
// under dir make from third-party hosts in the browser.
func thirdPartyHosts(t *testing.T, dir string) []thirdPartyLoad {
	t.Helper()
	seen := map[thirdPartyLoad]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".templ", ".html", ".js":
		default:
			return nil
		}
		source, err := os.ReadFile(path) // #nosec G304 -- paths come from walking the repository tree
		if err != nil {
			return err
		}
		for _, load := range thirdPartyLoads {
			for _, match := range load.pattern.FindAllStringSubmatch(string(source), -1) {
				seen[thirdPartyLoad{load.directive, match[1]}] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	loads := make([]thirdPartyLoad, 0, len(seen))
	for load := range seen {
		loads = append(loads, load)
	}
	sort.Slice(loads, func(i, j int) bool {
		if loads[i].host != loads[j].host {
			return loads[i].host < loads[j].host
		}
		return loads[i].directive < loads[j].directive
	})
	return loads
}

// cspAllowsHost reports whether a CSP source list admits https://host, either
// exactly or through a https://*.domain wildcard.
func cspAllowsHost(sources []string, host string) bool {
	for _, source := range sources {
		pattern := strings.TrimPrefix(source, "https://")
		if pattern == host {
			return true
		}
		if suffix, ok := strings.CutPrefix(pattern, "*."); ok && strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
