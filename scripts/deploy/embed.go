// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package deployfiles embeds the deployment files that cmd/ocmsctl installs
// or runs: the systemd template unit and the production-to-dev scrub policy.
// They live here because the shell scripts in this directory still use them.
package deployfiles

import _ "embed"

// Unit is the systemd template unit ocms@.service.
//
//go:embed ocms@.service
var Unit []byte

// ScrubSQL returns the statements that scrub a database pulled from
// production (see sync-db-policy.sql).
//
//go:embed sync-db-policy.sql
var ScrubSQL string
