// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	lib "github.com/go-oauth2/oauth2/v4"
	liberrors "github.com/go-oauth2/oauth2/v4/errors"
	"github.com/go-oauth2/oauth2/v4/models"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// tokenStore is private to one transaction and one token request.
type tokenStore struct {
	s        *Service
	q        *store.Queries
	grant    store.OauthGrant
	clientID string
	scope    string
	resource string
	// verifier and redirectURI are the PKCE code_verifier and redirect_uri
	// of an authorization_code request. A replayed code revokes its grant
	// only when both match what the code was issued for.
	verifier    string
	redirectURI string
	refresh     *store.OauthToken
	rotated     bool
	// reused marks a spent credential presented again; reuseEvent names it in
	// the audit event Token writes once the revocation is committed.
	reused     bool
	reuseEvent string
}

func (t *tokenStore) Create(ctx context.Context, info lib.TokenInfo) error {
	now := t.s.now().Unix()
	if info.GetCode() != "" {
		return t.q.CreateOAuthCode(ctx, store.CreateOAuthCodeParams{
			CodeHash: tokenHash(info.GetCode()), GrantID: t.grant.ID, RedirectUri: info.GetRedirectURI(),
			Challenge: info.GetCodeChallenge(), CreatedAt: now, ExpiresAt: now + 60})
	}
	if t.grant.ID == "" || t.grant.ClientID != info.GetClientID() {
		return liberrors.ErrInvalidGrant
	}
	if t.refresh != nil {
		n, err := t.q.ConsumeOAuthRefresh(ctx, store.ConsumeOAuthRefreshParams{
			RefreshUsedAt: now, AccessRevokedAt: now, ID: t.refresh.ID})
		if err != nil {
			return fmt.Errorf("consuming OAuth refresh: %w", err)
		}
		if n != 1 {
			return liberrors.ErrInvalidRefreshToken
		}
		t.rotated = true
	}
	expiry := min(now+int64(t.s.options.AccessTTL/time.Second), t.grant.ExpiresAt)
	info.SetAccessExpiresIn(time.Duration(expiry-now) * time.Second)
	info.SetRefreshCreateAt(time.Unix(t.grant.CreatedAt, 0))
	info.SetRefreshExpiresIn(time.Duration(t.grant.ExpiresAt-t.grant.CreatedAt) * time.Second)
	if err := t.q.CreateOAuthToken(ctx, store.CreateOAuthTokenParams{
		ID: randomCredential(""), GrantID: t.grant.ID, AccessHash: tokenHash(info.GetAccess()),
		RefreshHash: tokenHash(info.GetRefresh()), Scopes: info.GetScope(), CreatedAt: now, AccessExpiresAt: expiry,
	}); err != nil {
		return fmt.Errorf("creating OAuth token: %w", err)
	}
	return t.q.TouchOAuthGrant(ctx, store.TouchOAuthGrantParams{LastUsedAt: now, ID: t.grant.ID})
}

func (t *tokenStore) GetByCode(ctx context.Context, raw string) (lib.TokenInfo, error) {
	if !validCredential(raw, codePrefix) {
		return nil, liberrors.ErrInvalidAuthorizeCode
	}
	// Storage faults return a non-library error, which Token answers as a
	// retryable 5xx instead of telling the client the code is dead.
	c, err := t.q.GetOAuthCode(ctx, tokenHash(raw))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, storageProblem(ctx, "failed to load OAuth authorization code", err, "client_id", util.BoundedLogField(t.clientID))
	}
	if err == nil && c.ConsumedAt != 0 {
		return nil, t.revokeReplayedCode(ctx, c)
	}
	if err != nil || c.ExpiresAt <= t.s.now().Unix() {
		return nil, liberrors.ErrInvalidAuthorizeCode
	}
	g, err := t.s.activeGrant(ctx, t.q, c.GrantID)
	if Retryable(err) {
		return nil, err
	}
	if err != nil || g.ClientID != t.clientID || g.Resource != t.resource {
		return nil, liberrors.ErrInvalidAuthorizeCode
	}
	t.grant = g
	info := models.NewToken()
	info.SetCode(raw) // Only the presented credential is rehydrated; the DB stores its hash.
	info.SetClientID(g.ClientID)
	info.SetUserID(fmt.Sprint(g.UserID))
	info.SetScope(g.Scopes)
	info.SetRedirectURI(c.RedirectUri)
	info.SetCodeChallenge(c.Challenge)
	info.SetCodeChallengeMethod(lib.CodeChallengeS256)
	info.SetCodeCreateAt(time.Unix(c.CreatedAt, 0))
	info.SetCodeExpiresIn(time.Duration(c.ExpiresAt-c.CreatedAt) * time.Second)
	return info, nil
}

