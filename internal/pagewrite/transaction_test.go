// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type fixture struct {
	db   *sql.DB
	q    *store.Queries
	user store.User
	page store.Page
}

func setup(t *testing.T) fixture {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: "writer@example.test", PasswordHash: "unused", Role: "editor", Name: "Writer", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	work, err := pagewrite.InTx(ctx, db, provenance.Principal{UserID: u.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		p, createErr := work.Q.CreatePage(ctx, store.CreatePageParams{Title: "Original", Slug: "original", Body: "original body", Summary: "original summary", Status: "draft", AuthorID: u.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now})
		if createErr != nil {
			return createErr
		}
		work.Created(p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range work.Pages {
		return fixture{db, q, u, p}
	}
	t.Fatal("missing inserted page")
	return fixture{}
}

func TestRevisionCoversRelationsNoOpsAndFreshProvenance(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	base := f.page.Revision
	category, err := f.q.CreateCategory(ctx, store.CreateCategoryParams{Name: "News", Slug: "news", LanguageCode: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	work, err := pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "oauth", OAuthGrantID: "authenticated-grant", OAuthClientID: "authenticated-client"}, func(work *pagewrite.Tx) error {
		if _, trackErr := work.Track(ctx, f.page.ID, &base, true); trackErr != nil {
			return trackErr
		}
		work.SetEvidence(provenance.Evidence{Reported: json.RawMessage(`{"model":"reported-model"}`), Validated: json.RawMessage(`{"taxonomy":true}`)})
		return work.Q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: f.page.ID, CategoryID: category.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	page := work.Pages[f.page.ID]
	if page.Revision != base+1 {
		t.Fatalf("relation edit revision=%d", page.Revision)
	}
	version, err := f.q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: page.Revision, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err = json.Unmarshal([]byte(version.SnapshotJson.String), &saved); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"summary", "slug", "language_code", "meta_title", "featured_image_id", "category_ids", "tag_ids", "translations"} {
		if saved[field] == nil {
			t.Fatalf("snapshot missing %s", field)
		}
	}
	work, err = pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		current, workErr := work.Track(ctx, page.ID, &page.Revision, true)
		if workErr != nil {
			return workErr
		}
		params := pagewrite.ParamsFor(current)
		params.MetaTitle = "SEO only"
		_, workErr = work.Q.UpdatePage(ctx, params)
		return workErr
	})
	if err != nil {
		t.Fatal(err)
	}
	page = work.Pages[page.ID]
	prov, err := f.q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if prov.Source != "human" || prov.ReportedJson != "{}" || prov.ValidatedJson != "{}" || prov.OauthGrantID != "" {
		t.Fatalf("human revision inherited agent claims: %+v", prov)
	}
	work, err = pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		current, workErr := work.Track(ctx, page.ID, &page.Revision, true)
		if workErr != nil {
			return workErr
		}
		_, workErr = work.Q.UpdatePage(ctx, pagewrite.ParamsFor(current))
		return workErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if work.Pages[page.ID].Revision != page.Revision {
		t.Fatal("no-op advanced revision")
	}
}

func TestCASRaceAndConflictFields(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, source := range []string{"human", "oauth"} {
		wg.Go(func() {
			<-start
			principal := provenance.Principal{UserID: f.user.ID, Source: source}
			if source == "oauth" {
				principal.OAuthGrantID, principal.OAuthClientID = "grant-1", "client-1"
			}
			_, err := pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
				current, err := work.Track(ctx, f.page.ID, &f.page.Revision, true)
				if err != nil {
					return err
				}
				params := pagewrite.ParamsFor(current)
				params.Title = source
				_, err = work.Q.UpdatePage(ctx, params)
				return err
			})
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	passed, conflicted := 0, 0
	for err := range results {
		if err == nil {
			passed++
			continue
		}
		var conflict *pagewrite.Conflict
		if !errors.As(err, &conflict) {
			t.Fatalf("race returned %v", err)
		}
		conflicted++
		if conflict.CurrentRevision != 2 || len(conflict.ChangedFields) != 1 || conflict.ChangedFields[0] != "title" {
			t.Fatalf("conflict leaked content or missing fields: %+v", conflict)
		}
	}
	if passed != 1 || conflicted != 1 {
		t.Fatalf("wins=%d conflicts=%d", passed, conflicted)
	}
}

func TestSnapshotFailureRollsBackPageAndRelations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`CREATE TRIGGER fail_provenance BEFORE INSERT ON content_provenance BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		current, err := work.Track(ctx, f.page.ID, &f.page.Revision, true)
		if err != nil {
			return err
		}
		params := pagewrite.ParamsFor(current)
		params.Title = "must roll back"
		if _, err = work.Q.UpdatePage(ctx, params); err != nil {
			return err
		}
		return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: f.page.ID, AiEditable: true, SetBy: f.user.ID, SetAt: time.Now()})
	})
	if err == nil {
		t.Fatal("injected failure succeeded")
	}
	page, err := f.q.GetPageByID(ctx, f.page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != f.page.Title || page.Revision != 1 {
		t.Fatalf("partial content committed: %+v", page)
	}
	if _, err = f.q.GetPageAIAccess(ctx, page.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partial AI flag committed: %v", err)
	}
	count, err := f.q.CountPageVersions(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("partial version committed: %d", count)
	}
}

func TestTranslationMembershipTracksBothPages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now()
	_, err := f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	var target store.Page
	work, err := pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		var createErr error
		target, createErr = work.Q.CreatePage(ctx, store.CreatePageParams{Title: "Translation", Slug: "translated", Status: "draft", AuthorID: f.user.ID, LanguageCode: "ru", PageType: "post", CreatedAt: now, UpdatedAt: now})
		if createErr != nil {
			return createErr
		}
		work.Created(target)
		return work.LinkTranslation(ctx, f.page.ID, "ru", target.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if work.Pages[f.page.ID].Revision != 2 || work.Pages[target.ID].Revision != 1 {
		t.Fatalf("membership revisions: %+v", work.Pages)
	}
}

func TestJoinedWriteFailureDoesNotLeakIntoOuterCommit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`CREATE TRIGGER fail_one_snapshot BEFORE INSERT ON page_versions
		WHEN NEW.title = 'Rejected' BEGIN SELECT RAISE(FAIL, 'injected snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	q := f.q.WithTx(tx)
	principal := provenance.Principal{UserID: f.user.ID, Source: "system", Transport: "import"}
	params := store.CreatePageParams{Title: "Rejected", Slug: "rejected", Status: "draft", AuthorID: f.user.ID, LanguageCode: "en", PageType: "post", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_, err = pagewrite.Insert(ctx, q, principal, params, func(work *pagewrite.Tx, page store.Page) error {
		return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: f.user.ID, SetAt: time.Now()})
	})
	if err == nil {
		t.Fatal("injected snapshot failure succeeded")
	}
	params.Title, params.Slug = "Accepted", "accepted"
	accepted, err := pagewrite.Insert(ctx, q, principal, params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.GetPageBySlug(ctx, "rejected"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected page survived outer commit: %v", err)
	}
	var flags, snapshots, facts int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM page_ai_access`).Scan(&flags); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM page_versions WHERE legacy = 0`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM content_provenance`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if flags != 0 || snapshots != 2 || facts != 2 || accepted.Revision != 1 {
		t.Fatalf("partial write: flags=%d snapshots=%d provenance=%d accepted revision=%d", flags, snapshots, facts, accepted.Revision)
	}
}

func TestNewTxRejectsAutocommitQueries(t *testing.T) {
	f := setup(t)
	if work, err := pagewrite.NewTx(f.q, provenance.Principal{Source: "system"}); err == nil || work != nil {
		t.Fatal("accepted autocommit queries as a page-write transaction")
	}
}

func TestReportedClaimsCannotReplaceVerifiedProvenance(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	work, err := pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "oauth", OAuthGrantID: "real-grant", OAuthClientID: "registered-client", Transport: "mcp"}, func(work *pagewrite.Tx) error {
		current, err := work.Track(ctx, f.page.ID, &f.page.Revision, true)
		if err != nil {
			return err
		}
		work.SetEvidence(provenance.Evidence{
			Validated: json.RawMessage(`{"references":[7]}`),
			Reported:  json.RawMessage(`{"user_id":999,"oauth_client_id":"claimed-client","validated":true,"model":"claimed-model"}`),
		})
		params := pagewrite.ParamsFor(current)
		params.Summary = "Changed summary"
		_, err = work.Q.UpdatePage(ctx, params)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	page := work.Pages[f.page.ID]
	facts, err := f.q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if facts.UserID.Int64 != f.user.ID || facts.OauthGrantID != "real-grant" || facts.OauthClientID != "registered-client" || facts.Source != "oauth" || facts.Transport != "mcp" || facts.ValidatedJson != `{"references":[7]}` || facts.ReportedJson != `{"user_id":999,"oauth_client_id":"claimed-client","validated":true,"model":"claimed-model"}` {
		t.Fatalf("provenance classes mixed: %+v", facts)
	}
	var missing int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM page_versions v LEFT JOIN content_provenance p
		ON p.page_id = v.page_id AND p.revision = v.revision
		WHERE v.legacy = 0 AND p.id IS NULL`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("%d complete revisions lack provenance", missing)
	}
}

func TestTranslationMergeRejectsDuplicateSiblingLanguage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	principal := provenance.Principal{UserID: f.user.ID, Source: "human"}
	for _, code := range []string{"ru", "de"} {
		if _, err := f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: code, Name: code, NativeName: code, IsActive: true, Direction: "ltr", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	create := func(slug, language string, source int64) store.Page {
		t.Helper()
		page, err := pagewrite.Insert(ctx, f.q, principal, store.CreatePageParams{Title: slug, Slug: slug, Status: "draft", AuthorID: f.user.ID, LanguageCode: language, PageType: "post", CreatedAt: time.Now(), UpdatedAt: time.Now()}, func(work *pagewrite.Tx, page store.Page) error {
			if source == 0 {
				return nil
			}
			return work.LinkTranslation(ctx, source, language, page.ID)
		})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	firstRU := create("first-ru", "ru", f.page.ID)
	secondDE := create("second-de", "de", 0)
	secondRU := create("second-ru", "ru", secondDE.ID)
	_, err := pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
		return work.LinkTranslation(ctx, f.page.ID, "de", secondDE.ID)
	})
	if !errors.Is(err, pagewrite.ErrTranslationExists) {
		t.Fatalf("duplicate sibling language merge: %v", err)
	}
	for id, want := range map[int64]int64{f.page.ID: 2, firstRU.ID: 1, secondDE.ID: 2, secondRU.ID: 1} {
		testutil.AssertPageRevision(t, f.q, id, want)
	}
	var edges int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM translations WHERE entity_type = 'page'`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 2 {
		t.Fatalf("rejected merge changed the graph: %d edges", edges)
	}
}

// TestTranslationLinkRecordsTheSourceAsASystemChange checks that linking an
// agent's translation draft credits the agent only with the new page: the
// source page, whose text the agent did not change, gets its new revision
// recorded as a system change by the acting account, with no credential and
// none of the agent's evidence (2026-11-03 audit B-4).
//
// Bug state: record every tracked page with the transaction principal in
// Tx.Finish and this fails.
func TestTranslationLinkRecordsTheSourceAsASystemChange(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	agent := provenance.Principal{UserID: f.user.ID, Source: "oauth", OAuthGrantID: "grant-1", OAuthClientID: "client-1", Transport: "mcp"}
	var target store.Page
	_, err := pagewrite.InTx(ctx, f.db, agent, func(work *pagewrite.Tx) error {
		var createErr error
		target, createErr = work.Q.CreatePage(ctx, store.CreatePageParams{Title: "Перевод", Slug: "perevod", Status: "draft", AuthorID: f.user.ID, LanguageCode: "ru", PageType: "post", CreatedAt: now, UpdatedAt: now})
		if createErr != nil {
			return createErr
		}
		work.Created(target)
		work.SetEvidence(provenance.Evidence{Validated: []byte(`{"checked":true}`)})
		return work.LinkTranslation(ctx, f.page.ID, "ru", target.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.q.GetPageByID(ctx, f.page.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		page                     store.Page
		source, transport, grant string
		evidence                 string
	}{
		{source, "system", "translation_link", "", "{}"},
		{target, "oauth", "mcp", "grant-1", `{"checked":true}`},
	} {
		row, loadErr := f.q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: tc.page.ID, Revision: tc.page.Revision})
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if row.Source != tc.source || row.Transport != tc.transport || row.OauthGrantID != tc.grant || row.ValidatedJson != tc.evidence || row.UserID.Int64 != f.user.ID {
			t.Errorf("page %d provenance %+v, want source %s transport %s grant %q evidence %s by user %d", tc.page.ID, row, tc.source, tc.transport, tc.grant, tc.evidence, f.user.ID)
		}
	}
}
