// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// ErrUnavailable is a missing or inaccessible reference, never an outbound URL.
var ErrUnavailable = errors.New("draft reference unavailable")

// Resolver supplies current site facts. R1.4's shared visible-content resolver
// implements InternalLink; draft processing never fetches a submitted URL.
type Resolver interface {
	Language(context.Context, string) (bool, error)
	SlugAvailable(context.Context, string, int64) (bool, error)
	Tag(context.Context, string, string) (bool, error)
	Category(context.Context, int64, string) (bool, error)
	Media(context.Context, int64) (Media, error)
	InternalLink(context.Context, string) (bool, error)
}

// LinkCheckerSource is optionally implemented by a Resolver that can load the
// site facts behind internal-link checks (redirects, active languages) once and
// reuse them for every link of one body, instead of reloading them per link.
type LinkCheckerSource interface {
	LinkChecker(context.Context) (func(context.Context, string) (bool, error), error)
}

// MediaURLResolver optionally resolves old editor image URLs during conversion.
type MediaURLResolver interface {
	MediaForURL(context.Context, string) (Media, error)
}

// Variant describes an existing uncropped image variant; nothing generates it.
type Variant struct {
	Type          string
	Width, Height int64
}

// Media contains the stored facts needed to render an existing library image.
type Media struct {
	ID              int64
	UUID            string
	Filename        string
	MimeType        string
	Alt             string
	Caption         string
	Width, Height   int64
	DimensionsKnown bool
	Variants        []Variant
}

func (m Media) validImage() bool {
	if m.ID <= 0 || !imaging.IsCanonicalMediaUUID(m.UUID) || m.Filename == "" || strings.ContainsAny(m.Filename, "/\\\x00\r\n") {
		return false
	}
	switch m.MimeType {
	case model.MimeTypeJPEG, model.MimeTypePNG, model.MimeTypeGIF, model.MimeTypeWebP:
		return true
	}
	return false
}

// StoreResolver reads through the supplied sqlc handle, including a transaction
// supplied by a future authoring adapter. Link checks must come from the shared
// route/visibility resolver; their absence fails closed instead of guessing.
type StoreResolver struct {
	Queries   *store.Queries
	CheckLink func(context.Context, string) (bool, error)
	// NewLinkCheck, when set, returns a checker that has already loaded the
	// shared site facts; applyLinks asks for one per run.
	NewLinkCheck func(context.Context) (func(context.Context, string) (bool, error), error)
}

func (s StoreResolver) Language(ctx context.Context, code string) (bool, error) {
	language, err := s.Queries.GetLanguageByCode(ctx, code)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return language.IsActive && util.IsRoutableLanguageCode(language.Code), err
}

func (s StoreResolver) SlugAvailable(ctx context.Context, slug string, exceptID int64) (bool, error) {
	conflict, err := pagewrite.RouteConflict(ctx, s.Queries, slug)
	if err != nil || conflict != "" {
		return false, err
	}
	exists, err := s.Queries.SlugOrAliasExistsExcluding(ctx, store.SlugOrAliasExistsExcludingParams{Slug: slug, ID: exceptID, Alias: slug, PageID: exceptID})
	return exists == 0, err
}

func (s StoreResolver) Tag(ctx context.Context, slug, language string) (bool, error) {
	tag, err := s.Queries.GetTagBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return tag.LanguageCode == language, err
}

func (s StoreResolver) Category(ctx context.Context, id int64, language string) (bool, error) {
	category, err := s.Queries.GetCategoryByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return category.LanguageCode == language, err
}

func (s StoreResolver) Media(ctx context.Context, id int64) (Media, error) {
	stored, err := s.Queries.GetMediaByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrUnavailable
	}
	if err != nil {
		return Media{}, err
	}
	variants, err := s.Queries.GetMediaVariants(ctx, id)
	if err != nil {
		return Media{}, err
	}
	media := Media{ID: stored.ID, UUID: stored.Uuid, Filename: stored.Filename, MimeType: stored.MimeType, Alt: stored.Alt.String, Caption: stored.Caption.String,
		Width: stored.Width.Int64, Height: stored.Height.Int64, DimensionsKnown: stored.Width.Valid && stored.Height.Valid}
	for _, variant := range variants {
		media.Variants = append(media.Variants, Variant{Type: variant.Type, Width: variant.Width, Height: variant.Height})
	}
	if !media.validImage() {
		return Media{}, ErrUnavailable
	}
	return media, nil
}

func (s StoreResolver) InternalLink(ctx context.Context, path string) (bool, error) {
	if s.CheckLink == nil {
		return false, errors.New("draft link checking requires a site visibility resolver")
	}
	return s.CheckLink(ctx, path)
}

// LinkChecker returns the checker one body walk uses for all its internal links.
func (s StoreResolver) LinkChecker(ctx context.Context) (func(context.Context, string) (bool, error), error) {
	if s.NewLinkCheck != nil {
		return s.NewLinkCheck(ctx)
	}
	return s.InternalLink, nil
}

// MediaForURL accepts only a canonical local uploads path. Origin checking is
// the converter's responsibility before it calls this path-only method.
func (s StoreResolver) MediaForURL(ctx context.Context, path string) (Media, error) {
	parsed, err := url.Parse(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Media{}, ErrUnavailable
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "uploads" || !imaging.IsCanonicalMediaUUID(parts[3]) {
		return Media{}, ErrUnavailable
	}
	stored, err := s.Queries.GetMediaByUUID(ctx, parts[3])
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrUnavailable
	}
	if err != nil {
		return Media{}, err
	}
	media, err := s.Media(ctx, stored.ID)
	if err != nil {
		return Media{}, err
	}
	if !media.matchesURL(parsed.EscapedPath()) {
		return Media{}, ErrUnavailable
	}
	return media, nil
}

func (m Media) matchesURL(path string) bool {
	if path == model.MediaURL(model.VariantOriginal, m.UUID, m.Filename) {
		return true
	}
	for _, variant := range m.Variants {
		if supportedVariant(variant.Type) && path == model.MediaURL(variant.Type, m.UUID, m.Filename) {
			return true
		}
	}
	return false
}

func supportedVariant(variant string) bool {
	switch variant {
	case model.VariantSmall, model.VariantMedium, model.VariantLarge, model.VariantOG, model.VariantGrid, model.VariantThumbnail:
		return true
	}
	return false
}
