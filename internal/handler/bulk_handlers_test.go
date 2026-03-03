// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestPagesHandler_BulkDelete_Success(t *testing.T) {
	db, sm := testHandlerSetup(t)
	q := store.New(db)
	user := createTestAdminUser(t, db)

	now := time.Now()
	page1, err := q.CreatePage(context.Background(), store.CreatePageParams{
		Title:     "Page 1",
		Slug:      "page-1",
		Body:      "one",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage(1) failed: %v", err)
	}
	page2, err := q.CreatePage(context.Background(), store.CreatePageParams{
		Title:     "Page 2",
		Slug:      "page-2",
		Body:      "two",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage(2) failed: %v", err)
	}

	h := NewPagesHandler(db, nil, sm)
	req := httptest.NewRequest(http.MethodPost, "/admin/pages/bulk-delete",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, page1.ID, page2.ID)))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithSession(sm, req)
	req = addUserToContext(req, &user)
	w := httptest.NewRecorder()

	h.BulkDelete(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 2 {
		t.Fatalf("deleted = %v, want 2", resp["deleted"])
	}
	if failed, ok := resp["failed"].([]any); !ok || len(failed) != 0 {
		t.Fatalf("failed = %v, want empty", resp["failed"])
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages").Scan(&count); err != nil {
		t.Fatalf("count pages failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("pages count = %d, want 0", count)
	}
}

func TestTaxonomyHandler_BulkDeleteTags_Success(t *testing.T) {
	db, sm := testHandlerSetup(t)
	q := store.New(db)
	now := time.Now()

	tag1, err := q.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Tag A", Slug: "tag-a", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag(1) failed: %v", err)
	}
	tag2, err := q.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Tag B", Slug: "tag-b", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag(2) failed: %v", err)
	}

	h := NewTaxonomyHandler(db, nil, sm)
	req := httptest.NewRequest(http.MethodPost, "/admin/tags/bulk-delete",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, tag1.ID, tag2.ID)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BulkDeleteTags(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 2 {
		t.Fatalf("deleted = %v, want 2", resp["deleted"])
	}
	if failed, ok := resp["failed"].([]any); !ok || len(failed) != 0 {
		t.Fatalf("failed = %v, want empty", resp["failed"])
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&count); err != nil {
		t.Fatalf("count tags failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("tags count = %d, want 0", count)
	}
}

func TestUsersHandler_BulkDelete_Partial(t *testing.T) {
	db, sm := testHandlerSetup(t)

	lastAdmin := createTestUser(t, db, testUser{
		Email: "last-admin@example.com",
		Name:  "Last Admin",
		Role:  "admin",
	})
	editorToDelete := createTestUser(t, db, testUser{
		Email: "delete-me@example.com",
		Name:  "Delete Me",
		Role:  "editor",
	})
	actingEditor := createTestUser(t, db, testUser{
		Email: "acting-editor@example.com",
		Name:  "Acting Editor",
		Role:  "editor",
	})

	h := NewUsersHandler(db, nil, sm)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/bulk-delete",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d]}`, actingEditor.ID, lastAdmin.ID, editorToDelete.ID)))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithSession(sm, req)
	req = addUserToContext(req, &actingEditor)
	w := httptest.NewRecorder()

	h.BulkDelete(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 1 {
		t.Fatalf("deleted = %v, want 1", resp["deleted"])
	}
	failed, ok := resp["failed"].([]any)
	if !ok || len(failed) != 2 {
		t.Fatalf("failed = %v, want 2 entries", resp["failed"])
	}

	failedIDs := make([]int64, 0, len(failed))
	for _, entry := range failed {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("failed entry type = %T, want map", entry)
		}
		idValue, ok := item["id"].(float64)
		if !ok {
			t.Fatalf("failed entry id = %v", item["id"])
		}
		failedIDs = append(failedIDs, int64(idValue))
	}
	slices.Sort(failedIDs)
	expectedFailed := []int64{actingEditor.ID, lastAdmin.ID}
	slices.Sort(expectedFailed)
	if !slices.Equal(failedIDs, expectedFailed) {
		t.Fatalf("failed ids = %v, want %v", failedIDs, expectedFailed)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", editorToDelete.ID).Scan(&count); err != nil {
		t.Fatalf("check deleted editor failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("editor should be deleted, count = %d", count)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", lastAdmin.ID).Scan(&count); err != nil {
		t.Fatalf("check admin failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("last admin should remain, count = %d", count)
	}
}

func TestAPIKeysHandler_BulkDelete_Success(t *testing.T) {
	db, sm := testHandlerSetup(t)
	q := store.New(db)
	user := createTestAdminUser(t, db)
	now := time.Now()

	key1, err := q.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name: "Key 1", KeyHash: "hash-1", KeyPrefix: "k1_", Permissions: `["pages:read"]`,
		IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey(1) failed: %v", err)
	}
	key2, err := q.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
		Name: "Key 2", KeyHash: "hash-2", KeyPrefix: "k2_", Permissions: `["pages:read"]`,
		IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey(2) failed: %v", err)
	}

	h := NewAPIKeysHandler(db, nil, sm)
	req := httptest.NewRequest(http.MethodPost, "/admin/api-keys/bulk-delete",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, key1.ID, key2.ID)))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithSession(sm, req)
	req = addUserToContext(req, &user)
	w := httptest.NewRecorder()

	h.BulkDelete(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 2 {
		t.Fatalf("deleted = %v, want 2", resp["deleted"])
	}
	if failed, ok := resp["failed"].([]any); !ok || len(failed) != 0 {
		t.Fatalf("failed = %v, want empty", resp["failed"])
	}

	var activeCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE is_active = 1").Scan(&activeCount); err != nil {
		t.Fatalf("count active api keys failed: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("active api key count = %d, want 0", activeCount)
	}
}

