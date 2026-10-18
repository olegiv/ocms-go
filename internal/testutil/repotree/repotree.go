// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package repotree helps tests that walk the repository's source tree. It
// imports only the standard library, so any package's tests can use it.
package repotree

import (
	"io/fs"
	"strings"
)

// Skip reports whether a walk of the repository rooted at root should skip
// the entry d at path: a dot directory below the root (.git, .audit,
// .claude, ...) or a dependency, build, data, upload, wiki or scratch
// directory. None of them holds code the repository ships. The gitignored
// copies of older trees in .audit/ and the scratch programs in output/
// failed drift tests on a developer's checkout, and blocked its pushes,
// while CI, which has neither, passed.
func Skip(root, path string, d fs.DirEntry) bool {
	if !d.IsDir() || path == root {
		return false
	}
	if strings.HasPrefix(d.Name(), ".") {
		return true
	}
	switch d.Name() {
	case "node_modules", "vendor", "bin", "data", "uploads", "wiki", "dist", "output":
		return true
	}
	return false
}
