// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"net/http"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

type auditOriginKey struct{}

// auditOrigin is where an audited request came from: the client address and
// the request path.
type auditOrigin struct{ ip, path string }

// WithRequest returns a context that makes the OAuth audit events written
// under it carry the caller's address and the request path. The address is
// the one middleware.GetClientIP derives, so forwarding headers count only
// from a trusted proxy. The path never includes the query string or fragment,
// which hold the authorization request's state and PKCE values
// (security audit 2026-11-03, A-10). Handlers pass it to the Service methods
// that audit and take no *http.Request themselves; Token and Revoke apply it
// to their own request.
func WithRequest(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, auditOriginKey{}, auditOrigin{ip: middleware.GetClientIP(r), path: middleware.GetRequestURL(r)})
}

// audit runs after the state transaction commits. Never include credentials,
// authorization query strings, client state or unpublished content here.
func (s *Service) audit(ctx context.Context, g store.OauthGrant, message string, warning bool) {
	level := model.EventLevelInfo
	if warning {
		level = model.EventLevelWarning
	}
	origin, _ := ctx.Value(auditOriginKey{}).(auditOrigin)
	// LogAuthEvent logs its own write failures (service.LogEvent).
	_ = service.NewEventService(s.db).LogAuthEvent(ctx, level, message, &g.UserID, origin.ip, origin.path, map[string]any{
		"grant_id": g.ID, "client_id": g.ClientID, "scopes": g.Scopes,
	})
}
