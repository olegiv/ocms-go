// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/oauth"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
)

const (
	oauthPending = "oauth_pending_query"
	oauthNonce   = "oauth_consent_nonce"
)

// SetOAuthService enables the opt-in consent and connection-management surfaces.
func (h *AuthHandler) SetOAuthService(s *oauth.Service) { h.oauth = s }

// OAuthAuthorize binds consent to a fresh complete sign-in and a server-held request.
func (h *AuthHandler) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if middleware.IsDemoMode() {
		http.Error(w, "OAuth authorization is disabled in demo mode", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		h.oauthConsent(w, r)
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		http.Error(w, "Invalid authorization request", 400)
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		oauth.WriteProblem(w, &oauth.Problem{Code: "invalid_request"})
		return
	}
	req, err := h.oauth.Prepare(r.Context(), values)
	if err != nil {
		h.oauthPreConsentError(w, r, req, err)
		return
	}
	ctx := r.Context()
	canonical := values.Encode()
	if h.sessionManager.GetString(ctx, oauthPending) != canonical {
		h.sessionManager.Put(ctx, oauthPending, canonical)
		h.sessionManager.Put(ctx, middleware.SessionKeyOAuthReauthAfter, time.Now().UnixNano())
		h.sessionManager.Remove(ctx, oauthNonce)
	}
	since := h.sessionManager.GetInt64(ctx, middleware.SessionKeyOAuthReauthAfter)
	if since <= 0 || time.Since(time.Unix(0, since)) > 10*time.Minute {
		h.clearOAuthConsent(r)
		http.Redirect(w, r, "/oauth/authorize?"+canonical, http.StatusSeeOther)
		return
	}
	user, ok := middleware.SessionUser(ctx, h.sessionManager, h.queries)
	if !ok || h.sessionManager.GetInt64(ctx, middleware.SessionKeyAuthenticatedAt) < since {
		h.sessionManager.Put(ctx, middleware.SessionKeyOAuthReturnTo, "/oauth/authorize?"+canonical)
		http.Redirect(w, r, RouteLogin, http.StatusSeeOther)
		return
	}
	if len(oauth.IntersectScopes(strings.Fields(req.Scope), user.Role)) != len(strings.Fields(req.Scope)) {
		// Clear first: the session is saved on the response's first write.
		h.clearOAuthConsent(r)
		h.oauthPreConsentError(w, r, req, &oauth.Problem{Code: "access_denied"})
		return
	}
	nonce := rand.Text()
	h.sessionManager.Put(ctx, oauthNonce, nonce)
	renderTempl(w, r, accountviews.OAuthConsent(h.accountPageContext(r, &user, "oauth.consent_title"),
		accountviews.ConsentData{Name: req.ClientName, ClientID: req.ClientID, ClientHost: consentURLHost(req.ClientID),
			RedirectHost: consentURLHost(req.RedirectURI), Scopes: strings.Fields(req.Scope), Nonce: nonce}))
}

// oauthPreConsentError keeps dynamic-client errors local until a consent decision.
func (h *AuthHandler) oauthPreConsentError(w http.ResponseWriter, r *http.Request, req oauth.AuthorizationRequest, err error) {
	if redirect := h.oauth.PreConsentErrorRedirect(req, protocolErrorCode(err)); redirect != "" {
		oauthClientRedirect(w, r, redirect)
	} else {
		oauth.WriteProblem(w, err)
	}
}

func consentURLHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Host
}

func protocolErrorCode(err error) string {
	var p *oauth.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return "invalid_request"
}

func (h *AuthHandler) oauthConsent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid consent", 400)
		return
	}
	ctx := r.Context()
	nonce := h.sessionManager.GetString(ctx, oauthNonce)
	since := h.sessionManager.GetInt64(ctx, middleware.SessionKeyOAuthReauthAfter)
	user, ok := middleware.SessionUser(ctx, h.sessionManager, h.queries)
	if !ok || nonce == "" || len(r.PostForm["consent_token"]) != 1 ||
		subtle.ConstantTimeCompare([]byte(nonce), []byte(r.PostForm.Get("consent_token"))) != 1 ||
		since <= 0 || time.Since(time.Unix(0, since)) > 10*time.Minute ||
		h.sessionManager.GetInt64(ctx, middleware.SessionKeyAuthenticatedAt) < since {
		http.Error(w, "Consent expired or invalid", http.StatusForbidden)
		return
	}
	values, err := url.ParseQuery(h.sessionManager.GetString(ctx, oauthPending))
	if err != nil {
		http.Error(w, "Invalid consent", 400)
		return
	}
	req, err := h.oauth.Prepare(ctx, values)
	if err != nil {
		oauth.WriteProblem(w, err)
		return
	}
	h.clearOAuthConsent(r) // A consent decision is single-use, including a failed grant.
	if r.PostForm.Get("decision") != "allow" {
		oauthClientRedirect(w, r, h.oauth.Redirect(req, url.Values{"error": {"access_denied"}}))
		return
	}
	code, err := h.oauth.Authorize(oauth.WithRequest(ctx, r), req, user.ID)
	if err != nil {
		oauthClientRedirect(w, r, h.oauth.Redirect(req, url.Values{"error": {protocolErrorCode(err)}}))
		return
	}
	oauthClientRedirect(w, r, h.oauth.Redirect(req, url.Values{"code": {code}}))
}

// oauthClientRedirect receives only callback URLs validated by Service.Redirect.
// A fresh document performs a GET navigation: Chrome otherwise applies the
// consent form's form-action 'self' policy to the cross-origin HTTP redirect.
func oauthClientRedirect(w http.ResponseWriter, r *http.Request, target string) {
	renderTempl(w, r, accountviews.OAuthRedirect(middleware.GetAdminLang(r), target))
}

func (h *AuthHandler) clearOAuthConsent(r *http.Request) {
	for _, key := range []string{oauthPending, oauthNonce, middleware.SessionKeyOAuthReturnTo, middleware.SessionKeyOAuthReauthAfter} {
		h.sessionManager.Remove(r.Context(), key)
	}
}

// RevokeOAuthGrant removes only the current account's selected connection.
func (h *AuthHandler) RevokeOAuthGrant(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil {
		http.NotFound(w, r)
		return
	}
	user := accountUser(w, r)
	if user == nil || demoGuard(w, r, h.renderer, middleware.RestrictionAPIKeys, RouteAccountSecurity) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.PostForm.Get("grant_id")) > 128 {
		http.Error(w, "Invalid connection", 400)
		return
	}
	if err := h.oauth.RevokeGrant(oauth.WithRequest(r.Context(), r), user.ID, r.PostForm.Get("grant_id")); err != nil {
		if errors.Is(err, oauth.ErrGrantNotFound) {
			// Not an error of ours: the connection is gone, was never this
			// account's, or the form is stale. Say so instead of claiming success.
			flashError(w, r, h.renderer, RouteAccountSecurity, i18n.T(middleware.GetAdminLang(r), "oauth.connection_not_found"))
			return
		}
		logAndInternalError(w, "failed to revoke OAuth grant", "error", err, "user_id", user.ID)
		return
	}
	http.Redirect(w, r, RouteAccountSecurity, http.StatusSeeOther)
}
