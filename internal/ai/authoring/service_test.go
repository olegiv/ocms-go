// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo/markdown"
	"github.com/olegiv/ocms-go/internal/store"
)

type fixture struct {
	db    *sql.DB
	q     *store.Queries
	svc   *authoring.Service
	pages *pages.Service
	actor v2.Actor
	user  store.User
	ctx   context.Context
}

func setup(t testing.TB) *fixture {
	t.Helper()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "authoring.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q := store.New(db)
	ctx := context.Background()
	now := time.Now()
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: "agent-" + uuid.NewString() + "@example.test", Name: "Agent", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, q: q, ctx: ctx, user: u, pages: pages.NewService(db, q, nil, nil, pages.Policy{SanitizeHTML: true})}
	f.actor = f.key(t)
	f.svc = &authoring.Service{Q: q, Pages: f.pages, SiteURL: "https://example.test", Access: grounding.Access{UserID: u.ID, OwnDrafts: true}, Authorize: func(context.Context, *store.Queries) error { return nil }}
	return f
}
func (f *fixture) key(t testing.TB) v2.Actor {
	t.Helper()
	now := time.Now()
	key, err := f.q.CreateAPIKey(f.ctx, store.CreateAPIKeyParams{Name: "Fixture", KeyHash: "unused", KeyPrefix: uuid.NewString()[:8], Permissions: `["mcp:access","mcp:drafts"]`, CreatedBy: f.user.ID, IsActive: true, CreatedAt: now, UpdatedAt: now, ExpiresAt: sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	return v2.Actor{APIKey: &key, Transport: "mcp", Permissions: []string{model.PermissionMCPAccess, model.PermissionMCPDrafts}}
}

func BenchmarkAuthoringWrites(b *testing.B) {
	for _, operation := range []string{"create", "update"} {
		b.Run(operation, func(b *testing.B) {
			f := setup(b)
			// Keep historical test receipts outside the quota window; the quota
			// query still runs on every measured transaction.
			f.svc.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
			saved, err := f.svc.Create(f.ctx, f.actor, input("Benchmark initial draft"))
			if err != nil {
				b.Fatal(err)
			}
			n := 0
			for b.Loop() {
				n++
				if operation == "create" {
					saved, err = f.svc.Create(f.ctx, f.actor, input(fmt.Sprintf("Benchmark draft %d", n)))
				} else {
					saved, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision, Changes: draft.Changes{Summary: draft.Set(fmt.Sprintf("Review %d", n))}})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func input(title string) authoring.CreateInput {
	return authoring.CreateInput{RequestID: uuid.NewString(), Draft: draft.PageDraft{Title: title, Summary: "Review this draft", BodyMarkdown: "## Heading\n\nA **safe** paragraph."}}
}
func code(err error) string {
	var domain *v2.Error
	if errors.As(err, &domain) {
		return domain.Code()
	}
	var issue *authoring.Error
	if errors.As(err, &issue) {
		return issue.Code
	}
	return ""
}

func TestCreateReplayAcrossAppsAndBeforeMutableValidation(t *testing.T) {
	f := setup(t)
	in := input("Replay original draft")
	first, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.q.GetPageByID(f.ctx, first.PageID)
	if err != nil || p.Status != "draft" || p.AuthorID != f.user.ID || p.PublishedAt.Valid || p.ScheduledAt.Valid || p.Revision != 1 {
		t.Fatalf("draft lifecycle %+v %v", p, err)
	}
	if _, err = f.db.Exec("UPDATE languages SET is_active=0 WHERE code='en'"); err != nil {
		t.Fatal(err)
	}
	f.svc.SiteURL = "invalid current setting"
	second, err := f.svc.Create(f.ctx, f.key(t), in)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("replay recomputed mutable defaults: %+v %+v %v", first, second, err)
	}
	in.Draft.Title = "Changed payload"
	if _, err = f.svc.Create(f.ctx, f.actor, in); code(err) != "idempotency_key_reused" {
		t.Fatalf("changed request ID: %v", err)
	}
	var count int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM page_versions WHERE page_id=?", p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay added revision: %d %v", count, err)
	}
	var body string
	if err = f.db.QueryRow("SELECT result_json FROM idempotency_keys WHERE request_id=?", in.RequestID).Scan(&body); err != nil || strings.Contains(body, "safe</strong>") {
		t.Fatalf("replay stored the full body %v", err)
	}
}

// receiptFaultDB simulates a fault on the plain query handle once the request
// has committed. Reads on the write or replay transaction are unaffected.
type receiptFaultDB struct {
	*sql.DB
	requestID string
}

func (db receiptFaultDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "FROM content_provenance WHERE page_id = ? AND revision = ?") {
		var committed bool
		err := db.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM idempotency_keys WHERE request_id = ?)`, db.requestID).Scan(&committed)
		if err != nil || committed {
			return db.DB.QueryRowContext(ctx, `SELECT * FROM unavailable_provenance`)
		}
	}
	return db.DB.QueryRowContext(ctx, query, args...)
}

func TestDraftReceiptsSurvivePostCommitProvenanceFault(t *testing.T) {
	for _, operation := range []string{"create", "update", "translate"} {
		t.Run(operation, func(t *testing.T) {
			f := setup(t)
			in := input("Draft receipt")
			var save func() (authoring.SaveResult, error)
			expectedPages := int64(1)
			if operation == "create" {
				save = func() (authoring.SaveResult, error) { return f.svc.Create(f.ctx, f.actor, in) }
			} else {
				source, err := f.svc.Create(f.ctx, f.actor, input("Source draft"))
				if err != nil {
					t.Fatal(err)
				}
				if operation == "update" {
					patch := authoring.UpdateInput{RequestID: in.RequestID, PageID: source.PageID, BaseRevision: source.Revision,
						Changes: draft.Changes{Summary: draft.Set("Updated draft")}}
					save = func() (authoring.SaveResult, error) { return f.svc.Update(f.ctx, f.actor, patch) }
				} else {
					translation := authoring.TranslateInput{RequestID: in.RequestID, SourcePageID: source.PageID,
						SourceRevision: source.Revision, LanguageCode: "ru", Draft: in.Draft}
					save = func() (authoring.SaveResult, error) { return f.svc.Translate(f.ctx, f.actor, translation) }
					expectedPages++
				}
			}
			f.svc.Q = store.New(receiptFaultDB{DB: f.db, requestID: in.RequestID})
			first, err := save()
			if err != nil {
				count, countErr := f.q.CountPages(f.ctx)
				t.Fatalf("draft write returned %v with %d pages committed (count error: %v)", err, count, countErr)
			}
			page, err := f.q.GetPageByID(f.ctx, first.PageID)
			if err != nil || first.Revision != page.Revision || first.PreviewURL == "" {
				t.Fatalf("receipt does not identify the committed draft: %+v, %v", first, err)
			}
			if _, faultErr := f.svc.Q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision}); faultErr == nil {
				t.Fatal("post-commit provenance fault did not activate")
			}
			replay, err := save()
			if err != nil || !reflect.DeepEqual(replay, first) {
				t.Fatalf("replay returned %+v, %v, want %+v", replay, err, first)
			}
			if count, countErr := f.q.CountPages(f.ctx); countErr != nil || count != expectedPages {
				t.Fatalf("page count after replay = %d, %v, want %d", count, countErr, expectedPages)
			}
			page, err = f.q.GetPageByID(f.ctx, first.PageID)
			if err != nil || page.Revision != first.Revision {
				t.Fatalf("revision after replay = %d, %v, want %d", page.Revision, err, first.Revision)
			}
		})
	}
}

func TestDraftListDistinguishesLanguageFaultFromInvalidInput(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.List(f.ctx, f.actor, authoring.ListInput{LanguageCode: "en"}); err != nil {
		t.Fatalf("active language: %v", err)
	}
	if _, err := f.svc.List(f.ctx, f.actor, authoring.ListInput{LanguageCode: "absent"}); code(err) != "validation_error" {
		t.Fatalf("absent language = %v, want validation error", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE languages RENAME TO unavailable_languages`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.List(f.ctx, f.actor, authoring.ListInput{LanguageCode: "en"}); err == nil || code(err) == "validation_error" || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("storage fault = %v, want underlying query failure", err)
	}
}

func TestDraftReadsAndReferencesHandleOversizedStoredHTML(t *testing.T) {
	f := setup(t)
	human := v2.ActorForUser(f.user, []string{model.PermissionPagesWrite})
	longBody := "<p>" + strings.Repeat("x", markdown.MaxHTMLBytes) + "</p>"
	example, err := f.pages.Create(f.ctx, human, pages.CreatePageBody{Title: "Large example", Slug: "large-example", Body: longBody, Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	in := input("Draft with a large example")
	in.Draft.References = draft.Set([]draft.Reference{{Ref: fmt.Sprint(example.ID), Role: "example"}})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	assertExampleWarning := func(pageID int64) {
		t.Helper()
		detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: pageID})
		if err != nil || detail.BodyMarkdown == "" || len(detail.Validated.References) != 1 || detail.Validated.References[0].PageID != example.ID {
			t.Fatalf("ordinary draft lost its body or reference: %v", err)
		}
		for _, issue := range detail.Validated.Issues {
			if issue.Code == "html_conversion_lossy" && issue.Field == "references" {
				return
			}
		}
		t.Fatal("oversized example omitted the conversion warning")
	}
	assertExampleWarning(saved.PageID)
	updated, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{
		RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision,
		Changes: draft.Changes{BodyMarkdown: draft.Set("A new body with the inherited example.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertExampleWarning(saved.PageID)
	if _, err = f.pages.Update(f.ctx, human, saved.PageID, pages.UpdatePageBody{BaseRevision: &updated.Revision, Body: &longBody}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.BodyHTML != longBody || detail.BodyMarkdown != "" {
		t.Fatalf("oversized draft should return HTML and omit the Markdown conversion: %v", err)
	}
	for _, issue := range detail.ConversionWarnings {
		if issue.Code == "html_conversion_lossy" && issue.Field == "body_markdown" {
			return
		}
	}
	t.Fatal("oversized draft omitted the conversion warning")
}

func TestHistoricalFeaturedImageCanBeRetainedByRESTAndMCP(t *testing.T) {
	f := setup(t)
	newMedia := func(width int64) int64 {
		t.Helper()
		now := time.Now()
		media, err := f.q.CreateMedia(f.ctx, store.CreateMediaParams{
			Uuid: uuid.NewString(), Filename: "image.jpg", MimeType: model.MimeTypeJPEG, Size: 1,
			Width: sql.NullInt64{Int64: width, Valid: true}, Height: sql.NullInt64{Int64: 900, Valid: true},
			UploadedBy: f.user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	original, replacement := newMedia(1500), newMedia(100)
	in := input("Historical image draft")
	in.Draft.FeaturedImageID = draft.SetNullable(original)
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE media SET width = 100 WHERE id = ?`, original); err != nil {
		t.Fatal(err)
	}
	human := v2.ActorForUser(f.user, []string{model.PermissionPagesWrite})
	title := "REST title"
	edited, err := f.pages.Update(f.ctx, human, saved.PageID, pages.UpdatePageBody{
		BaseRevision: &saved.Revision, Title: &title, FeaturedImageID: &original,
	})
	if err != nil || edited.FeaturedImageID == nil || *edited.FeaturedImageID != original || edited.Title != title {
		t.Fatalf("REST retention failed: %+v %v", edited, err)
	}
	changed, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{
		RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: edited.Revision,
		Changes: draft.Changes{Title: draft.Set("MCP title"), FeaturedImageID: draft.SetNullable(original)},
	})
	if err != nil || changed.Revision != edited.Revision+1 {
		t.Fatalf("MCP retention failed: %+v %v", changed, err)
	}
	if _, err = f.pages.Update(f.ctx, human, saved.PageID, pages.UpdatePageBody{
		BaseRevision: &changed.Revision, FeaturedImageID: &replacement,
	}); code(err) != "validation_error" {
		t.Fatalf("new undersized image = %v, want validation refusal", err)
	}
}

func TestMetadataUpdatesPreserveRichHTMLAndClearNullableAssignments(t *testing.T) {
	f := setup(t)
	now := time.Now()
	media, err := f.q.CreateMedia(f.ctx, store.CreateMediaParams{Uuid: uuid.NewString(), Filename: "existing.jpg", MimeType: model.MimeTypeJPEG, Size: 1, Width: sql.NullInt64{Int64: 1536, Valid: true}, Height: sql.NullInt64{Int64: 1024, Valid: true}, UploadedBy: f.user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	in := input("Rich body")
	in.Draft.FeaturedImageID = draft.SetNullable(media.ID)
	in.Draft.MetaTitle = draft.SetNullable("Existing SEO title")
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	human := v2.ActorForUser(f.user, []string{model.PermissionPagesWrite})
	rich := "<div class=\"layout\"><h2>Human layout</h2><p><span class=\"special\">Preserve me.</span> <a href=\"https://example.org/review\">Review link</a></p></div>"
	p, err := f.pages.Update(f.ctx, human, saved.PageID, pages.UpdatePageBody{BaseRevision: &saved.Revision, Body: &rich})
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.q.GetPageByID(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	changes := draft.Changes{Summary: draft.Set("Metadata only"), MetaTitle: draft.Clear[string](), FeaturedImageID: draft.Clear[int64]()}
	out, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: p.ID, BaseRevision: p.Revision, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.q.GetPageByID(f.ctx, p.ID)
	if err != nil || after.Body != before.Body || after.Summary != "Metadata only" || after.MetaTitle != "" || after.FeaturedImageID.Valid || out.Revision != p.Revision+1 {
		t.Fatalf("metadata body preservation %+v %v", after, err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: p.ID})
	if err != nil || detail.BodyHTML != before.Body || detail.Verified.APIKeyID != f.actor.APIKey.ID {
		t.Fatalf("draft read/provenance %+v %v", detail, err)
	}
	if len(detail.Validated.Links) != 1 || detail.Validated.Links[0].Kind != "external" {
		t.Fatalf("metadata edit lost link review: %+v", detail.Validated.Links)
	}
	_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: p.ID, BaseRevision: p.Revision, Changes: changes})
	if code(err) != "revision_conflict" {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestSourceFingerprintsIgnoreLifecycleButRejectTextChanges(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Source for translation"))
	if err != nil {
		t.Fatal(err)
	}
	human := v2.ActorForUser(f.user, []string{model.PermissionPagesWrite})
	published := "published"
	current, err := f.pages.Update(f.ctx, human, source.PageID, pages.UpdatePageBody{BaseRevision: &source.Revision, Status: &published})
	if err != nil {
		t.Fatal(err)
	}
	in := authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: input("Перевод").Draft}
	translated, err := f.svc.Translate(f.ctx, f.actor, in)
	if err != nil || translated.TranslationStatus != "current" {
		t.Fatalf("lifecycle caused staleness: %+v %v", translated, err)
	}
	status, err := f.svc.TranslationStatus(f.ctx, f.actor, authoring.PageInput{PageID: source.PageID})
	if err != nil || len(status.Translations) != 2 {
		t.Fatalf("translation component: %+v %v", status, err)
	}
	latest, err := f.q.GetPageByID(f.ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	title := "Changed source text"
	_, err = f.pages.Update(f.ctx, human, source.PageID, pages.UpdatePageBody{BaseRevision: &latest.Revision, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: translated.PageID})
	if err != nil || detail.TranslationStatus != "stale" {
		t.Fatalf("text change not stale: %+v %v", detail, err)
	}
	// The existing translation may still be edited without claiming a refreshed source basis.
	_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: translated.PageID, BaseRevision: translated.Revision, Changes: draft.Changes{Summary: draft.Set("Edited translation")}})
	if err != nil {
		t.Fatal(err)
	}
	in.RequestID = uuid.NewString()
	if _, err = f.svc.Translate(f.ctx, f.actor, in); code(err) != "source_revision_conflict" {
		t.Fatalf("stale source accepted: %v", err)
	}
}

func TestOwnDraftMatrixAndAtomicTranslationConflicts(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Source"))
	if err != nil {
		t.Fatal(err)
	}
	in := authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: input("Translation").Draft}
	first, err := f.svc.Translate(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.q.CountPages(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	in.RequestID = uuid.NewString()
	if _, err = f.svc.Translate(f.ctx, f.actor, in); code(err) != "translation_exists" {
		t.Fatalf("duplicate language accepted: %v", err)
	}
	after, err := f.q.CountPages(f.ctx)
	if err != nil || after != before {
		t.Fatalf("orphan translation: %d -> %d %v", before, after, err)
	}
	for _, change := range []string{"UPDATE pages SET scheduled_at=CURRENT_TIMESTAMP WHERE id=?", "UPDATE pages SET status='published' WHERE id=?", "UPDATE page_ai_access SET ai_editable=0 WHERE page_id=?"} {
		if _, err = f.db.Exec("UPDATE pages SET scheduled_at=NULL,status='draft' WHERE id=?", first.PageID); err != nil {
			t.Fatal(err)
		}
		if err = f.q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: first.PageID, AiEditable: true, SetBy: f.user.ID, SetAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(change, first.PageID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: first.PageID}); code(err) != "not_found" {
			t.Fatalf("private matrix allowed %s: %v", change, err)
		}
	}
}

func TestHiddenTranslationSourcesReferencesAndSiblingsStayPrivate(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Private source"))
	if err != nil {
		t.Fatal(err)
	}
	in := input("Private translation").Draft
	in.References = draft.Set([]draft.Reference{{Ref: fmt.Sprint(source.PageID), Role: "source"}})
	target, err := f.svc.Translate(f.ctx, f.actor, authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: in})
	if err != nil {
		t.Fatal(err)
	}
	// The owner withdraws source AI access after the translation was saved.
	if _, err = f.db.Exec("UPDATE page_ai_access SET ai_editable=0 WHERE page_id=?", source.PageID); err != nil {
		t.Fatal(err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.PageID})
	if err != nil || detail.TranslationStatus != "unavailable" || detail.TranslationBasis != nil || detail.Validated.TranslationBasis != nil || len(detail.Validated.References) != 1 || detail.Validated.References[0].PageID != 0 || detail.Validated.References[0].Revision != 0 {
		t.Fatalf("hidden source leaked via provenance: %+v %v", detail, err)
	}
	status, err := f.svc.TranslationStatus(f.ctx, f.actor, authoring.PageInput{PageID: target.PageID})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range status.Translations {
		if entry.LanguageCode == "en" && (entry.Status != "unavailable" || entry.PageID != 0 || entry.Revision != 0) {
			t.Fatalf("hidden sibling leaked: %+v", entry)
		}
	}
	if err = f.q.DeleteTranslationsRelatedToEntity(f.ctx, store.DeleteTranslationsRelatedToEntityParams{
		EntityType: "page", EntityID: target.PageID, TranslationID: target.PageID,
	}); err != nil {
		t.Fatal(err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.PageID})
	if err != nil || detail.TranslationStatus != "not_tracked" || detail.TranslationBasis != nil || detail.Validated.TranslationBasis != nil {
		t.Fatalf("unlinked source leaked via provenance: %+v %v", detail, err)
	}
}

func TestExplicitReportAndReferencesAreRevisionedWithoutCopyingToSource(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Original source"))
	if err != nil {
		t.Fatal(err)
	}
	in := input("Referenced article")
	in.Draft.References = draft.Set([]draft.Reference{{Ref: fmt.Sprint(source.PageID), Role: "source"}})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	newTitle := "Changed source"
	_, err = f.pages.Update(f.ctx, v2.ActorForUser(f.user, []string{model.PermissionPagesWrite}), source.PageID, pages.UpdatePageBody{BaseRevision: &source.Revision, Title: &newTitle})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision, Changes: draft.Changes{Summary: draft.Set("Only metadata")}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || len(detail.Validated.References) != 1 || detail.Validated.References[0].Revision != source.Revision {
		t.Fatalf("reference snapshot silently replaced %+v %v", detail.Validated, err)
	}
	changes := draft.Changes{Reported: draft.Set(draft.AgentReport{Model: draft.Set("agent-reported-model")})}
	reportSave, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: updated.Revision, Changes: changes})
	if err != nil || reportSave.Revision != updated.Revision+1 {
		t.Fatalf("report-only change lost revision %+v %v", reportSave, err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Reported.Agent.Model.Value != "agent-reported-model" {
		t.Fatalf("report missing %+v %v", detail.Reported, err)
	}
	noop, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: reportSave.Revision, Changes: changes})
	if err != nil || noop.Revision != reportSave.Revision {
		t.Fatalf("identical report not a no-op %+v %v", noop, err)
	}
	// Simulate the bare report persisted by the already shipped R1.3 adapter.
	if _, err = f.db.Exec(`UPDATE content_provenance SET reported_json='{"model":"agent-reported-model"}' WHERE page_id=? AND revision=?`, saved.PageID, noop.Revision); err != nil {
		t.Fatal(err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Reported.Agent.Model.Value != "agent-reported-model" {
		t.Fatalf("legacy report projection lost: %+v %v", detail.Reported, err)
	}
	metadata, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: noop.Revision, Changes: draft.Changes{Summary: draft.Set("Keep the prior report")}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Reported.Agent.Model.Value != "agent-reported-model" {
		t.Fatalf("omitted report was lost: %+v %v", detail.Reported, err)
	}
	_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: metadata.Revision, Changes: draft.Changes{Reported: draft.Set(draft.AgentReport{})}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Reported.Agent.Model.Present {
		t.Fatalf("explicit empty report did not clear: %+v %v", detail.Reported, err)
	}
	current, err := f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	translatedInput := input("Translation with report").Draft
	translatedInput.Reported = draft.Set(draft.AgentReport{Notes: draft.Set("Target-only claim")})
	_, err = f.svc.Translate(f.ctx, f.actor, authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: current.ID, SourceRevision: current.Revision, LanguageCode: "ru", Draft: translatedInput})
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: current.ID, Revision: current.Revision})
	if err != nil || fact.ReportedJson != "{}" || fact.ValidatedJson != "{}" {
		t.Fatalf("translation claims copied to source %+v %v", fact, err)
	}
}

func TestTranslationAndEditorConcurrencyLeaveNoOrphans(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Concurrent source"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, translateErr := f.svc.Translate(f.ctx, f.actor, authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: input("Concurrent translation").Draft})
			results <- translateErr
		}()
	}
	close(start)
	success := 0
	for range 2 {
		resultErr := <-results
		if resultErr == nil {
			success++
		} else if code(resultErr) != "translation_exists" && code(resultErr) != "write_retryable" {
			t.Fatal(resultErr)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent translations=%d", success)
	}
	count, err := f.q.CountPages(f.ctx)
	if err != nil || count != 2 {
		t.Fatalf("orphan count=%d %v", count, err)
	}
	current, err := f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	results = make(chan error, 2)
	go func() {
		<-start
		_, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: current.ID, BaseRevision: current.Revision, Changes: draft.Changes{Summary: draft.Set("Agent update")}})
		results <- err
	}()
	go func() {
		<-start
		summary := "Editor update"
		_, err := f.pages.Update(f.ctx, v2.ActorForUser(f.user, []string{model.PermissionPagesWrite}), current.ID, pages.UpdatePageBody{BaseRevision: &current.Revision, Summary: &summary})
		results <- err
	}()
	close(start)
	success = 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if code(err) != "revision_conflict" && code(err) != "write_retryable" {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent edits=%d", success)
	}
}

func TestFailedAtomicSaveRetainsNoPageProvenanceOrRequest(t *testing.T) {
	f := setup(t)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_retained_request BEFORE INSERT ON idempotency_keys BEGIN SELECT RAISE(FAIL,'injected retained-result failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(f.ctx, f.actor, input("Rollback all rows")); err == nil {
		t.Fatal("failure not injected")
	}
	for _, table := range []string{"pages", "page_versions", "page_ai_access", "content_provenance", "idempotency_keys"} {
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s=%d %v", table, count, err)
		}
	}
}

func TestMutationQuotaAcrossKeysDoesNotChargeReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := setup(t)
		in := input("Quota replay")
		saved, err := f.svc.Create(f.ctx, f.actor, in)
		if err != nil {
			t.Fatal(err)
		}
		for range 59 {
			err = f.q.CreateIdempotencyKey(f.ctx, store.CreateIdempotencyKeyParams{UserID: f.user.ID, Tool: "create_draft_page", RequestID: uuid.NewString(), RequestHash: "fixture", EntityType: "page", EntityID: saved.PageID, EntityRevision: saved.Revision, CreatedAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = f.svc.Create(f.ctx, f.key(t), in); err != nil {
			t.Fatalf("replay spent quota: %v", err)
		}
		if _, err = f.svc.Create(f.ctx, f.key(t), input("Over quota")); code(err) != "mutation_rate_limited" {
			t.Fatalf("aggregate quota: %v", err)
		}
		time.Sleep(time.Hour + time.Second)
		if _, err = f.svc.Create(f.ctx, f.actor, input("New window")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReportedFieldsCannotBecomeVerifiedAndLargeReplayStaysCompact(t *testing.T) {
	f := setup(t)
	in := input("Large body")
	in.Draft.BodyMarkdown = strings.Repeat("A paragraph.\n\n", 8500)
	in.Draft.Reported = draft.Set(draft.AgentReport{Notes: draft.Set("Verified admin token should remain reported")})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Reported.Agent.Notes.Value != in.Draft.Reported.Value.Notes.Value || detail.Verified.Source != "api" {
		t.Fatalf("classification: %v", err)
	}
	var retained string
	if err = f.db.QueryRow("SELECT result_json FROM idempotency_keys WHERE request_id=?", in.RequestID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if len(retained) > 4096 {
		t.Fatalf("large retained receipt: %d", len(retained))
	}
	var wire map[string]any
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["verified"].(map[string]any)["notes"]; ok {
		t.Fatal("reported text promoted")
	}
}
