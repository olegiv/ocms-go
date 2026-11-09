// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type loadFixture struct {
	db      *sql.DB
	q       *store.Queries
	viewer  store.User
	author  store.User
	key     store.ApiKey
	revised func(provenance.Principal) store.Page
}

func setupLoad(t *testing.T) loadFixture {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	newUser := func(email, name string) store.User {
		user, err := q.CreateUser(ctx, store.CreateUserParams{Email: email, PasswordHash: "unused", Role: "admin", Name: name, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return user
	}
	viewer, author := newUser("viewer@example.test", "Viewer"), newUser("author@example.test", "Recorded Author")
	key, err := q.CreateAPIKey(ctx, store.CreateAPIKeyParams{Name: "Recorded key", KeyHash: "unused", KeyPrefix: "test", Permissions: "[]", IsActive: true, CreatedBy: author.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.UpsertOAuthClient(ctx, store.UpsertOAuthClientParams{ID: "client-1", Name: "Recorded client", RedirectUris: "[]", Source: "cimd", ExpiresAt: now.Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	slug := 0
	revised := func(principal provenance.Principal) store.Page {
		t.Helper()
		slug++
		page, insertErr := pagewrite.Insert(ctx, q, principal, store.CreatePageParams{
			Title: "Reviewed", Slug: fmt.Sprintf("reviewed-%d", slug), Body: "<p>body</p>", Status: "draft", AuthorID: author.ID,
			LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
		}, nil)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return page
	}
	return loadFixture{db, q, viewer, author, key, revised}
}

// TestLoadNamesTheRecordedActorAndReportsFaultsInTheLookups checks that the
// review panel names the account, client and API key a revision was recorded
// for, shows nothing for one that no longer exists, and fails when a lookup
// itself fails instead of presenting a revision with no recorded actor
// (2026-11-03 review S14: every lookup error was dropped).
//
// Bug state: ignore the lookup errors in Load again (`if x, e := ...; e == nil`)
// and each fault case returns a review with an empty actor.
func TestLoadNamesTheRecordedActorAndReportsFaultsInTheLookups(t *testing.T) {
	ctx := context.Background()
	ofClient := func(f loadFixture) provenance.Principal {
		return provenance.Principal{UserID: f.author.ID, Source: "oauth", OAuthGrantID: "grant-1", OAuthClientID: "client-1", Transport: "mcp"}
	}
	ofKey := func(f loadFixture) provenance.Principal {
		return provenance.Principal{UserID: f.author.ID, Source: "api", APIKeyID: f.key.ID, Transport: "rest"}
	}
	exec := func(t *testing.T, f loadFixture, statement string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("recorded actors are named", func(t *testing.T) {
		f := setupLoad(t)
		got, err := review.Load(ctx, f.q, f.revised(ofKey(f)), f.viewer.ID)
		if err != nil || got.ActorName != f.author.Name || got.RegisteredApp != f.key.Name {
			t.Fatalf("API key revision: %+v %v", got, err)
		}
		got, err = review.Load(ctx, f.q, f.revised(ofClient(f)), f.viewer.ID)
		if err != nil || got.RegisteredApp != "Recorded client" {
			t.Fatalf("OAuth revision: %+v %v", got, err)
		}
	})

	t.Run("an actor that no longer exists is shown as nothing", func(t *testing.T) {
		f := setupLoad(t)
		keyPage, clientPage := f.revised(ofKey(f)), f.revised(ofClient(f))
		exec(t, f, `DELETE FROM api_keys WHERE id = ?`, f.key.ID)
		exec(t, f, `DELETE FROM oauth_clients WHERE id = 'client-1'`)
		for _, page := range []store.Page{keyPage, clientPage} {
			got, err := review.Load(ctx, f.q, page, f.viewer.ID)
			if err != nil || got.RegisteredApp != "" {
				t.Errorf("revision %d of a deleted actor: %+v %v, want no registered app and no error", page.ID, got, err)
			}
		}
	})

	faults := []struct {
		name    string
		breakIt func(*testing.T, loadFixture)
		page    func(loadFixture) store.Page
	}{
		{"account lookup fails", func(t *testing.T, f loadFixture) {
			// A row the driver cannot read, for the author only: the viewer's
			// own role check, which reads another row, still passes.
			exec(t, f, `UPDATE users SET created_at = 'not a time' WHERE id = ?`, f.author.ID)
		}, func(f loadFixture) store.Page { return f.revised(ofKey(f)) }},
		{"OAuth client lookup fails", func(t *testing.T, f loadFixture) {
			exec(t, f, `ALTER TABLE oauth_clients RENAME TO oauth_clients_away`)
		}, func(f loadFixture) store.Page { return f.revised(ofClient(f)) }},
		{"API key lookup fails", func(t *testing.T, f loadFixture) {
			exec(t, f, `ALTER TABLE api_keys RENAME TO api_keys_away`)
		}, func(f loadFixture) store.Page { return f.revised(ofKey(f)) }},
	}
	for _, tc := range faults {
		t.Run(tc.name, func(t *testing.T) {
			f := setupLoad(t)
			page := tc.page(f)
			tc.breakIt(t, f)
			got, err := review.Load(ctx, f.q, page, f.viewer.ID)
			if err == nil {
				t.Fatalf("Load succeeded with %+v, want the lookup failure", got)
			}
			if errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("Load reported a fault as a missing actor: %v", err)
			}
		})
	}
}
