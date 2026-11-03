// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Refusal is a write refused for a reason the editor can act on, such as a
// taken slug or an inactive language. Its Reason is fixed text written for
// people, never a storage error, so callers may show it; anything that is not
// a Refusal (or another typed refusal) is a fault to log and report generically.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

func refuse(reason string) error { return &Refusal{Reason: reason} }

// ErrEditorRequired reports that the acting account no longer holds the
// editor or admin role. Callers map it to a refusal, never a server error.
var ErrEditorRequired = errors.New("editor role required")

// RequireEditor rechecks current role inside the mutation transaction. A
// missing account also matches sql.ErrNoRows; other errors are storage faults.
func RequireEditor(ctx context.Context, q *store.Queries, userID int64) error {
	user, err := q.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrEditorRequired, err)
	}
	if err != nil {
		return err
	}
	if user.Role != model.RoleAdmin && user.Role != model.RoleEditor {
		return ErrEditorRequired
	}
	return nil
}

func RouteConflict(ctx context.Context, queries *store.Queries, path string) (string, error) {
	firstSegment, rest, _ := strings.Cut(path, "/")
	if rest == "" && util.IsReservedPageSlug(firstSegment) {
		return fmt.Sprintf("Conflicts with the application route /%s", firstSegment), nil
	}
	if pattern := util.ReservedRoutePath(path); pattern != "" {
		if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
			prefix = strings.TrimSuffix(prefix, "/*")
			return fmt.Sprintf("Conflicts with the application routes under /%s/", prefix), nil
		}
		return fmt.Sprintf("Conflicts with the application route /%s", pattern), nil
	}
	if firstSegment == "" || !util.IsRoutableLanguageCode(firstSegment) {
		return "", nil
	}
	languages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		return "", fmt.Errorf("list active languages: %w", err)
	}
	for _, language := range languages {
		if language.Code == firstSegment {
			return fmt.Sprintf("Conflicts with the URL prefix of active language %q", language.Code), nil
		}
	}
	return "", nil
}

// SlugAvailable validates the route namespace using the transaction's queries.
func SlugAvailable(ctx context.Context, q *store.Queries, slug string, exceptID int64) error {
	if message := ValidateSlugFormat(slug); message != "" {
		return refuse(message)
	}
	conflict, err := RouteConflict(ctx, q, slug)
	if err != nil {
		return err
	}
	if conflict != "" {
		return refuse(conflict)
	}
	return slugUnique(ctx, q, slug, exceptID)
}

// SlugAvailableInLanguage also supports an imported non-default language's
// canonical route. A top-level /admin does not own /ru/admin; the configured
// active language and global slug/alias uniqueness are still checked in the tx.
func SlugAvailableInLanguage(ctx context.Context, q *store.Queries, slug, language string, exceptID int64) error {
	if language == "" {
		return SlugAvailable(ctx, q, slug, exceptID)
	}
	configured, err := q.GetLanguageByCode(ctx, language)
	if err != nil {
		return err
	}
	if !configured.IsActive || !util.IsRoutableLanguageCode(language) {
		return refuse("language is not active and routable")
	}
	defaultLanguage, err := q.GetDefaultLanguage(ctx)
	if err != nil {
		return err
	}
	if language == defaultLanguage.Code {
		return SlugAvailable(ctx, q, slug, exceptID)
	}
	if message := ValidateSlugFormat(slug); message != "" {
		return refuse(message)
	}
	if util.ReservedRoutePath(language+"/"+slug) != "" {
		return refuse("canonical page route is reserved")
	}
	return slugUnique(ctx, q, slug, exceptID)
}

func slugUnique(ctx context.Context, q *store.Queries, slug string, exceptID int64) error {
	exists, err := q.SlugOrAliasExistsExcluding(ctx, store.SlugOrAliasExistsExcludingParams{Slug: slug, ID: exceptID, Alias: slug, PageID: exceptID})
	if err != nil {
		return err
	}
	if exists != 0 {
		return refuse("slug already exists or is used by an alias")
	}
	return nil
}

// ValidateSlugFormat validates a required REST/page slug.
func ValidateSlugFormat(slug string) string {
	if slug == "" {
		return "Slug is required"
	}
	if !util.IsValidSlug(slug) {
		return "Invalid slug format"
	}
	return ""
}

// FeaturedImageSize checks the same minimum dimensions for all write surfaces.
func FeaturedImageSize(ctx context.Context, q *store.Queries, id sql.NullInt64) error {
	if !id.Valid {
		return nil
	}
	media, err := q.GetMediaByID(ctx, id.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("featured image not found")
	}
	if err != nil {
		return fmt.Errorf("featured image: %w", err)
	}
	return CheckFeaturedImageDimensions(media.Width.Int64, media.Height.Int64, media.Width.Valid && media.Height.Valid)
}

// CheckFeaturedImageDimensions shares the editor's minimum with draft validation.
// Missing historical dimensions retain the existing grandfathering behavior.
func CheckFeaturedImageDimensions(width, height int64, known bool) error {
	if known && (width < model.MinFeaturedImageWidth || height < model.MinFeaturedImageHeight) {
		return &FeaturedImageSizeError{Width: width, Height: height}
	}
	return nil
}

// FeaturedImageSizeError carries dimensions for translated editor feedback.
type FeaturedImageSizeError struct{ Width, Height int64 }

func (e *FeaturedImageSizeError) Error() string {
	return fmt.Sprintf("featured image must be at least %d×%d; got %d×%d", model.MinFeaturedImageWidth, model.MinFeaturedImageHeight, e.Width, e.Height)
}
