// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	lib "github.com/go-oauth2/oauth2/v4"
	liberrors "github.com/go-oauth2/oauth2/v4/errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/olegiv/ocms-go/internal/store"
)

// AuthorizationRequest contains only validated consent data.
type AuthorizationRequest struct {
	ClientID, ClientName, RedirectURI, State, Scope, Challenge string
	client                                                     registeredClient
}

// ProtectedResourceMetadata uses the SDK handler with configured, validated discovery data.
func (s *Service) ProtectedResourceMetadata() http.Handler {
	return auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: s.Resource(), AuthorizationServers: []string{s.Issuer()}, ScopesSupported: RoleScopes("editor"),
		BearerMethodsSupported: []string{"header"},
	})
}

// Prepare validates a request before authentication, consent or redirect.
func (s *Service) Prepare(ctx context.Context, values url.Values) (AuthorizationRequest, error) {
	var out AuthorizationRequest
	if len(values.Encode()) > 8192 || !singleValues(values) {
		return out, problem("invalid_request")
	}
	client, err := s.clients.resolve(ctx, values.Get("client_id"))
	if err != nil {
		return out, err
	}
	if !client.acceptsRedirect(values.Get("redirect_uri")) {
		return out, problem("invalid_request")
	}
	out = AuthorizationRequest{ClientID: client.ID, ClientName: client.Name, RedirectURI: values.Get("redirect_uri"),
		State: values.Get("state"), Challenge: values.Get("code_challenge"), client: client}
	if values.Get("response_type") != "code" {
		return out, problem("unsupported_response_type")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(out.Challenge)
	if err != nil || len(challenge) != 32 || len(out.Challenge) != 43 || values.Get("code_challenge_method") != "S256" {
		return out, problem("invalid_request")
	}
	if values.Get("resource") != s.Resource() {
		return out, problem("invalid_target")
	}
	scopes := strings.Fields(values.Get("scope"))
	if len(scopes) == 0 {
		scopes = []string{"mcp:access"}
	}
	for _, scope := range scopes {
		known := false
		for _, allowed := range RoleScopes("editor") {
			if scope == allowed {
				known = true
			}
		}
		if !known {
			return out, problem("invalid_scope")
		}
	}
	// Endpoint access is a prerequisite, shown explicitly on the consent screen.
	scopes = append(scopes, "mcp:access")
	out.Scope = scopeString(scopes)
	return out, nil
}

// Redirect builds a response only for a URI already validated by Prepare.
func (s *Service) Redirect(req AuthorizationRequest, values url.Values) string {
	u, err := url.Parse(req.RedirectURI)
	if err != nil || !req.client.acceptsRedirect(req.RedirectURI) {
		return ""
	}
	q := u.Query()
	for key, entries := range values {
		if len(entries) > 0 {
			q.Set(key, entries[0])
		}
	}
	q.Set("iss", s.Issuer())
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Authorize issues a single-use code after the application's fresh-login consent check.
func (s *Service) Authorize(ctx context.Context, req AuthorizationRequest, userID int64) (string, error) {
	if req.client.ID != req.ClientID || !req.client.acceptsRedirect(req.RedirectURI) || req.Scope == "" {
		return "", problem("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", failure(ctx, "temporarily_unavailable", "failed to begin OAuth authorization", err, "client_id", req.ClientID)
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", failure(ctx, "temporarily_unavailable", "failed to load OAuth consenting account", err, "client_id", req.ClientID)
	}
	if err != nil || len(IntersectScopes(strings.Fields(req.Scope), user.Role)) != len(strings.Fields(req.Scope)) {
		return "", problem("access_denied")
	}
	now := s.now().Unix()
	g, err := q.CreateOAuthGrant(ctx, store.CreateOAuthGrantParams{ID: randomCredential(""),
		UserID: user.ID, ClientID: req.ClientID, Scopes: req.Scope, Resource: s.Resource(),
		SessionVersion: user.SessionVersion, CreatedAt: now, ExpiresAt: now + int64(s.options.RefreshTTL/time.Second)})
	if err != nil {
		return "", failure(ctx, "server_error", "failed to create OAuth grant", err, "client_id", req.ClientID)
	}
	// Consent is the first use of a dynamically registered client: it stops
	// being the short-lived registration and gets the full lifetime.
	if err = q.ExtendDCROAuthClient(ctx, store.ExtendDCROAuthClientParams{
		ExpiresAt: s.now().Add(DCRClientLifetime).Unix(), ID: req.ClientID}); err != nil {
		return "", failure(ctx, "server_error", "failed to extend OAuth client lifetime", err, "client_id", req.ClientID)
	}
	ts := &tokenStore{s: s, q: q, grant: g}
	manager, _ := s.engine(ts, req.client)
	info, err := manager.GenerateAuthToken(ctx, lib.Code, &lib.TokenGenerateRequest{
		ClientID: req.ClientID, UserID: strconv.FormatInt(user.ID, 10), RedirectURI: req.RedirectURI,
		Scope: req.Scope, CodeChallenge: req.Challenge, CodeChallengeMethod: lib.CodeChallengeS256})
	if err != nil {
		slog.ErrorContext(ctx, "failed to issue OAuth authorization code", "error", err, "client_id", req.ClientID)
		return "", protocolProblem(err)
	}
	if err := tx.Commit(); err != nil {
		return "", failure(ctx, "server_error", "failed to commit OAuth authorization", err, "client_id", req.ClientID)
	}
	s.audit(ctx, g, "OAuth connection authorized", false)
	return info.GetCode(), nil
}

func singleValues(values url.Values) bool {
	for _, entries := range values {
		if len(entries) != 1 {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteProblem writes a bounded protocol error without the internal cause or request values.
func WriteProblem(w http.ResponseWriter, err error) {
	p := protocolProblem(err)
	status := http.StatusBadRequest
	if p.Code == "server_error" || p.Code == "temporarily_unavailable" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": p.Code, "error_description": p.Description})
}

// Token handles only form-encoded authorization_code and refresh_token grants.
func (s *Service) Token(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(WithRequest(r.Context(), r))
	if !parseProtocolForm(w, r) {
		return
	}
	grant := r.PostForm.Get("grant_type")
	if grant != "authorization_code" && grant != "refresh_token" {
		WriteProblem(w, problem("unsupported_grant_type"))
		return
	}
	if r.Header.Get("Authorization") != "" || r.PostForm.Get("client_secret") != "" ||
		r.PostForm.Get("client_assertion") != "" {
		WriteProblem(w, problem("invalid_client"))
		return
	}
	if r.PostForm.Get("resource") != s.Resource() {
		WriteProblem(w, problem("invalid_target"))
		return
	}
	client, err := s.clients.resolve(r.Context(), r.PostForm.Get("client_id"))
	if err != nil {
		WriteProblem(w, err)
		return
	}
	if grant == "authorization_code" && !validVerifier(r.PostForm.Get("code_verifier")) {
		WriteProblem(w, problem("invalid_grant"))
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		WriteProblem(w, failure(r.Context(), "temporarily_unavailable", "failed to begin OAuth token exchange", err, "client_id", client.ID))
		return
	}
	defer rollback(tx)
	ts := &tokenStore{s: s, q: s.q.WithTx(tx), clientID: client.ID, resource: s.Resource(), scope: r.PostForm.Get("scope")}
	_, engine := s.engine(ts, client)
	gt, request, err := engine.ValidationTokenRequest(r)
	if err != nil {
		WriteProblem(w, problem("invalid_request"))
		return
	}
	info, err := engine.GetAccessToken(r.Context(), gt, request)
	if err != nil {
		if ts.reused {
			// Replay revocation must survive the invalid_grant response.
			if commitErr := tx.Commit(); commitErr != nil {
				WriteProblem(w, failure(r.Context(), "server_error", "failed to commit OAuth credential reuse revocation", commitErr, "grant_id", ts.grant.ID))
				return
			}
			s.audit(r.Context(), ts.grant, ts.reuseEvent, true)
		}
		code := "server_error"
		var p *Problem
		switch {
		case errors.As(err, &p):
			code = p.Code
		case errors.Is(err, liberrors.ErrInvalidAuthorizeCode) || errors.Is(err, liberrors.ErrInvalidRefreshToken) || errors.Is(err, liberrors.ErrInvalidRedirectURI) ||
			errors.Is(err, liberrors.ErrInvalidGrant) ||
			errors.Is(err, liberrors.ErrExpiredRefreshToken) || errors.Is(err, liberrors.ErrInvalidCodeChallenge) ||
			errors.Is(err, liberrors.ErrMissingCodeChallenge) || errors.Is(err, liberrors.ErrMissingCodeVerifier):
			code = "invalid_grant"
		case errors.Is(err, liberrors.ErrInvalidScope):
			code = "invalid_scope"
		default:
			// Storage faults were logged with their cause where they occurred;
			// this records the rest (for example a failed code consumption).
			slog.ErrorContext(r.Context(), "OAuth token exchange failed", "error", err, "grant_type", grant, "client_id", client.ID)
		}
		WriteProblem(w, problem(code))
		return
	}
	if err := tx.Commit(); err != nil {
		WriteProblem(w, failure(r.Context(), "server_error", "failed to commit OAuth token exchange", err, "client_id", client.ID))
		return
	}
	writeJSON(w, http.StatusOK, engine.GetTokenData(info))
}

func validVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("-._~", r):
		default:
			return false
		}
	}
	return true
}

func parseProtocolForm(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || err != nil || mediaType != "application/x-www-form-urlencoded" {
		WriteProblem(w, problem("invalid_request"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || !singleValues(r.PostForm) {
		WriteProblem(w, problem("invalid_request"))
		return false
	}
	return true
}

// Revoke implements public-client token revocation without exposing token existence.
func (s *Service) Revoke(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(WithRequest(r.Context(), r))
	if !parseProtocolForm(w, r) {
		return
	}
	if r.Header.Get("Authorization") != "" || r.PostForm.Get("client_secret") != "" {
		WriteProblem(w, problem("invalid_client"))
		return
	}
	client, err := s.clients.resolve(r.Context(), r.PostForm.Get("client_id"))
	if err != nil {
		WriteProblem(w, err)
		return
	}
	// RFC 7009 §2.2: an unknown token is answered 200, but a storage fault is
	// a 503 so the client retries instead of discarding a still-active grant.
	ctx := r.Context()
	raw := r.PostForm.Get("token")
	var row store.OauthToken
	switch {
	case validCredential(raw, refreshPrefix):
		row, err = s.q.GetOAuthTokenByRefresh(ctx, tokenHash(raw))
	case validCredential(raw, AccessPrefix):
		row, err = s.q.GetOAuthTokenByAccess(ctx, tokenHash(raw))
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if err != nil {
		WriteProblem(w, failure(ctx, "temporarily_unavailable", "failed to load OAuth token for revocation", err, "client_id", client.ID))
		return
	}
	g, err := s.q.GetOAuthGrant(ctx, row.GrantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if err != nil {
		WriteProblem(w, failure(ctx, "temporarily_unavailable", "failed to load OAuth grant for revocation", err, "client_id", client.ID))
		return
	}
	if g.ClientID == client.ID {
		if err := s.q.RevokeOAuthGrant(ctx, store.RevokeOAuthGrantParams{ID: g.ID, RevokedAt: s.now().Unix()}); err != nil {
			WriteProblem(w, failure(ctx, "temporarily_unavailable", "failed to revoke OAuth grant", err, "grant_id", g.ID, "client_id", client.ID))
			return
		}
		if g.RevokedAt == 0 {
			s.audit(ctx, g, "OAuth connection revoked by client", false)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// Discovery publishes only the supported OAuth subset.
func (s *Service) Discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	metadata := map[string]any{
		"issuer": s.Issuer(), "authorization_endpoint": s.Issuer() + "/oauth/authorize",
		"token_endpoint": s.Issuer() + "/oauth/token", "revocation_endpoint": s.Issuer() + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
		"client_id_metadata_document_supported": true, "authorization_response_iss_parameter_supported": true,
		"scopes_supported": RoleScopes("editor"),
	}
	if s.options.AllowDCR {
		metadata["registration_endpoint"] = s.Issuer() + "/oauth/register"
	}
	writeJSON(w, http.StatusOK, metadata)
}

// Register implements optional, bounded public-client registration.
func (s *Service) Register(w http.ResponseWriter, r *http.Request) {
	if !s.options.AllowDCR {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || r.URL.RawQuery != "" {
		WriteProblem(w, problem("invalid_client_metadata"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var c registeredClient
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		WriteProblem(w, problem("invalid_client_metadata"))
		return
	}
	c.ID = randomCredential("ocms.client.")
	if c.AuthMethod == "" {
		c.AuthMethod = "none"
	}
	if !validClient(c) {
		WriteProblem(w, problem("invalid_client_metadata"))
		return
	}
	if err := s.saveClient(r.Context(), c, "dcr", s.now().Add(DCRUnusedClientLifetime).Unix()); err != nil {
		logFault(r.Context(), "failed to register OAuth client", err)
		WriteProblem(w, problem("temporarily_unavailable"))
		return
	}
	writeJSON(w, http.StatusCreated, c)
}
