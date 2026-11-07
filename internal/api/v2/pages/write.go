// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/idempotency"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// WriteOptions is supplied by a trusted transport, never decoded from draft
// input. ForceDraft requires a request ID and a transaction-bound authorization
// callback that checks module settings and credential/source policies again.
type WriteOptions struct {
	rawBody       string
	ForceDraft    bool
	TranslationOf int64
	RequestID     string
	Evidence      provenance.Evidence
	Authorize     func(context.Context, *store.Queries) error
	// Idempotency retains the original typed agent request before normalization.
	// Only ForceDraft transports may supply it; REST keeps its existing hash.
	Idempotency *idempotency.Request
	// PrepareMutation checks quotas and translation/reference state after replay
	// lookup, returning the evidence for this transaction without charging replays.
	PrepareMutation func(context.Context, *store.Queries) (provenance.Evidence, error)
	EvidenceChanges bool
	// PrepareResult builds the transport's receipt inside the write or replay transaction.
	PrepareResult func(context.Context, *store.Queries, *Page) error
}

func validateTitle(title string) error {
	if size := utf8.RuneCountInString(title); size < 1 || size > 255 {
		return v2.NewValidationError(map[string]string{"title": "Title must be between 1 and 255 characters"}, "Validation failed")
	}
	return nil
}
func validateEnums(status, pageType string) error {
	if status != "" && status != "draft" && status != "published" {
		return v2.NewValidationError(map[string]string{"status": "Invalid status"}, "Validation failed")
	}
	if pageType != "" && pageType != "post" && pageType != "page" {
		return v2.NewValidationError(map[string]string{"page_type": "Invalid page type"}, "Validation failed")
	}
	return nil
}

// Create saves content and every related record in one shared transaction.
func (s *Service) Create(ctx context.Context, a v2.Actor, in CreatePageBody) (*Page, error) {
	return s.CreateWith(ctx, a, in, WriteOptions{})
}

// CreateWith applies trusted draft/translation options without weakening REST.
func (s *Service) CreateWith(ctx context.Context, a v2.Actor, in CreatePageBody, opt WriteOptions) (*Page, error) {
	if opt.ForceDraft && (in.Status != "" || in.ScheduledAt != nil || lifecycleSupplied(in.supplied)) {
		return nil, forbiddenFields()
	}
	opt.rawBody = in.Body
	return s.write(ctx, a, opt, "create_page", in, func(svc *Service, actor v2.Actor, work *pagewrite.Tx) (*Page, error) {
		page, err := svc.create(ctx, actor, in, work)
		if err != nil {
			return nil, err
		}
		if opt.ForceDraft {
			if err = work.RequireTracked(page.ID); err != nil {
				return nil, err
			}
			if err = work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: a.ActingUserID(), SetAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
		}
		if opt.TranslationOf != 0 {
			if err = work.LinkTranslation(ctx, opt.TranslationOf, page.LanguageCode, page.ID); err != nil {
				return nil, err
			}
		}
		return page, nil
	})
}

// Update retains unconditional REST compatibility for ordinary pages only.
func (s *Service) Update(ctx context.Context, a v2.Actor, id int64, in UpdatePageBody) (*Page, error) {
	return s.UpdateWith(ctx, a, id, in, WriteOptions{})
}

// UpdateWith requires ownership, opt-in and a plain draft for agent writes.
func (s *Service) UpdateWith(ctx context.Context, a v2.Actor, id int64, in UpdatePageBody, opt WriteOptions) (*Page, error) {
	if opt.ForceDraft && (in.Status != nil || in.ScheduledAt != nil || lifecycleSupplied(in.supplied)) {
		return nil, forbiddenFields()
	}
	if in.Body != nil {
		opt.rawBody = *in.Body
	}
	input := updateRequest{PageID: id, Changes: in}
	return s.write(ctx, a, opt, "update_page", input, func(svc *Service, actor v2.Actor, work *pagewrite.Tx) (*Page, error) {
		if opt.ForceDraft {
			if err := authorizeDraft(ctx, work.Q, a.ActingUserID(), id); err != nil {
				return nil, err
			}
			if in.BaseRevision == nil {
				return nil, writeError(pagewrite.ErrRevisionRequired)
			}
		}
		return svc.update(ctx, actor, id, in, work, opt.ForceDraft)
	})
}

