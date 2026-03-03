// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func createSortTestUser(t *testing.T, q *Queries, ctx context.Context, email, name, role string) User {
	t.Helper()
	now := time.Now()
	user, err := q.CreateUser(ctx, CreateUserParams{
		Email:        email,
		PasswordHash: "hash",
		Role:         role,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func createSortTestPage(
	t *testing.T,
	q *Queries,
	ctx context.Context,
	authorID int64,
	title string,
	slug string,
	status string,
	languageCode string,
	createdAt time.Time,
	updatedAt time.Time,
) Page {
	t.Helper()
	page, err := q.CreatePage(ctx, CreatePageParams{
		Title:             title,
		Slug:              slug,
		Body:              "<p>" + title + "</p>",
		Status:            status,
		AuthorID:          authorID,
		FeaturedImageID:   sql.NullInt64{},
		MetaTitle:         "",
		MetaDescription:   "",
		MetaKeywords:      "",
		OgImageID:         sql.NullInt64{},
		NoIndex:           0,
		NoFollow:          0,
		CanonicalUrl:      "",
		ScheduledAt:       sql.NullTime{},
		LanguageCode:      languageCode,
		HideFeaturedImage: 0,
		PageType:          "post",
		ExcludeFromLists:  0,
		PublishedAt:       sql.NullTime{},
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	})
	if err != nil {
		t.Fatalf("CreatePage(%s): %v", slug, err)
	}
	return page
}

func TestListPagesSorted_WithFilters(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	lang := getDefaultLangCode(t, q, ctx)
	user := createSortTestUser(t, q, ctx, "pages-sort@example.com", "Pages Sort", "admin")
	base := time.Now().Add(-24 * time.Hour)

	pageC := createSortTestPage(t, q, ctx, user.ID, "Gamma", "gamma", "draft", lang, base.Add(1*time.Hour), base.Add(1*time.Hour))
	pageA := createSortTestPage(t, q, ctx, user.ID, "Alpha", "alpha", "draft", lang, base.Add(2*time.Hour), base.Add(4*time.Hour))
	pageB := createSortTestPage(t, q, ctx, user.ID, "Beta", "beta", "published", lang, base.Add(3*time.Hour), base.Add(2*time.Hour))

	category, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name:         "Sorting",
		Slug:         "sorting",
		Description:  sql.NullString{},
		ParentID:     sql.NullInt64{},
		Position:     0,
		LanguageCode: lang,
		CreatedAt:    base,
		UpdatedAt:    base,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := q.AddCategoryToPage(ctx, AddCategoryToPageParams{PageID: pageA.ID, CategoryID: category.ID}); err != nil {
		t.Fatalf("AddCategoryToPage(pageA): %v", err)
	}
	if err := q.AddCategoryToPage(ctx, AddCategoryToPageParams{PageID: pageC.ID, CategoryID: category.ID}); err != nil {
		t.Fatalf("AddCategoryToPage(pageC): %v", err)
	}

	byTitle, err := q.ListPagesSorted(ctx, ListPagesSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "title",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("ListPagesSorted(title asc): %v", err)
	}
	if len(byTitle) < 3 || byTitle[0].ID != pageA.ID || byTitle[1].ID != pageB.ID || byTitle[2].ID != pageC.ID {
		t.Fatalf("unexpected title order: got ids [%d,%d,%d], want [%d,%d,%d]", byTitle[0].ID, byTitle[1].ID, byTitle[2].ID, pageA.ID, pageB.ID, pageC.ID)
	}

	filteredSearch, err := q.ListPagesSorted(ctx, ListPagesSortedParams{
		SearchPattern: sql.NullString{String: "%a%", Valid: true},
		Limit:         10,
		Offset:        0,
		SortField:     "updated_at",
		SortDir:       "desc",
	})
	if err != nil {
		t.Fatalf("ListPagesSorted(search): %v", err)
	}
	if len(filteredSearch) < 3 || filteredSearch[0].ID != pageA.ID {
		t.Fatalf("unexpected search order, first id=%d want %d", filteredSearch[0].ID, pageA.ID)
	}

	filteredCategory, err := q.ListPagesSorted(ctx, ListPagesSortedParams{
		CategoryID: sql.NullInt64{Int64: category.ID, Valid: true},
		Limit:      10,
		Offset:     0,
		SortField:  "created_at",
		SortDir:    "desc",
	})
	if err != nil {
		t.Fatalf("ListPagesSorted(category): %v", err)
	}
	if len(filteredCategory) != 2 {
		t.Fatalf("filtered category count=%d, want 2", len(filteredCategory))
	}
	if filteredCategory[0].ID != pageA.ID || filteredCategory[1].ID != pageC.ID {
		t.Fatalf("unexpected category order: [%d,%d], want [%d,%d]", filteredCategory[0].ID, filteredCategory[1].ID, pageA.ID, pageC.ID)
	}
}

func TestGetTagUsageCountsSorted(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	lang := getDefaultLangCode(t, q, ctx)
	user := createSortTestUser(t, q, ctx, "tags-sort@example.com", "Tags Sort", "admin")
	base := time.Now().Add(-12 * time.Hour)

	page1 := createSortTestPage(t, q, ctx, user.ID, "Page 1", "tags-page-1", "published", lang, base, base)
	page2 := createSortTestPage(t, q, ctx, user.ID, "Page 2", "tags-page-2", "published", lang, base.Add(time.Hour), base.Add(time.Hour))

	tagA, err := q.CreateTag(ctx, CreateTagParams{Name: "Alpha", Slug: "alpha-tag", LanguageCode: lang, CreatedAt: base, UpdatedAt: base})
	if err != nil {
		t.Fatalf("CreateTag Alpha: %v", err)
	}
	tagB, err := q.CreateTag(ctx, CreateTagParams{Name: "Beta", Slug: "beta-tag", LanguageCode: lang, CreatedAt: base.Add(time.Hour), UpdatedAt: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateTag Beta: %v", err)
	}

	if err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page1.ID, TagID: tagA.ID}); err != nil {
		t.Fatalf("AddTagToPage A1: %v", err)
	}
	if err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page2.ID, TagID: tagA.ID}); err != nil {
		t.Fatalf("AddTagToPage A2: %v", err)
	}
	if err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page1.ID, TagID: tagB.ID}); err != nil {
		t.Fatalf("AddTagToPage B1: %v", err)
	}

	byUsage, err := q.GetTagUsageCountsSorted(ctx, GetTagUsageCountsSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "usage_count",
		SortDir:   "desc",
	})
	if err != nil {
		t.Fatalf("GetTagUsageCountsSorted(usage): %v", err)
	}
	if len(byUsage) < 2 || byUsage[0].ID != tagA.ID {
		t.Fatalf("unexpected usage order, first id=%d want %d", byUsage[0].ID, tagA.ID)
	}

	byName, err := q.GetTagUsageCountsSorted(ctx, GetTagUsageCountsSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "name",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("GetTagUsageCountsSorted(name): %v", err)
	}
	if len(byName) < 2 || byName[0].ID != tagA.ID || byName[1].ID != tagB.ID {
		t.Fatalf("unexpected name order: [%d,%d], want [%d,%d]", byName[0].ID, byName[1].ID, tagA.ID, tagB.ID)
	}
}

