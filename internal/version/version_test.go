// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package version

import "testing"

func TestInfoStruct(t *testing.T) {
	// Test that Info struct can be created and fields are accessible
	info := Info{
		Version:   "v1.0.0",
		GitCommit: "abc1234",
		BuildTime: "2025-01-30T12:00:00Z",
	}

	if info.Version != "v1.0.0" {
		t.Errorf("Version = %q, want %q", info.Version, "v1.0.0")
	}
	if info.GitCommit != "abc1234" {
		t.Errorf("GitCommit = %q, want %q", info.GitCommit, "abc1234")
	}
	if info.BuildTime != "2025-01-30T12:00:00Z" {
		t.Errorf("BuildTime = %q, want %q", info.BuildTime, "2025-01-30T12:00:00Z")
	}
}

func TestInfoZeroValue(t *testing.T) {
	// Test zero value (before ldflags injection)
	var info Info

	if info.Version != "" {
		t.Errorf("zero value Version = %q, want empty", info.Version)
	}
	if info.GitCommit != "" {
		t.Errorf("zero value GitCommit = %q, want empty", info.GitCommit)
	}
	if info.BuildTime != "" {
		t.Errorf("zero value BuildTime = %q, want empty", info.BuildTime)
	}
}
