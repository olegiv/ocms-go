// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

// ErrTranslationReviewUnavailable means the recorded source is no longer
// available in this translation group, or the target is not a plain draft.
var ErrTranslationReviewUnavailable = errors.New("translation review unavailable")

// TranslationSource identifies the live source an editor must review. It is
// resolved from recorded provenance and group membership, never from form input.
type TranslationSource struct {
	PageID, Revision int64
	Title, Language  string
}

type TranslationReviewInput struct {
	PageID, Revision, SourcePageID, SourceRevision int64
}

func translationSource(ctx context.Context, q *store.Queries, page store.Page) (*store.Page, error) {
	fact, err := q.GetLatestTranslationProvenance(ctx, store.GetLatestTranslationProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var validation Validation
	if err = json.Unmarshal([]byte(fact.ValidatedJson), &validation); err != nil {
		return nil, err
	}
	basis := validation.TranslationBasis
	if basis == nil || basis.SourcePageID <= 0 || basis.SourcePageID == page.ID {
		return nil, nil
	}
	members, err := q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: page.ID})
	if err != nil {
		return nil, err
	}
	linked := false
	for _, member := range members {
		if member.EntityID == basis.SourcePageID {
			linked = true
			break
		}
	}
	if !linked {
		return nil, nil
	}
	source, err := q.GetPageByID(ctx, basis.SourcePageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if source.LanguageCode == page.LanguageCode {
		return nil, nil
	}
	for _, code := range []string{page.LanguageCode, source.LanguageCode} {
		language, langErr := q.GetLanguageByCode(ctx, code)
		if errors.Is(langErr, sql.ErrNoRows) || langErr == nil && !language.IsActive {
			return nil, nil
		}
		if langErr != nil {
			return nil, langErr
		}
	}
	return &source, nil
}

// MarkTranslationReviewed records an editor's explicit review in a new target
// revision. Both displayed revisions are checked in the same transaction. It
// preserves article fields, prior evidence/checks, source and sibling revisions.
func MarkTranslationReviewed(ctx context.Context, q *store.Queries, reviewerID int64, in TranslationReviewInput) (store.Page, error) {
	work, err := pagewrite.InQueriesTx(ctx, q, provenance.Principal{UserID: reviewerID, Source: "human", Transport: "translation_review"}, func(work *pagewrite.Tx) error {
		if err := pagewrite.RequireEditor(ctx, work.Q, reviewerID); err != nil {
			return err
		}
		if in.PageID <= 0 || in.Revision <= 0 || in.SourcePageID <= 0 || in.SourceRevision <= 0 {
			return ErrTranslationReviewUnavailable
		}
		target, err := work.Track(ctx, in.PageID, &in.Revision, true)
		if err != nil {
			return err
		}
		if target.Status != "draft" || target.ScheduledAt.Valid {
			return ErrTranslationReviewUnavailable
		}
		source, err := translationSource(ctx, work.Q, target)
		if err != nil {
			return err
		}
		if source == nil || source.ID != in.SourcePageID {
			return ErrTranslationReviewUnavailable
		}
		if source.Revision != in.SourceRevision {
			return &pagewrite.Conflict{CurrentRevision: source.Revision}
		}
		validated, err := json.Marshal(struct {
			TranslationBasis Basis `json:"translation_basis"`
			ReviewedRevision int64 `json:"translation_reviewed_revision"`
		}{Basis{SourcePageID: source.ID, SourceRevision: source.Revision, Fingerprint: provenance.PageFingerprint(*source)}, target.Revision})
		if err != nil {
			return err
		}
		// This is a human review, not a new agent report or a factual validation
		// of the article. Earlier claims/checks remain on their own revisions.
		work.SetPageEvidence(target.ID, provenance.Evidence{Validated: validated})
		work.TrackEvidenceChanges(target.ID)
		return nil
	})
	if err != nil {
		return store.Page{}, err
	}
	return work.Pages[in.PageID], nil
}
