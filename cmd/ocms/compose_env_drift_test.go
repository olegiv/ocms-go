// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestComposeForwardsEveryVariable fails when docker-compose.yml does not pass
// an OCMS_* variable the application reads into the ocms container, or passes
// one that has a production default with a value. Compose forwards only the
// variables its environment list names, so OCMS_TRUSTED_PROXIES (commented
// out) and OCMS_API_ALLOWED_CIDRS (missing) never reached the app and a
// production container could not start whatever .env said; and an entry such
// as OCMS_REQUIRE_TRUSTED_PROXIES=${OCMS_REQUIRE_TRUSTED_PROXIES:-} would set
// it empty, which turned its production default off. Config now treats an
// empty value as unset too; a bare entry keeps it out of the container
// altogether (review of the 2026-10-17 audit fixes). The variables are the env tags in
// internal/config/config.go and the OCMS_* string literals in the non-test
// code of cmd/ocms, internal and modules.
//
// Bug state: comment out OCMS_TRUSTED_PROXIES in docker-compose.yml, or give
// OCMS_REQUIRE_TRUSTED_PROXIES a ${...:-} value, and this fails.
func TestComposeForwardsEveryVariable(t *testing.T) {
	root := filepath.Join("..", "..")
	// OCMS_ENV_FILE names a file for the binary to read; Compose passes the
	// environment itself.
	notForwarded := []string{"OCMS_ENV_FILE"}

	literal := regexp.MustCompile(`"(OCMS_[A-Z0-9_]+)"`)
	read := map[string]bool{}
	err := walkRepoFiles(root, func(rel string) bool {
		return (strings.HasPrefix(rel, "cmd/ocms/") || strings.HasPrefix(rel, "internal/") || strings.HasPrefix(rel, "modules/")) &&
			strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(path, _ string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		for _, m := range literal.FindAllStringSubmatch(string(src), -1) {
			read[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(root, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`env:"(OCMS_[A-Z0-9_]+)`).FindAllStringSubmatch(string(config), -1) {
		read[m[1]] = true
	}
	var productionDefaults []string
	for _, m := range regexp.MustCompile(`set(?:Bool|Int)IfUnset\("(OCMS_[A-Z0-9_]+)"`).FindAllStringSubmatch(string(config), -1) {
		productionDefaults = append(productionDefaults, m[1])
	}
	if len(read) < 40 || len(productionDefaults) < 10 {
		t.Fatalf("found %d variables and %d production defaults; the scan is broken and this test is vacuous", len(read), len(productionDefaults))
	}

	var compose struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	data, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatalf("parsing docker-compose.yml: %v", err)
	}
	service, ok := compose.Services["ocms"]
	if !ok {
		t.Fatal("docker-compose.yml has no ocms service; the layout changed and this test is vacuous")
	}
	entries := map[string]string{} // name -> the entry as written
	for _, entry := range service.Environment {
		name, _, _ := strings.Cut(entry, "=")
		entries[name] = entry
	}

	var missing []string
	for name := range read {
		if _, ok := entries[name]; !ok && !slices.Contains(notForwarded, name) {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("docker-compose.yml does not pass %s into the ocms container; list each as a bare entry", strings.Join(missing, ", "))
	}
	for _, name := range productionDefaults {
		if entry, ok := entries[name]; ok && entry != name {
			t.Errorf("docker-compose.yml sets %q; pass %s without a value, so an unset variable stays unset and its production default applies", entry, name)
		}
	}
}
