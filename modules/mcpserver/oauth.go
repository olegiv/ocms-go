// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"errors"
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
	if oauth.Retryable(err) {
		// A storage fault: the SDK answers 500 with the bare protocol code, so
		// the client retries instead of discarding a token that may be valid.
		return nil, err
	}
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
		// A missing account or a role without MCP scopes is a key that cannot
		// be used. A storage fault says nothing about the key: 401 would tell
		// the agent to drop it, so it is a logged 500 instead, as in the
		// API-key middleware.
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			m.logger.ErrorContext(r.Context(), "failed to load the account of an MCP API key", "error", err, "api_key_id", key.ID, "user_id", key.CreatedBy)
			middleware.WriteAPIError(w, http.StatusInternalServerError, codeInternal, msgInternal, nil)
			return
		}
		if err != nil || len(oauth.RoleScopes(user.Role)) == 0 {
			middleware.WriteAPIError(w, 401, codeUnauthorized, msgUnauthenticated, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Module) mixedAuthentication(keyHandler, inner http.Handler) http.Handler {
	metadataURL := m.ctx.OAuth.Issuer() + oauth.MetadataPath
	oauthHandler := auth.RequireBearerToken(m.verifyOAuth, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: metadataURL,
		Scopes:              []string{model.PermissionMCPAccess},
	})(middleware.PrincipalRateLimit(func(r *http.Request) string {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil {
			return ""
		}
		return info.UserID
	}, keyRateLimitRPS, keyRateLimitBurst)(inner))
	draftConnectChallenge := auth.RequireBearerToken(m.verifyOAuth, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: metadataURL,
		Scopes:              []string{model.PermissionMCPAccess, model.PermissionMCPDrafts},
	})(http.NotFoundHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		settings, _ := r.Context().Value(requestSettingsKey{}).(Settings)
		// The SDK uses the same scopes for the challenge and token validation.
		// Send the draft challenge only on initial requests without credentials.
		if header == "" && settings.DraftToolsEnabled && m.ctx.Config != nil && !m.ctx.Config.AIDisabled && !middleware.IsDemoMode() {
			draftConnectChallenge.ServeHTTP(w, r)
			return
		}
		parts := strings.Fields(header)
		if header == "" || len(parts) >= 2 && strings.EqualFold(parts[0], "Bearer") && oauth.RecognizesAccessToken(parts[1]) {
			oauthHandler.ServeHTTP(w, r)
			return
		}
		keyHandler.ServeHTTP(w, r)
	})
}
