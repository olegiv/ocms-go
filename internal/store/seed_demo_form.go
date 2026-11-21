// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DemoContactFormSlug is the public preview linked from the demo Contact page.
const DemoContactFormSlug = "contact"

const demoContactFormName = "Sample contact form"

// IsDemoContactForm identifies the seeded example without treating other
// operators' contact forms (including translations) as shared previews.
func IsDemoContactForm(form Form) bool {
	return form.Slug == DemoContactFormSlug && form.Name == demoContactFormName
}

func seedDemoContactForm(ctx context.Context, q *Queries, langCode string) error {
	exists, err := q.FormSlugExistsForLanguage(ctx, FormSlugExistsForLanguageParams{Slug: DemoContactFormSlug, LanguageCode: langCode})
	if err != nil || exists != 0 {
		return err
	}
	now := time.Now()
	form, err := q.CreateForm(ctx, CreateFormParams{
		Name: demoContactFormName, Slug: DemoContactFormSlug, Title: "Contact the editorial team",
		Description:    sql.NullString{String: "Preview the name, email and message fields. This example does not accept submissions. Use Forms in the admin panel to inspect its settings.", Valid: true},
		SuccessMessage: sql.NullString{String: "Thank you for contacting the editorial team.", Valid: true},
		IsActive:       true, LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}

	fields := []struct {
		name, kind, label, example, help, validation string
		required                                     bool
	}{
		{"name", "text", "Name (optional)", "Alex Example", "Optional, up to 100 characters.", `{"maxLength":100}`, false},
		{"email", "email", "Reply email", "alex@example.org", "A valid email address, up to 254 characters.", `{"maxLength":254}`, true},
		{"message", "textarea", "Message", "I have an idea for your next issue.", "Use 10 to 2,000 characters.", `{"minLength":10,"maxLength":2000}`, true},
	}
	for i, field := range fields {
		_, err := q.CreateFormField(ctx, CreateFormFieldParams{
			FormID: form.ID, Name: field.name, Type: field.kind, Label: field.label, IsRequired: field.required,
			Placeholder: sql.NullString{String: field.example, Valid: true},
			HelpText:    sql.NullString{String: field.help, Valid: true},
			Validation:  sql.NullString{String: field.validation, Valid: true},
			Position:    int64(i + 1), LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating %s field: %w", field.name, err)
		}
	}
	return nil
}
