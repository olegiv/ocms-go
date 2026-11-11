// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	templModuleVersion = regexp.MustCompile(`(?m)^\s*github\.com/a-h/templ\s+(v\S+)`)
	templInstallPin    = regexp.MustCompile(`github\.com/a-h/templ/cmd/templ@([A-Za-z0-9._+-]+)`)
)

// TestTemplInstallPinsMatchGoMod checks that every instruction or workflow
// step installing the templ CLI names the version go.mod requires. Generated
// code only compiles against the runtime of the templ that generated it, so a
// CLI at another version, @latest included, writes views that do not build or
// that the release workflow reports as stale.
func TestTemplInstallPinsMatchGoMod(t *testing.T) {
	root := filepath.Join("..", "..")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatal(err)
	}
	m := templModuleVersion.FindSubmatch(goMod)
	if m == nil {
		t.Fatal("go.mod does not require github.com/a-h/templ")
	}
	want := string(m[1])

	workflowPins := 0
	check := func(rel string, data []byte) {
		for _, pin := range templInstallPin.FindAllSubmatch(data, -1) {
			if strings.HasPrefix(rel, ".github/workflows/") {
				workflowPins++
			}
			if string(pin[1]) != want {
				t.Errorf("%s installs templ@%s; go.mod requires %s", rel, pin[1], want)
			}
		}
	}

	// Documentation and scripts; CHANGELOG.md is history.
	keep := func(rel string) bool {
		switch ext := path.Ext(rel); {
		case rel == "CHANGELOG.md", strings.HasPrefix(rel, ".github/"):
			return false
		case ext == ".md", ext == ".sh", ext == ".mk", path.Base(rel) == "Makefile", path.Base(rel) == "Dockerfile":
			return true
		}
		return false
	}
	if err := walkRepoFiles(root, keep, func(file, rel string) error {
		data, err := os.ReadFile(file) // #nosec G304 -- path from listing the repository
		if err != nil {
			return err
		}
		check(rel, data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Workflows are read directly: a walk of an exported tree skips dot directories.
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range workflows {
		data, err := os.ReadFile(file) // #nosec G304 -- path from a fixed repository glob
		if err != nil {
			t.Fatal(err)
		}
		check(".github/workflows/"+filepath.Base(file), data)
	}
	if workflowPins == 0 {
		t.Error("no workflow installs the templ CLI; the release workflow's pin was not found")
	}
}
