// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func newTestService(t *testing.T) (*pages.Service, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	return pages.NewService(db, store.New(db), nil, nil, pages.Policy{}), cleanup
}

func TestListPagesEmptyShowsOnlyPublishedWithoutReadPerm(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	res, err := svc.List(context.Background(), v2.Actor{}, pages.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("expected no pages, got total=%d", res.Total)
	}
	if res.Page != 1 || res.PerPage != 20 {
		t.Errorf("expected defaulted pagination, got page=%d per_page=%d", res.Page, res.PerPage)
	}
}

func TestListPagesRejectsDraftStatusWithoutReadPerm(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.List(context.Background(), v2.Actor{}, pages.ListFilter{Status: model.PageStatusDraft})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrForbidden {
		t.Errorf("expected ErrForbidden, got kind=%d", de.Kind)
	}
}

func TestGetPageReturnsNotFound(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.Get(context.Background(), v2.Actor{}, 99999, pages.ListFilter{})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got kind=%d", de.Kind)
	}
}

func TestCreatePageRequiresWritePermission(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	tests := map[string]v2.Actor{
		"anonymous":   {},
		"readOnlyKey": {APIKey: &store.ApiKey{ID: 1}, Permissions: []string{model.PermissionPagesRead}},
	}
	for name, actor := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), actor, pages.CreatePageBody{
				Title: "T", Slug: "t", Body: "b",
			})
			var de *v2.Error
			if !errors.As(err, &de) {
				t.Fatalf("want *v2.Error, got %T: %v", err, err)
			}
			if actor.APIKey == nil && de.Kind != v2.ErrUnauthorized {
				t.Errorf("anonymous caller should get Unauthorized, got %d", de.Kind)
			}
			if actor.APIKey != nil && de.Kind != v2.ErrForbidden {
				t.Errorf("read-only caller should get Forbidden, got %d", de.Kind)
			}
		})
	}
}

func TestDeletePageReturnsNotFoundForUnknownID(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1},
		Permissions: []string{model.PermissionPagesWrite},
	}
	err := svc.Delete(context.Background(), writer, 99999)
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got kind=%d", de.Kind)
	}
}

// TestCreateAndUpdateRejectSlugsShadowedByLanguagePrefix covers the API half of
// a cross-namespace collision the admin form already refuses.
//
// The language middleware strips a leading segment matching an active language
// code before the frontend router runs, so a page written at that segment is
// answered by the language homepage and never by itself. Slug validation here
// checks format and page/alias uniqueness, neither of which sees a language, so
// an API client could create or rename a page straight into oblivion.
func TestCreateAndUpdateRejectSlugsShadowedByLanguagePrefix(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true,
		Direction: "ltr", Position: 3, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "api@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1, CreatedBy: author.ID},
		Permissions: []string{model.PermissionPagesWrite},
	}

	_, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Engineering", Slug: "eng", Body: "b"})
	var de *v2.Error
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Create() error = %v, want a validation error for the language prefix", err)
	}

	created, err := svc.Create(ctx, writer, pages.CreatePageBody{Title: "Engineering", Slug: "engineering", Body: "b"})
	if err != nil {
		t.Fatalf("Create() error = %v, want a free slug to be accepted", err)
	}
	shadowed := "eng"
	_, err = svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{Slug: &shadowed})
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Update() error = %v, want a validation error for the language prefix", err)
	}
	unchanged, err := queries.GetPageByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Slug != "engineering" {
		t.Fatalf("stored slug = %q, want the rejected rename not to have landed", unchanged.Slug)
	}
}