func TestMediaHandler_BulkDelete_Success(t *testing.T) {
	db, sm := testHandlerSetup(t)
	q := store.New(db)
	user := createTestAdminUser(t, db)
	now := time.Now()

	media1, err := q.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid: "bulk-media-1", Filename: "one.jpg", MimeType: "image/jpeg", Size: 100,
		UploadedBy: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMedia(1) failed: %v", err)
	}
	media2, err := q.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid: "bulk-media-2", Filename: "two.jpg", MimeType: "image/jpeg", Size: 100,
		UploadedBy: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMedia(2) failed: %v", err)
	}

	h := NewMediaHandler(db, nil, sm, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/admin/media/bulk-delete",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, media1.ID, media2.ID)))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithSession(sm, req)
	req = addUserToContext(req, &user)
	w := httptest.NewRecorder()

	h.BulkDelete(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 2 {
		t.Fatalf("deleted = %v, want 2", resp["deleted"])
	}
	if failed, ok := resp["failed"].([]any); !ok || len(failed) != 0 {
		t.Fatalf("failed = %v, want empty", resp["failed"])
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM media").Scan(&count); err != nil {
		t.Fatalf("count media failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("media count = %d, want 0", count)
	}
}

func TestFormsHandler_BulkDeleteSubmissions_CrossForm(t *testing.T) {
	db, sm := testHandlerSetup(t)
	q := store.New(db)
	now := time.Now()

	form1, err := q.CreateForm(context.Background(), store.CreateFormParams{
		Name: "Contact", Slug: "contact", Title: "Contact", IsActive: true, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm(1) failed: %v", err)
	}
	form2, err := q.CreateForm(context.Background(), store.CreateFormParams{
		Name: "Support", Slug: "support", Title: "Support", IsActive: true, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm(2) failed: %v", err)
	}

	sub1, err := q.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
		FormID: form1.ID, Data: `{"email":"a@example.com"}`, IsRead: false, LanguageCode: "en", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission(1) failed: %v", err)
	}
	sub2, err := q.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
		FormID: form2.ID, Data: `{"email":"b@example.com"}`, IsRead: false, LanguageCode: "en", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission(2) failed: %v", err)
	}

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/forms/%d/submissions/bulk-delete", form1.ID),
		strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, sub1.ID, sub2.ID)))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", form1.ID)})
	w := httptest.NewRecorder()

	h.BulkDeleteSubmissions(w, req)

	resp := assertJSONResponse(t, w, http.StatusOK, true)
	if deleted, ok := resp["deleted"].(float64); !ok || int(deleted) != 1 {
		t.Fatalf("deleted = %v, want 1", resp["deleted"])
	}
	if failed, ok := resp["failed"].([]any); !ok || len(failed) != 1 {
		t.Fatalf("failed = %v, want 1 entry", resp["failed"])
	}

	if _, err := q.GetFormSubmissionByID(context.Background(), sub1.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("submission from form1 should be deleted, err = %v", err)
	}
	if _, err := q.GetFormSubmissionByID(context.Background(), sub2.ID); err != nil {
		t.Fatalf("submission from form2 should remain, err = %v", err)
	}
}
