// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import "testing"

func assertIDNameSlug(t *testing.T, gotID, wantID int64, gotName, wantName, gotSlug, wantSlug string) {
	t.Helper()
	if gotID != wantID {
		t.Errorf("ID = %d, want %d", gotID, wantID)
	}
	if gotName != wantName {
		t.Errorf("Name = %q, want %q", gotName, wantName)
	}
	if gotSlug != wantSlug {
		t.Errorf("Slug = %q, want %q", gotSlug, wantSlug)
	}
}
