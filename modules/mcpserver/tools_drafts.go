// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

func (m *Module) authorizeDraftCall(ctx context.Context, q *store.Queries, id *identity) error {
	// Fail closed: without configuration the OCMS_AI_DISABLED switch is unknown.
	if id == nil || m.ctx == nil || m.ctx.Config == nil || m.ctx.Config.AIDisabled || middleware.IsDemoMode() {
		return v2.NewError(v2.ErrForbidden, "Draft authoring is unavailable")
	}
	active, err := q.IsModuleActive(ctx, ModuleName)
	if err != nil {
		return m.credentialFault(ctx, "failed to check MCP draft module status", err, "user_id", id.userID)
	}
	if !active {
		return v2.NewError(v2.ErrForbidden, "MCP server is disabled")
	}
	// Read policy through the supplied transaction handle, never a cached server snapshot.
	settings, err := loadSettings(ctx, q.DBHandle())
	if err != nil {
		return m.credentialFault(ctx, "failed to load MCP draft authorization settings", err, "user_id", id.userID)
	}
	if !settings.DraftToolsEnabled {
		return &v2.Error{Kind: v2.ErrForbidden, MachineCode: "draft_tools_disabled", Msg: "An administrator must enable draft authoring"}
	}
	scopes, key, err := m.credentialScopes(ctx, q, id)
	if err != nil {
		return err
	}
	if key != nil {
		allowed, policyErr := middleware.RecheckAPIKeyPolicy(ctx, q, *key, id.sourceIP)
		if policyErr != nil {
			return m.credentialFault(ctx, "failed to re-check MCP API key policy", policyErr, "api_key_id", key.ID, "user_id", id.userID)
		}
		if !allowed {
			return credentialUnavailable()
		}
	}
	if !slices.Contains(scopes, model.PermissionMCPAccess) || !slices.Contains(scopes, model.PermissionMCPDrafts) {
		return v2.NewError(v2.ErrForbidden, "Draft permission required")
	}
	return nil
}

// credentialScopes re-reads, through q, the scopes the call's credential holds
// now: an OAuth grant through the OAuth service, an API key from its row. The
// draft authorization and the queued write's policy check both run it, so they
// cannot disagree about what the credential may do. For an API key it also
// returns the row, for callers that go on to apply the key policy.
//
// A credential that is gone, or is no longer this identity's, is an
// unauthorized "Credential unavailable". A storage fault is not: answering 401
// would tell the agent its credential was revoked, so it is logged and
// reported as retryable.
func (m *Module) credentialScopes(ctx context.Context, q *store.Queries, id *identity) ([]string, *store.ApiKey, error) {
	if id.grantID != "" {
		if m.ctx.OAuth == nil {
			return nil, nil, credentialUnavailable()
		}
		p, err := m.ctx.OAuth.RecheckAccess(ctx, q, oauth.Principal{TokenID: id.tokenID, UserID: id.userID, GrantID: id.grantID, ClientID: id.clientID})
		if err != nil {
			return nil, nil, recheckError(err)
		}
		return p.Scopes, nil, nil
	}
	key, err := q.GetAPIKeyByID(ctx, id.key.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, credentialUnavailable()
	}
	if err != nil {
		return nil, nil, m.credentialFault(ctx, "failed to re-check the MCP API key", err, "api_key_id", id.key.ID)
	}
	if key.CreatedBy != id.userID {
		return nil, nil, credentialUnavailable()
	}
	return middleware.ParseAPIKeyPermissions(&key), &key, nil
}

// credentialUnavailable is the answer for a credential that is genuinely no
// longer usable: revoked, deleted, expired or not this caller's.
func credentialUnavailable() error {
	return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
}

// credentialFault logs a storage fault while checking credentials or their
// authorization policy and reports it as retryable, like the OAuth re-check.
func (m *Module) credentialFault(ctx context.Context, msg string, err error, attrs ...any) error {
	if canceled := util.RequestCancellation(ctx, err); canceled != nil {
		return canceled
	}
	m.logger.ErrorContext(ctx, msg, append([]any{"error", err}, attrs...)...)
	return retryableCredentialCheck(err)
}

// retryableCredentialCheck is the retryable answer for a credential check that
// could not run: the credential may well be valid.
func retryableCredentialCheck(cause error) error {
	return v2.NewRetryableWriteError("Credential check is temporarily unavailable; retry the call", cause)
}

// recheckError keeps a storage fault during the OAuth re-check retryable: an
// unauthorized answer would tell the agent its credential is gone.
func recheckError(err error) error {
	if oauth.Retryable(err) {
		return retryableCredentialCheck(err)
	}
	return credentialUnavailable()
}