// TestCreateRejectsLanguagesThePublicRouterWillNotServe covers content the API
// could write but no visitor could ever read.
//
// A language row can exist while being inactive, or carry a legacy code that
// is malformed or owned by an application route. The public router installs
// none of those, so a published page filed under one answers 404 on every URL.
// Existence checks alone let the API create exactly that.
func TestCreateRejectsLanguagesThePublicRouterWillNotServe(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

	for code, active := range map[string]bool{"de": false, "admin": true} {
		if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
			Code: code, Name: code, NativeName: code, IsActive: active,
			Direction: "ltr", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("CreateLanguage(%q): %v", code, err)
		}
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "unroutable@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1, CreatedBy: author.ID},
		Permissions: []string{model.PermissionPagesWrite},
	}

	for name, code := range map[string]string{"inactive": "de", "reserved route": "admin"} {
		t.Run(name, func(t *testing.T) {
			langCode := code
			_, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "Unreachable", Slug: "unreachable-" + code, Body: "b", LanguageCode: &langCode,
			})
			var de *v2.Error
			if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
				t.Fatalf("Create() error = %v, want a validation error for an unroutable language", err)
			}
			if _, lookupErr := queries.GetPageBySlug(ctx, "unreachable-"+code); lookupErr == nil {
				t.Fatal("the rejected page was written anyway")
			}
		})
	}

	// The site default still works, so the guard has not closed the ordinary path.
	if _, err := svc.Create(ctx, writer, pages.CreatePageBody{
		Title: "Reachable", Slug: "reachable", Body: "b",
	}); err != nil {
		t.Fatalf("Create() with the default language error = %v", err)
	}
}

// TestURLSchemeAllowlistIsEnforced covers the service-level URL checks on
// canonical_url and video_url, which are the only gate those fields have.
//
// The huma `uri-reference` format accepts any relative reference, and a value
// like "javascript:alert(1)" parses as one — request validation will not stop
// it. Both URLs are rendered into page markup, so these checks are what keep a
// scripting scheme out of an href.
//
// The two fields deliberately diverge. video_url keeps the scheme allowlist in
// validateSafeURL, because a self-hosted clip at "/media/clip.mp4" is valid.
// canonical_url goes through util.ValidateCanonicalURL, which additionally
// requires an absolute URL with a host and no credentials: the same value is
// emitted as og:url, which Open Graph requires to be absolute, and the admin
// form and the content importer enforce the identical rule.
//
// Bug state: widen either check in service.go and this reports the value that
// got through.
func TestURLSchemeAllowlistIsEnforced(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

	// The accepted cases below reach the insert, which needs a real author row.
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "scheme@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1, CreatedBy: author.ID},
		Permissions: []string{model.PermissionPagesWrite},
	}

	rejected := map[string]string{
		"javascript": "javascript:alert(1)",
		"data":       "data:text/html;base64,PHNjcmlwdD4=",
		"file":       "file:///etc/passwd",
	}
	for name, raw := range rejected {
		t.Run("canonical "+name, func(t *testing.T) {
			_, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "scheme-" + name, Body: "b", CanonicalURL: raw,
			})
			var de *v2.Error
			if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
				t.Fatalf("Create(canonical_url=%q) error = %v, want a validation error", raw, err)
			}
		})
		t.Run("video "+name, func(t *testing.T) {
			_, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "video-scheme-" + name, Body: "b", VideoURL: raw,
			})
			var de *v2.Error
			if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
				t.Fatalf("Create(video_url=%q) error = %v, want a validation error", raw, err)
			}
		})
	}

	// canonical_url is held to a stricter rule than video_url: it is also
	// emitted as og:url, which Open Graph requires to be absolute, so a
	// reference with no usable host is rejected even though the request schema
	// still declares uri-reference.
	for name, raw := range map[string]string{
		"relative":        "/about",
		"scheme relative": "//cdn.example.com/a",
		"credentials":     "https://user:pass@example.com/a",
	} {
		t.Run("canonical "+name, func(t *testing.T) {
			_, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "canonical-" + strings.ReplaceAll(name, " ", "-"), Body: "b", CanonicalURL: raw,
			})
			var de *v2.Error
			if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
				t.Fatalf("Create(canonical_url=%q) error = %v, want a validation error", raw, err)
			}
		})
	}

	// Absolute http(s) values stay accepted, as does the empty string a client
	// sends to clear the field.
	for name, raw := range map[string]string{
		"empty": "",
		"https": "https://example.com/a",
		"http":  "http://example.com/a",
	} {
		t.Run("accepted "+name, func(t *testing.T) {
			if _, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "ok-" + name, Body: "b", CanonicalURL: raw,
			}); err != nil {
				t.Fatalf("Create(canonical_url=%q) error = %v, want it accepted", raw, err)
			}
		})
	}

	// video_url keeps the looser scheme allowlist here. This pins the v2
	// behavior only — it is NOT a claim that the rule is coherent across the
	// codebase. The admin form runs videoRegistry.ValidateURL, which requires a
	// registered provider and would reject "/media/clip.mp4" outright, and the
	// importer checks video_url not at all. video_url therefore still has three
	// write paths with three rules, which is the same defect canonical_url just
	// had. Out of scope here; see the PR discussion.
	for name, raw := range map[string]string{
		"empty":    "",
		"relative": "/media/clip.mp4",
		"https":    "https://example.com/clip.mp4",
	} {
		t.Run("accepted video "+name, func(t *testing.T) {
			if _, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "ok-video-" + name, Body: "b", VideoURL: raw,
			}); err != nil {
				t.Fatalf("Create(video_url=%q) error = %v, want it accepted", raw, err)
			}
		})
	}
}

