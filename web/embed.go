// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package web

import "embed"

//go:embed all:templates
var Templates embed.FS

//go:embed all:static/dist
var Static embed.FS
