// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package version provides build-time version information.
package version

// Info contains build-time version information injected via ldflags.
type Info struct {
	Version   string // Semantic version from git tags (e.g., "v1.2.3")
	GitCommit string // Short git commit hash (e.g., "abc1234")
	BuildTime string // Build timestamp in RFC3339 format
}
