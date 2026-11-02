// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

type fixture struct {
	foreign store.User
	db      *sql.DB
	q       *store.Queries
	page    store.Page
	user    store.User
	svc     preview.Service
	ctx     context.Context
	now     time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, q: store.New(db), ctx: context.Background(), now: time.Now().UTC().Truncate(time.Second)}
	f.user, err = f.q.CreateUser(f.ctx, store.CreateUserParams{Email: "preview@example.test", Name: "Editor", Role: "editor", PasswordHash: "unused", CreatedAt: f.now, UpdatedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.page, err = pagewrite.Insert(f.ctx, f.q, provenance.Principal{UserID: f.user.ID, Source: "human"}, store.CreatePageParams{Title: "Recorded title", Slug: "preview-fixture", Body: "<p>Recorded body.</p>", Summary: "Recorded summary", Status: "draft", AuthorID: f.user.ID, LanguageCode: "en", PageType: "post", CreatedAt: f.now, UpdatedAt: f.now}, func(work *pagewrite.Tx, p store.Page) error {
		return work.Q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: f.user.ID, SetAt: f.now})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.foreign, err = f.q.CreateUser(f.ctx, store.CreateUserParams{Email: "other@example.test", Name: "Other Editor", Role: "editor", PasswordHash: "unused", CreatedAt: f.now, UpdatedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.svc = preview.Service{Q: f.q, Now: func() time.Time { return f.now }}
	return f
}
func (f *fixture) issue(t *testing.T, principal ...provenance.Principal) preview.Link {
	t.Helper()
	p := provenance.Principal{UserID: f.user.ID, Source: "human", Transport: "admin"}
	if len(principal) > 0 {
		p = principal[0]
	}
	link, err := f.svc.Issue(f.ctx, p, f.page.ID, f.page.Revision, "https://cms.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return link
}
func token(t *testing.T, link preview.Link) string {
	t.Helper()
	u, err := url.Parse(link.URL)
	if err != nil || u == nil || u.Fragment == "" {
		t.Fatalf("preview URL has no valid token: %v", err)
	}
	return u.Fragment
}

func TestPreviewShareURLKeepsBearerOutOfRequestTarget(t *testing.T) {
	f := setup(t)
	link := f.issue(t)
	u, err := url.Parse(link.URL)
	if err != nil || u.Path != "/preview/share" || u.RawQuery != "" || u.ForceQuery || len(u.Fragment) != 43 {
		t.Fatal("share bearer must exist only in the fragment of a fixed-path URL")
	}
	if strings.Contains(u.RequestURI(), u.Fragment) {
		t.Fatal("share bearer reached the HTTP request target")
	}
	if _, err := f.svc.Resolve(f.ctx, u.Fragment); err != nil {
		t.Fatal("fragment bearer no longer resolves its pinned snapshot")
	}
}
func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewPinsRecordedRevisionAndStoresOnlyHash(t *testing.T) {
	f := setup(t)
	link := f.issue(t)
	raw := token(t, link)
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		t.Fatal("token must contain 32 random bytes")
	}
	var persisted string
	if err = f.db.QueryRow("SELECT token_hash FROM preview_links").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted == raw || len(persisted) != 64 {
		t.Fatal("persisted bearer token instead of hash")
	}
	if link.ExpiresAt.Sub(f.now) != 24*time.Hour {
		t.Fatal("wrong default lifetime")
	}
	base := f.page.Revision
	_, err = pagewrite.InTx(f.ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		p, e := work.Track(f.ctx, f.page.ID, &base, true)
		if e != nil {
			return e
		}
		params := pagewrite.ParamsFor(p)
		params.Title = "Later title"
		params.Body = "<p>Later body.</p>"
		_, e = work.Q.UpdatePage(f.ctx, params)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.svc.Resolve(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Page.Revision != base || snapshot.Page.Title != "Recorded title" || snapshot.Page.Body != "<p>Recorded body.</p>" {
		t.Fatal("preview followed current content")
	}
	if _, err = preview.Load(f.ctx, f.q, f.page.ID, 999); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatal("missing revision must not fall back")
	}
}

func TestPreviewLifecycleDoesNotResurrect(t *testing.T) {
	for _, scenario := range []string{"publication", "schedule", "ai_access", "ownership", "role", "password", "explicit", "expiry", "deletion", "missing_snapshot", "legacy_snapshot"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			link := f.issue(t)
			switch scenario {
			case "publication":
				f.exec(t, "UPDATE pages SET status='published' WHERE id=?", f.page.ID)
				f.exec(t, "UPDATE pages SET status='draft' WHERE id=?", f.page.ID)
			case "schedule":
				f.exec(t, "UPDATE pages SET scheduled_at=CURRENT_TIMESTAMP WHERE id=?", f.page.ID)
				f.exec(t, "UPDATE pages SET scheduled_at=NULL WHERE id=?", f.page.ID)
			case "ai_access":
				f.exec(t, "UPDATE page_ai_access SET ai_editable=0 WHERE page_id=?", f.page.ID)
				f.exec(t, "UPDATE page_ai_access SET ai_editable=1 WHERE page_id=?", f.page.ID)
			case "ownership":
				f.exec(t, "UPDATE pages SET author_id=? WHERE id=?", f.foreign.ID, f.page.ID)
				f.exec(t, "UPDATE pages SET author_id=? WHERE id=?", f.user.ID, f.page.ID)
			case "role":
				f.exec(t, "UPDATE users SET role='public' WHERE id=?", f.user.ID)
				f.exec(t, "UPDATE users SET role='editor' WHERE id=?", f.user.ID)
			case "password":
				f.exec(t, "UPDATE users SET session_version=session_version+1 WHERE id=?", f.user.ID)
			case "explicit":
				f.exec(t, "UPDATE preview_links SET revoked_at=1")
			case "expiry":
				f.now = f.now.Add(25 * time.Hour)
			case "deletion":
				if _, err := pagewrite.InTx(f.ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error { return work.Delete(f.ctx, f.page.ID) }); err != nil {
					t.Fatal(err)
				}
			case "missing_snapshot":
				f.exec(t, "DELETE FROM page_versions WHERE page_id=?", f.page.ID)
			case "legacy_snapshot":
				f.exec(t, "UPDATE page_versions SET legacy=1 WHERE page_id=?", f.page.ID)
			}
			if _, err := f.svc.Resolve(f.ctx, token(t, link)); !errors.Is(err, preview.ErrUnavailable) {
				t.Fatal("unavailable link remained usable")
			}
		})
	}
}

func TestPreviewCredentialRevocationAndExpiry(t *testing.T) {
	for _, kind := range []string{"key", "grant"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			p := provenance.Principal{UserID: f.user.ID, Source: kind}
			if kind == "key" {
				key, err := f.q.CreateAPIKey(f.ctx, store.CreateAPIKeyParams{Name: "Share fixture", KeyHash: "unused", KeyPrefix: "fixture", Permissions: `["mcp:access","mcp:drafts"]`, CreatedBy: f.user.ID, IsActive: true, CreatedAt: f.now, UpdatedAt: f.now, ExpiresAt: sql.NullTime{Time: f.now.Add(time.Hour), Valid: true}})
				if err != nil {
					t.Fatal(err)
				}
				p.APIKeyID = key.ID
			} else {
				f.exec(t, "INSERT INTO oauth_clients(id,name,redirect_uris,source,expires_at) VALUES('fixture-client','Fixture','[]','cimd',?)", f.now.Add(48*time.Hour).Unix())
				f.exec(t, "INSERT INTO oauth_grants(id,user_id,client_id,scopes,resource,session_version,created_at,expires_at) VALUES('fixture-grant',?,'fixture-client','mcp:access mcp:drafts','https://cms.test/api/mcp',0,?,?)", f.user.ID, f.now.Unix(), f.now.Add(time.Hour).Unix())
				p.OAuthGrantID = "fixture-grant"
			}
			link := f.issue(t, p)
			if link.ExpiresAt.After(f.now.Add(time.Hour)) {
				t.Fatal("link outlives issuing credential")
			}
			if _, err := f.svc.Resolve(f.ctx, token(t, link)); err != nil {
				t.Fatal(err)
			}
			if kind == "key" {
				f.exec(t, "UPDATE api_keys SET is_active=0 WHERE id=?", p.APIKeyID)
				f.exec(t, "UPDATE api_keys SET is_active=1 WHERE id=?", p.APIKeyID)
			} else {
				f.exec(t, "UPDATE oauth_grants SET revoked_at=1 WHERE id='fixture-grant'")
				f.exec(t, "UPDATE oauth_grants SET revoked_at=0 WHERE id='fixture-grant'")
			}
			if _, err := f.svc.Resolve(f.ctx, token(t, link)); !errors.Is(err, preview.ErrUnavailable) {
				t.Fatal("revoked credential link resurrected")
			}
		})
	}
}

