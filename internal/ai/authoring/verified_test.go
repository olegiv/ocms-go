// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"database/sql"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestVerifiedNamesOnlyTheCallersOwnAccount checks the verified block of
// get_my_draft: a revision the caller saved names the caller's account and
// credential, one saved last by another person keeps only the kind of change
// (2026-11-03 audit B-10).
//
// Bug state: copy fact.UserID into Verified unconditionally and this fails.
func TestVerifiedNamesOnlyTheCallersOwnAccount(t *testing.T) {
	saved := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	fact := func(userID int64) store.ContentProvenance {
		return store.ContentProvenance{UserID: sql.NullInt64{Int64: userID, Valid: true}, Source: "oauth", ApiKeyID: sql.NullInt64{Int64: 9, Valid: true},
			OauthGrantID: "grant", OauthClientID: "client", Transport: "mcp", CreatedAt: saved}
	}
	caller := v2.Actor{UserID: 7, OAuthGrantID: "grant"}
	own := verifiedFor(caller, fact(7))
	if own != (Verified{UserID: 7, Source: "oauth", APIKeyID: 9, OAuthGrantID: "grant", OAuthClientID: "client", Transport: "mcp", SavedAt: saved}) {
		t.Errorf("own revision: %+v", own)
	}
	other := verifiedFor(caller, fact(8))
	if other != (Verified{Source: "oauth", Transport: "mcp", SavedAt: saved}) {
		t.Errorf("another person's revision exposes their identifiers: %+v", other)
	}
	if system := verifiedFor(caller, store.ContentProvenance{Source: "system", Transport: "translation_link", CreatedAt: saved}); system.UserID != 0 || system.Source != "system" {
		t.Errorf("system revision: %+v", system)
	}
}
