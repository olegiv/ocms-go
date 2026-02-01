// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package themes embeds core themes into the binary.
package themes

import "embed"

// FS contains embedded core themes (default, developer).
// These themes are available without any external files.
//
//go:embed all:default all:developer
var FS embed.FS
