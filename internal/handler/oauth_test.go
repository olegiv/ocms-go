// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
	"golang.org/x/net/html"
)

func TestConsentURLHost(t *testing.T) {
	for _, tc := range []struct{ raw, host string }{
		{"https://client.test:8443/callback", "client.test:8443"},
		{"http://127.0.0.1:8100/callback", "127.0.0.1:8100"},
		{"opaque-client-id", ""},
		{"https://client.test/%zz", ""},
	} {
		if got := consentURLHost(tc.raw); got != tc.host {
			t.Errorf("consentURLHost(%q) = %q, want %q", tc.raw, got, tc.host)
		}
	}
}

func enableTestOAuth(t *testing.T, env *mfaTestEnv) url.Values {
	t.Helper()
	// The shared handler fixture has a minimal schema rather than goose's
	// migration history. Apply the actual OAuth migration, not a mirrored schema.
	migration, err := os.ReadFile("../store/migrations/20261030170000_create_oauth.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err := env.db.Exec(up); err != nil {
		t.Fatal(err)
	}
	s, err := oauth.New(env.db, oauth.Options{Issuer: "https://cms.test", AllowDCR: true})
	if err != nil {
		t.Fatal(err)
	}
	env.h.SetOAuthService(s)
	r := env.router.(*chi.Mux)
	r.Get(RouteLogin, env.h.LoginForm)
	r.Get("/oauth/authorize", env.h.OAuthAuthorize)
	r.Post("/oauth/authorize", env.h.OAuthAuthorize)
	r.With(middleware.Auth(env.sm), middleware.LoadUser(env.sm, env.db)).Post("/account/security/oauth/revoke", env.h.RevokeOAuthGrant)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"Test <script>client</script>","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}`))
	req.Header.Set("Content-Type", "application/json")
	s.Register(w, req)
	if w.Code != 201 {
		t.Fatalf("register: %d", w.Code)
	}
	var client struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(strings.Repeat("a", 64)))
	env.router = middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false))(env.router)
	return url.Values{"client_id": {client.ID}, "redirect_uri": {"https://client.test/callback"},
		"response_type": {"code"}, "scope": {"mcp:access mcp:drafts"}, "resource": {s.Resource()},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "state": {"browser-state"}}
}