func forbiddenFields() error {
	return &v2.Error{Kind: v2.ErrForbidden, Msg: "Lifecycle fields cannot be set by an agent", MachineCode: "forbidden_field"}
}

func authorizeDraft(ctx context.Context, q *store.Queries, userID, id int64) error {
	page, err := q.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return pageNotFound(id)
		}
		return err
	}
	access, err := q.GetPageAIAccess(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if page.AuthorID != userID || page.Status != "draft" || page.ScheduledAt.Valid || !access.AiEditable {
		return pageNotFound(id)
	}
	return nil
}

func (s *Service) write(ctx context.Context, a v2.Actor, opt WriteOptions, tool string, input any, mutate func(*Service, v2.Actor, *pagewrite.Tx) (*Page, error)) (*Page, error) {
	var request idempotency.Request
	var err error
	if opt.Idempotency != nil {
		request, err = draftRequest(a, opt, tool)
		if err != nil {
			return nil, writeError(err)
		}
	} else if opt.RequestID != "" || opt.ForceDraft {
		request, err = idempotency.New(a.ActingUserID(), tool, opt.RequestID, struct {
			Input         any                 `json:"input"`
			ForceDraft    bool                `json:"force_draft"`
			TranslationOf int64               `json:"translation_of"`
			Evidence      provenance.Evidence `json:"evidence"`
		}{canonicalWriteInput(input), opt.ForceDraft, opt.TranslationOf, opt.Evidence})
		if err != nil {
			return nil, writeError(err)
		}
	}
	principal := a.Principal(a.Transport)
	var result *Page
	replayed := false
	work, err := pagewrite.InTx(ctx, s.db, principal, func(work *pagewrite.Tx) error {
		if authorizeErr := s.authorizeWrite(ctx, work.Q, a, opt); authorizeErr != nil {
			return authorizeErr
		}
		if request.ID != "" {
			record, lookupErr := idempotency.Lookup(ctx, work.Q, request, time.Now().UTC(), func(entity string, id int64) error {
				if entity != "page" {
					return v2.NewError(v2.ErrForbidden, "Invalid replay entity")
				}
				_, readErr := work.Q.GetPageByID(ctx, id)
				if errors.Is(readErr, sql.ErrNoRows) {
					return idempotency.ErrExpired
				}
				if readErr != nil {
					return readErr
				}
				if opt.ForceDraft {
					return authorizeDraft(ctx, work.Q, a.ActingUserID(), id)
				}
				return nil
			})
			if lookupErr != nil {
				return lookupErr
			}
			if record != nil {
				replayed = true
				result, err = readReplayResult(ctx, work.Q, record, opt.PrepareResult)
				return err
			}
		}
		evidence := opt.Evidence
		if opt.PrepareMutation != nil {
			var prepareErr error
			evidence, prepareErr = opt.PrepareMutation(ctx, work.Q)
			if prepareErr != nil {
				return prepareErr
			}
		}
		transactional := *s
		transactional.queries = work.Q
		actor := a
		if opt.ForceDraft {
			actor.Permissions = append(slices.Clone(a.Permissions), model.PermissionPagesWrite)
		}
		if !opt.ForceDraft {
			work.SetEvidence(evidence)
		}
		result, err = mutate(&transactional, actor, work)
		if err != nil {
			return err
		}
		if result == nil {
			return errors.New("page mutation returned no result")
		}
		if stored, storedErr := work.Q.GetPageByID(ctx, result.ID); storedErr != nil {
			return storedErr
		} else if stored.Status == "published" || stored.ScheduledAt.Valid {
			if validateErr := review.ValidatePublication(ctx, work.Q, stored); validateErr != nil {
				if errors.Is(validateErr, review.ErrHardValidation) {
					return v2.NewValidationError(map[string]string{"body": "Draft failed publication validation"}, "Draft failed publication validation")
				}
				if errors.Is(validateErr, review.ErrSiteURLRequired) {
					return v2.NewError(v2.ErrConflict, "Configure the site URL before publishing AI drafts")
				}
				return validateErr
			}
		}
		if opt.ForceDraft {
			work.SetPageEvidence(result.ID, evidence)
			if opt.EvidenceChanges {
				work.TrackEvidenceChanges(result.ID)
			}
		}
		if err = work.Finish(ctx); err != nil {
			return err
		}
		result.Revision = work.Pages[result.ID].Revision
		if opt.PrepareResult != nil {
			if prepareErr := opt.PrepareResult(ctx, work.Q, result); prepareErr != nil {
				return prepareErr
			}
		}
		if request.ID != "" {
			if opt.Idempotency != nil {
				// Agent tools return a receipt, not the full HTML body. Keep its
				// replay compact even for a maximum-size Markdown draft.
				compact := &Page{ID: result.ID, Revision: result.Revision, Slug: result.Slug, LanguageCode: result.LanguageCode, Status: result.Status}
				return idempotency.Store(ctx, work.Q, request, "page", result.ID, result.Revision, compact, time.Now().UTC())
			}
			return idempotency.Store(ctx, work.Q, request, "page", result.ID, result.Revision, result, time.Now().UTC())
		}
		return nil
	})
	if err != nil {
		mapped := writeError(err)
		var domain *v2.Error
		if errors.As(mapped, &domain) && domain.Code() == "write_retryable" && request.ID != "" {
			domain.Fields = map[string]string{"request_id": request.ID}
		}
		return nil, mapped
	}
	if result == nil {
		return nil, writeError(errors.New("page transaction returned no result"))
	}
	if !replayed && work.Changed[result.ID] {
		page := work.Pages[result.ID]
		for id := range work.Pages {
			s.invalidatePageCache(id)
		}
		if opt.TranslationOf != 0 && s.cache != nil {
			s.cache.Translation.InvalidateType(model.EntityTypePage)
		}
		event := model.EventPageUpdated
		if tool == "create_page" {
			event = model.EventPageCreated
		}
		s.logPageAudit(ctx, a, "Page saved", map[string]any{"page_id": result.ID, "revision": result.Revision})
		s.dispatchPageEvent(ctx, event, page)
		if !s.policy.BlockSuspiciousMarkup && len(security.DetectSuspiciousHTMLTokens(opt.rawBody)) > 0 {
			s.logSuspicious(ctx, a, page, opt.rawBody)
		}
	}
	return result, nil
}

