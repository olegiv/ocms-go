// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/store"
)

type translatedFields struct {
	Title           string `json:"title"`
	Body            string `json:"body"`
	Summary         string `json:"summary"`
	MetaTitle       string `json:"meta_title"`
	MetaDescription string `json:"meta_description"`
	MetaKeywords    string `json:"meta_keywords"`
}

func fingerprint(fields translatedFields) string {
	raw, _ := json.Marshal(fields)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func currentFingerprint(p store.Page) string {
	return fingerprint(translatedFields{p.Title, p.Body, p.Summary, p.MetaTitle, p.MetaDescription, p.MetaKeywords})
}

func (s *Service) checkSource(ctx context.Context, q *store.Queries, basis Basis) error {
	r, err := grounding.NewResolver(q, s.SiteURL, s.Access)
	if err != nil {
		return err
	}
	p, err := r.Resolve(ctx, fmt.Sprint(basis.SourcePageID))
	if errors.Is(err, grounding.ErrUnavailable) {
		return v2.NewError(v2.ErrNotFound, "Source page not found")
	}
	if err != nil {
		return err
	}
	if currentFingerprint(p) != basis.Fingerprint {
		return sourceConflict(p.LanguageCode)
	}
	return nil
}

func (s *Service) Translate(ctx context.Context, a v2.Actor, in TranslateInput) (SaveResult, error) {
	opt, replay, err := s.writeOptions(ctx, a, "create_translation_draft", in.RequestID, in)
	if err != nil {
		return SaveResult{}, err
	}
	if replay != nil {
		return s.receipt(ctx, replay)
	}
	if in.SourcePageID <= 0 || in.SourceRevision <= 0 || in.LanguageCode == "" {
		return SaveResult{}, invalid([]draft.Issue{draft.NewIssue("invalid_input", "source_revision", "en")})
	}
	resolver, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return SaveResult{}, err
	}
	if _, err = resolver.Resolve(ctx, fmt.Sprint(in.SourcePageID)); errors.Is(err, grounding.ErrUnavailable) {
		return SaveResult{}, v2.NewError(v2.ErrNotFound, "Source page not found")
	} else if err != nil {
		return SaveResult{}, err
	}
	version, err := s.Q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: in.SourcePageID, Revision: sql.NullInt64{Int64: in.SourceRevision, Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return SaveResult{}, sourceConflict("en")
	}
	if err != nil {
		return SaveResult{}, err
	}
	var fields translatedFields
	if err = json.Unmarshal([]byte(version.SnapshotJson.String), &fields); err != nil {
		return SaveResult{}, err
	}
	basis := &Basis{SourcePageID: in.SourcePageID, SourceRevision: in.SourceRevision, Fingerprint: fingerprint(fields)}
	if err = s.checkSource(ctx, s.Q, *basis); err != nil {
		return SaveResult{}, err
	}
	if in.Draft.LanguageCode.Present && (in.Draft.LanguageCode.Null || in.Draft.LanguageCode.Value != in.LanguageCode) {
		return SaveResult{}, invalid([]draft.Issue{draft.NewIssue("invalid_input", "language_code", "en")})
	}
	input := in.Draft
	input.LanguageCode = draft.SetNullable(in.LanguageCode)
	opt.TranslationOf = in.SourcePageID
	return s.create(ctx, a, input, opt, basis)
}

func (s *Service) latestBasis(ctx context.Context, id, revision int64) (*Basis, error) {
	row, err := s.Q.GetLatestTranslationProvenance(ctx, store.GetLatestTranslationProvenanceParams{PageID: id, Revision: revision})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value Validation
	if err = json.Unmarshal([]byte(row.ValidatedJson), &value); err != nil {
		return nil, err
	}
	if value.TranslationBasis != nil {
		members, err := s.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: id})
		if err != nil {
			return nil, err
		}
		linked := false
		for _, member := range members {
			if member.EntityID == value.TranslationBasis.SourcePageID {
				linked = true
				break
			}
		}
		if !linked {
			return nil, nil
		}
	}
	return value.TranslationBasis, nil
}

func (s *Service) basisStatus(ctx context.Context, q *store.Queries, basis *Basis) (string, error) {
	if basis == nil {
		return "not_tracked", nil
	}
	r, err := grounding.NewResolver(q, s.SiteURL, s.Access)
	if err != nil {
		return "", err
	}
	p, err := r.Resolve(ctx, fmt.Sprint(basis.SourcePageID))
	if errors.Is(err, grounding.ErrUnavailable) {
		return "unavailable", nil
	}
	if err != nil {
		return "", err
	}
	if currentFingerprint(p) != basis.Fingerprint {
		return "stale", nil
	}
	return "current", nil
}
