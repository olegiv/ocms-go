// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"errors"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
)

// TestResolveWithIssuerNamesTheUserWhoIssuedTheLink gives the anonymous share
// the one fact it needs to decide whose profile may appear (security audit
// 2026-11-03, C-8): the issuer, which is not the recorded author once the page
// has been reassigned.
func TestResolveWithIssuerNamesTheUserWhoIssuedTheLink(t *testing.T) {
	f := setup(t)
	raw := token(t, f.issue(t))
	snapshot, issuer, err := f.svc.ResolveWithIssuer(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if issuer != f.user.ID || snapshot.Page.AuthorID != f.user.ID {
		t.Fatalf("issuer=%d recorded author=%d, want both %d", issuer, snapshot.Page.AuthorID, f.user.ID)
	}
	// Plain Resolve keeps returning the snapshot alone.
	plain, resolveErr := f.svc.Resolve(f.ctx, raw)
	if resolveErr != nil || plain.Page.ID != snapshot.Page.ID {
		t.Fatalf("Resolve = %v, %v", plain.Page.ID, resolveErr)
	}
	for _, bad := range []string{"", "not-a-token", raw[:len(raw)-1]} {
		if _, issuer, err = f.svc.ResolveWithIssuer(f.ctx, bad); !errors.Is(err, preview.ErrUnavailable) || issuer != 0 {
			t.Errorf("ResolveWithIssuer(%q) = issuer %d, %v; want no issuer and ErrUnavailable", bad, issuer, err)
		}
	}
}
