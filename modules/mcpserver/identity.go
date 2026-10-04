// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// identityKey is the auth.TokenInfo.Extra key that carries the caller from
// the HTTP layer to tool handlers. The SDK runs handlers on its own
// goroutines without the HTTP request's context values; TokenInfo, exposed
// as CallToolRequest.Extra.TokenInfo, is the channel it provides for this.
const identityKey = "ocms.identity"

// identity is the authenticated caller of one MCP request.
type identity struct {
	key    store.ApiKey
	scopes []string // permissions granted to the key
}

// verifyValidatedKey is the auth.TokenVerifier for the endpoint. It does not
// verify the token itself: middleware.APIKeyAuth already did, applying every
// API key policy (source CIDRs, expiry, maximum lifetime, IP-anomaly
// revocation, verification throttling). It only converts the key that
// middleware stored in the request context into the TokenInfo the SDK hands
// to tool handlers, after checking it belongs to the presented token.
func verifyValidatedKey(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	key := middleware.GetAPIKey(r)
	if key == nil || model.ExtractAPIKeyPrefix(token) != key.KeyPrefix {
		return nil, auth.ErrInvalidToken
	}
	scopes := middleware.ParseAPIKeyPermissions(key)
	info := &auth.TokenInfo{
		Scopes: scopes,
		UserID: "apikey:" + strconv.FormatInt(key.ID, 10),
		Extra:  map[string]any{identityKey: &identity{key: *key, scopes: scopes}},
	}
	if key.ExpiresAt.Valid {
		info.Expiration = key.ExpiresAt.Time
	}
	return info, nil
}

// identityFromRequest returns the caller of a tool call.
func identityFromRequest(req *mcp.CallToolRequest) (*identity, bool) {
	if req == nil || req.Extra == nil {
		return nil, false
	}
	return identityFromTokenInfo(req.Extra.TokenInfo)
}

// identityFromTokenInfo extracts the identity stored by verifyValidatedKey.
func identityFromTokenInfo(info *auth.TokenInfo) (*identity, bool) {
	if info == nil {
		return nil, false
	}
	id, ok := info.Extra[identityKey].(*identity)
	return id, ok && id != nil
}

// actorFor returns the v2.Actor tools run as.
//
// In the v2 services pages:read is what grants access to drafts. The MCP
// policy withholds it unless the administrator exposed drafts to AI agents,
// so the services apply their published-only rule: drafts then answer "not
// found", exactly as they do for an unauthenticated REST caller.
func actorFor(id *identity, s Settings) v2.Actor {
	perms := slices.Clone(id.scopes)
	if !s.AllowDrafts {
		perms = slices.DeleteFunc(perms, func(p string) bool {
			return p == model.PermissionPagesRead
		})
	}
	key := id.key
	return v2.Actor{APIKey: &key, Permissions: perms}
}

// draftsVisible reports whether the actor may see unpublished pages.
func draftsVisible(a v2.Actor) bool {
	return a.HasPermission(model.PermissionPagesRead)
}