func TestListUsersSorted_NullsLast(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	alice := createSortTestUser(t, q, ctx, "alice@example.com", "Alice", "admin")
	bob := createSortTestUser(t, q, ctx, "bob@example.com", "Bob", "editor")
	_ = createSortTestUser(t, q, ctx, "charlie@example.com", "Charlie", "editor")

	if err := q.UpdateUserLastLogin(ctx, UpdateUserLastLoginParams{
		LastLoginAt: sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		ID:          bob.ID,
	}); err != nil {
		t.Fatalf("UpdateUserLastLogin(bob): %v", err)
	}
	if err := q.UpdateUserLastLogin(ctx, UpdateUserLastLoginParams{
		LastLoginAt: sql.NullTime{Time: time.Now().Add(-2 * time.Hour), Valid: true},
		ID:          alice.ID,
	}); err != nil {
		t.Fatalf("UpdateUserLastLogin(alice): %v", err)
	}

	byLastLogin, err := q.ListUsersSorted(ctx, ListUsersSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "last_login_at",
		SortDir:   "desc",
	})
	if err != nil {
		t.Fatalf("ListUsersSorted(last_login_at): %v", err)
	}
	if len(byLastLogin) < 3 {
		t.Fatalf("users len=%d, want >=3", len(byLastLogin))
	}
	if !byLastLogin[2].LastLoginAt.Valid {
		// null-last expectation
	} else {
		t.Fatalf("expected null last_login_at at the end, got valid at index 2")
	}

	byName, err := q.ListUsersSorted(ctx, ListUsersSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "name",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("ListUsersSorted(name): %v", err)
	}
	if byName[0].Name != "Alice" {
		t.Fatalf("first name=%q, want Alice", byName[0].Name)
	}
}