// TestUpdateEnforcesCanonicalURLRule covers the PATCH path, which applies the
// caller's value in applyUpdate rather than in Update. That is a separate
// assignment from the create path and had no coverage: a test that only drives
// Create leaves the field writable through the API.
//
// Bug state: replace the validateCanonicalURLField call in applyUpdate with a
// direct assignment and the rejected subtests report the value that landed.
func TestUpdateEnforcesCanonicalURLRule(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "patch@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1, CreatedBy: author.ID},
		Permissions: []string{model.PermissionPagesWrite},
	}

	created, err := svc.Create(ctx, writer, pages.CreatePageBody{
		Title: "Patchable", Slug: "patchable", Body: "b",
		CanonicalURL: "https://example.com/original",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for name, raw := range map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"relative":          "/about",
		"scheme relative":   "//cdn.example.com/a",
		"credentials":       "https://user:pass@example.com/a",
	} {
		t.Run("rejected "+name, func(t *testing.T) {
			value := raw
			_, err := svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{CanonicalURL: &value})
			var de *v2.Error
			if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
				t.Fatalf("Update(canonical_url=%q) error = %v, want a validation error", raw, err)
			}
			page, getErr := queries.GetPageByID(ctx, created.ID)
			if getErr != nil {
				t.Fatalf("GetPageByID: %v", getErr)
			}
			if page.CanonicalUrl != "https://example.com/original" {
				t.Errorf("stored canonical_url = %q, want the original left untouched", page.CanonicalUrl)
			}
		})
	}

	// The empty string is how a client clears the field, and a valid value is
	// stored trimmed.
	for name, tc := range map[string]struct{ raw, want string }{
		"clears":                {"", ""},
		"absolute":              {"https://example.com/new", "https://example.com/new"},
		"whitespace is trimmed": {"  https://example.com/padded  ", "https://example.com/padded"},
	} {
		t.Run("accepted "+name, func(t *testing.T) {
			value := tc.raw
			if _, err := svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{CanonicalURL: &value}); err != nil {
				t.Fatalf("Update(canonical_url=%q) error = %v, want it accepted", tc.raw, err)
			}
			page, getErr := queries.GetPageByID(ctx, created.ID)
			if getErr != nil {
				t.Fatalf("GetPageByID: %v", getErr)
			}
			if page.CanonicalUrl != tc.want {
				t.Errorf("stored canonical_url = %q, want %q", page.CanonicalUrl, tc.want)
			}
		})
	}
}
