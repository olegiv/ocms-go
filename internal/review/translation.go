// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

// ErrTranslationReviewUnavailable means the recorded source is no longer
// available in this translation group, or the target is missing or not a plain draft.
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
// revision after checking both displayed revisions in one transaction. The
// revision carries forward validation and agent reports. Claim checks stay
// with the revision where the editor made them.
func MarkTranslationReviewed(ctx context.Context, q *store.Queries, reviewerID int64, in TranslationReviewInput) (store.Page, error) {
	work, err := pagewrite.InQueriesTx(ctx, q, provenance.Principal{UserID: reviewerID, Source: "human", Transport: "translation_review"}, func(work *pagewrite.Tx) error {
		if err := pagewrite.RequireEditor(ctx, work.Q, reviewerID); err != nil {
			return err
		}
		if in.PageID <= 0 || in.Revision <= 0 || in.SourcePageID <= 0 || in.SourceRevision <= 0 {
			return ErrTranslationReviewUnavailable
		}
		// Only the initial target lookup can legitimately miss. Track also
		// loads snapshots, whose missing rows are internal faults.
		target, err := work.Q.GetPageByID(ctx, in.PageID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTranslationReviewUnavailable
		}
		if err != nil {
			return err
		}
		if _, err = work.Track(ctx, in.PageID, &in.Revision, true); err != nil {
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
		previous, err := work.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: target.ID, Revision: target.Revision})
		if err != nil {
			return fmt.Errorf("load translation review evidence: %w", err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal([]byte(previous.ValidatedJson), &fields); err != nil {
			return fmt.Errorf("decode translation review evidence: %w", err)
		}
		if fields == nil {
			fields = make(map[string]json.RawMessage)
		}
		basis, err := json.Marshal(Basis{SourcePageID: source.ID, SourceRevision: source.Revision, Fingerprint: provenance.PageFingerprint(*source)})
		if err != nil {
			return err
		}
		fields["translation_basis"] = basis
		fields["translation_reviewed_revision"] = json.RawMessage(strconv.FormatInt(target.Revision, 10))
		validated, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		work.SetPageEvidence(target.ID, provenance.Evidence{Validated: validated, Reported: json.RawMessage(previous.ReportedJson)})
		work.TrackEvidenceChanges(target.ID)
		return nil
	})
	if err != nil {
		return store.Page{}, err
	}
	return work.Pages[in.PageID], nil
}
