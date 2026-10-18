// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
)

// TestFormsHandlerSubmitKeepsEveryCheckedOption submits a form whose checkbox
// field has several boxes checked: the submission stores them all, a form
// re-rendered for a validation error keeps them all checked, and a value
// that is not one of the options is refused. Each box sends its option under
// the field's name, and the handler kept only the first value, so the other
// choices were lost (review of the 2026-10-17 audit fixes). It runs with the
// templ form (default theme) and an HTML theme's form (developer theme).
//
// Bug state: read the field with r.FormValue in Submit, drop the checkbox
// loop after the vals[0] copy in renderFormWithErrors, or drop the checkbox
// case of the type validation, and this fails.
func TestFormsHandlerSubmitKeepsEveryCheckedOption(t *testing.T) {
	db, sm := testHandlerSetup(t)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	form, err := queries.CreateForm(ctx, store.CreateFormParams{
		Name: "Contact", Slug: "contact", Title: "Contact", IsActive: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, field := range []store.CreateFormFieldParams{
		{Type: "checkbox", Name: "reach", Label: "Reach me by", Options: sql.NullString{String: `["Email","Phone","Post","Postcard"]`, Valid: true}},
		{Type: "text", Name: "name", Label: "Name"},
	} {
		field.FormID, field.Position, field.CreatedAt, field.UpdatedAt = form.ID, int64(i), now, now
		if _, err := queries.CreateFormField(ctx, field); err != nil {
			t.Fatal(err)
		}
	}
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	formFuncs := template.FuncMap{} // the test map stubs these out
	for _, name := range []string{"parseJSON", "checkedOption"} {
		formFuncs[name] = renderer.TemplateFuncs()[name]
	}
	for name, tm := range map[string]*theme.Manager{
		"templ (default theme)":  loadedFrontendThemeManager(t, "default"),
		"html (developer theme)": loadedFrontendThemeManager(t, "developer", formFuncs),
	} {
		t.Run(name, func(t *testing.T) { checkCheckboxSubmissions(t, db, sm, queries, form.ID, tm) })
	}
}

// checkCheckboxSubmissions runs TestFormsHandlerSubmitKeepsEveryCheckedOption
// with the theme manager tm.
func checkCheckboxSubmissions(t *testing.T, db *sql.DB, sm *scs.SessionManager, queries *store.Queries, formID int64, tm *theme.Manager) {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `DELETE FROM form_submissions; UPDATE form_fields SET validation = NULL`); err != nil {
		t.Fatal(err)
	}
	menuService := service.NewMenuService(db, nil)
	frontend := NewFrontendHandler(db, tm, nil, slog.Default(), menuService, nil)
	h := NewFormsHandler(db, nil, sm, nil, tm, nil, menuService, frontend)
	submit := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/forms/contact", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = requestWithURLParams(req, map[string]string{"slug": "contact"})
		w := httptest.NewRecorder()
		h.Submit(w, req)
		return w
	}

	// The size limit applies to the joined value: five 1000-byte boxes pass
	// it one by one, but not together.
	if rr := submit(strings.Repeat("reach="+strings.Repeat("a", 1000)+"&", 5) + "name=Ana"); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("five 1000-byte checked boxes: status %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}

	submit("reach=Email&reach=Post&name=Ana")
	submissions, err := queries.GetFormSubmissions(ctx, store.GetFormSubmissionsParams{FormID: formID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 1 {
		t.Fatalf("stored %d submissions, want 1", len(submissions))
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(submissions[0].Data), &data); err != nil {
		t.Fatal(err)
	}
	if got := data["reach"]; got != "Email, Post" {
		t.Errorf("stored checkbox value = %q, want %q", got, "Email, Post")
	}

	// A pattern the name breaks makes the form re-render with the values.
	if _, err := db.ExecContext(ctx, `UPDATE form_fields SET validation = '{"pattern":"^[0-9]+$"}' WHERE name = 'name'`); err != nil {
		t.Fatal(err)
	}
	if body := submit("reach=Fax&name=Ana").Body.String(); !strings.Contains(body, "Please choose from the listed options") {
		t.Errorf("a value that is not an option was not refused:\n%s", body)
	}
	body := submit("reach=Phone&reach=Postcard&name=Ana").Body.String()
	for option, wantChecked := range map[string]bool{"Email": false, "Phone": true, "Post": false, "Postcard": true} {
		input := checkboxInput(body, option)
		if input == "" {
			t.Fatalf("the re-rendered form has no %s box:\n%s", option, body)
		}
		if strings.Contains(input, "checked") != wantChecked {
			t.Errorf("re-rendered %s box %q, want checked=%t", option, input, wantChecked)
		}
	}
}

// checkboxInput returns the <input> tag of the checkbox whose value is option
// in body, or "".
func checkboxInput(body, option string) string {
	at := strings.Index(body, `value="`+option+`"`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(body[:at], "<input")
	end := strings.Index(body[at:], ">")
	if start < 0 || end < 0 {
		return ""
	}
	return body[start : at+end+1]
}