// ReplayDraft checks a retained agent request before Markdown, mutable defaults,
// media or current-content validation. CreateWith/UpdateWith repeat the lookup
// under their transaction, so a concurrent retry cannot insert another page.
func (s *Service) ReplayDraft(ctx context.Context, a v2.Actor, opt WriteOptions) (*Page, error) {
	request, err := draftRequest(a, opt, "")
	if err != nil {
		return nil, writeError(err)
	}
	var result *Page
	_, err = pagewrite.InTx(ctx, s.db, a.Principal(a.Transport), func(work *pagewrite.Tx) error {
		if authorizeErr := s.authorizeWrite(ctx, work.Q, a, opt); authorizeErr != nil {
			return authorizeErr
		}
		record, lookupErr := idempotency.Lookup(ctx, work.Q, request, time.Now().UTC(), func(entity string, id int64) error {
			if entity != "page" {
				return v2.NewError(v2.ErrForbidden, "Invalid replay entity")
			}
			if _, readErr := work.Q.GetPageByID(ctx, id); errors.Is(readErr, sql.ErrNoRows) {
				return idempotency.ErrExpired
			} else if readErr != nil {
				return readErr
			}
			return authorizeDraft(ctx, work.Q, a.ActingUserID(), id)
		})
		if lookupErr != nil || record == nil {
			return lookupErr
		}
		var readErr error
		result, readErr = readReplayResult(ctx, work.Q, record, opt.PrepareResult)
		return readErr
	})
	return result, writeError(err)
}

