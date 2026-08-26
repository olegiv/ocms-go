// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"strings"
	"testing"
)

// TestTrustedRootErrorNamesEveryAcceptedEnv is a drift guard.
//
// The "no trusted media roots configured" error used to spell its list out by
// hand. When PHPNUKE_FILES was added to legacyFileRootEnvs the message was not
// updated, so a PHP-Nuke operator hitting the error was told to set
// DRUPAL_FILES or ELEFANT_FILES — two variables that would not help and one
// that would, unmentioned. The behaviour was right and only the guidance was
// wrong, which is the kind of bug nothing else catches.
//
// This fails if a source adds an accepted variable without the message
// following it.
func TestTrustedRootErrorNamesEveryAcceptedEnv(t *testing.T) {
	// No trusted root of any kind is configured, which is the error path.
	t.Setenv(EnvAllowedFileRoots, "")
	for _, name := range legacyFileRootEnvs {
		t.Setenv(name, "")
	}

	_, err := trustedMediaLocations("/some/absolute/path")
	if err == nil {
		t.Fatal("expected an error when no trusted media root is configured")
	}
	message := err.Error()

	for _, name := range TrustedFileRootEnvNames() {
		if !strings.Contains(message, name) {
			t.Errorf("error message omits %q, so an operator is told to configure the wrong variable\n  got: %s",
				name, message)
		}
	}
}

// TestTrustedFileRootEnvNamesLeadsWithAllowlist keeps the canonical variable
// first: the legacy per-source names are compatibility shims, and an operator
// reading the message should reach for the allowlist.
func TestTrustedFileRootEnvNamesLeadsWithAllowlist(t *testing.T) {
	names := TrustedFileRootEnvNames()
	if len(names) != len(legacyFileRootEnvs)+1 {
		t.Fatalf("got %d names, want %d", len(names), len(legacyFileRootEnvs)+1)
	}
	if names[0] != EnvAllowedFileRoots {
		t.Errorf("names[0] = %q, want %q", names[0], EnvAllowedFileRoots)
	}
}

// TestTrustedFileRootEnvNamesIsNotAliased proves the returned slice cannot be
// mutated into legacyFileRootEnvs by a caller appending to it.
func TestTrustedFileRootEnvNamesIsNotAliased(t *testing.T) {
	first := TrustedFileRootEnvNames()
	first = append(first, "INJECTED")
	if len(first) == 0 {
		t.Fatal("unexpected empty result")
	}
	for _, name := range TrustedFileRootEnvNames() {
		if name == "INJECTED" {
			t.Fatal("appending to the result mutated the package-level list")
		}
	}
}

// TestPHPNukeFilesIsAnAcceptedTrustedRoot pins the behaviour the message was
// wrong about: setting PHPNUKE_FILES alone is enough to import media.
func TestPHPNukeFilesIsAnAcceptedTrustedRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvAllowedFileRoots, "")
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")
	t.Setenv("PHPNUKE_FILES", root)

	locations, err := trustedMediaLocations(root)
	if err != nil {
		t.Fatalf("PHPNUKE_FILES alone should authorize its own root: %v", err)
	}
	if len(locations) == 0 {
		t.Fatal("expected at least one trusted media location")
	}
}
