// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import "embed"

//go:embed testdata
var testModuleFS embed.FS

//go:embed testdata/locales
var testNoPrefixFS embed.FS

//go:embed testdata/bad
var testBadFS embed.FS
