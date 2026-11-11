// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestFeaturedICOServiceAssignmentsWithTransportMetadata(t *testing.T) {
	for _, transport := range []string{"rest", "mcp"} {
		for _, mimeType := range []string{model.MimeTypeICO, "image/vnd.microsoft.icon"} {
			t.Run("actor_transport="+transport+"/"+mimeType, func(t *testing.T) {
				db, cleanup := testutil.TestDB(t)
				t.Cleanup(cleanup)
				q, ctx, now := store.New(db), t.Context(), time.Now()
				author, err := q.CreateUser(ctx, store.CreateUserParams{Email: "ico@example.test", PasswordHash: "unused", Role: model.RoleEditor, Name: "Editor", CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				icon, err := q.CreateMedia(ctx, store.CreateMediaParams{Uuid: "icon", Filename: "favicon.ico", MimeType: mimeType, Size: 1, UploadedBy: author.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				svc := pages.NewService(db, q, nil, nil, pages.Policy{})
				actor := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: author.ID}, Permissions: []string{model.PermissionPagesWrite}, Transport: transport}
				assertRefused := func(err error) {
					t.Helper()
					var refusal *v2.Error
					if !errors.As(err, &refusal) || refusal.Kind != v2.ErrValidation {
						t.Fatalf("ICO must produce validation refusal: %v", err)
					}
				}
				_, err = svc.Create(ctx, actor, pages.CreatePageBody{Title: "ICO", Slug: "ico", FeaturedImageID: &icon.ID})
				assertRefused(err)
				page, err := svc.Create(ctx, actor, pages.CreatePageBody{Title: "Rasterless", Slug: "rasterless", OGImageID: &icon.ID})
				if err != nil {
					t.Fatalf("OG remains supported: %v", err)
				}
				_, err = svc.Update(ctx, actor, page.ID, pages.UpdatePageBody{FeaturedImageID: &icon.ID})
				assertRefused(err)
				// Model a historical assignment; normal updates must retain it.
				if _, err := db.ExecContext(ctx, `UPDATE pages SET featured_image_id = ? WHERE id = ?`, icon.ID, page.ID); err != nil {
					t.Fatal(err)
				}
				title := fmt.Sprintf("Legacy %s", transport)
				updated, err := svc.Update(ctx, actor, page.ID, pages.UpdatePageBody{Title: &title, FeaturedImageID: &icon.ID})
				if err != nil || updated.FeaturedImageID == nil || *updated.FeaturedImageID != icon.ID {
					t.Fatalf("historical ICO must survive: %+v %v", updated, err)
				}
				stored, err := q.GetMediaByID(ctx, icon.ID)
				if err != nil || stored.Width != (sql.NullInt64{}) {
					t.Fatalf("ICO record changed: %+v %v", stored, err)
				}
			})
		}
	}
}

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

// TestURLSchemeAllowlistIsEnforced covers the service-level scheme check on
// canonical_url and video_url, which is the only gate those fields have.
//
// The huma `uri-reference` format accepts any relative reference, and a value
// like "javascript:alert(1)" parses as one — request validation will not stop
// it. Both URLs are rendered into page markup, so the allowlist in
// validateSafeURL is what keeps a scripting scheme out of an href.
//
// Bug state: widen the scheme switch in service.go and this reports the value
// that got through.
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

	// Relative and absolute http(s) values stay accepted, including the empty
	// string a client sends to clear the field.
	for name, raw := range map[string]string{
		"empty":    "",
		"relative": "/about",
		"https":    "https://example.com/a",
	} {
		t.Run("accepted "+name, func(t *testing.T) {
			if _, err := svc.Create(ctx, writer, pages.CreatePageBody{
				Title: "T", Slug: "ok-" + name, Body: "b", CanonicalURL: raw,
			}); err != nil {
				t.Fatalf("Create(canonical_url=%q) error = %v, want it accepted", raw, err)
			}
		})
	}
}

// TestCreateAndUpdateRejectApplicationRouteSlugs refuses page slugs that an
// application route such as /account answers first (security audit
// 2026-10-17, L-2).
func TestCreateAndUpdateRejectApplicationRouteSlugs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

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

	var de *v2.Error
	_, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Account", Slug: "account", Body: "b"})
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Create() error = %v, want a validation error for the application route", err)
	}
	created, err := svc.Create(ctx, writer, pages.CreatePageBody{Title: "Accounting", Slug: "accounting", Body: "b"})
	if err != nil {
		t.Fatalf("Create() error = %v, want a free slug to be accepted", err)
	}
	reserved := "login"
	_, err = svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{Slug: &reserved})
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Update() error = %v, want a validation error for the application route", err)
	}

	// A page stored at a now-reserved slug must stay editable through a
	// GET-then-PATCH round trip that sends the unchanged slug back.
	legacy, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Account", Slug: "account", Body: "b", Status: "draft", AuthorID: author.ID,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	unchanged, title := legacy.Slug, "Account (edited)"
	if _, err := svc.Update(ctx, writer, legacy.ID, pages.UpdatePageBody{Slug: &unchanged, Title: &title}); err != nil {
		t.Fatalf("Update() with the unchanged legacy slug: %v", err)
	}
}

// TestSlugAndImageChecksSeparateRefusalsFromFaults checks that a slug or
// featured-image check answers 422 only for a refusal the caller can fix, and
// an internal error for a storage fault. A fault was a 422 carrying the
// driver's text, which the AI create path then retried as a taken slug
// (2026-11-03 review R2-1/B-6).
//
// Bug state: return v2.NewValidationError for every SlugAvailable or
// FeaturedImageSize error again and this fails.
func TestSlugAndImageChecksSeparateRefusalsFromFaults(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "api@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: author.ID}, Permissions: []string{model.PermissionPagesWrite}}
	kind := func(err error) v2.ErrorKind {
		t.Helper()
		var de *v2.Error
		if !errors.As(err, &de) {
			t.Fatalf("want *v2.Error, got %T: %v", err, err)
		}
		return de.Kind
	}

	if _, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Taken", Slug: "taken", Body: "b"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Again", Slug: "taken", Body: "b"}); kind(err) != v2.ErrValidation {
		t.Errorf("taken slug: %v, want a validation error", err)
	}
	missing := int64(999)
	if _, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Image", Slug: "image", Body: "b", FeaturedImageID: &missing}); kind(err) != v2.ErrValidation {
		t.Errorf("missing featured image: %v, want a validation error", err)
	}

	if _, err = db.ExecContext(ctx, `ALTER TABLE page_aliases RENAME TO page_aliases_unavailable`); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Fault", Slug: "fault", Body: "b"})
	if _, renameErr := db.ExecContext(ctx, `ALTER TABLE page_aliases_unavailable RENAME TO page_aliases`); renameErr != nil {
		t.Fatal(renameErr)
	}
	if kind(err) != v2.ErrInternal {
		t.Errorf("slug check during storage fault: %v, want an internal error", err)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE media RENAME TO media_unavailable`); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Image fault", Slug: "image-fault", Body: "b", FeaturedImageID: &missing})
	if _, renameErr := db.ExecContext(ctx, `ALTER TABLE media_unavailable RENAME TO media`); renameErr != nil {
		t.Fatal(renameErr)
	}
	if kind(err) != v2.ErrInternal {
		t.Errorf("image check during storage fault: %v, want an internal error", err)
	}
}
