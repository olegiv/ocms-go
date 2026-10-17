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

// thirdPartyLoad matches a browser load from an https host in module source:
// an src attribute or property, or a fetch call.
var thirdPartyLoad = regexp.MustCompile("(?:\\bsrc\\s*=\\s*|\\.src\\s*=\\s*|fetch\\(\\s*)[\"'`]?https://([a-z0-9.-]+)")

// TestModuleThirdPartyHostsAreInCSPSources fails when a built-in module loads
// a resource from a third-party host that its CSPSources does not allow.
//
// The base Content-Security-Policy is first-party; each module adds the hosts
// it needs only while it is active (security audit 2026-10-17, I-4). A module
// that loads a host it does not declare is blocked by the browser in every
// environment, which this catches before a release does.
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
		hosts := thirdPartyHosts(t, dir)
		var allowed []string
		if sourcer, ok := mod.(module.CSPSourcer); ok {
			for _, values := range sourcer.CSPSources() {
				allowed = append(allowed, values...)
			}
		}
		for _, host := range hosts {
			found[host] = true
			if !cspAllowsHost(allowed, host) {
				problems = append(problems, mod.Name()+" loads https://"+host)
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

// thirdPartyHosts returns the hosts that non-test, non-generated source files
// under dir load in the browser.
func thirdPartyHosts(t *testing.T, dir string) []string {
	t.Helper()
	seen := map[string]bool{}
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
		for _, match := range thirdPartyLoad.FindAllStringSubmatch(string(source), -1) {
			seen[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
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