func (m *Module) authoringService(ctx context.Context, call *toolCall) (*authoring.Service, v2.Actor, error) {
	actor := actorFor(call.identity, call.settings)
	if err := m.authorizeDraftCall(ctx, m.svc.queries, call.identity); err != nil {
		return nil, actor, err
	}
	origin, _, err := m.resolveSiteURL(ctx)
	if err != nil {
		return nil, actor, err
	}
	service := &authoring.Service{Q: m.svc.queries, Pages: m.svc.pages, SiteURL: origin, Access: grounding.Access{UserID: call.identity.userID, OwnDrafts: true, AllDrafts: draftsVisible(actor)}, LinkPolicy: draft.LinkPolicy(linkPolicy(call.settings)), ClientName: call.clientName, ClientVersion: call.clientVersion, Authorize: func(ctx context.Context, q *store.Queries) error { return m.authorizeDraftCall(ctx, q, call.identity) }}
	if m.ctx.Config != nil && m.ctx.Config.PreviewLinkTTLHours > 0 {
		service.PreviewTTL = time.Duration(m.ctx.Config.PreviewLinkTTLHours) * time.Hour
	}
	service.CheckPolicy = func(ctx context.Context, q *store.Queries) error {
		settings, err := loadSettings(ctx, q.DBHandle())
		if err != nil {
			return m.credentialFault(ctx, "failed to re-check MCP draft write settings", err, "user_id", call.identity.userID)
		}
		if linkPolicy(settings) != linkPolicy(call.settings) || settings.AllowDrafts != call.settings.AllowDrafts {
			return v2.NewRetryableWriteError("Draft link policy changed; retry with the same request_id", nil)
		}
		scopes, _, err := m.credentialScopes(ctx, q, call.identity)
		if err != nil {
			return err
		}
		if (settings.AllowDrafts && slices.Contains(scopes, model.PermissionPagesRead)) != service.Access.AllDrafts {
			return v2.NewRetryableWriteError("Draft read policy changed; retry with the same request_id", nil)
		}
		return nil
	}
	return service, actor, nil
}

// draftActor is used only by the three content mutations. pages.Service grants
// pages:write solely inside ForceDraft; no general tool gains that permission.
func (m *Module) draftActor(ctx context.Context, call *toolCall) (*authoring.Service, v2.Actor, error) {
	return m.authoringService(ctx, call)
}

func (m *Module) createDraftPage(ctx context.Context, call *toolCall, in authoring.CreateInput) (authoring.SaveResult, error) {
	s, a, err := m.draftActor(ctx, call)
	if err != nil {
		return authoring.SaveResult{}, err
	}
	return s.Create(ctx, a, in)
}
func (m *Module) updateDraftPage(ctx context.Context, call *toolCall, in authoring.UpdateInput) (authoring.SaveResult, error) {
	s, a, err := m.draftActor(ctx, call)
	if err != nil {
		return authoring.SaveResult{}, err
	}
	return s.Update(ctx, a, in)
}
func (m *Module) createTranslationDraft(ctx context.Context, call *toolCall, in authoring.TranslateInput) (authoring.SaveResult, error) {
	s, a, err := m.draftActor(ctx, call)
	if err != nil {
		return authoring.SaveResult{}, err
	}
	return s.Translate(ctx, a, in)
}
func (m *Module) getMyDraft(ctx context.Context, call *toolCall, in authoring.PageInput) (authoring.DraftDetail, error) {
	s, a, err := m.authoringService(ctx, call)
	if err != nil {
		return authoring.DraftDetail{}, err
	}
	return s.Get(ctx, a, in)
}
func (m *Module) listMyDrafts(ctx context.Context, call *toolCall, in authoring.ListInput) (authoring.ListResult, error) {
	s, a, err := m.authoringService(ctx, call)
	if err != nil {
		return authoring.ListResult{}, err
	}
	return s.List(ctx, a, in)
}
func (m *Module) getTranslationStatus(ctx context.Context, call *toolCall, in authoring.PageInput) (authoring.TranslationResult, error) {
	s, a, err := m.authoringService(ctx, call)
	if err != nil {
		return authoring.TranslationResult{}, err
	}
	return s.TranslationStatus(ctx, a, in)
}

func (m *Module) createPreviewLink(ctx context.Context, call *toolCall, in authoring.PreviewInput) (preview.Link, error) {
	s, a, err := m.authoringService(ctx, call)
	if err != nil {
		return preview.Link{}, err
	}
	if err = m.checkAgentShareLinks(ctx, m.ctx.DB, a.ActingUserID(), in.PageID); err != nil {
		return preview.Link{}, err
	}
	// Recheck through the issuance transaction in case sharing was disabled
	// after preflight.
	checkPolicy := s.CheckPolicy
	s.CheckPolicy = func(ctx context.Context, q *store.Queries) error {
		// Reserve the SQLite writer even with a deferred, read-uncommitted
		// connection. Matching no rows keeps settings intact while holding
		// the write lock through the policy read and link insertion.
		if _, lockErr := q.DBHandle().ExecContext(ctx, `UPDATE mcp_settings SET agent_share_links = agent_share_links WHERE 0`); lockErr != nil {
			return m.credentialFault(ctx, "failed to reserve MCP preview issuance writer", lockErr,
				"user_id", a.ActingUserID(), "page_id", in.PageID)
		}
		if shareErr := m.checkAgentShareLinks(ctx, q.DBHandle(), a.ActingUserID(), in.PageID); shareErr != nil {
			return shareErr
		}
		return checkPolicy(ctx, q)
	}
	return s.CreatePreviewLink(ctx, a, in)
}

func (m *Module) checkAgentShareLinks(ctx context.Context, db settingsReader, userID, pageID int64) error {
	settings, err := loadSettings(ctx, db)
	if err != nil {
		return m.credentialFault(ctx, "failed to check MCP preview sharing policy", err, "user_id", userID, "page_id", pageID)
	}
	if !settings.AgentShareLinks {
		return &v2.Error{Kind: v2.ErrForbidden, MachineCode: "share_links_disabled", Msg: "An administrator has turned off share links for AI clients"}
	}
	return nil
}
