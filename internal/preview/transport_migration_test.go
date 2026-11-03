// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestFragmentMigrationRevokesDisclosedLinksWithoutRevokingIssuers(t *testing.T) {
	f := setup(t)
	if err := store.RunMigrationCommand(f.ctx, f.db, "down-to", "20261101230000"); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "INSERT INTO oauth_clients(id,name,redirect_uris,source,expires_at) VALUES('fixture-client','Fixture','[]','cimd',?)", f.now.Add(48*time.Hour).Unix())
	f.exec(t, "INSERT INTO oauth_grants(id,user_id,client_id,scopes,resource,session_version,created_at,expires_at) VALUES('fixture-grant',?,'fixture-client','mcp:access mcp:drafts','https://cms.test/api/mcp',0,?,?)", f.user.ID, f.now.Unix(), f.now.Add(time.Hour).Unix())
	key, err := f.q.CreateAPIKey(f.ctx, store.CreateAPIKeyParams{Name: "Retained issuer", KeyHash: "unused", KeyPrefix: "fixture", Permissions: `["mcp:access","mcp:drafts"]`, CreatedBy: f.user.ID, IsActive: true, CreatedAt: f.now, UpdatedAt: f.now, ExpiresAt: sql.NullTime{Time: f.now.Add(time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	principal := provenance.Principal{UserID: f.user.ID, Source: "oauth", OAuthGrantID: "fixture-grant"}
	legacy := f.issue(t, principal)
	f.issue(t, principal)
	f.exec(t, "UPDATE preview_links SET revoked_at=123 WHERE id=2")
	f.issue(t, principal)
	f.exec(t, "UPDATE preview_links SET expires_at=1 WHERE id=3")
	if _, err = f.svc.Resolve(f.ctx, token(t, legacy)); err != nil {
		t.Fatal("pre-upgrade control did not resolve")
	}
	if err = store.Migrate(f.db); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Resolve(f.ctx, token(t, legacy)); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatal("disclosed credential could be repackaged into the new transport")
	}
	var active, retainedRevocation, revokedExpired int
	if err = f.db.QueryRow("SELECT count(*) FROM preview_links WHERE revoked_at=0").Scan(&active); err != nil || active != 0 {
		t.Fatal("upgrade retained a pre-upgrade credential")
	}
	if err = f.db.QueryRow("SELECT revoked_at FROM preview_links WHERE id=2").Scan(&retainedRevocation); err != nil || retainedRevocation != 123 {
		t.Fatal("upgrade replaced an earlier revocation timestamp")
	}
	if err = f.db.QueryRow("SELECT count(*) FROM preview_links WHERE id=3 AND revoked_at>0").Scan(&revokedExpired); err != nil || revokedExpired != 1 {
		t.Fatal("upgrade left expired legacy credentials dependent on the host clock")
	}
	user, err := f.q.GetUserByID(f.ctx, f.user.ID)
	if err != nil || !reflect.DeepEqual(user, f.user) {
		t.Fatal("upgrade changed the account or its session state")
	}
	page, err := f.q.GetPageByID(f.ctx, f.page.ID)
	if err != nil || !reflect.DeepEqual(page, f.page) {
		t.Fatal("upgrade changed the draft")
	}
	retainedKey, err := f.q.GetAPIKeyByID(f.ctx, key.ID)
	if err != nil || !reflect.DeepEqual(retainedKey, key) {
		t.Fatal("upgrade changed an API key")
	}
	fresh := f.issue(t, principal)
	if err := store.Migrate(f.db); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Resolve(f.ctx, token(t, fresh)); err != nil {
		t.Fatal("a normal subsequent startup revoked a fresh link or its issuers")
	}
	if err := store.RunMigrationCommand(f.ctx, f.db, "down"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Resolve(f.ctx, token(t, legacy)); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatal("rollback resurrected a disclosed credential")
	}
	if _, err := f.svc.Resolve(f.ctx, token(t, fresh)); err != nil {
		t.Fatal("rollback changed a fresh link or its issuers")
	}
}