func oauthHTMLAttribute(t *testing.T, body, tag, key, value, attribute string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var found string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			attributes := make(map[string]string, len(n.Attr))
			for _, a := range n.Attr {
				attributes[a.Key] = a.Val
			}
			if attributes[key] == value {
				found = attributes[attribute]
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if found == "" {
		t.Fatalf("missing %s attribute on %s %s=%q", attribute, tag, key, value)
	}
	return found
}

func oauthCallbackResponse(t *testing.T, rr *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	if rr.Code != http.StatusOK || rr.Header().Get("Location") != "" {
		t.Fatalf("OAuth callback must leave the form POST with a navigation page, got %d", rr.Code)
	}
	if rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(rr.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal("callback navigation weakened the browser security policy")
	}
	body := rr.Body.String()
	target := oauthHTMLAttribute(t, body, "a", "id", "oauth-continue", "href")
	nonce := oauthHTMLAttribute(t, body, "script", "type", "", "nonce")
	if !strings.Contains(rr.Header().Get("Content-Security-Policy"), "'nonce-"+nonce+"'") ||
		!strings.Contains(body, "window.location.replace(") {
		t.Fatal("callback navigation script is missing its CSP nonce")
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOAuthConsentRequiresFreshLoginAndMFA(t *testing.T) {
	env := newMFATestEnv(t, false)
	values := enableTestOAuth(t, env)
	user := env.createUser(t, model.RoleEditor)
	secret, _ := env.enroll(t, user)
	cookie, _ := env.signInWithPassword(t, user)
	rr := authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
	cookie = sessionCookie(t, rr, env.sm)
	target := "/oauth/authorize?" + values.Encode()
	rr = authRequest(env.router, http.MethodGet, target, nil, cookie)
	if rr.Header().Get("Location") != RouteLogin {
		t.Fatalf("old session bypassed fresh login: %d", rr.Code)
	}
	cookie = sessionCookie(t, rr, env.sm)
	if rr := authRequest(env.router, http.MethodGet, RouteLogin, nil, cookie); rr.Code != 200 {
		t.Fatalf("fresh login form=%d", rr.Code)
	}
	// The existing mfa_verified bit and a password-only attempt are insufficient.
	rr = authRequest(env.router, http.MethodPost, RouteLogin, url.Values{"email": {user.Email}, "password": {mfaTestPassword}}, cookie)
	if rr.Header().Get("Location") != RouteLoginMFA {
		t.Fatal("OAuth sign-in skipped second factor")
	}
	cookie = sessionCookie(t, rr, env.sm)
	if rr := authRequest(env.router, http.MethodGet, target, nil, cookie); rr.Header().Get("Location") != RouteLogin {
		t.Fatal("pending MFA reached consent")
	}
	// Enrollment used period -1, the first login period 0; use period +1 now.
	rr = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 1)}}, cookie)
	if rr.Header().Get("Location") != target {
		t.Fatalf("MFA did not resume authorization: %q", rr.Header().Get("Location"))
	}
	cookie = sessionCookie(t, rr, env.sm)
	rr = authRequest(env.router, http.MethodGet, target, nil, cookie)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `name="consent_token"`) {
		t.Fatalf("consent=%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "<script>client</script>") {
		t.Fatal("client display name rendered as HTML")
	}
	cookie = sessionCookie(t, rr, env.sm)
	nonce, _ := env.session(t, cookie)(oauthNonce).(string)
	if nonce == "" {
		t.Fatal("no server-held consent nonce")
	}
	if rendered := oauthHTMLAttribute(t, rr.Body.String(), "input", "name", "consent_token", "value"); rendered != nonce {
		t.Fatal("rendered consent token differs from the session token")
	}
	bad := url.Values{"consent_token": {"wrong"}, "decision": {"allow"}}
	if rr := authRequest(env.router, http.MethodPost, "/oauth/authorize", bad, cookie); rr.Code != 403 {
		t.Fatal("bad consent nonce accepted")
	}
	form := url.Values{"consent_token": {nonce}, "decision": {"allow"}, "redirect_uri": {"https://attacker.test"}, "scope": {"pages:write"}}
	// Cross-site POST is rejected by the same middleware used in main.
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross-site consent accepted")
	}
	rr = authRequest(env.router, http.MethodPost, "/oauth/authorize", form, cookie)
	u := oauthCallbackResponse(t, rr)
	if u.Host != "client.test" || u.Query().Get("code") == "" || u.Query().Get("iss") != "https://cms.test" || u.Query().Get("state") != "browser-state" {
		t.Fatalf("consent response status=%d (invalid redirect)", rr.Code)
	}
	cookie = sessionCookie(t, rr, env.sm)
	if rr := authRequest(env.router, http.MethodPost, "/oauth/authorize", form, cookie); rr.Code != 403 {
		t.Fatal("consent nonce reused")
	}
	grants, err := env.h.oauth.ListGrants(t.Context(), user.ID)
	if err != nil || len(grants) != 1 || strings.Contains(grants[0].Scopes, "pages:write") {
		t.Fatal("consent did not use server-held scopes")
	}
	rr = authRequest(env.router, http.MethodGet, RouteAccountSecurity, nil, cookie)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), grants[0].ID) {
		t.Fatalf("connection list=%d", rr.Code)
	}
	rr = authRequest(env.router, http.MethodPost, "/account/security/oauth/revoke", url.Values{"grant_id": {grants[0].ID}}, cookie)
	if rr.Header().Get("Location") != RouteAccountSecurity {
		t.Fatalf("revoke=%d", rr.Code)
	}
	grants, err = env.h.oauth.ListGrants(t.Context(), user.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("revoked connection still listed")
	}
}

func TestOAuthConsentDenialPublicRoleAndUnregisteredRedirect(t *testing.T) {
	for _, role := range []string{model.RoleEditor, model.RolePublic} {
		t.Run(role, func(t *testing.T) {
			env := newMFATestEnv(t, false)
			values := enableTestOAuth(t, env)
			user := env.createUser(t, role)
			target := "/oauth/authorize?" + values.Encode()
			rr := authRequest(env.router, http.MethodGet, target, nil)
			cookie := sessionCookie(t, rr, env.sm)
			rr = authRequest(env.router, http.MethodPost, RouteLogin, url.Values{"email": {user.Email}, "password": {mfaTestPassword}}, cookie)
			cookie = sessionCookie(t, rr, env.sm)
			rr = authRequest(env.router, http.MethodGet, target, nil, cookie)
			if role == model.RoleEditor {
				cookie = sessionCookie(t, rr, env.sm)
				nonce, _ := env.session(t, cookie)(oauthNonce).(string)
				rr = authRequest(env.router, http.MethodPost, "/oauth/authorize", url.Values{"consent_token": {nonce}, "decision": {"deny"}}, cookie)
			}
			u := oauthCallbackResponse(t, rr)
			if u.Query().Get("error") != "access_denied" || u.Query().Get("iss") != "https://cms.test" {
				t.Fatalf("denied redirect=%d", rr.Code)
			}
			grants, err := env.h.oauth.ListGrants(t.Context(), user.ID)
			if err != nil || len(grants) != 0 {
				t.Fatal("denied consent created grant")
			}
			values.Set("redirect_uri", "https://attacker.test/callback")
			rr = authRequest(env.router, http.MethodGet, "/oauth/authorize?"+values.Encode(), nil, cookie)
			if rr.Code != 400 || rr.Header().Get("Location") != "" {
				t.Fatal("unregistered URI received redirect")
			}
		})
	}
}