// revokeReplayedCode answers an authorization code that was already redeemed.
// RFC 6749 4.1.2 says to deny the request and SHOULD revoke the tokens the
// first redemption issued, because a code seen twice may have been stolen
// (security audit 2026-11-03, A-1). The grant is the whole family: its access
// token and refresh chain stop working. Only the client the grant was issued
// to, for this resource, can trigger that, so another client presenting the
// code cannot revoke a family it does not own. A grant that is already
// revoked needs nothing more.
//
// Codes travel in redirect URLs, so someone who merely saw a spent code and
// the public client_id must not be able to revoke the connection: the
// replaying request has to prove possession by presenting the PKCE
// code_verifier that matches the stored challenge and the redirect_uri the
// code was issued for, which is what the first redemption needed. Anything
// less is a plain invalid_grant that revokes nothing.
//
// The revocation must outlive the invalid_grant response, so the caller
// commits it, as it does for a replayed refresh token.
func (t *tokenStore) revokeReplayedCode(ctx context.Context, c store.OauthCode) error {
	g, err := t.q.GetOAuthGrant(ctx, c.GrantID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return storageProblem(ctx, "failed to load OAuth grant of a replayed authorization code", err, "client_id", util.BoundedLogField(t.clientID))
	}
	if err != nil || g.ClientID != t.clientID || g.Resource != t.resource || g.RevokedAt != 0 {
		return liberrors.ErrInvalidAuthorizeCode
	}
	if !t.provesCodePossession(c) {
		return liberrors.ErrInvalidAuthorizeCode
	}
	if err := t.q.RevokeOAuthGrant(ctx, store.RevokeOAuthGrantParams{RevokedAt: t.s.now().Unix(), ID: g.ID}); err != nil {
		return storageProblem(ctx, "failed to revoke the grant of a replayed authorization code", err, "grant_id", g.ID)
	}
	t.grant = g
	t.reused = true
	t.reuseEvent = "OAuth authorization code reuse revoked connection"
	return liberrors.ErrInvalidAuthorizeCode
}

// provesCodePossession reports whether the request carries the redirect_uri
// the code was issued for and the PKCE code_verifier whose S256 transform is
// the code's stored challenge: the proof the first redemption required.
func (t *tokenStore) provesCodePossession(c store.OauthCode) bool {
	if c.Challenge == "" || t.verifier == "" || t.redirectURI != c.RedirectUri {
		return false
	}
	sum := sha256.Sum256([]byte(t.verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(c.Challenge)) == 1
}

func (t *tokenStore) RemoveByCode(ctx context.Context, raw string) error {
	n, err := t.q.ConsumeOAuthCode(ctx, store.ConsumeOAuthCodeParams{ConsumedAt: t.s.now().Unix(), CodeHash: tokenHash(raw)})
	if err != nil {
		return err
	}
	if n != 1 {
		return liberrors.ErrInvalidAuthorizeCode
	}
	return nil
}

func (t *tokenStore) GetByRefresh(ctx context.Context, raw string) (lib.TokenInfo, error) {
	if !validCredential(raw, refreshPrefix) {
		return nil, liberrors.ErrInvalidRefreshToken
	}
	row, err := t.q.GetOAuthTokenByRefresh(ctx, tokenHash(raw))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, storageProblem(ctx, "failed to load OAuth refresh token", err, "client_id", util.BoundedLogField(t.clientID))
	}
	if err != nil {
		return nil, liberrors.ErrInvalidRefreshToken
	}
	g, err := t.s.activeGrant(ctx, t.q, row.GrantID)
	if Retryable(err) {
		return nil, err
	}
	if err != nil || g.ClientID != t.clientID || g.Resource != t.resource {
		return nil, liberrors.ErrInvalidRefreshToken
	}
	t.grant = g
	if row.RefreshUsedAt != 0 {
		if err := t.q.RevokeOAuthGrant(ctx, store.RevokeOAuthGrantParams{RevokedAt: t.s.now().Unix(), ID: g.ID}); err != nil {
			return nil, err
		}
		t.reused = true
		t.reuseEvent = "OAuth refresh reuse revoked connection"
		return nil, liberrors.ErrInvalidRefreshToken
	}
	effectiveScopes := intersectGranted(row.Scopes, g.Scopes)
	if t.scope != "" {
		old := effectiveScopes
		for _, scope := range strings.Fields(t.scope) {
			found := false
			for _, allowed := range old {
				if scope == allowed {
					found = true
					break
				}
			}
			if !found {
				return nil, liberrors.ErrInvalidScope
			}
		}
	}
	t.refresh = &row
	info := models.NewToken()
	info.SetRefresh(raw)
	info.SetClientID(g.ClientID)
	info.SetUserID(fmt.Sprint(g.UserID))
	info.SetScope(scopeString(effectiveScopes))
	info.SetAccessCreateAt(time.Unix(row.CreatedAt, 0))
	info.SetAccessExpiresIn(time.Duration(row.AccessExpiresAt-row.CreatedAt) * time.Second)
	info.SetRefreshCreateAt(time.Unix(g.CreatedAt, 0))
	info.SetRefreshExpiresIn(time.Duration(g.ExpiresAt-g.CreatedAt) * time.Second)
	return info, nil
}

func (t *tokenStore) RemoveByRefresh(_ context.Context, raw string) error {
	if t.rotated && t.refresh != nil && tokenHash(raw) == t.refresh.RefreshHash {
		return nil
	}
	return liberrors.ErrInvalidRefreshToken
}

func (t *tokenStore) GetByAccess(context.Context, string) (lib.TokenInfo, error) {
	return nil, liberrors.ErrInvalidAccessToken
}

func (t *tokenStore) RemoveByAccess(context.Context, string) error {
	return liberrors.ErrInvalidAccessToken
}
