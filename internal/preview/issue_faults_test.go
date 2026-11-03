// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"errors"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
)

// TestIssueSeparatesUnavailableDraftsFromStorageFaults checks that issuing a
// share link reports a missing row (no AI access, no page) as
// ErrUnavailable, which callers answer as "cannot be shared", but a storage
// fault in the same lookups as an error of its own, which they log and answer
// as a temporary failure (2026-11-03 review S11).
//
// Bug state: return ErrUnavailable for every lookup error in Issue and this
// fails.
func TestIssueSeparatesUnavailableDraftsFromStorageFaults(t *testing.T) {
	f := setup(t)
	principal := provenance.Principal{UserID: f.user.ID, Source: "human", Transport: "admin"}
	issue := func() error {
		_, err := f.svc.Issue(f.ctx, principal, f.page.ID, f.page.Revision, "https://cms.test", nil)
		return err
	}

	f.exec(t, `ALTER TABLE page_ai_access RENAME TO page_ai_access_unavailable`)
	faulted := issue()
	f.exec(t, `ALTER TABLE page_ai_access_unavailable RENAME TO page_ai_access`)
	if faulted == nil || errors.Is(faulted, preview.ErrUnavailable) {
		t.Errorf("storage fault while loading AI access: %v, want a fault distinct from ErrUnavailable", faulted)
	}

	f.exec(t, `DELETE FROM page_ai_access WHERE page_id = ?`, f.page.ID)
	if err := issue(); !errors.Is(err, preview.ErrUnavailable) {
		t.Errorf("page without AI access: %v, want ErrUnavailable", err)
	}
}
