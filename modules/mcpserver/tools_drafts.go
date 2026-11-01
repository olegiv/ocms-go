// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
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
)

func (m *Module) authorizeDraftCall(ctx context.Context, q *store.Queries, id *identity) error {
	if id == nil || m.ctx == nil || m.ctx.Config != nil && m.ctx.Config.AIDisabled || middleware.IsDemoMode() {
		return v2.NewError(v2.ErrForbidden, "Draft authoring is unavailable")
	}
	active, err := q.IsModuleActive(ctx, ModuleName)
	if err != nil {
		return err
	}
	if !active {
		return v2.NewError(v2.ErrForbidden, "MCP server is disabled")
	}
	// Read policy through the supplied transaction handle, never a cached server snapshot.
	settings, err := loadSettings(ctx, q.DBHandle())
	if err != nil {
		return err
	}
	if !settings.DraftToolsEnabled {
		return &v2.Error{Kind: v2.ErrForbidden, MachineCode: "draft_tools_disabled", Msg: "An administrator must enable draft authoring"}
	}
	if id.grantID != "" {
		if m.ctx.OAuth == nil {
			return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
		}
		p, err := m.ctx.OAuth.RecheckAccess(ctx, q, oauth.Principal{TokenID: id.tokenID, UserID: id.userID, GrantID: id.grantID, ClientID: id.clientID})
		if err != nil {
			return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
		}
		if !slices.Contains(p.Scopes, model.PermissionMCPAccess) || !slices.Contains(p.Scopes, model.PermissionMCPDrafts) {
			return v2.NewError(v2.ErrForbidden, "Draft permission required")
		}
		return nil
	}
	key, err := q.GetAPIKeyByID(ctx, id.key.ID)
	if err != nil || key.CreatedBy != id.userID {
		return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
	}
	allowed, err := middleware.RecheckAPIKeyPolicy(ctx, q, key, id.sourceIP)
	if err != nil {
		return err
	}
	if !allowed {
		return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
	}
	scopes := middleware.ParseAPIKeyPermissions(&key)
	if !slices.Contains(scopes, model.PermissionMCPAccess) || !slices.Contains(scopes, model.PermissionMCPDrafts) {
		return v2.NewError(v2.ErrForbidden, "Draft permission required")
	}
	return nil
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
			return err
		}
		if linkPolicy(settings) != linkPolicy(call.settings) || settings.AllowDrafts != call.settings.AllowDrafts {
			return &v2.Error{Kind: v2.ErrConflict, MachineCode: "write_retryable", Msg: "Draft link policy changed; retry with the same request_id"}
		}
		var scopes []string
		if call.identity.grantID != "" {
			p, err := m.ctx.OAuth.RecheckAccess(ctx, q, oauth.Principal{TokenID: call.identity.tokenID, UserID: call.identity.userID, GrantID: call.identity.grantID, ClientID: call.identity.clientID})
			if err != nil {
				return v2.NewError(v2.ErrUnauthorized, "Credential unavailable")
			}
			scopes = p.Scopes
		} else {
			key, err := q.GetAPIKeyByID(ctx, call.identity.key.ID)
			if err != nil {
				return err
			}
			scopes = middleware.ParseAPIKeyPermissions(&key)
		}
		if (settings.AllowDrafts && slices.Contains(scopes, model.PermissionPagesRead)) != service.Access.AllDrafts {
			return &v2.Error{Kind: v2.ErrConflict, MachineCode: "write_retryable", Msg: "Draft read policy changed; retry with the same request_id"}
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
	return s.CreatePreviewLink(ctx, a, in)
}