func TestListAPIKeysSorted_NullsLast(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createSortTestUser(t, q, ctx, "keys-sort@example.com", "Keys Sort", "admin")
	now := time.Now()

	keyA, err := q.CreateAPIKey(ctx, CreateAPIKeyParams{
		Name:        "Alpha",
		KeyHash:     "hash-a",
		KeyPrefix:   "a_",
		Permissions: "[]",
		ExpiresAt:   sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true},
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now.Add(-3 * time.Hour),
		UpdatedAt:   now.Add(-3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateAPIKey Alpha: %v", err)
	}
	keyB, err := q.CreateAPIKey(ctx, CreateAPIKeyParams{
		Name:        "Beta",
		KeyHash:     "hash-b",
		KeyPrefix:   "b_",
		Permissions: "[]",
		ExpiresAt:   sql.NullTime{},
		IsActive:    true,
		CreatedBy:   user.ID,
		CreatedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:   now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateAPIKey Beta: %v", err)
	}

	if err := q.UpdateAPIKeyLastUsed(ctx, UpdateAPIKeyLastUsedParams{
		LastUsedAt: sql.NullTime{Time: now.Add(-10 * time.Minute), Valid: true},
		ID:         keyA.ID,
	}); err != nil {
		t.Fatalf("UpdateAPIKeyLastUsed: %v", err)
	}

	byLastUsed, err := q.ListAPIKeysSorted(ctx, ListAPIKeysSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "last_used_at",
		SortDir:   "desc",
	})
	if err != nil {
		t.Fatalf("ListAPIKeysSorted(last_used_at): %v", err)
	}
	if len(byLastUsed) < 2 || byLastUsed[0].ID != keyA.ID {
		t.Fatalf("unexpected last_used_at order, first id=%d want %d", byLastUsed[0].ID, keyA.ID)
	}

	byExpires, err := q.ListAPIKeysSorted(ctx, ListAPIKeysSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "expires_at",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("ListAPIKeysSorted(expires_at): %v", err)
	}
	if len(byExpires) < 2 || byExpires[0].ID != keyA.ID || byExpires[1].ID != keyB.ID {
		t.Fatalf("unexpected expires_at order: [%d,%d], want [%d,%d]", byExpires[0].ID, byExpires[1].ID, keyA.ID, keyB.ID)
	}
}

func TestListMediaSorted_WithFilters(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	lang := getDefaultLangCode(t, q, ctx)
	user := createSortTestUser(t, q, ctx, "media-sort@example.com", "Media Sort", "admin")
	base := time.Now().Add(-6 * time.Hour)

	folder, err := q.CreateMediaFolder(ctx, CreateMediaFolderParams{
		Name:      "Docs",
		ParentID:  sql.NullInt64{},
		Position:  0,
		CreatedAt: base,
	})
	if err != nil {
		t.Fatalf("CreateMediaFolder: %v", err)
	}

	_, err = q.CreateMedia(ctx, CreateMediaParams{
		Uuid:         "media-sort-a",
		Filename:     "zeta.png",
		MimeType:     "image/png",
		Size:         100,
		Width:        sql.NullInt64{},
		Height:       sql.NullInt64{},
		Alt:          sql.NullString{String: "zeta", Valid: true},
		Caption:      sql.NullString{},
		FolderID:     sql.NullInt64{},
		UploadedBy:   user.ID,
		LanguageCode: lang,
		CreatedAt:    base,
		UpdatedAt:    base,
	})
	if err != nil {
		t.Fatalf("CreateMedia zeta: %v", err)
	}
	_, err = q.CreateMedia(ctx, CreateMediaParams{
		Uuid:         "media-sort-b",
		Filename:     "alpha.pdf",
		MimeType:     "application/pdf",
		Size:         300,
		Width:        sql.NullInt64{},
		Height:       sql.NullInt64{},
		Alt:          sql.NullString{String: "doc alpha", Valid: true},
		Caption:      sql.NullString{},
		FolderID:     sql.NullInt64{Int64: folder.ID, Valid: true},
		UploadedBy:   user.ID,
		LanguageCode: lang,
		CreatedAt:    base.Add(time.Hour),
		UpdatedAt:    base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateMedia alpha: %v", err)
	}

	byFilename, err := q.ListMediaSorted(ctx, ListMediaSortedParams{
		Limit:     10,
		Offset:    0,
		SortField: "filename",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("ListMediaSorted(filename): %v", err)
	}
	if len(byFilename) < 2 || byFilename[0].Filename != "alpha.pdf" {
		t.Fatalf("unexpected filename order, first=%q want alpha.pdf", byFilename[0].Filename)
	}

	filteredType, err := q.ListMediaSorted(ctx, ListMediaSortedParams{
		MimeType:  sql.NullString{String: "image/%", Valid: true},
		Limit:     10,
		Offset:    0,
		SortField: "size",
		SortDir:   "desc",
	})
	if err != nil {
		t.Fatalf("ListMediaSorted(type): %v", err)
	}
	if len(filteredType) != 1 || filteredType[0].MimeType != "image/png" {
		t.Fatalf("unexpected type filter result: len=%d mime=%q", len(filteredType), filteredType[0].MimeType)
	}

	filteredSearch, err := q.ListMediaSorted(ctx, ListMediaSortedParams{
		SearchPattern: sql.NullString{String: "%doc%", Valid: true},
		Limit:         10,
		Offset:        0,
		SortField:     "created_at",
		SortDir:       "desc",
	})
	if err != nil {
		t.Fatalf("ListMediaSorted(search): %v", err)
	}
	if len(filteredSearch) != 1 || filteredSearch[0].Filename != "alpha.pdf" {
		t.Fatalf("unexpected search result: len=%d first=%q", len(filteredSearch), filteredSearch[0].Filename)
	}
}

func TestGetFormSubmissionsSorted(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	lang := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	form, err := q.CreateForm(ctx, CreateFormParams{
		Name:         "Sort Form",
		Slug:         "sort-form",
		Title:        "Sort Form",
		IsActive:     true,
		LanguageCode: lang,
		CreatedAt:    now.Add(-2 * time.Hour),
		UpdatedAt:    now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	_, err = q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
		FormID:       form.ID,
		Data:         `{"name":"A"}`,
		IpAddress:    sql.NullString{String: "", Valid: false},
		UserAgent:    sql.NullString{},
		IsRead:       false,
		LanguageCode: lang,
		CreatedAt:    now.Add(-90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission #1: %v", err)
	}
	sub2, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
		FormID:       form.ID,
		Data:         `{"name":"B"}`,
		IpAddress:    sql.NullString{String: "10.0.0.2", Valid: true},
		UserAgent:    sql.NullString{},
		IsRead:       true,
		LanguageCode: lang,
		CreatedAt:    now.Add(-60 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission #2: %v", err)
	}
	sub3, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
		FormID:       form.ID,
		Data:         `{"name":"C"}`,
		IpAddress:    sql.NullString{String: "10.0.0.1", Valid: true},
		UserAgent:    sql.NullString{},
		IsRead:       false,
		LanguageCode: lang,
		CreatedAt:    now.Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission #3: %v", err)
	}

	byCreated, err := q.GetFormSubmissionsSorted(ctx, GetFormSubmissionsSortedParams{
		FormID:    form.ID,
		Limit:     10,
		Offset:    0,
		SortField: "created_at",
		SortDir:   "desc",
	})
	if err != nil {
		t.Fatalf("GetFormSubmissionsSorted(created_at): %v", err)
	}
	if len(byCreated) != 3 || byCreated[0].ID != sub3.ID {
		t.Fatalf("unexpected created_at order, first=%d want %d", byCreated[0].ID, sub3.ID)
	}

	byIP, err := q.GetFormSubmissionsSorted(ctx, GetFormSubmissionsSortedParams{
		FormID:    form.ID,
		Limit:     10,
		Offset:    0,
		SortField: "ip_address",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("GetFormSubmissionsSorted(ip): %v", err)
	}
	if len(byIP) != 3 || byIP[0].ID != sub3.ID || byIP[1].ID != sub2.ID {
		t.Fatalf("unexpected ip order: [%d,%d,%d]", byIP[0].ID, byIP[1].ID, byIP[2].ID)
	}

	byRead, err := q.GetFormSubmissionsSorted(ctx, GetFormSubmissionsSortedParams{
		FormID:    form.ID,
		Limit:     10,
		Offset:    0,
		SortField: "is_read",
		SortDir:   "asc",
	})
	if err != nil {
		t.Fatalf("GetFormSubmissionsSorted(is_read): %v", err)
	}
	if len(byRead) != 3 || byRead[0].IsRead {
		t.Fatalf("expected unread first when sorting is_read asc")
	}
}
