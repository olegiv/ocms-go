// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type retentionFixture struct {
	t    *testing.T
	db   *sql.DB
	q    *store.Queries
	ctx  context.Context
	user store.User
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	f := &retentionFixture{t: t, db: db, q: store.New(db), ctx: context.Background()}
	f.user = f.newUser()
	return f
}

func (f *retentionFixture) newUser() store.User {
	f.t.Helper()
	now := time.Now().UTC()
	u, err := f.q.CreateUser(f.ctx, store.CreateUserParams{Email: uuid.NewString() + "@example.test", PasswordHash: "unused", Role: "editor", Name: "Editor", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f *retentionFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *retentionFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// authored is the validation evidence of an ordinary agent write.
func authored(int64) string { return `{"prompt_contract_version":"1","references":[]}` }

// pageSpec describes a page with complete revisions 1..revisions. The default
// is an unpublished AI draft owned by the fixture user.
type pageSpec struct {
	status    string
	scheduled bool
	notAI     bool
	author    int64
	revisions int64
	evidence  func(revision int64) string
	// source names who wrote a revision; nil means every revision is an
	// agent's ("api").
	source func(revision int64) string
}

func (f *retentionFixture) page(spec pageSpec) int64 {
	f.t.Helper()
	now := time.Now().UTC()
	if spec.status == "" {
		spec.status = "draft"
	}
	if spec.author == 0 {
		spec.author = f.user.ID
	}
	if spec.evidence == nil {
		spec.evidence = authored
	}
	if spec.source == nil {
		spec.source = func(int64) string { return "api" }
	}
	params := store.CreatePageParams{Title: "Page", Slug: "page-" + uuid.NewString(), Body: "<p>Body</p>", Status: spec.status, AuthorID: spec.author, LanguageCode: "en", PageType: "page", CreatedAt: now, UpdatedAt: now}
	if spec.scheduled {
		params.ScheduledAt = sql.NullTime{Time: now.Add(time.Hour), Valid: true}
	}
	page, err := f.q.CreatePage(f.ctx, params)
	if err != nil {
		f.t.Fatal(err)
	}
	if !spec.notAI {
		if err = f.q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: spec.author, SetAt: now}); err != nil {
			f.t.Fatal(err)
		}
	}
	f.exec("UPDATE pages SET revision=? WHERE id=?", spec.revisions, page.ID)
	for revision := int64(1); revision <= spec.revisions; revision++ {
		if _, err = f.q.CreateCompletePageVersion(f.ctx, store.CreateCompletePageVersionParams{PageID: page.ID, Title: "Page", Body: "<p>Body</p>", ChangedBy: spec.author, CreatedAt: now, Revision: sql.NullInt64{Int64: revision, Valid: true}, SnapshotJson: sql.NullString{String: `{"title":"Page"}`, Valid: true}}); err != nil {
			f.t.Fatal(err)
		}
		err = f.q.CreateContentProvenance(f.ctx, store.CreateContentProvenanceParams{PageID: page.ID, Revision: revision, UserID: sql.NullInt64{Int64: spec.author, Valid: true}, Source: spec.source(revision), Transport: "mcp", ValidatedJson: spec.evidence(revision), ReportedJson: `{}`, CreatedAt: now})
		if err != nil {
			f.t.Fatal(err)
		}
	}
	return page.ID
}

func (f *retentionFixture) revisions(pageID int64) []int64 {
	f.t.Helper()
	rows, err := f.db.Query("SELECT revision FROM page_versions WHERE page_id=? AND legacy=0 ORDER BY revision", pageID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var result []int64
	for rows.Next() {
		var revision int64
		if err = rows.Scan(&revision); err != nil {
			f.t.Fatal(err)
		}
		result = append(result, revision)
	}
	if err = rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *retentionFixture) evidenceRevisions(pageID int64) []int64 {
	f.t.Helper()
	rows, err := f.db.Query("SELECT revision FROM content_provenance WHERE page_id=? ORDER BY revision", pageID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var result []int64
	for rows.Next() {
		var revision int64
		if err = rows.Scan(&revision); err != nil {
			f.t.Fatal(err)
		}
		result = append(result, revision)
	}
	return result
}

func (f *retentionFixture) prune() pagewrite.RevisionPruneResult {
	f.t.Helper()
	result, err := pagewrite.PruneAIDraftRevisions(f.ctx, f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func rangeOf(from, to int64) []int64 {
	var result []int64
	for n := from; n <= to; n++ {
		result = append(result, n)
	}
	return result
}

func (f *retentionFixture) wantRevisions(pageID int64, want []int64) {
	f.t.Helper()
	if got := f.revisions(pageID); !slices.Equal(got, want) {
		f.t.Fatalf("page %d versions = %v, want %v", pageID, got, want)
	}
	if got := f.evidenceRevisions(pageID); !slices.Equal(got, want) {
		f.t.Fatalf("page %d provenance = %v, want %v (versions and evidence must go together)", pageID, got, want)
	}
}

func (f *retentionFixture) pin(pageID, revision int64, expires time.Time, revoked int64) {
	f.t.Helper()
	f.exec(`INSERT INTO preview_links(token_hash,page_id,revision,created_by,session_version,created_at,expires_at,revoked_at) VALUES (?,?,?,?,?,?,?,?)`,
		uuid.NewString(), pageID, revision, f.user.ID, f.user.SessionVersion, time.Now().Unix(), expires.Unix(), revoked)
}

func (f *retentionFixture) replay(pageID, revision int64) string {
	f.t.Helper()
	id := uuid.NewString()
	result := sql.NullString{String: `{"id":1}`, Valid: true}
	if err := f.q.CreateIdempotencyKey(f.ctx, store.CreateIdempotencyKeyParams{UserID: f.user.ID, Tool: "update_draft_page", RequestID: id, RequestHash: "fixture", ResultJson: result, EntityType: "page", EntityID: pageID, EntityRevision: revision, CreatedAt: time.Now().UTC()}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *retentionFixture) provenanceID(pageID, revision int64) int64 {
	f.t.Helper()
	fact, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: pageID, Revision: revision})
	if err != nil {
		f.t.Fatal(err)
	}
	return fact.ID
}

func (f *retentionFixture) assertConsistent() {
	f.t.Helper()
	rows, err := f.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		f.t.Fatal("retention left a foreign-key violation")
	}
	// Every complete revision keeps its evidence and the other way round, so no
	// revision list, comparison or review panel meets half a record.
	if n := f.count(`SELECT COUNT(*) FROM page_versions pv WHERE pv.legacy=0 AND NOT EXISTS (SELECT 1 FROM content_provenance c WHERE c.page_id=pv.page_id AND c.revision=pv.revision)`); n != 0 {
		f.t.Fatalf("%d versions lost their provenance", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM content_provenance c WHERE NOT EXISTS (SELECT 1 FROM page_versions pv WHERE pv.page_id=c.page_id AND pv.revision=c.revision)`); n != 0 {
		f.t.Fatalf("%d provenance rows lost their version", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM claims_checks k WHERE NOT EXISTS (SELECT 1 FROM content_provenance c WHERE c.id=k.provenance_id)`); n != 0 {
		f.t.Fatalf("%d claim acknowledgments lost their provenance", n)
	}
}

// Fix invariant: an AI draft keeps its newest AIDraftRevisionsKept revisions
// plus what is still pinned, and loses the rest with their dependents. Bug
// state: no retention (every revision survives) or a version removed without
// its provenance (the pair check fails).
func TestPruneKeepsNewestRevisionsAndWhatIsStillPinned(t *testing.T) {
	f := newRetentionFixture(t)
	draft := f.page(pageSpec{revisions: 30})
	f.pin(draft, 3, time.Now().Add(time.Hour), 0)                                                 // active share link: kept
	f.pin(draft, 4, time.Now().Add(time.Hour), 1)                                                 // revoked: no longer reads revision 4
	f.pin(draft, 5, time.Now().Add(-time.Hour), 0)                                                // expired: no longer reads revision 5
	f.replay(draft, 20)                                                                           // newest 20: untouched
	prunedReplay := f.replay(draft, 6)                                                            // revision 6 goes: receipt becomes a tombstone
	f.replay(draft, 3)                                                                            // pinned revision 3 stays: receipt stays
	f.exec("UPDATE idempotency_keys SET result_json=NULL WHERE request_id=?", f.replay(draft, 7)) // already a tombstone
	if err := f.q.CheckClaim(f.ctx, store.CheckClaimParams{ProvenanceID: f.provenanceID(draft, 7), ClaimIndex: 0, CheckedBy: sql.NullInt64{Int64: f.user.ID, Valid: true}, CheckedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.q.CheckClaim(f.ctx, store.CheckClaimParams{ProvenanceID: f.provenanceID(draft, 25), ClaimIndex: 0, CheckedBy: sql.NullInt64{Int64: f.user.ID, Valid: true}, CheckedAt: 1}); err != nil {
		t.Fatal(err)
	}
	keysBefore := f.count("SELECT COUNT(*) FROM idempotency_keys")

	result := f.prune()
	if result != (pagewrite.RevisionPruneResult{Versions: 9, Provenance: 9, Replays: 1}) {
		t.Fatalf("result = %+v", result)
	}
	f.wantRevisions(draft, append([]int64{3}, rangeOf(11, 30)...))
	f.assertConsistent()

	// Acknowledgments of a removed revision go; those of a kept one stay.
	if n := f.count("SELECT COUNT(*) FROM claims_checks"); n != 1 {
		t.Fatalf("claims_checks = %d, want only the kept revision's", n)
	}
	// Receipts of removed revisions are tombstones, not deleted: quota counting
	// and the never-re-execute guarantee depend on the rows.
	if got := f.count("SELECT COUNT(*) FROM idempotency_keys"); got != keysBefore {
		t.Fatalf("idempotency rows %d, was %d", got, keysBefore)
	}
	if n := f.count("SELECT COUNT(*) FROM idempotency_keys WHERE request_id=? AND result_json IS NULL", prunedReplay); n != 1 {
		t.Fatal("receipt of a removed revision still replays")
	}
	if n := f.count("SELECT COUNT(*) FROM idempotency_keys WHERE entity_revision IN (3,20) AND result_json IS NOT NULL"); n != 2 {
		t.Fatal("receipt of a kept revision was compacted")
	}

	// Repeatable: a second run has nothing left to do.
	if again := f.prune(); again != (pagewrite.RevisionPruneResult{}) {
		t.Fatalf("second run = %+v", again)
	}
}

// Fix invariant: only unpublished, unscheduled, AI-editable pages are pruned
// (the definition ListOwnAIDrafts uses, for every owner); everything else
// keeps all its history. Bug state: pruning by revision count alone.
func TestPruneUsesTheAuthoringDefinitionOfAnAIDraft(t *testing.T) {
	f := newRetentionFixture(t)
	other := f.newUser()
	legacyHolder := f.page(pageSpec{revisions: 25})
	f.exec(`INSERT INTO page_versions(page_id,title,body,changed_by,created_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)`, legacyHolder, "Old", "old", f.user.ID)
	f.exec(`INSERT INTO page_versions(page_id,title,body,changed_by,created_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)`, legacyHolder, "Older", "older", f.user.ID)

	pages := map[string]int64{
		"own AI draft":           legacyHolder,
		"other user's AI draft":  f.page(pageSpec{author: other.ID, revisions: 30}),
		"published AI page":      f.page(pageSpec{status: "published", revisions: 30}),
		"scheduled AI page":      f.page(pageSpec{scheduled: true, revisions: 30}),
		"draft without AI":       f.page(pageSpec{notAI: true, revisions: 30}),
		"published human page":   f.page(pageSpec{status: "published", notAI: true, revisions: 30}),
		"archived AI page":       f.page(pageSpec{status: "archived", revisions: 30}),
		"short AI draft":         f.page(pageSpec{revisions: 20}),
		"other user's published": f.page(pageSpec{author: other.ID, status: "published", revisions: 30}),
	}
	pruned := map[string]bool{}
	f.prune()
	for name, id := range pages {
		want := int64(0)
		if err := f.db.QueryRow("SELECT revision FROM pages WHERE id=?", id).Scan(&want); err != nil {
			t.Fatal(err)
		}
		pruned[name] = int64(len(f.revisions(id))) < want
	}
	for name, want := range map[string]bool{"own AI draft": true, "other user's AI draft": true} {
		if pruned[name] != want {
			t.Errorf("%s pruned = %v, want %v", name, pruned[name], want)
		}
	}
	for name := range pages {
		if name != "own AI draft" && name != "other user's AI draft" && pruned[name] {
			t.Errorf("%s lost history", name)
		}
	}
	if n := f.count("SELECT COUNT(*) FROM page_versions WHERE page_id=? AND legacy=1", legacyHolder); n != 2 {
		t.Fatalf("legacy history changed: %d rows", n)
	}

	// The same set as the authoring service's own-draft listing, for every user.
	listed := map[int64]bool{}
	for _, user := range []int64{f.user.ID, other.ID} {
		drafts, err := f.q.ListOwnAIDrafts(f.ctx, store.ListOwnAIDraftsParams{UserID: user, LanguageCode: "", ResultLimit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		for _, draft := range drafts {
			listed[draft.ID] = true
		}
	}
	for name, id := range pages {
		var revision int64
		if err := f.db.QueryRow("SELECT revision FROM pages WHERE id=?", id).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if listed[id] && revision > pagewrite.AIDraftRevisionsKept != pruned[name] {
			t.Errorf("%s: authoring lists it as an AI draft = %v but retention pruned = %v", name, listed[id], pruned[name])
		}
		if !listed[id] && pruned[name] {
			t.Errorf("%s is not an AI draft for authoring but was pruned", name)
		}
	}
	f.assertConsistent()
}

func TestPruneBatchIsBoundedAndDrainsOldestFirst(t *testing.T) {
	f := newRetentionFixture(t)
	first := f.page(pageSpec{revisions: pagewrite.AIDraftRevisionsKept + 60})
	second := f.page(pageSpec{revisions: pagewrite.AIDraftRevisionsKept + 47})

	// 107 prunable revisions: the batch bound removes 100, then the remaining 7.
	if result := f.prune(); result.Versions != pagewrite.RevisionPruneBatchSize || result.Provenance != pagewrite.RevisionPruneBatchSize {
		t.Fatalf("first run = %+v", result)
	}
	f.wantRevisions(first, rangeOf(61, 80))
	f.wantRevisions(second, rangeOf(41, 67))
	f.assertConsistent()
	if result := f.prune(); result.Versions != 7 {
		t.Fatalf("second run = %+v", result)
	}
	f.wantRevisions(second, rangeOf(48, 67))
	if result := f.prune(); result != (pagewrite.RevisionPruneResult{}) {
		t.Fatalf("third run = %+v", result)
	}
}

// Fix invariant: evidence other code reads by "latest at or before current" or
// by recorded reference is never pruned out from under it. Bug state: pruning
// by age alone drops them (publication validation would see no AI history, and
// the next update of the referencing draft would fail).
func TestPruneKeepsLatestEvidenceAndReferencedRevisions(t *testing.T) {
	f := newRetentionFixture(t)
	// Only revisions 2 and 4 carry authoring and translation evidence, as when a
	// human edited the draft many times since.
	evidence := func(revision int64) string {
		switch revision {
		case 2:
			return `{"prompt_contract_version":"1"}`
		case 4:
			return `{"translation_basis":{"source_page_id":99,"source_revision":1,"fingerprint":"x"}}`
		}
		return `{}`
	}
	edited := f.page(pageSpec{revisions: 30, evidence: evidence})
	authoringBefore, err := f.q.GetLatestAuthoringProvenance(f.ctx, store.GetLatestAuthoringProvenanceParams{PageID: edited, Revision: 30})
	if err != nil {
		t.Fatal(err)
	}
	translationBefore, err := f.q.GetLatestTranslationProvenance(f.ctx, store.GetLatestTranslationProvenanceParams{PageID: edited, Revision: 30})
	if err != nil {
		t.Fatal(err)
	}

	// The current evidence of two drafts references revisions of "cited": edited cites 2;
	cited := f.page(pageSpec{revisions: 30, evidence: func(int64) string { return `{}` }})
	f.exec("UPDATE content_provenance SET validated_json=? WHERE page_id=? AND revision=30", fmt.Sprintf(`{"references":[{"page_id":%d,"revision":2,"role":"example"}]}`, cited), edited)
	// holder's current revision cites 5 and 7. Its older revisions cite 9, which is not current evidence.
	holder := f.page(pageSpec{revisions: 3, evidence: func(revision int64) string {
		if revision == 3 {
			return fmt.Sprintf(`{"references":[{"page_id":%d,"revision":5,"role":"example"},{"page_id":%d,"revision":7,"role":"style"}]}`, cited, cited)
		}
		return fmt.Sprintf(`{"references":[{"page_id":%d,"revision":9,"role":"example"}]}`, cited)
	}})

	// Damaged evidence JSON cannot make a run fail: it is simply not an anchor.
	damaged := f.page(pageSpec{revisions: 25, evidence: func(revision int64) string {
		if revision == 2 {
			return `not json`
		}
		return `{}`
	}})

	f.prune()
	f.wantRevisions(damaged, rangeOf(6, 25))
	f.wantRevisions(edited, append([]int64{2, 4}, rangeOf(11, 30)...))
	f.wantRevisions(cited, append([]int64{2, 5, 7}, rangeOf(11, 30)...))
	f.wantRevisions(holder, []int64{1, 2, 3})

	authoringAfter, err := f.q.GetLatestAuthoringProvenance(f.ctx, store.GetLatestAuthoringProvenanceParams{PageID: edited, Revision: 30})
	if err != nil || authoringAfter.ID != authoringBefore.ID {
		t.Fatalf("latest authoring evidence changed: %+v %v", authoringAfter, err)
	}
	translationAfter, err := f.q.GetLatestTranslationProvenance(f.ctx, store.GetLatestTranslationProvenanceParams{PageID: edited, Revision: 30})
	if err != nil || translationAfter.ID != translationBefore.ID {
		t.Fatalf("latest translation evidence changed: %+v %v", translationAfter, err)
	}
	f.assertConsistent()
}

func TestPruneIsAtomicAcrossEveryTableAndFailedCommit(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(commitFailure), func(t *testing.T) {
			f := newRetentionFixture(t)
			draft := f.page(pageSpec{revisions: 30})
			f.replay(draft, 1)
			if commitFailure {
				f.exec(`CREATE TABLE retention_commit_guard (page_id INTEGER REFERENCES pages(id) DEFERRABLE INITIALLY DEFERRED)`)
				f.exec(`CREATE TRIGGER retention_failure AFTER DELETE ON content_provenance BEGIN INSERT INTO retention_commit_guard VALUES (-1); END`)
			} else {
				f.exec(`CREATE TRIGGER retention_failure BEFORE DELETE ON content_provenance BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
			}
			if result, err := pagewrite.PruneAIDraftRevisions(f.ctx, f.db); err == nil || result != (pagewrite.RevisionPruneResult{}) {
				t.Fatalf("failed run = %+v, %v", result, err)
			}
			f.wantRevisions(draft, rangeOf(1, 30))
			if f.count("SELECT COUNT(*) FROM idempotency_keys WHERE result_json IS NOT NULL") != 1 {
				t.Fatal("replay compaction survived the rollback")
			}
			f.exec("DROP TRIGGER retention_failure")
			if result := f.prune(); result.Versions != 10 {
				t.Fatalf("run after repair = %+v", result)
			}
		})
	}
}

func TestPruneCancelledContextLeavesHistoryUntouched(t *testing.T) {
	f := newRetentionFixture(t)
	draft := f.page(pageSpec{revisions: 30})
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := pagewrite.PruneAIDraftRevisions(ctx, f.db); err == nil {
		t.Fatal("cancelled run reported success")
	}
	f.wantRevisions(draft, rangeOf(1, 30))
}

// Fix invariant: every column that stores a page revision number, and every
// foreign key into page_versions/content_provenance, is handled by retention.
// A new one fails here until PruneAIDraftRevisions protects or removes it.
// Bug state: a new revision-referencing table is added and pruning orphans it.
func TestEveryRevisionReferenceIsAccountedForByRetention(t *testing.T) {
	f := newRetentionFixture(t)
	accounted := map[string]string{
		"pages.revision":                   "current revision: never pruned (the age rule keeps the newest 20)",
		"page_versions.revision":           "pruned together with its provenance",
		"content_provenance.revision":      "pruned together with its version; claims_checks cascade",
		"preview_links.revision":           "revisions pinned by an unrevoked, unexpired link are kept",
		"idempotency_keys.entity_revision": "receipts of a pruned revision are compacted to tombstones",
	}
	rows, err := f.db.Query(`SELECT m.name || '.' || p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table' AND p.name LIKE '%revision%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	found := map[string]bool{}
	for rows.Next() {
		var column string
		if err = rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		found[column] = true
		if _, ok := accounted[column]; !ok {
			t.Errorf("column %s stores a revision number that retention does not account for", column)
		}
	}
	for column := range accounted {
		if !found[column] {
			t.Errorf("accounted column %s no longer exists; update retention and this inventory", column)
		}
	}

	references, err := f.db.Query(`SELECT m.name, f."table", f.on_delete FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f WHERE m.type='table' AND f."table" IN ('page_versions','content_provenance')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = references.Close() }()
	var got []string
	for references.Next() {
		var table, target, action string
		if err = references.Scan(&table, &target, &action); err != nil {
			t.Fatal(err)
		}
		got = append(got, table+" -> "+target+" ON DELETE "+action)
	}
	if want := []string{"claims_checks -> content_provenance ON DELETE CASCADE"}; !slices.Equal(got, want) {
		t.Errorf("foreign keys into revision tables = %v, want %v", got, want)
	}
}

// TestPruneKeepsHumanAndSystemRevisions checks that retention removes only
// revisions an agent or API key wrote. A long-lived human draft whose AI
// access is switched on later, or an AI page published by an editor and moved
// back to draft, keeps every revision a person or the system saved
// (2026-11-03 review of the B-1 fix).
//
// Bug state: drop the provenance source condition from
// ListPrunableAIDraftRevisions and this fails.
func TestPruneKeepsHumanAndSystemRevisions(t *testing.T) {
	f := newRetentionFixture(t)
	// Revisions 1-10 by an editor, 11 a system publication, 12-40 the agent's.
	page := f.page(pageSpec{revisions: 40, source: func(revision int64) string {
		switch {
		case revision <= 10:
			return "human"
		case revision == 11:
			return "system"
		default:
			return "oauth"
		}
	}})
	f.prune()
	f.wantRevisions(page, append(rangeOf(1, 11), rangeOf(21, 40)...))
	f.assertConsistent()
}