func readReplayResult(ctx context.Context, q *store.Queries, record *store.IdempotencyKey, prepare func(context.Context, *store.Queries, *Page) error) (*Page, error) {
	var result *Page
	if err := json.Unmarshal([]byte(record.ResultJson.String), &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("page replay returned no result")
	}
	if prepare != nil {
		if err := prepare(ctx, q, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func draftRequest(a v2.Actor, opt WriteOptions, operation string) (idempotency.Request, error) {
	if !opt.ForceDraft || opt.Idempotency == nil {
		return idempotency.Request{}, idempotency.ErrInvalidRequest
	}
	r := *opt.Idempotency
	if r.UserID != a.ActingUserID() || r.ID == "" || r.ID != opt.RequestID || len(r.Hash) != 64 {
		return idempotency.Request{}, idempotency.ErrInvalidRequest
	}
	switch r.Tool {
	case "create_draft_page", "create_translation_draft":
		if operation != "" && operation != "create_page" {
			return idempotency.Request{}, idempotency.ErrInvalidRequest
		}
	case "update_draft_page":
		if operation != "" && operation != "update_page" {
			return idempotency.Request{}, idempotency.ErrInvalidRequest
		}
	default:
		return idempotency.Request{}, idempotency.ErrInvalidRequest
	}
	return r, nil
}

func (s *Service) authorizeWrite(ctx context.Context, q *store.Queries, a v2.Actor, opt WriteOptions) error {
	if !opt.ForceDraft {
		if err := s.requireWritePerm(a); err != nil {
			return err
		}
	}
	if middleware.IsDemoMode() {
		return v2.NewError(v2.ErrForbidden, v2.DemoReadOnlyMessage)
	}
	// Keep role and account refusals definitive. A failed read during a draft
	// write is retryable because the current role could not be established.
	if err := v2.RequireEditor(ctx, q, a); err != nil {
		var accountErr *v2.Error
		if opt.ForceDraft && errors.As(err, &accountErr) && accountErr.Kind == v2.ErrInternal {
			return draftCredentialLookupError(ctx, accountErr.Wrap, "Account unavailable", "check", "account", "user_id", a.ActingUserID())
		}
		return err
	}
	if !opt.ForceDraft {
		return s.requireWritePerm(a)
	}
	user, err := q.GetUserByID(ctx, a.ActingUserID())
	if err != nil {
		return draftCredentialLookupError(ctx, err, "Account unavailable", "check", "account", "user_id", a.ActingUserID())
	}
	if !a.HasPermission(model.PermissionMCPAccess) || !a.HasPermission(model.PermissionMCPDrafts) {
		return v2.NewError(v2.ErrForbidden, "mcp:access and mcp:drafts required")
	}
	if opt.Authorize == nil {
		return v2.NewError(v2.ErrForbidden, "Agent writes require transaction-bound credential and module checks")
	}
	switch {
	case a.APIKey != nil:
		const unavailable = "Credential unavailable"
		key, keyErr := q.GetAPIKeyByID(ctx, a.APIKey.ID)
		if keyErr != nil {
			return draftCredentialLookupError(ctx, keyErr, unavailable, "api_key_id", a.APIKey.ID, "user_id", user.ID)
		}
		if !key.IsActive || key.CreatedBy != user.ID || (key.ExpiresAt.Valid && !time.Now().Before(key.ExpiresAt.Time)) {
			return v2.NewError(v2.ErrUnauthorized, unavailable)
		}
		permissions := middleware.ParseAPIKeyPermissions(&key)
		if !slices.Contains(permissions, model.PermissionMCPAccess) || !slices.Contains(permissions, model.PermissionMCPDrafts) {
			return v2.NewError(v2.ErrForbidden, "Draft permission removed")
		}
	case a.OAuthGrantID != "":
		const unavailable = "Grant unavailable"
		grant, grantErr := q.GetOAuthGrant(ctx, a.OAuthGrantID)
		if grantErr != nil {
			return draftCredentialLookupError(ctx, grantErr, unavailable, "grant_id", a.OAuthGrantID, "client_id", a.OAuthClientID, "user_id", user.ID)
		}
		if grant.UserID != user.ID || grant.RevokedAt != 0 || grant.ExpiresAt <= time.Now().Unix() || grant.SessionVersion != user.SessionVersion || grant.ClientID != a.OAuthClientID {
			return v2.NewError(v2.ErrUnauthorized, unavailable)
		}
		scopes := strings.Fields(grant.Scopes)
		if !slices.Contains(scopes, model.PermissionMCPAccess) || !slices.Contains(scopes, model.PermissionMCPDrafts) {
			return v2.NewError(v2.ErrForbidden, "Draft permission removed")
		}
	default:
		return v2.NewError(v2.ErrUnauthorized, "An app credential is required")
	}
	return opt.Authorize(ctx, q)
}

func draftCredentialLookupError(ctx context.Context, err error, unavailable string, attrs ...any) error {
	if errors.Is(err, sql.ErrNoRows) {
		return v2.NewError(v2.ErrUnauthorized, unavailable)
	}
	slog.ErrorContext(service.WithoutEventLog(ctx), "failed to re-check draft write credential", append([]any{"error", err}, attrs...)...)
	return &v2.Error{Kind: v2.ErrConflict, Msg: "Credential check is temporarily unavailable; retry the call", MachineCode: "write_retryable", Wrap: err}
}

func writeError(err error) error {
	if store.IsSQLiteBusy(err) {
		return &v2.Error{Kind: v2.ErrConflict, Msg: "Write is busy; retry with the same request_id", MachineCode: "write_retryable", Wrap: err}
	}

	var conflict *pagewrite.Conflict
	if errors.As(err, &conflict) {
		return &v2.Error{Kind: v2.ErrConflict, Msg: err.Error(), MachineCode: "revision_conflict", Fields: map[string]string{"current_revision": strconv.FormatInt(conflict.CurrentRevision, 10), "changed_fields": strings.Join(conflict.ChangedFields, ",")}, Wrap: err}
	}
	switch {
	case errors.Is(err, pagewrite.ErrTranslationExists):
		return &v2.Error{Kind: v2.ErrConflict, Msg: err.Error(), MachineCode: "translation_exists", Wrap: err}
	case errors.Is(err, pagewrite.ErrRevisionRequired):
		return &v2.Error{Kind: v2.ErrConflict, Msg: err.Error(), MachineCode: "revision_required", Wrap: err}
	case errors.Is(err, idempotency.ErrKeyReused), errors.Is(err, idempotency.ErrExpired):
		return &v2.Error{Kind: v2.ErrConflict, Msg: err.Error(), MachineCode: err.Error(), Wrap: err}
	case errors.Is(err, idempotency.ErrInvalidRequest):
		return &v2.Error{Kind: v2.ErrValidation, Msg: err.Error(), MachineCode: err.Error(), Wrap: err}
	}
	return err
}

func (s *Service) logSuspicious(ctx context.Context, a v2.Actor, page store.Page, rawBody string) {
	if s.events == nil {
		return
	}
	userID := a.ActingUserID()
	if err := s.events.LogSecurityEvent(ctx, model.EventLevelWarning, "Suspicious page content saved", &userID, "", "", map[string]any{"page_id": page.ID, "tokens": security.DetectSuspiciousHTMLTokens(rawBody)}); err != nil {
		slog.ErrorContext(service.WithoutEventLog(ctx), "failed to log suspicious page content", "error", err, "page_id", page.ID)
	}
}
