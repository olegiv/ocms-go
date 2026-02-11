// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package bookmarks

import "github.com/olegiv/ocms-go/internal/module"

func init() {
	module.RegisterCustomModule(New())
}
