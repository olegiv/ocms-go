// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestSiteMakefileRequiresTheEnvFile fails when a site.mk target that runs,
// stops or migrates the site's server goes ahead without the site .env.
// site.mk reads the file with errors hidden, so without it the database fell
// back to data/ocms.db and the port to 8080, which may be another server's,
// for `make stop` to kill (review of the 2026-10-17 audit fixes). It checks
// the prerequisites and, with make installed, runs the guard itself.
//
// Bug state: drop require-env-file from the stop target, or make its recipe
// succeed without the file, and this fails.
func TestSiteMakefileRequiresTheEnvFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	siteMK := filepath.Join(root, "site.mk")
	data, err := os.ReadFile(siteMK) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := func(target string) []string {
		m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:([^#\n]*)`).FindSubmatch(data)
		if m == nil {
			t.Fatalf("site.mk has no %s target; the layout changed and this test is vacuous", target)
		}
		return strings.Fields(string(m[1]))
	}
	for _, target := range []string{"dev", "run", "stop", "restart", "require-db-path"} {
		if !slices.Contains(prerequisites(target), "require-env-file") {
			t.Errorf("site.mk's %s target runs without require-env-file", target)
		}
	}
	for _, target := range []string{"migrate-up", "migrate-down", "migrate-status"} {
		if !slices.Contains(prerequisites(target), "require-db-path") {
			t.Errorf("site.mk's %s target runs without require-db-path, which requires the env file", target)
		}
	}

	t.Run("guard", func(t *testing.T) {
		makeBin, err := exec.LookPath("make")
		if err != nil {
			t.Skip("make is not installed")
		}
		site := t.TempDir()
		guard := func() (string, error) {
			cmd := exec.Command(makeBin, "-s", "-f", siteMK, "SITE_DIR="+site, "CORE_DIR="+root, "require-env-file") // #nosec G204 -- fixed arguments
			cmd.Dir = site
			cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "OCMS_ENV_FILE=") })
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		if out, err := guard(); err == nil || !strings.Contains(out, "does not exist") {
			t.Errorf("require-env-file without a site .env: err=%v, output %q; want it to fail naming the file", err, out)
		}
		if err := os.WriteFile(filepath.Join(site, ".env"), []byte("OCMS_SERVER_PORT=8397\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := guard(); err != nil {
			t.Errorf("require-env-file with a site .env failed: %v\n%s", err, out)
		}
	})
}

// TestSiteMakefileMigratesWithTheSiteEnv runs the site.mk migrate targets
// with make -n and checks that each hands ocms migrate the site's env file,
// resolved against the site. The recipes cd into CORE_DIR and passed none,
// so ocms migrate read the core checkout's .env, and a relative
// OCMS_ENV_FILE=site.env resolved against CORE_DIR and failed (local Codex
// review of the audit branch).
//
// Bug state: drop $(site_env) from a migrate recipe in site.mk and this
// fails.
func TestSiteMakefileMigratesWithTheSiteEnv(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	cases := []struct{ name, override, envFile string }{
		{"default .env", "", filepath.Join(site, ".env")},
		{"relative OCMS_ENV_FILE", "site.env", filepath.Join(site, "site.env")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(tc.envFile, []byte("OCMS_DB_PATH=data/site.db\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"migrate-up", "migrate-down", "migrate-status"} {
				cmd := exec.Command(makeBin, "-n", "-f", filepath.Join(root, "site.mk"), "SITE_DIR="+site, "CORE_DIR="+root, target) // #nosec G204 -- fixed arguments
				cmd.Dir = site
				cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "OCMS_") })
				if tc.override != "" {
					cmd.Env = append(cmd.Env, "OCMS_ENV_FILE="+tc.override)
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("make -n %s: %v\n%s", target, err, out)
				}
				var recipe string
				for line := range strings.Lines(string(out)) {
					if strings.Contains(line, " migrate ") {
						recipe = line
					}
				}
				if want := `OCMS_ENV_FILE="` + tc.envFile + `"`; !strings.Contains(recipe, want) {
					t.Errorf("%s runs %q, want it to pass %s", target, strings.TrimSpace(recipe), want)
				}
			}
		})
	}
}
