// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

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
		if _, err := work.Track(ctx, f.page.ID, &f.page.Revision, true); err != nil {
			return err
		}
		if err := work.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: f.page.ID, TagID: tag.ID}); err != nil {
			return err
		}
		_, err := work.Q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: f.page.ID, Alias: "recorded-alias", CreatedAt: now})
		return err
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
		current, err := work.Track(ctx, page.ID, &page.Revision, true)
		if err != nil {
			return err
		}
		params := pagewrite.ParamsFor(current)
		params.Title, params.Summary, params.Slug = "Current title", "Current summary", "current-slug"
		params.Status = "published"
		params.PublishedAt = sql.NullTime{Time: now, Valid: true}
		params.ScheduledAt = sql.NullTime{Time: now.Add(time.Hour), Valid: true}
		if _, err = work.Q.UpdatePage(ctx, params); err != nil {
			return err
		}
		if err = work.Q.ClearPageTags(ctx, page.ID); err != nil {
			return err
		}
		if err = work.Q.ClearPageAliases(ctx, page.ID); err != nil {
			return err
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
			current, err := work.Track(ctx, page.ID, &base, true)
			if err != nil {
				return err
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
