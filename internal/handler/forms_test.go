// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewFormsHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewFormsHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestPublicFormPathFailsClosedWithAmbiguousDefaults(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()
	fr, err := queries.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsDefault: true, IsActive: true,
		Direction: "ltr", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage(fr): %v", err)
	}
	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name: "French", Slug: "contact", Title: "Contact", IsActive: true,
		LanguageCode: fr.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if got := publicFormPath(context.Background(), queries, form); got != "" {
		t.Fatalf("publicFormPath = %q; want empty with multiple defaults", got)
	}
}

func TestFormsHandlerShowScopesSameSlugToVerifiedLanguageRoute(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()
	seedSiteURL(t, db, "https://example.com")
	for _, language := range []store.CreateLanguageParams{
		{Code: "fr", Name: "French", NativeName: "Français", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
		{Code: "es", Name: "Spanish", NativeName: "Español", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
		{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
		{Code: "de", Name: "German", NativeName: "Deutsch", IsActive: false, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
		{Code: "blog", Name: "Legacy", NativeName: "Legacy", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
		{Code: "x", Name: "Invalid", NativeName: "Invalid", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := queries.CreateLanguage(context.Background(), language); err != nil {
			t.Fatalf("CreateLanguage(%q): %v", language.Code, err)
		}
	}
	createdForms := make(map[string]store.Form)
	for _, form := range []store.CreateFormParams{
		{Name: "English", Slug: "contact", Title: "Contact EN", IsActive: true, LanguageCode: "en", CreatedAt: now, UpdatedAt: now},
		{Name: "French", Slug: "contact", Title: "Contact FR", IsActive: true, LanguageCode: "fr", CreatedAt: now, UpdatedAt: now},
		{Name: "Spanish", Slug: "contact", Title: "Contact ES", IsActive: true, LanguageCode: "es", CreatedAt: now, UpdatedAt: now},
		{Name: "Inactive Russian", Slug: "contact", Title: "Contact RU", IsActive: false, LanguageCode: "ru", CreatedAt: now, UpdatedAt: now},
		{Name: "Inactive", Slug: "contact-de", Title: "Contact DE", IsActive: true, LanguageCode: "de", CreatedAt: now, UpdatedAt: now},
		{Name: "Reserved", Slug: "contact-blog", Title: "Contact Blog", IsActive: true, LanguageCode: "blog", CreatedAt: now, UpdatedAt: now},
		{Name: "Invalid", Slug: "contact-x", Title: "Contact X", IsActive: true, LanguageCode: "x", CreatedAt: now, UpdatedAt: now},
		{Name: "Orphan", Slug: "contact-zz", Title: "Contact ZZ", IsActive: true, LanguageCode: "zz", CreatedAt: now, UpdatedAt: now},
	} {
		created, err := queries.CreateForm(context.Background(), form)
		if err != nil {
			t.Fatalf("CreateForm(%q): %v", form.Slug, err)
		}
		createdForms[form.Title] = created
	}
	for _, translation := range []struct {
		languageCode string
		targetTitle  string
	}{
		{languageCode: "fr", targetTitle: "Contact FR"},
		{languageCode: "es", targetTitle: "Contact ES"},
		{languageCode: "ru", targetTitle: "Contact RU"},
	} {
		language, err := queries.GetLanguageByCode(context.Background(), translation.languageCode)
		if err != nil {
			t.Fatalf("GetLanguageByCode(%q): %v", translation.languageCode, err)
		}
		if _, err := queries.CreateTranslation(context.Background(), store.CreateTranslationParams{
			EntityType: "form", EntityID: createdForms["Contact EN"].ID, LanguageID: language.ID,
			TranslationID: createdForms[translation.targetTitle].ID, CreatedAt: now,
		}); err != nil {
			t.Fatalf("CreateTranslation(%q): %v", translation.languageCode, err)
		}
	}

	tm := loadedFrontendThemeManager(t, "default")
	menuService := service.NewMenuService(db, nil)
	frontend := NewFrontendHandler(db, tm, nil, slog.Default(), menuService, nil)
	h := NewFormsHandler(db, nil, sm, nil, tm, nil, menuService, frontend)
	router := chi.NewRouter()
	formsRouter := chi.NewRouter()
	formsRouter.Use(middleware.Language(db))
	formsRouter.Get(RouteFormsSlug, h.Show)
	router.Mount(RouteRoot, formsRouter)

	for _, tc := range []struct {
		path      string
		title     string
		canonical string
		action    string
	}{
		{path: "/forms/contact", title: "Contact EN", canonical: "https://example.com/forms/contact", action: `/forms/contact`},
		{path: "/fr/forms/contact", title: "Contact FR", canonical: "https://example.com/fr/forms/contact", action: `/fr/forms/contact`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, tc.title) ||
				!strings.Contains(body, `rel="canonical" href="`+tc.canonical+`"`) ||
				!strings.Contains(body, `property="og:url" content="`+tc.canonical+`"`) ||
				!strings.Contains(body, `action="`+tc.action+`"`) {
				t.Fatalf("language-scoped form output is incomplete: %s", body)
			}
			if !strings.Contains(body, `hreflang="en" href="https://example.com/forms/contact"`) ||
				!strings.Contains(body, `hreflang="fr" href="https://example.com/fr/forms/contact"`) ||
				!strings.Contains(body, `hreflang="es" href="https://example.com/es/forms/contact"`) ||
				!strings.Contains(body, `href="/es/forms/contact"`) ||
				strings.Contains(body, `/ru/forms/contact`) {
				t.Fatalf("form translation links are incomplete or expose an inactive form: %s", body)
			}
		})
	}

	for _, slug := range []string{"contact-de", "contact-blog", "contact-x", "contact-zz"} {
		t.Run(slug, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/forms/"+slug, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestFormsHandlerSubmit_RejectsOversizedPayload(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()

	_, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Contact Form",
		Slug:      "contact-form",
		Title:     "Contact",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)

	payload := "message=" + strings.Repeat("a", int(maxPublicFormBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/forms/contact-form", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithURLParams(req, map[string]string{"slug": "contact-form"})
	w := httptest.NewRecorder()

	h.Submit(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d; want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestFormsHandlerSubmit_RequireCaptchaPolicy_NoCaptchaField(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Contact Form",
		Slug:      "contact-form",
		Title:     "Contact",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)
	h.SetRequireCaptcha(true)

	req := httptest.NewRequest(http.MethodPost, "/forms/contact-form", strings.NewReader("name=Alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithURLParams(req, map[string]string{"slug": "contact-form"})
	w := httptest.NewRecorder()

	h.Submit(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want %d", w.Code, http.StatusServiceUnavailable)
	}

	count, err := queries.CountFormSubmissions(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("CountFormSubmissions failed: %v", err)
	}
	if count != 0 {
		t.Errorf("submission count = %d; want 0", count)
	}
}

func TestFormsHandlerSubmit_RejectsOversizedFieldValue(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Contact Form",
		Slug:      "contact-form",
		Title:     "Contact",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}
	if _, err := queries.CreateFormField(context.Background(), store.CreateFormFieldParams{
		FormID:     form.ID,
		Type:       "text",
		Name:       "message",
		Label:      "Message",
		IsRequired: true,
		Position:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateFormField failed: %v", err)
	}

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)

	payload := "message=" + strings.Repeat("a", maxPublicFormFieldValueBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/forms/contact-form", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithURLParams(req, map[string]string{"slug": "contact-form"})
	w := httptest.NewRecorder()

	h.Submit(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d; want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	count, err := queries.CountFormSubmissions(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("CountFormSubmissions failed: %v", err)
	}
	if count != 0 {
		t.Errorf("submission count = %d; want 0", count)
	}
}

func TestFormsHandlerSubmit_RejectsOversizedSubmissionData(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	now := time.Now()

	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:      "Contact Form",
		Slug:      "contact-form",
		Title:     "Contact",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	fieldNames := []string{"f1", "f2", "f3", "f4", "f5"}
	for i, name := range fieldNames {
		if _, err := queries.CreateFormField(context.Background(), store.CreateFormFieldParams{
			FormID:     form.ID,
			Type:       "text",
			Name:       name,
			Label:      "Field " + name,
			IsRequired: false,
			Position:   int64(i),
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			t.Fatalf("CreateFormField(%s) failed: %v", name, err)
		}
	}

	h := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, nil)

	value := strings.Repeat("a", maxPublicFormFieldValueBytes)
	payload := strings.Join([]string{
		"f1=" + value,
		"f2=" + value,
		"f3=" + value,
		"f4=" + value,
		"f5=" + value,
	}, "&")
	req := httptest.NewRequest(http.MethodPost, "/forms/contact-form", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithURLParams(req, map[string]string{"slug": "contact-form"})
	w := httptest.NewRecorder()

	h.Submit(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d; want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	count, err := queries.CountFormSubmissions(context.Background(), form.ID)
	if err != nil {
		t.Fatalf("CountFormSubmissions failed: %v", err)
	}
	if count != 0 {
		t.Errorf("submission count = %d; want 0", count)
	}
}

func TestRedactFormEventData(t *testing.T) {
	input := map[string]string{
		"name":              "Alice",
		"email":             "alice@example.com",
		"password":          "super-secret",
		"api_token":         "tok_123",
		"authorizationCode": "123456",
		"notes":             strings.Repeat("x", maxFormEventValueLen+10),
	}

	redacted := redactFormEventData(input)

	if redacted["name"] != "Alice" {
		t.Errorf("name = %q, want %q", redacted["name"], "Alice")
	}
	if redacted["email"] != "alice@example.com" {
		t.Errorf("email = %q, want %q", redacted["email"], "alice@example.com")
	}
	if redacted["password"] != redactedFormValue {
		t.Errorf("password should be redacted, got %q", redacted["password"])
	}
	if redacted["api_token"] != redactedFormValue {
		t.Errorf("api_token should be redacted, got %q", redacted["api_token"])
	}
	if redacted["authorizationCode"] != redactedFormValue {
		t.Errorf("authorizationCode should be redacted, got %q", redacted["authorizationCode"])
	}
	if len(redacted["notes"]) != maxFormEventValueLen {
		t.Errorf("notes length = %d, want %d", len(redacted["notes"]), maxFormEventValueLen)
	}
}

func TestBuildFormEventDataForMode(t *testing.T) {
	input := map[string]string{
		"name":     "Alice",
		"password": "super-secret",
		"notes":    strings.Repeat("x", maxFormEventValueLen+10),
	}

	redacted := buildFormEventDataForMode(input, formWebhookDataModeRedacted)
	if redacted["password"] != redactedFormValue {
		t.Errorf("redacted password = %q, want %q", redacted["password"], redactedFormValue)
	}
	if len(redacted["notes"]) != maxFormEventValueLen {
		t.Errorf("redacted notes length = %d, want %d", len(redacted["notes"]), maxFormEventValueLen)
	}

	full := buildFormEventDataForMode(input, formWebhookDataModeFull)
	if full["password"] != "super-secret" {
		t.Errorf("full password = %q, want %q", full["password"], "super-secret")
	}
	if len(full["notes"]) != maxFormEventValueLen {
		t.Errorf("full notes length = %d, want %d", len(full["notes"]), maxFormEventValueLen)
	}

	none := buildFormEventDataForMode(input, formWebhookDataModeNone)
	if none != nil {
		t.Errorf("none mode data = %#v, want nil", none)
	}
}

// TestFormTemplateData_CSRFTokenCompatibility verifies that HTML theme
// templates which still reference {{.CSRFToken}} render successfully against
// FormTemplateData and do not receive a session token value. The starter
// theme's form.html references .CSRFToken; removing the field would fail
// html/template execution with "can't evaluate field CSRFToken".
func TestFormTemplateData_CSRFTokenCompatibility(t *testing.T) {
	tmpl, err := template.New("form").Parse(
		`<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">`,
	)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, FormTemplateData{}); err != nil {
		t.Fatalf("execute template: %v", err)
	}

	got := buf.String()
	want := `<input type="hidden" name="csrf_token" value="">`
	if got != want {
		t.Errorf("rendered = %q, want %q", got, want)
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

func TestFormWithLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Get the default English language
	lang, err := queries.GetDefaultLanguage(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultLanguage failed: %v", err)
	}

	now := time.Now()
	form, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:         "English Form",
		Slug:         "english-form",
		Title:        "English Form Title",
		LanguageCode: lang.Code,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	if form.LanguageCode != lang.Code {
		t.Errorf("LanguageCode = %q, want %q", form.LanguageCode, lang.Code)
	}
}

func TestFormSlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	// Create a form
	_, err := queries.CreateForm(context.Background(), store.CreateFormParams{
		Name:         "Test Form",
		Slug:         "test-form-slug",
		Title:        "Test Form",
		LanguageCode: "en",
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateForm failed: %v", err)
	}

	t.Run("existing slug", func(t *testing.T) {
		exists, err := queries.FormSlugExists(context.Background(), "test-form-slug")
		if err != nil {
			t.Fatalf("FormSlugExists failed: %v", err)
		}
		if exists == 0 {
			t.Error("FormSlugExists should return non-zero for existing slug")
		}
	})

	t.Run("non-existing slug", func(t *testing.T) {
		exists, err := queries.FormSlugExists(context.Background(), "non-existing-slug")
		if err != nil {
			t.Fatalf("FormSlugExists failed: %v", err)
		}
		if exists != 0 {
			t.Error("FormSlugExists should return 0 for non-existing slug")
		}
	})
}

func TestFormTranslationInfo(t *testing.T) {
	info := FormTranslationInfo{
		Language: store.Language{
			ID:   1,
			Code: "en",
			Name: "English",
		},
		Form: store.Form{
			ID:   1,
			Name: "Test Form",
			Slug: "test-form",
		},
	}

	if info.Language.Code != "en" {
		t.Errorf("Language.Code = %q, want %q", info.Language.Code, "en")
	}
	if info.Form.Name != "Test Form" {
		t.Errorf("Form.Name = %q, want %q", info.Form.Name, "Test Form")
	}
}

func TestFormFormDataWithTranslations(t *testing.T) {
	data := FormFormData{
		Form: &store.Form{
			ID:           1,
			Name:         "Contact Form",
			Slug:         "contact",
			LanguageCode: "en",
		},
		IsEdit: true,
		Language: &store.Language{
			ID:   1,
			Code: "en",
			Name: "English",
		},
		AllLanguages: []store.Language{
			{ID: 1, Code: "en", Name: "English"},
			{ID: 2, Code: "ru", Name: "Russian"},
		},
		Translations: []FormTranslationInfo{
			{
				Language: store.Language{ID: 2, Code: "ru", Name: "Russian"},
				Form:     store.Form{ID: 2, Name: "Contact Form", Slug: "contact-ru"},
			},
		},
		MissingLanguages: []store.Language{},
	}

	if data.Language.Code != "en" {
		t.Errorf("Language.Code = %q, want %q", data.Language.Code, "en")
	}
	if len(data.AllLanguages) != 2 {
		t.Errorf("AllLanguages count = %d, want 2", len(data.AllLanguages))
	}
	if len(data.Translations) != 1 {
		t.Errorf("Translations count = %d, want 1", len(data.Translations))
	}
	if data.Translations[0].Language.Code != "ru" {
		t.Errorf("Translation language = %q, want %q", data.Translations[0].Language.Code, "ru")
	}
}

func TestEscapeCSVRow_NeutralizesFormulaValues(t *testing.T) {
	row := []string{"=2+3", "+cmd", "-10", "@sum(A1:A2)", "safe", "  =still-dangerous"}

	got := escapeCSVRow(row)
	want := "'=2+3,'+cmd,'-10,'@sum(A1:A2),safe,'  =still-dangerous"

	if got != want {
		t.Fatalf("escapeCSVRow() = %q, want %q", got, want)
	}
}

func TestEscapeCSVRow_StillEscapesQuotesAndCommas(t *testing.T) {
	row := []string{`=1,2`, `hello "quoted"`}

	got := escapeCSVRow(row)
	want := "\"'=1,2\",\"hello \"\"quoted\"\"\""

	if got != want {
		t.Fatalf("escapeCSVRow() = %q, want %q", got, want)
	}
}