func TestPreviewCapsOwnershipAndMalformedTokens(t *testing.T) {
	f := setup(t)
	for range preview.MaxActiveLinks {
		f.issue(t)
	}
	if _, err := f.svc.Issue(f.ctx, provenance.Principal{UserID: f.user.ID}, f.page.ID, 1, "https://cms.test", nil); !errors.Is(err, preview.ErrLimit) {
		t.Fatal("active cap not enforced")
	}
	for _, raw := range []string{"", "short", strings.Repeat("A", 44), strings.Repeat("/", 43)} {
		if _, err := f.svc.Resolve(f.ctx, raw); !errors.Is(err, preview.ErrUnavailable) {
			t.Fatal("malformed token accepted")
		}
	}
	for _, origin := range []string{"http://public.test", "https://cms.test/path", "https://user:pass@cms.test", "https://cms.test?x=y", "https://cms.test?"} {
		if _, err := f.svc.Issue(f.ctx, provenance.Principal{UserID: f.user.ID}, f.page.ID, 1, origin, nil); !errors.Is(err, preview.ErrUnavailable) {
			t.Fatal("unsafe origin accepted")
		}
	}
	if _, err := f.svc.Issue(f.ctx, provenance.Principal{UserID: f.foreign.ID}, f.page.ID, 1, "https://cms.test", nil); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatal("foreign draft share accepted")
	}
}

