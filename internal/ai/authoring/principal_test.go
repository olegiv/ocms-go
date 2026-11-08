// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestDraftPrincipalMatchesPreviewIssuer(t *testing.T) {
	for _, credential := range []string{"key", "oauth", "key with unrelated grant fields"} {
		t.Run(credential, func(t *testing.T) {
			f := setup(t)
			a := f.actor
			wantSource, wantKey, wantGrant, wantClient := "api", a.APIKey.ID, "", ""
			expires := a.APIKey.ExpiresAt.Time
			if credential != "key" {
				now := time.Now()
				if _, err := f.db.ExecContext(f.ctx, "INSERT INTO oauth_clients(id,name,redirect_uris,source,expires_at) VALUES('client','Fixture','[]','cimd',?)", now.Add(48*time.Hour).Unix()); err != nil {
					t.Fatal(err)
				}
				grant, err := f.q.CreateOAuthGrant(f.ctx, store.CreateOAuthGrantParams{
					ID: "grant", UserID: f.user.ID, ClientID: "client", Scopes: "mcp:access mcp:drafts",
					Resource: f.svc.SiteURL + "/api/mcp", SessionVersion: f.user.SessionVersion,
					CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
				})
				if err != nil {
					t.Fatal(err)
				}
				a.UserID, a.OAuthGrantID, a.OAuthClientID = grant.UserID, grant.ID, grant.ClientID
				if credential == "oauth" {
					a.APIKey = nil
					wantSource, wantKey, wantGrant, wantClient = "oauth", 0, grant.ID, grant.ClientID
					expires = time.Unix(grant.ExpiresAt, 0)
				} else {
					// Match authorization's key-owner precedence even for a
					// malformed actor assembled by a future internal caller.
					a.UserID = f.user.ID + 100
				}
			}
			saved, err := f.svc.Create(f.ctx, a, input("Credential-bound draft"))
			if err != nil {
				t.Fatal(err)
			}
			updated, err := f.svc.Update(f.ctx, a, authoring.UpdateInput{
				RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision,
				Changes: draft.Changes{Summary: draft.Set("Updated draft")},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, revision := range []int64{saved.Revision, updated.Revision} {
				fact, factErr := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: saved.PageID, Revision: revision})
				if factErr != nil {
					t.Fatal(factErr)
				}
				if fact.UserID != (sql.NullInt64{Int64: f.user.ID, Valid: true}) || fact.Source != wantSource || fact.Transport != "mcp" ||
					fact.ApiKeyID != (sql.NullInt64{Int64: wantKey, Valid: wantKey > 0}) || fact.OauthGrantID != wantGrant || fact.OauthClientID != wantClient {
					t.Errorf("revision %d provenance does not match the authorized credential: %+v", revision, fact)
				}
			}
			link, err := f.svc.CreatePreviewLink(f.ctx, a, authoring.PreviewInput{PageID: saved.PageID, Revision: updated.Revision})
			if err != nil {
				t.Fatalf("issue preview: %v", err)
			}
			var issuer int64
			var keyID sql.NullInt64
			var grantID sql.NullString
			if err = f.db.QueryRowContext(f.ctx, "SELECT created_by,issuing_key_id,issuing_grant_id FROM preview_links WHERE page_id=? AND revision=?", link.PageID, link.Revision).Scan(&issuer, &keyID, &grantID); err != nil {
				t.Fatal(err)
			}
			if issuer != f.user.ID || keyID != (sql.NullInt64{Int64: wantKey, Valid: wantKey > 0}) || grantID != (sql.NullString{String: wantGrant, Valid: wantGrant != ""}) {
				t.Fatal("preview issuer does not match the authorized credential")
			}
			if link.ExpiresAt.After(expires) {
				t.Fatal("preview outlives its credential")
			}
			parsed, err := url.Parse(link.URL.Reveal())
			if err != nil || parsed == nil || parsed.Fragment == "" {
				t.Fatal("preview URL has no bearer")
			}
			resolver := preview.Service{Q: f.q, AgentShareAllowed: func(context.Context, *store.Queries) (bool, error) { return true, nil }}
			if snapshot, resolveErr := resolver.Resolve(f.ctx, parsed.Fragment); resolveErr != nil || snapshot.Page.Revision != updated.Revision {
				t.Fatalf("resolve issued revision: %v", resolveErr)
			}
			if wantKey > 0 {
				_, err = f.db.ExecContext(f.ctx, "UPDATE api_keys SET is_active=0 WHERE id=?", wantKey)
			} else {
				_, err = f.db.ExecContext(f.ctx, "UPDATE oauth_grants SET revoked_at=1 WHERE id=?", wantGrant)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = resolver.Resolve(f.ctx, parsed.Fragment); !errors.Is(err, preview.ErrUnavailable) {
				t.Fatalf("revoked credential still resolves preview: %v", err)
			}
			_, err = f.svc.Update(f.ctx, a, authoring.UpdateInput{
				RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: updated.Revision,
				Changes: draft.Changes{Summary: draft.Set("Must not save")},
			})
			if code(err) != "unauthorized" {
				t.Fatalf("revoked credential update = %v, want unauthorized", err)
			}
			current, err := f.q.GetPageByID(f.ctx, saved.PageID)
			if err != nil || current.Revision != updated.Revision || current.Summary != "Updated draft" {
				t.Fatalf("revoked credential changed the page: %v", err)
			}
		})
	}
}
