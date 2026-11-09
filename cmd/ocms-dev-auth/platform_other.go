//go:build !darwin && !linux

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io/fs"
	"os"
)

// shutdownSignals end a run gracefully. Only the interrupt is portable here.
var shutdownSignals = []os.Signal{os.Interrupt}

// ownedByCurrentUser cannot prove ownership on this platform, so the stale
// directory sweep never removes anything.
func ownedByCurrentUser(fs.FileInfo) bool { return false }
