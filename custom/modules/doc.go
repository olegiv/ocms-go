// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package modules is the registration point for custom modules.
//
// It exists so cmd/ocms can blank-import one stable path
// (_ "github.com/olegiv/ocms-go/custom/modules") and pick up whatever modules
// are present in the tree. This file declares the package and nothing else —
// there is deliberately no shared list to edit.
//
// # Adding a module
//
// Create custom/modules/<name>/ with a register.go whose init() calls
// module.RegisterCustomModule(New()), then add a file next to this one:
//
//	// custom/modules/imports_<name>.go
//	package modules
//
//	import _ "github.com/olegiv/ocms-go/custom/modules/<name>"
//
// One file per module, never a shared one. Two reasons:
//
//  1. A site-specific module is not in this repository — .gitignore keeps
//     custom/modules/* out except for what ships with oCMS. Putting its import
//     in a tracked shared file would commit a reference to a package that is
//     absent from a fresh clone, and the build would fail with "no required
//     module provides package". As its own ignored file, it stays invisible
//     here and this repository still builds standalone.
//
//  2. Several deployments share one checkout of this repository. A per-module
//     file means they never edit the same line.
//
// Do not delete this file to "clean up": a Go package with no source files does
// not exist, so cmd/ocms would fail to compile even though nothing here is
// referenced directly.
//
// See docs/custom-modules.md for the full guide, and custom/modules/bookmarks/
// for a worked example.
package modules
