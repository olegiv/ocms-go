// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sharedResolverFile = "internal/util/network.go"

// TestSSRFChecksUseSharedResolver keeps host-name resolution replaceable in
// tests. Validation that resolves names with net.DefaultResolver reaches the
// real network, so tests that use stand-in hosts (example.com, api.dify.ai)
// fail wherever DNS is unavailable. Production code resolves through
// util.LookupIPAddr, which tests swap for the offline fixture in
// internal/testutil/dnsfixture. The swap itself stays in test files, so no
// production path can replace the resolver that backs the SSRF checks.
//
// Bug state: call net.DefaultResolver from internal/, modules/ or cmd/, or
// call util.ReplaceLookupIPAddrForTest from a non-test file, and this fails.
func TestSSRFChecksUseSharedResolver(t *testing.T) {
	root := filepath.Join("..", "..")
	keep := func(rel string) bool {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || rel == sharedResolverFile || strings.HasPrefix(rel, "internal/testutil/") {
			return false
		}
		for _, dir := range []string{"internal/", "modules/", "cmd/", "custom/"} {
			if strings.HasPrefix(rel, dir) {
				return true
			}
		}
		return false
	}
	err := walkRepoFiles(root, keep, func(file, rel string) error {
		data, err := os.ReadFile(file) // #nosec G304 -- path from listing the repository
		if err != nil {
			return err
		}
		text := string(data)
		if strings.Contains(text, "net.DefaultResolver") {
			t.Errorf("%s uses net.DefaultResolver; resolve through util.LookupIPAddr so tests can run offline", rel)
		}
		if strings.Contains(text, "ReplaceLookupIPAddrForTest") {
			t.Errorf("%s calls ReplaceLookupIPAddrForTest outside a test file", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