func TestPreviewConcurrentCapAndRevokedIssuanceRate(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, err := f.svc.Issue(f.ctx, provenance.Principal{UserID: f.user.ID}, f.page.ID, 1, "https://cms.test", nil)
			if err != nil && !errors.Is(err, preview.ErrLimit) {
				t.Errorf("concurrent issue: %v", err)
			}
		})
	}
	wg.Wait()
	n, err := f.q.CountActivePreviewLinks(f.ctx, store.CountActivePreviewLinksParams{CreatedBy: f.user.ID, ExpiresAt: f.now.Unix()})
	if err != nil || n != 10 {
		t.Fatalf("active links=%d err=%v", n, err)
	}
	f.exec(t, "UPDATE preview_links SET revoked_at=1")
	for range 50 {
		f.issue(t)
		f.exec(t, "UPDATE preview_links SET revoked_at=1")
	}
	if _, err = f.svc.Issue(f.ctx, provenance.Principal{UserID: f.user.ID}, f.page.ID, 1, "https://cms.test", nil); !errors.Is(err, preview.ErrLimit) {
		t.Fatal("revocation bypassed issuance rate")
	}
}

func TestReviewMigrationRollbackPreservesPagesAndSnapshots(t *testing.T) {
	f := setup(t)
	f.issue(t)
	if err := store.RunMigrationCommand(f.ctx, f.db, "down-to", "20261101220000"); err != nil {
		t.Fatal(err)
	}
	if _, err := preview.Load(f.ctx, f.q, f.page.ID, 1); err != nil {
		t.Fatal("rollback damaged earlier snapshots")
	}
	var n int
	if err := f.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('claims_checks','preview_links')").Scan(&n); err != nil || n != 0 {
		t.Fatal("rollback retained review tables")
	}
	if err := store.Migrate(f.db); err != nil {
		t.Fatal(err)
	}
	if _, err := preview.Load(f.ctx, f.q, f.page.ID, 1); err != nil {
		t.Fatal(err)
	}
	f.issue(t)
}
