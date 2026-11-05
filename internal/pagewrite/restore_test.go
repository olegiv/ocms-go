// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// Every restored foreign key needs a case here. The schema supplies the
// inventory so adding an assignment cannot silently bypass restore validation.
func TestRestoreRefusesMissingSnapshotReferences(t *testing.T) {
	type deletion struct {
		kind, statement string
		id              any
	}
	cases := map[string]deletion{
		"page_tags.tag_id -> tags":                  {"tag", `DELETE FROM tags WHERE id = ?`, 10},
		"page_categories.category_id -> categories": {"category", `DELETE FROM categories WHERE id = ?`, 10},
		"pages.featured_image_id -> media":          {"featured image", `DELETE FROM media WHERE id = ?`, 10},
		"pages.og_image_id -> media":                {"Open Graph image", `DELETE FROM media WHERE id = ?`, 11},
	}
	f := setup(t)
	rows, err := f.db.Query(`SELECT m.name || '.' || f."from" || ' -> ' || f."table"
		FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.name IN ('pages', 'page_tags', 'page_categories') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var covered []deletion
	for rows.Next() {
		var reference string
		if err = rows.Scan(&reference); err != nil {
			t.Fatal(err)
		}
		switch reference {
		case "pages.author_id -> users", "page_tags.page_id -> pages", "page_categories.page_id -> pages":
			// Restore keeps the current author and page identity.
			continue
		}
		if tc, ok := cases[reference]; ok {
			covered = append(covered, tc)
		} else {
			t.Errorf("no restore coverage for %s", reference)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(covered) != len(cases) {
		t.Fatalf("restore fixtures cover %d of %d expected foreign keys", len(covered), len(cases))
	}
	// language_code is a validated reference without a declared foreign key.
	covered = append(covered, deletion{"language", `DELETE FROM languages WHERE code = ?`, "ru"})
	for _, tc := range covered {
		t.Run(tc.kind, func(t *testing.T) {
			candidate := setup(t)
			version := snapshotWithReferences(t, candidate)
			if _, deleteErr := candidate.db.ExecContext(t.Context(), tc.statement, tc.id); deleteErr != nil {
				t.Fatal(deleteErr)
			}
			before, loadErr := candidate.q.GetPageSnapshot(t.Context(), candidate.page.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			_, restoreErr := pagewrite.InTx(t.Context(), candidate.db, provenance.Principal{UserID: candidate.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
				current, trackErr := work.Track(t.Context(), candidate.page.ID, &candidate.page.Revision, true)
				if trackErr != nil {
					return trackErr
				}
				refusalErr := work.Restore(t.Context(), current, version, version.Body)
				if after, snapshotErr := work.Q.GetPageSnapshot(t.Context(), current.ID); snapshotErr != nil || before != after {
					t.Errorf("restore mutated the page or assignments before refusing: %v", snapshotErr)
				}
				return refusalErr
			})
			var refusal *pagewrite.Refusal
			if !errors.As(restoreErr, &refusal) || !strings.Contains(refusal.Reason, fmt.Sprintf("%s %v", tc.kind, tc.id)) {
				t.Errorf("restore returned %v, want a refusal identifying the missing %s %v", restoreErr, tc.kind, tc.id)
			}
			if after, snapshotErr := candidate.q.GetPageSnapshot(t.Context(), candidate.page.ID); snapshotErr != nil || before != after {
				t.Errorf("refused restore changed the stored page or assignments: %v", snapshotErr)
			}
			if versions, countErr := candidate.q.CountPageVersions(t.Context(), candidate.page.ID); countErr != nil || versions != 1 {
				t.Errorf("refused restore left %d versions, %v", versions, countErr)
			}
		})
	}
}

func snapshotWithReferences(t *testing.T, f fixture) store.PageVersion {
	t.Helper()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := f.db.ExecContext(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO languages (code, name, native_name, is_active) VALUES ('ru','Russian','Русский',1)`)
	exec(`INSERT INTO tags (id, name, slug, language_code) VALUES (10,'Saved','saved','ru'), (11,'Current','current','en')`)
	exec(`INSERT INTO categories (id, name, slug, language_code) VALUES (10,'Saved','saved','ru')`)
	exec(`INSERT INTO media (id, uuid, filename, mime_type, size, uploaded_by, language_code)
		VALUES (10,'featured','featured.png','image/png',1,?,'ru'), (11,'og','og.png','image/png',1,?,'ru')`, f.user.ID, f.user.ID)
	exec(`UPDATE pages SET featured_image_id = 10, og_image_id = 11, language_code = 'ru' WHERE id = ?`, f.page.ID)
	exec(`INSERT INTO page_tags (page_id, tag_id) VALUES (?, 10)`, f.page.ID)
	exec(`INSERT INTO page_categories (page_id, category_id) VALUES (?, 10)`, f.page.ID)
	snapshot, err := f.q.GetPageSnapshot(t.Context(), f.page.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE pages SET body = 'Current body', language_code = 'en' WHERE id = ?`, f.page.ID)
	exec(`UPDATE page_tags SET tag_id = 11 WHERE page_id = ?`, f.page.ID)
	return store.PageVersion{PageID: f.page.ID, Title: f.page.Title, Body: f.page.Body, SnapshotJson: sql.NullString{String: snapshot, Valid: true}}
}

func TestRestoreCompleteAndLegacyVersionsPreservesCurrentPolicy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	principal := provenance.Principal{UserID: f.user.ID, Source: "human"}
	now := time.Now().UTC().Truncate(time.Second)
	tag, err := f.q.CreateTag(ctx, store.CreateTagParams{Name: "Recorded tag", Slug: "recorded-tag", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	work, err := pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
		if _, trackErr := work.Track(ctx, f.page.ID, &f.page.Revision, true); trackErr != nil {
			return trackErr
		}
		if tagErr := work.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: f.page.ID, TagID: tag.ID}); tagErr != nil {
			return tagErr
		}
		_, aliasErr := work.Q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: f.page.ID, Alias: "recorded-alias", CreatedAt: now})
		return aliasErr
	})
	if err != nil {
		t.Fatal(err)
	}
	page := work.Pages[f.page.ID]
	recorded, err := f.q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: 2, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := f.q.CreateUser(ctx, store.CreateUserParams{Email: "new-owner@example.test", PasswordHash: "unused", Role: "editor", Name: "New Owner", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a prior ownership reassignment. Restore must never copy the owner
	// in the historical snapshot over this current account.
	if _, err = f.db.Exec(`UPDATE pages SET author_id = ? WHERE id = ?`, owner.ID, page.ID); err != nil {
		t.Fatal(err)
	}
	work, err = pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
		current, workErr := work.Track(ctx, page.ID, &page.Revision, true)
		if workErr != nil {
			return workErr
		}
		params := pagewrite.ParamsFor(current)
		params.Title, params.Summary, params.Slug = "Current title", "Current summary", "current-slug"
		params.Status = "published"
		params.PublishedAt = sql.NullTime{Time: now, Valid: true}
		params.ScheduledAt = sql.NullTime{Time: now.Add(time.Hour), Valid: true}
		if _, workErr = work.Q.UpdatePage(ctx, params); workErr != nil {
			return workErr
		}
		if workErr = work.Q.ClearPageTags(ctx, page.ID); workErr != nil {
			return workErr
		}
		if workErr = work.Q.ClearPageAliases(ctx, page.ID); workErr != nil {
			return workErr
		}
		return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: f.user.ID, SetAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	page = work.Pages[page.ID]
	if _, err = f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_, err = pagewrite.Insert(ctx, f.q, principal, store.CreatePageParams{Title: "Sibling", Slug: "sibling", Status: "draft", AuthorID: owner.ID, LanguageCode: "ru", PageType: "post", CreatedAt: now, UpdatedAt: now}, func(work *pagewrite.Tx, sibling store.Page) error {
		return work.LinkTranslation(ctx, page.ID, "ru", sibling.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err = f.q.GetPageByID(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	restore := func(base int64, version store.PageVersion) (*pagewrite.Tx, error) {
		return pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
			current, trackErr := work.Track(ctx, page.ID, &base, true)
			if trackErr != nil {
				return trackErr
			}
			return work.Restore(ctx, current, version, version.Body)
		})
	}
	work, err = restore(page.Revision, recorded)
	if err != nil {
		t.Fatal(err)
	}
	restored := work.Pages[page.ID]
	if restored.Title != f.page.Title || restored.Summary != f.page.Summary || restored.Slug != f.page.Slug || restored.AuthorID != owner.ID || restored.Status != page.Status || restored.PublishedAt != page.PublishedAt || restored.ScheduledAt != page.ScheduledAt {
		t.Fatalf("restore lost content or current policy: %+v", restored)
	}
	access, err := f.q.GetPageAIAccess(ctx, page.ID)
	if err != nil || !access.AiEditable {
		t.Fatalf("restore removed current AI access: %+v %v", access, err)
	}
	members, err := f.q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: page.ID})
	if err != nil || len(members) != 1 {
		t.Fatalf("restore changed current translation membership: %+v %v", members, err)
	}
	tags, err := f.q.GetTagsForPage(ctx, page.ID)
	if err != nil || len(tags) != 1 || tags[0].ID != tag.ID {
		t.Fatalf("recorded taxonomy not restored: %+v %v", tags, err)
	}
	aliases, err := f.q.GetAliasesForPage(ctx, page.ID)
	if err != nil || len(aliases) != 1 || aliases[0].Alias != "recorded-alias" {
		t.Fatalf("recorded aliases not restored: %+v %v", aliases, err)
	}
	_, err = restore(page.Revision, recorded)
	var conflict *pagewrite.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("stale restore succeeded: %v", err)
	}
	legacy := store.PageVersion{PageID: page.ID, Title: "Legacy title", Body: "Legacy body", Legacy: true}
	work, err = restore(restored.Revision, legacy)
	if err != nil {
		t.Fatal(err)
	}
	latest := work.Pages[page.ID]
	if latest.Title != legacy.Title || latest.Body != legacy.Body || latest.Summary != restored.Summary || latest.Slug != restored.Slug || latest.AuthorID != owner.ID || latest.Status != restored.Status {
		t.Fatalf("legacy restore changed unrecorded fields: %+v", latest)
	}
	testutil.AssertPageRevision(t, f.q, page.ID, latest.Revision)
}
