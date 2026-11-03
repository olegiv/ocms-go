// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/idempotency"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

// Service is constructed from trusted transport policy for one authenticated call.
type Service struct {
	Q                         *store.Queries
	Pages                     *pages.Service
	SiteURL                   string
	Access                    grounding.Access
	LinkPolicy                draft.LinkPolicy
	Authorize                 func(context.Context, *store.Queries) error
	CheckPolicy               func(context.Context, *store.Queries) error
	PreviewTTL                time.Duration
	ClientName, ClientVersion string
	Now                       func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) authorize(ctx context.Context, q *store.Queries, actor v2.Actor) error {
	if s.Authorize == nil || !actor.HasPermission("mcp:access") || !actor.HasPermission("mcp:drafts") || actor.ActingUserID() <= 0 || actor.ActingUserID() != s.Access.UserID {
		return v2.NewError(v2.ErrForbidden, "Draft access is unavailable")
	}
	u, err := q.GetUserByID(ctx, actor.ActingUserID())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v2.NewError(v2.ErrUnauthorized, "Account unavailable")
		}
		return err
	}
	if u.Role != "admin" && u.Role != "editor" {
		return v2.NewError(v2.ErrForbidden, "Editor role required")
	}
	return s.Authorize(ctx, q)
}

func (s *Service) options(ctx context.Context) (draft.Options, error) {
	lang, err := contentpath.RouteLanguage(ctx, s.Q, "")
	if err != nil {
		return draft.Options{}, err
	}
	r, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return draft.Options{}, v2.NewValidationError(map[string]string{"site_url": "Configure the site's public HTTP(S) origin"}, "Validation failed")
	}
	return draft.Options{Resolver: r.DraftResolver(), DefaultLanguage: lang.Code, UILanguage: lang.Code, SiteURL: s.SiteURL, LinkPolicy: s.LinkPolicy}, nil
}

func (s *Service) writeOptions(ctx context.Context, a v2.Actor, tool, id string, input any) (pages.WriteOptions, *pages.Page, error) {
	r, err := idempotency.New(a.ActingUserID(), tool, id, input)
	if err != nil {
		return pages.WriteOptions{}, nil, &Error{Code: "invalid_request_id", Message: "Use one non-zero UUID request_id for this operation"}
	}
	opt := pages.WriteOptions{ForceDraft: true, RequestID: r.ID, Idempotency: &r, Authorize: func(ctx context.Context, q *store.Queries) error { return s.authorize(ctx, q, a) }}
	replayed, err := s.Pages.ReplayDraft(ctx, a, opt)
	return opt, replayed, err
}

func (s *Service) Create(ctx context.Context, a v2.Actor, in CreateInput) (SaveResult, error) {
	opt, replay, err := s.writeOptions(ctx, a, "create_draft_page", in.RequestID, in)
	if err != nil {
		return SaveResult{}, err
	}
	if replay != nil {
		return s.receipt(ctx, replay)
	}
	return s.create(ctx, a, in.Draft, opt, nil)
}

