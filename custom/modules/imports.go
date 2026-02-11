// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package modules loads custom modules via blank imports.
//
// To register a custom module, add a blank import for it below.
// Each custom module must have a register.go with an init() that calls
// module.RegisterCustomModule(New()).
//
// Example:
//
//	import _ "github.com/olegiv/ocms-go/custom/modules/mymodule"
package modules

import (
	// Custom modules — add blank imports here:
	_ "github.com/olegiv/ocms-go/custom/modules/bookmarks"
)
