// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package provenance_test

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func principalFixture(t *testing.T) (*sql.DB, *store.Queries, store.Page, store.User) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q, ctx, now := store.New(db), t.Context(), time.Now().UTC()
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "principal@example.test", Name: "Editor", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	page, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{Title: "Original", Slug: "principal", Body: "<p>Original</p>", Status: "draft", AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, q, page, user
}

// The Go boundary and both SQL write paths must agree on attribution. These
// cases also prevent a new source or credential field from silently drifting
// between ordinary writers and callers that use the store directly.
func TestPrincipalConsistencyAcrossWriters(t *testing.T) {
	fields := map[string]bool{"UserID": true, "Source": true, "APIKeyID": true, "OAuthGrantID": true, "OAuthClientID": true, "Transport": true}
	principalType := reflect.TypeFor[provenance.Principal]()
	for index := 0; index < principalType.NumField(); index++ {
		if name := principalType.Field(index).Name; !fields[name] {
			t.Fatalf("review Go/SQL consistency cases for new principal field %q", name)
		}
	}
	db, q, page, user := principalFixture(t)
	for index, tt := range []struct {
		name  string
		actor provenance.Principal
		valid bool
	}{
		{"human", provenance.Principal{Source: "human", UserID: user.ID}, true},
		{"api", provenance.Principal{Source: "api", UserID: user.ID, APIKeyID: 17}, true},
		{"oauth", provenance.Principal{Source: "oauth", UserID: user.ID, OAuthGrantID: "grant", OAuthClientID: "client"}, true},
		{"system", provenance.Principal{Source: "system"}, true},
		{"system with actor", provenance.Principal{Source: "system", UserID: user.ID}, true},
		{"migration", provenance.Principal{Source: "migration"}, true},
		{"migration with actor", provenance.Principal{Source: "migration", UserID: user.ID}, true},
		{"empty", provenance.Principal{}, false},
		{"unknown source", provenance.Principal{Source: "agent", UserID: user.ID}, false},
		{"human without user", provenance.Principal{Source: "human"}, false},
		{"negative user", provenance.Principal{Source: "human", UserID: -1}, false},
		{"human with key", provenance.Principal{Source: "human", UserID: user.ID, APIKeyID: 17}, false},
		{"human with grant", provenance.Principal{Source: "human", UserID: user.ID, OAuthGrantID: "grant"}, false},
		{"human with client", provenance.Principal{Source: "human", UserID: user.ID, OAuthClientID: "client"}, false},
		{"api without user", provenance.Principal{Source: "api", APIKeyID: 17}, false},
		{"api without key", provenance.Principal{Source: "api", UserID: user.ID}, false},
		{"negative key", provenance.Principal{Source: "api", UserID: user.ID, APIKeyID: -1}, false},
		{"api with grant", provenance.Principal{Source: "api", UserID: user.ID, APIKeyID: 17, OAuthGrantID: "grant"}, false},
		{"api with client", provenance.Principal{Source: "api", UserID: user.ID, APIKeyID: 17, OAuthClientID: "client"}, false},
		{"oauth without user", provenance.Principal{Source: "oauth", OAuthGrantID: "grant", OAuthClientID: "client"}, false},
		{"oauth without grant", provenance.Principal{Source: "oauth", UserID: user.ID, OAuthClientID: "client"}, false},
		{"oauth without client", provenance.Principal{Source: "oauth", UserID: user.ID, OAuthGrantID: "grant"}, false},
		{"oauth with key", provenance.Principal{Source: "oauth", UserID: user.ID, APIKeyID: 17, OAuthGrantID: "grant", OAuthClientID: "client"}, false},
		{"system with negative user", provenance.Principal{Source: "system", UserID: -1}, false},
		{"system with key", provenance.Principal{Source: "system", APIKeyID: 17}, false},
		{"system with grant", provenance.Principal{Source: "system", OAuthGrantID: "grant"}, false},
		{"system with client", provenance.Principal{Source: "system", OAuthClientID: "client"}, false},
		{"migration with negative user", provenance.Principal{Source: "migration", UserID: -1}, false},
		{"migration with key", provenance.Principal{Source: "migration", APIKeyID: 17}, false},
		{"migration with grant", provenance.Principal{Source: "migration", OAuthGrantID: "grant"}, false},
		{"migration with client", provenance.Principal{Source: "migration", OAuthClientID: "client"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			revision := int64(2 + index*3)
			candidate := page
			candidate.Revision = revision
			err := provenance.Record(t.Context(), q, candidate, tt.actor, provenance.Evidence{})
			if tt.valid && err != nil || !tt.valid && !errors.Is(err, provenance.ErrInvalidPrincipal) {
				t.Errorf("Record: %v, valid=%v", err, tt.valid)
			}
			// Preserve negative IDs here: SQL must reject them, not coerce
			// them to the NULL representation of an absent identity.
			params := store.CreateContentProvenanceParams{
				PageID: page.ID, Revision: revision + 1, Source: tt.actor.Source,
				UserID:       sql.NullInt64{Int64: tt.actor.UserID, Valid: tt.actor.UserID != 0},
				ApiKeyID:     sql.NullInt64{Int64: tt.actor.APIKeyID, Valid: tt.actor.APIKeyID != 0},
				OauthGrantID: tt.actor.OAuthGrantID, OauthClientID: tt.actor.OAuthClientID,
				ValidatedJson: "{}", ReportedJson: "{}", CreatedAt: time.Now().UTC(),
			}
			err = q.CreateContentProvenance(t.Context(), params)
			assertPrincipalSQL(t, "insert", err, tt.valid)
			baseline := params
			baseline.Revision, baseline.Source = revision+2, "system"
			baseline.UserID, baseline.ApiKeyID = sql.NullInt64{}, sql.NullInt64{}
			baseline.OauthGrantID, baseline.OauthClientID = "", ""
			if err = q.CreateContentProvenance(t.Context(), baseline); err != nil {
				t.Fatal(err)
			}
			_, err = db.ExecContext(t.Context(), `UPDATE content_provenance SET user_id=?,source=?,api_key_id=?,oauth_grant_id=?,oauth_client_id=? WHERE page_id=? AND revision=?`, params.UserID, params.Source, params.ApiKeyID, params.OauthGrantID, params.OauthClientID, page.ID, baseline.Revision)
			assertPrincipalSQL(t, "update", err, tt.valid)
			if !tt.valid {
				var count int
				if err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM content_provenance WHERE page_id=? AND revision IN (?,?)`, page.ID, revision, revision+1).Scan(&count); err != nil {
					t.Fatal(err)
				}
				unchanged, loadErr := q.GetContentProvenance(t.Context(), store.GetContentProvenanceParams{PageID: page.ID, Revision: baseline.Revision})
				if loadErr != nil || count != 0 || unchanged.Source != "system" || unchanged.UserID.Valid || unchanged.ApiKeyID.Valid || unchanged.OauthGrantID != "" || unchanged.OauthClientID != "" {
					t.Fatalf("refused principal changed stored attribution: count=%d, row=%+v, error=%v", count, unchanged, loadErr)
				}
			}
		})
	}
	// SQLite affinity is weaker than the Go integer fields. SQL NULL is the
	// absent-ID representation; zero, text and fractional IDs are not IDs.
	for index, tt := range []struct {
		name, source  string
		userID, keyID any
	}{
		{"zero key", "api", user.ID, 0},
		{"fractional key", "api", user.ID, 1.5},
		{"text key", "api", user.ID, "not-an-id"},
		{"system zero key", "system", nil, 0},
		{"system zero user", "system", 0, nil},
		{"system fractional user", "system", 1.5, nil},
		{"system text user", "system", "not-an-id", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			revision := int64(200 + index)
			_, err := db.ExecContext(t.Context(), `INSERT INTO content_provenance(page_id,revision,user_id,source,api_key_id,created_at) VALUES (?,?,?,?,?,CURRENT_TIMESTAMP)`, page.ID, revision, tt.userID, tt.source, tt.keyID)
			assertPrincipalSQL(t, "insert representation", err, false)
			if _, err = db.ExecContext(t.Context(), `INSERT INTO content_provenance(page_id,revision,source,created_at) VALUES (?,?,'system',CURRENT_TIMESTAMP)`, page.ID, revision); err != nil {
				t.Fatal(err)
			}
			_, err = db.ExecContext(t.Context(), `UPDATE content_provenance SET source=?,user_id=?,api_key_id=? WHERE page_id=? AND revision=?`, tt.source, tt.userID, tt.keyID, page.ID, revision)
			assertPrincipalSQL(t, "update representation", err, false)
		})
	}
}

func assertPrincipalSQL(t *testing.T, operation string, err error, valid bool) {
	t.Helper()
	if valid && err != nil || !valid && (err == nil || !strings.Contains(err.Error(), provenance.ErrInvalidPrincipal.Error())) {
		t.Errorf("SQL %s: %v, valid=%v", operation, err, valid)
	}
}

func TestInvalidPrincipalRollsBackPageRevision(t *testing.T) {
	db, q, page, _ := principalFixture(t)
	_, err := pagewrite.InTx(t.Context(), db, provenance.Principal{}, func(work *pagewrite.Tx) error {
		tracked, trackErr := work.Track(t.Context(), page.ID, &page.Revision, true)
		if trackErr != nil {
			return trackErr
		}
		params := pagewrite.ParamsFor(tracked)
		params.Title = "Must roll back"
		_, updateErr := work.Q.UpdatePage(t.Context(), params)
		return updateErr
	})
	if !errors.Is(err, provenance.ErrInvalidPrincipal) {
		t.Fatalf("missing principal = %v, want invalid principal", err)
	}
	current, err := q.GetPageByID(t.Context(), page.ID)
	if err != nil || current.Title != page.Title || current.Revision != page.Revision {
		t.Fatalf("invalid principal committed a page: %+v, %v", current, err)
	}
	var versions, facts int
	if err = db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM page_versions WHERE page_id=?), (SELECT COUNT(*) FROM content_provenance WHERE page_id=?)`, page.ID, page.ID).Scan(&versions, &facts); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || facts != 1 {
		t.Fatalf("failed write left versions=%d, provenance=%d", versions, facts)
	}
}