func (s *Service) create(ctx context.Context, a v2.Actor, input draft.PageDraft, opt pages.WriteOptions, basis *Basis) (SaveResult, error) {
	options, err := s.options(ctx)
	if err != nil {
		return SaveResult{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		normal, err := draft.NormalizeCreate(ctx, input, options)
		if err != nil {
			return SaveResult{}, err
		}
		if normal.HasErrors() {
			return SaveResult{}, invalid(normal.Issues)
		}
		validation, err := s.validation(ctx, normal, input.BodyMarkdown, basis)
		if err != nil {
			return SaveResult{}, err
		}
		opt.PrepareMutation = s.prepare(a, validation, normal.Reported, input.References.Value, basis != nil, true)
		body, err := s.createBody(ctx, *normal.Content)
		if err != nil {
			return SaveResult{}, err
		}
		saved, err := s.Pages.CreateWith(ctx, a, body, opt)
		if err == nil {
			return s.receipt(ctx, saved)
		}
		var domain *v2.Error
		if input.Slug.Present && !input.Slug.Null || !errors.As(err, &domain) || domain.Fields["slug"] == "" {
			return SaveResult{}, err
		}
	}
	return SaveResult{}, invalid([]draft.Issue{draft.NewIssue("slug_taken", "slug", options.UILanguage)})
}

func (s *Service) Update(ctx context.Context, a v2.Actor, in UpdateInput) (SaveResult, error) {
	opt, replay, err := s.writeOptions(ctx, a, "update_draft_page", in.RequestID, in)
	if err != nil {
		return SaveResult{}, err
	}
	if replay != nil {
		return s.receipt(ctx, replay)
	}
	if in.BaseRevision <= 0 {
		return SaveResult{}, invalid([]draft.Issue{draft.NewIssue("revision_required", "base_revision", "en")})
	}
	p, err := s.own(ctx, s.Q, in.PageID)
	if err != nil {
		return SaveResult{}, err
	}
	options, err := s.options(ctx)
	if err != nil {
		return SaveResult{}, err
	}
	current, _, err := s.content(ctx, p)
	if err != nil {
		return SaveResult{}, err
	}
	normal, err := draft.NormalizeUpdate(ctx, in.Changes, current, options)
	if err != nil {
		return SaveResult{}, err
	}
	if normal.HasErrors() {
		return SaveResult{}, invalid(normal.Issues)
	}
	basis, err := s.latestBasis(ctx, p.ID, p.Revision)
	if err != nil {
		return SaveResult{}, err
	}
	validation, err := s.validation(ctx, normal, in.Changes.BodyMarkdown.Value, basis)
	if err != nil {
		return SaveResult{}, err
	}
	requested := in.Changes.References.Value
	if !in.Changes.References.Present {
		requested, err = s.inheritReferences(ctx, p, &validation, in.Changes.BodyMarkdown.Value, normal.BodyChanged)
		if err != nil {
			return SaveResult{}, err
		}
	}
	if !in.Changes.Reported.Present {
		previous, loadErr := s.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: p.ID, Revision: p.Revision})
		if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
			return SaveResult{}, loadErr
		}
		if loadErr == nil {
			reported, decodeErr := decodeReported(previous.ReportedJson)
			if decodeErr != nil {
				return SaveResult{}, decodeErr
			}
			normal.Reported = reported.Agent
		}
	}
	opt.EvidenceChanges = in.Changes.References.Present || in.Changes.Reported.Present
	opt.PrepareMutation = s.prepare(a, validation, normal.Reported, requested, false, false)
	body, err := s.updateBody(ctx, in, *normal.Content)
	if err != nil {
		return SaveResult{}, err
	}
	saved, err := s.Pages.UpdateWith(ctx, a, in.PageID, body, opt)
	if err != nil {
		return SaveResult{}, err
	}
	return s.receipt(ctx, saved)
}

func invalid(issues []draft.Issue) *Error {
	return &Error{Code: "validation_error", Message: "Draft validation failed", Issues: issues}
}

// MaxOwnAIDrafts is how many unpublished AI drafts one user may hold. A new
// draft beyond it is refused with draft_limit_reached; updates are unaffected.
const MaxOwnAIDrafts = 50

// checkDraftLimit refuses a new draft once the user holds MaxOwnAIDrafts. It
// counts with the query behind list_my_drafts, so "an AI draft" has one
// definition: owned by the user, AI access on, status draft, not scheduled.
// It runs in the write transaction, ahead of the insert, which that
// transaction serializes against concurrent creates.
func checkDraftLimit(ctx context.Context, q *store.Queries, userID int64) error {
	// The empty language means every language. It must not be left nil: the
	// parameter is untyped, and a NULL matches no page, so the count would be 0.
	count, err := q.CountOwnAIDrafts(ctx, store.CountOwnAIDraftsParams{UserID: userID, LanguageCode: ""})
	if err != nil {
		return err
	}
	if count >= MaxOwnAIDrafts {
		return &Error{Code: "draft_limit_reached", Message: fmt.Sprintf("You already have %d AI drafts, the maximum. Keep working in an existing draft, or ask the editor to publish or delete one or turn off its AI access before creating another.", MaxOwnAIDrafts)}
	}
	return nil
}

