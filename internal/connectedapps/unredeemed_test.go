// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package connectedapps

import (
	"errors"
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

// TestUnredeemedGrantsAreNotListedButStayRevocable pins audit A-3: a grant row
// exists from consent, before any token does, so grants whose code was never
// exchanged (and every repeated consent) were listed as active connections.
// Only a grant that issued a token is a connection; the others stay reachable
// by ID so that revocation covers them, and the retention job removes them.
func TestUnredeemedGrantsAreNotListedButStayRevocable(t *testing.T) {
	f := newConnectionFixture(t)
	f.grant(t, f.owner, "redeemed")
	// More unredeemed grants than a page holds must not push the real
	// connection off it.
	for i := range PageSize + 5 {
		f.unredeemedGrant(t, f.owner, fmt.Sprintf("pending-%02d", i))
	}
	f.unredeemedGrant(t, f.foreign, "foreign-pending")

	page, err := f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.OAuth) != 1 || page.OAuth[0].ID != "redeemed" || page.OAuthMore {
		t.Fatalf("owner overview lists %d grants, want only the redeemed one (more=%v)", len(page.OAuth), page.OAuthMore)
	}
	all, err := f.s.List(t.Context(), actor(f.admin), true, Pagination{1, 1}, true)
	if err != nil || len(all.OAuth) != 1 || all.OAuth[0].ID != "redeemed" {
		t.Fatalf("admin overview lists %d grants, want only the redeemed one: %v", len(all.OAuth), err)
	}

	// Revocation still reaches a grant that is not listed, for its owner only.
	if err = f.s.Revoke(t.Context(), actor(f.owner), false, "oauth", "foreign-pending"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner revoked another account's unredeemed grant: %v", err)
	}
	if err = f.s.Revoke(t.Context(), actor(f.owner), false, "oauth", "pending-00"); err != nil {
		t.Fatalf("unredeemed grant cannot be revoked: %v", err)
	}
	var revoked int64
	if err = f.db.QueryRowContext(t.Context(), `SELECT revoked_at FROM oauth_grants WHERE id = 'pending-00'`).Scan(&revoked); err != nil || revoked == 0 {
		t.Fatalf("revocation did not persist: %d %v", revoked, err)
	}

	// Exchanging the code is what makes it a connection.
	if err = f.q.CreateOAuthToken(t.Context(), store.CreateOAuthTokenParams{ID: "late-token", GrantID: "pending-01",
		AccessHash: "late-access", RefreshHash: "late-refresh", Scopes: "mcp:access", CreatedAt: f.now.Unix(), AccessExpiresAt: f.now.Unix() + 60}); err != nil {
		t.Fatal(err)
	}
	page, err = f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 1}, true)
	if err != nil || len(page.OAuth) != 2 {
		t.Fatalf("redeemed grant not listed: %d grants listed, want 2: %v", len(page.OAuth), err)
	}
}
