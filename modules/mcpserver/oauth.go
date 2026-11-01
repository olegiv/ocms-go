// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
)

func (m *Module) verifyOAuth(ctx context.Context, raw string, _ *http.Request) (*auth.TokenInfo, error) {
	p, err := m.ctx.OAuth.VerifyAccess(ctx, raw)
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{
		UserID: strconv.FormatInt(p.UserID, 10), Scopes: p.Scopes, Expiration: p.ExpiresAt,
		Extra: map[string]any{identityKey: &identity{userID: p.UserID, grantID: p.GrantID, clientID: p.ClientID, scopes: p.Scopes, expiresAt: p.ExpiresAt, tokenID: p.TokenID}},
	}, nil
}

func (m *Module) keyAccountGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := middleware.GetAPIKey(r)
		if key == nil {
			middleware.WriteAPIError(w, 401, codeUnauthorized, msgUnauthenticated, nil)
			return
		}
		user, err := m.svc.queries.GetUserByID(r.Context(), key.CreatedBy)
		if err != nil || len(oauth.RoleScopes(user.Role)) == 0 {
			middleware.WriteAPIError(w, 401, codeUnauthorized, msgUnauthenticated, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Module) mixedAuthentication(keyHandler, inner http.Handler) http.Handler {
	oauthHandler := auth.RequireBearerToken(m.verifyOAuth, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: m.ctx.OAuth.Issuer() + oauth.MetadataPath,
		Scopes:              []string{model.PermissionMCPAccess},
	})(middleware.PrincipalRateLimit(func(r *http.Request) string {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil {
			return ""
		}
		return info.UserID
	}, keyRateLimitRPS, keyRateLimitBurst)(inner))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		parts := strings.Fields(header)
		if header == "" || len(parts) >= 2 && strings.EqualFold(parts[0], "Bearer") && oauth.RecognizesAccessToken(parts[1]) {
			oauthHandler.ServeHTTP(w, r)
			return
		}
		keyHandler.ServeHTTP(w, r)
	})
}
