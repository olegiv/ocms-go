// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewFormsHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewFormsHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewFormsHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestFormCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:           "Contact Form",
		Slug:           "contact-form",
		Title:          "Contact Us",
		Description:    sql.NullString{String: "A contact form", Valid: true},
		SuccessMessage: sql.NullString{String: "Thank you!", Valid: true},
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	if form.Name != "Contact Form" {
		t.Errorf("Name = %q, want %q", form.Name, "Contact Form")
	}
	if form.Slug != "contact-form" {
		t.Errorf("Slug = %q, want %q", form.Slug, "contact-form")
	}
	if !form.IsActive {
		t.Error("IsActive should be true")
	}
}

func TestFormList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create test forms
	for i := 1; i <= 3; i++ {
		_, err := queries.CreateForm(context.Background(), store.CreateFormParams{
			Name:      "Form " + string(rune('A'+i-1)),
			Slug:      "form-" + string(rune('a'+i-1)),
			Title:     "Form " + string(rune('A'+i-1)),
			IsActive:  true,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateForm failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		forms, err := queries.ListForms(context.Background(), store.ListFormsParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListForms failed: %v", err)
		}
		if len(forms) != 3 {
			t.Errorf("got %d forms, want 3", len(forms))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountForms(context.Background())
		if err != nil {
			t.Fatalf("CountForms failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})
}

func TestFormGetBySlug(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	_, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Slug Test Form",
		Slug:      "slug-test-form",
		Title:     "Slug Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	form, err := queries.GetFormBySlug(context.Background(), "slug-test-form")
	if err != nil {
		t.Fatalf("GetFormBySlug failed: %v", err)
	}

	if form.Slug != "slug-test-form" {
		t.Errorf("Slug = %q, want %q", form.Slug, "slug-test-form")
	}
}

func TestFormUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Original Form",
		Slug:      "original-form",
		Title:     "Original",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	_, err = queries.UpdateForm(context.Background(), store.UpdateFormParams{
		ID:             form.ID,
		Name:           "Updated Form",
		Slug:           "updated-form",
		Title:          "Updated",
		SuccessMessage: sql.NullString{String: "Updated message", Valid: true},
		IsActive:       false,
		UpdatedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateForm failed: %v", err)
	}

	updated, err := queries.GetFormByID(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("GetFormByID failed: %v", err)
	}

	if updated.Name != "Updated Form" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Form")
	}
	if updated.IsActive {
		t.Error("IsActive should be false")
	}
}

func TestFormDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "To Delete Form",
		Slug:      "to-delete-form",
		Title:     "Delete",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	if err := queries.DeleteForm(context.Background(), form.ID); err != nil {
		t.Fatalf("DeleteForm failed: %v", err)
	}

	_, err = queries.GetFormByID(context.Background(), form.ID)
	if err == nil {
		t.Error("expected error when getting deleted form")
	}
}

func TestFormSubmissionCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Submission Test Form",
		Slug:      "submission-test-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	submission, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
		FormID:    form.ID,
		Data:      `{"name": "John", "email": "john@example.com"}`,
		IpAddress: sql.NullString{String: "127.0.0.1", Valid: true},
		UserAgent: sql.NullString{String: "Mozilla/5.0", Valid: true},
		IsRead:    false,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission failed: %v", err)
	}

	if submission.FormID != form.ID {
		t.Errorf("FormID = %d, want %d", submission.FormID, form.ID)
	}
	if submission.IsRead {
		t.Error("IsRead should be false initially")
	}
}

func TestFormSubmissionList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "List Submissions Form",
		Slug:      "list-submissions-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	// Create submissions
	for i := 0; i < 5; i++ {
		_, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
			FormID:    form.ID,
			Data:      `{}`,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateFormSubmission failed: %v", err)
		}
	}

	submissions, err := queries.GetFormSubmissions(context.Background(), store.GetFormSubmissionsParams{
		FormID: form.ID,
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("GetFormSubmissions failed: %v", err)
	}

	if len(submissions) != 5 {
		t.Errorf("got %d submissions, want 5", len(submissions))
	}
}

func TestFormSubmissionCount(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Count Submissions Form",
		Slug:      "count-submissions-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	// Create submissions
	for i := 0; i < 3; i++ {
		_, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
			FormID:    form.ID,
			Data:      `{}`,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateFormSubmission failed: %v", err)
		}
	}

	count, err := queries.CountFormSubmissions(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("CountFormSubmissions failed: %v", err)
	}

	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestFormSubmissionMarkRead(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Mark Read Form",
		Slug:      "mark-read-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	submission, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
		FormID:    form.ID,
		Data:      `{}`,
		IsRead:    false,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission failed: %v", err)
	}

	if submission.IsRead {
		t.Error("IsRead should be false initially")
	}

	if err := queries.MarkSubmissionRead(context.Background(), submission.ID); err != nil {
		t.Fatalf("MarkSubmissionRead failed: %v", err)
	}

	updated, err := queries.GetFormSubmissionByID(context.Background(), submission.ID)
	if err != nil {
		t.Fatalf("GetFormSubmissionByID failed: %v", err)
	}

	if !updated.IsRead {
		t.Error("IsRead should be true after marking read")
	}
}

func TestFormSubmissionDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Delete Submission Form",
		Slug:      "delete-submission-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	submission, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
		FormID:    form.ID,
		Data:      `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission failed: %v", err)
	}

	if err := queries.DeleteFormSubmission(context.Background(), submission.ID); err != nil {
		t.Fatalf("DeleteFormSubmission failed: %v", err)
	}

	_, err = queries.GetFormSubmissionByID(context.Background(), submission.ID)
	if err == nil {
		t.Error("expected error when getting deleted submission")
	}
}

func TestFormUnreadSubmissions(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Unread Test Form",
		Slug:      "unread-test-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	// Create unread submissions
	for i := 0; i < 3; i++ {
		_, err := queries.CreateFormSubmission(context.Background(), store.CreateFormSubmissionParams{
			FormID:    form.ID,
			Data:      `{}`,
			IsRead:    false,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateFormSubmission failed: %v", err)
		}
	}

	unreadCount, err := queries.CountUnreadSubmissions(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("CountUnreadSubmissions failed: %v", err)
	}

	if unreadCount != 3 {
		t.Errorf("unread count = %d, want 3", unreadCount)
	}
}

func TestFormField(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Field Test Form",
		Slug:      "field-test-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	field, err := queries.CreateFormField(context.Background(), store.CreateFormFieldParams{
		FormID:      form.ID,
		Name:        "email",
		Label:       "Email Address",
		Type:        "email",
		Placeholder: sql.NullString{String: "Enter your email", Valid: true},
		IsRequired:  true,
		Position:    0,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateFormField failed: %v", err)
	}

	if field.Name != "email" {
		t.Errorf("Name = %q, want %q", field.Name, "email")
	}
	if !field.IsRequired {
		t.Error("IsRequired should be true")
	}

	// Get fields for form
	fields, err := queries.GetFormFields(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("GetFormFields failed: %v", err)
	}

	if len(fields) != 1 {
		t.Errorf("got %d fields, want 1", len(fields))
	}
}
