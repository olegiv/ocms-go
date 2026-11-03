//go:build darwin || linux

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io/fs"
	"os"
	"syscall"
)

// shutdownSignals end a run gracefully. SIGTERM and SIGHUP (a supervising
// agent stopping the command, or its terminal closing) must clean up like
// Ctrl-C does.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// ownedByCurrentUser reports whether the file belongs to the calling user.
func ownedByCurrentUser(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