// prepare builds the in-transaction checks of one write. newDraft is true only
// for operations that insert a page, which are the ones the draft limit gates.
func (s *Service) prepare(a v2.Actor, validation Validation, report draft.AgentReport, requested []draft.Reference, requireSource, newDraft bool) func(context.Context, *store.Queries) (provenance.Evidence, error) {
	return func(ctx context.Context, q *store.Queries) (provenance.Evidence, error) {
		if s.CheckPolicy != nil {
			if err := s.CheckPolicy(ctx, q); err != nil {
				return provenance.Evidence{}, err
			}
		}
		// A refusal that waiting cannot clear is reported before the hourly
		// quota, whose retry would only run into it.
		if newDraft {
			if err := checkDraftLimit(ctx, q, a.ActingUserID()); err != nil {
				return provenance.Evidence{}, err
			}
		}
		since := s.now().Add(-time.Hour)
		count, err := q.CountRecentDraftMutations(ctx, store.CountRecentDraftMutationsParams{UserID: a.ActingUserID(), Since: since})
		if err != nil {
			return provenance.Evidence{}, err
		}
		if count >= 60 {
			first, err := q.EarliestRecentDraftMutation(ctx, store.EarliestRecentDraftMutationParams{UserID: a.ActingUserID(), Since: since})
			if err != nil {
				return provenance.Evidence{}, err
			}
			return provenance.Evidence{}, &Error{Code: "mutation_rate_limited", Message: "Draft mutation limit reached; retry with the same request_id", Retryable: true, RetryAfterSeconds: max(1, int64(first.Add(time.Hour).Sub(s.now()).Seconds())+1)}
		}
		r, err := grounding.NewResolver(q, s.SiteURL, s.Access)
		if err != nil {
			return provenance.Evidence{}, err
		}
		for i, reference := range requested {
			p, err := r.Resolve(ctx, reference.Ref)
			if errors.Is(err, grounding.ErrUnavailable) || err == nil && p.ID != validation.References[i].PageID {
				return provenance.Evidence{}, invalid([]draft.Issue{draft.NewIssue("reference_unresolved", "references", "en")})
			}
			if err != nil {
				return provenance.Evidence{}, err
			}
		}
		if validation.TranslationBasis != nil && requireSource {
			if err := s.checkSource(ctx, q, *validation.TranslationBasis); err != nil {
				return provenance.Evidence{}, err
			}
		}
		validation.TranslationStatus, err = s.basisStatus(ctx, q, validation.TranslationBasis)
		if err != nil {
			return provenance.Evidence{}, err
		}
		validated, err := json.Marshal(validation)
		if err != nil {
			return provenance.Evidence{}, err
		}
		reported, err := json.Marshal(Reported{Agent: report, MCPClientName: s.ClientName, MCPClientVersion: s.ClientVersion})
		if err != nil {
			return provenance.Evidence{}, err
		}
		return provenance.Evidence{Validated: validated, Reported: reported}, nil
	}
}

func (s *Service) receipt(ctx context.Context, p *pages.Page) (SaveResult, error) {
	fact, err := s.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: p.ID, Revision: p.Revision})
	if err != nil {
		return SaveResult{}, err
	}
	v := Validation{Issues: []draft.Issue{}, TranslationStatus: "not_tracked"}
	if err = json.Unmarshal([]byte(fact.ValidatedJson), &v); err != nil {
		return SaveResult{}, err
	}
	adminURL, previewURL := s.urls(v.SiteURL, p.ID, p.LanguageCode, p.Revision)
	return SaveResult{PageID: p.ID, Revision: p.Revision, AdminURL: adminURL, PreviewURL: previewURL, Issues: slices.Clone(v.Issues), TranslationStatus: v.TranslationStatus}, nil
}

func (s *Service) urls(origin string, id int64, language string, revision int64) (string, string) {
	origin = strings.TrimRight(origin, "/")
	return fmt.Sprintf("%s/admin/pages/%d", origin, id), preview.SessionURL(origin, language, id, revision)
}
