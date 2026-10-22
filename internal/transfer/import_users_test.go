// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestImportOverwriteNeverChangesAdminRole fails when an import overwrite
// grants or removes the admin role. A stolen administrator session could
// otherwise create an editor whose password it knows and import it as an
// administrator, without the password the user form asks for.
func TestImportOverwriteNeverChangesAdminRole(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	for _, u := range []struct{ email, role string }{
		{"editor@example.com", model.RoleEditor},
		{"public@example.com", model.RolePublic},
	} {
		if _, err := ts.Queries.CreateUser(ts.Ctx, store.CreateUserParams{Email: u.email, PasswordHash: "hash", Role: u.role, Name: "User", CreatedAt: ts.Now, UpdatedAt: ts.Now}); err != nil {
			t.Fatal(err)
		}
	}
	data := &ExportData{Version: "1.0", Users: []ExportUser{
		{Email: "editor@example.com", Name: "Promoted", Role: model.RoleAdmin},
		{Email: ts.User.Email, Name: "Demoted", Role: model.RoleEditor},
		{Email: "public@example.com", Name: "Editor now", Role: model.RoleEditor},
	}}
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, ImportOptions{ImportUsers: true, ConflictStrategy: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("import errors: %v", result.Errors)
	}
	for email, want := range map[string]struct{ role, name string }{
		"editor@example.com": {model.RoleEditor, "Promoted"},
		ts.User.Email:        {model.RoleAdmin, "Demoted"},
		"public@example.com": {model.RoleEditor, "Editor now"},
	} {
		got, err := ts.Queries.GetUserByEmail(ts.Ctx, email)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != want.role || got.Name != want.name {
			t.Errorf("%s: role %q, name %q; want %q, %q", email, got.Role, got.Name, want.role, want.name)
		}
	}
}

// TestImportRejectsUnknownUserRole fails when an archive can give a user a
// role the rest of the CMS does not know.
func TestImportRejectsUnknownUserRole(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	data := &ExportData{Version: "1.0", Users: []ExportUser{{Email: "new@example.com", Name: "New", Role: "superuser"}}}
	errs := NewImporter(ts.Queries, ts.DB, slog.Default()).Validate(data)
	found := false
	for _, e := range errs {
		found = found || strings.Contains(e.Message, `invalid user role "superuser"`)
	}
	if !found {
		t.Fatalf("validation errors %v do not name the role", errs)
	}
}
