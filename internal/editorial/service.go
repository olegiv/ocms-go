// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package editorial stores administrator-owned authoring guidance, not model output.
package editorial

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const MaxGuideRunes = 8000
const MaxPresetRunes = 4000

var presetSlugs = []string{"blog-post", "page", "faq", "how-to", "release-notes", "bot-blog"}

// ErrInvalid is a rejected editorial setting; callers localize the field error.
var ErrInvalid = errors.New("invalid editorial guidance")

// ErrForbidden protects site-wide guidance from non-administrator changes.
var ErrForbidden = errors.New("editorial settings require an administrator")

// Guide identifies the requested language and the actual fallback source.
type Guide struct {
	LanguageCode       string    `json:"language_code"`
	SourceLanguageCode string    `json:"source_language_code"`
	Body               string    `json:"body"`
	Inherited          bool      `json:"inherited"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Preset is a bounded, named article outline/instruction set.
type Preset struct {
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	Instructions       string `json:"instructions"`
	SourceLanguageCode string `json:"source_language_code"`
}

// Service uses the caller's sqlc handle, including a transaction when supplied.
type Service struct{ queries *store.Queries }

func New(q *store.Queries) *Service { return &Service{queries: q} }

func (s *Service) validLanguage(ctx context.Context, code string) error {
	if code == "" {
		return nil
	}
	lang, err := s.queries.GetLanguageByCode(ctx, code)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return fmt.Errorf("editorial language: %w", err)
	}
	if !lang.IsActive || !util.IsRoutableLanguageCode(lang.Code) {
		return ErrInvalid
	}
	return nil
}

// Guide resolves one override, falling back to the default without concatenating policies.
func (s *Service) Guide(ctx context.Context, language string) (Guide, error) {
	if err := s.validLanguage(ctx, language); err != nil {
		return Guide{}, err
	}
	row, err := s.queries.GetEditorialGuide(ctx, language)
	if errors.Is(err, sql.ErrNoRows) && language != "" {
		row, err = s.queries.GetEditorialGuide(ctx, "")
	}
	if err != nil {
		return Guide{}, fmt.Errorf("load editorial guide: %w", err)
	}
	return Guide{LanguageCode: language, SourceLanguageCode: row.LanguageCode, Body: row.Body, Inherited: row.LanguageCode != language, UpdatedAt: row.UpdatedAt}, nil
}

// Presets overlays each language-specific preset over its global default.
func (s *Service) Presets(ctx context.Context, language string) ([]Preset, error) {
	if err := s.validLanguage(ctx, language); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListEditorialPresets(ctx, language)
	if err != nil {
		return nil, fmt.Errorf("load editorial presets: %w", err)
	}
	bySlug := make(map[string]Preset)
	for _, row := range rows {
		bySlug[row.Slug] = Preset{Slug: row.Slug, Name: row.Name, Instructions: row.Instructions, SourceLanguageCode: row.LanguageCode}
	}
	result := make([]Preset, 0, len(bySlug))
	for _, slug := range presetSlugs {
		if preset, ok := bySlug[slug]; ok {
			result = append(result, preset)
		}
	}
	return result, nil
}

func (s *Service) authorize(ctx context.Context, userID int64) error {
	user, err := s.queries.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("editorial actor: %w", err)
	}
	if user.Role != model.RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// SaveGuide replaces one guide. Empty overrides revert to the default.
func (s *Service) SaveGuide(ctx context.Context, userID int64, language, body string) error {
	if err := s.authorize(ctx, userID); err != nil {
		return err
	}
	if err := s.validLanguage(ctx, language); err != nil {
		return err
	}
	body = strings.TrimSpace(body)
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) > MaxGuideRunes || body == "" && language == "" {
		return ErrInvalid
	}
	if body == "" {
		return s.queries.DeleteEditorialGuide(ctx, language)
	}
	if err := s.queries.UpsertEditorialGuide(ctx, store.UpsertEditorialGuideParams{LanguageCode: language, Body: body, UpdatedBy: sql.NullInt64{Int64: userID, Valid: true}}); err != nil {
		return fmt.Errorf("save editorial guide: %w", err)
	}
	return nil
}

// SavePreset replaces a known preset or removes an empty language override.
func (s *Service) SavePreset(ctx context.Context, userID int64, language, slug, name, instructions string) error {
	if err := s.authorize(ctx, userID); err != nil {
		return err
	}
	if err := s.validLanguage(ctx, language); err != nil {
		return err
	}
	name, instructions = strings.TrimSpace(name), strings.TrimSpace(instructions)
	if !slices.Contains(presetSlugs, slug) || !utf8.ValidString(name+instructions) || utf8.RuneCountInString(name) > 100 || utf8.RuneCountInString(instructions) > MaxPresetRunes {
		return ErrInvalid
	}
	if language != "" && name == "" && instructions == "" {
		return s.queries.DeleteEditorialPresetOverride(ctx, store.DeleteEditorialPresetOverrideParams{Slug: slug, LanguageCode: language})
	}
	if name == "" || instructions == "" {
		return ErrInvalid
	}
	if err := s.queries.UpsertEditorialPreset(ctx, store.UpsertEditorialPresetParams{Slug: slug, LanguageCode: language, Name: name, Instructions: instructions, UpdatedBy: sql.NullInt64{Int64: userID, Valid: true}}); err != nil {
		return fmt.Errorf("save editorial preset: %w", err)
	}
	return nil
}
