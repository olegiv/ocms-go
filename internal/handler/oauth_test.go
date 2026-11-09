// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
	"github.com/olegiv/ocms-go/internal/store"
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
	// The shared fixture provides the OAuth tables. Add DCR admission for these tests.
	migration, err := os.ReadFile("../store/migrations/20261107180000_oauth_dcr_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err = env.db.Exec(up); err != nil {
		t.Fatal(err)
	}
	s, err := oauth.New(env.db, oauth.Options{Issuer: "https://cms.test", AllowedHosts: []string{"client.test"}, AllowDCR: true})
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

func TestOAuthPreLoginErrorsRespectClientSource(t *testing.T) {
	env := newMFATestEnv(t, false)
	values := enableTestOAuth(t, env)
	assertLocalError := func(t *testing.T, params url.Values, code string) {
		t.Helper()
		rr := authRequest(env.router, http.MethodGet, "/oauth/authorize?"+params.Encode(), nil)
		assertLocalOAuthProblem(t, rr, code)
	}

	t.Run("new dynamic client", func(t *testing.T) {
		params := maps.Clone(values)
		params.Set("response_type", "token")
		assertLocalError(t, params, "unsupported_response_type")
	})
	t.Run("existing dynamic client with registration disabled", func(t *testing.T) {
		original := env.h.oauth
		s, err := oauth.New(env.db, oauth.Options{Issuer: "https://cms.test"})
		if err != nil {
			t.Fatal(err)
		}
		env.h.SetOAuthService(s)
		t.Cleanup(func() { env.h.SetOAuthService(original) })
		params := maps.Clone(values)
		params.Set("scope", "openid")
		assertLocalError(t, params, "invalid_scope")
	})
	t.Run("metadata client keeps its error callback", func(t *testing.T) {
		const id = "https://client.test/metadata"
		err := store.New(env.db).UpsertOAuthClient(t.Context(), store.UpsertOAuthClientParams{
			ID: id, Name: "Test metadata client", Source: "cimd",
			RedirectUris: "[\"https://client.test/callback\"]", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		params := maps.Clone(values)
		params.Set("client_id", id)
		params.Set("scope", "openid")
		rr := authRequest(env.router, http.MethodGet, "/oauth/authorize?"+params.Encode(), nil)
		u := oauthCallbackResponse(t, rr)
		q := u.Query()
		if u.Scheme != "https" || u.Host != "client.test" || u.Path != "/callback" ||
			q.Get("error") != "invalid_scope" || q.Get("state") != "browser-state" || q.Get("iss") != "https://cms.test" {
			t.Fatalf("unexpected OAuth error callback %q", u)
		}
	})
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
	if formRR := authRequest(env.router, http.MethodGet, RouteLogin, nil, cookie); formRR.Code != 200 {
		t.Fatalf("fresh login form=%d", formRR.Code)
	}
	// The existing mfa_verified bit and a password-only attempt are insufficient.
	rr = authRequest(env.router, http.MethodPost, RouteLogin, url.Values{"email": {user.Email}, "password": {mfaTestPassword}}, cookie)
	if rr.Header().Get("Location") != RouteLoginMFA {
		t.Fatal("OAuth sign-in skipped second factor")
	}
	cookie = sessionCookie(t, rr, env.sm)
	if pendingRR := authRequest(env.router, http.MethodGet, target, nil, cookie); pendingRR.Header().Get("Location") != RouteLogin {
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
	if badRR := authRequest(env.router, http.MethodPost, "/oauth/authorize", bad, cookie); badRR.Code != 403 {
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
	if reuseRR := authRequest(env.router, http.MethodPost, "/oauth/authorize", form, cookie); reuseRR.Code != 403 {
		t.Fatal("consent nonce reused")
	}
	var consented, consentedScopes string
	if err := env.db.QueryRow(`SELECT id, scopes FROM oauth_grants WHERE user_id = ?`, user.ID).Scan(&consented, &consentedScopes); err != nil ||
		strings.Contains(consentedScopes, "pages:write") {
		t.Fatalf("consent did not use server-held scopes: %v", err)
	}
	// Consent alone is not a connection: nothing is listed until the code is
	// exchanged for a token (security audit 2026-11-03, A-3).
	if grants, err := env.h.oauth.ListGrants(t.Context(), user.ID); err != nil || len(grants) != 0 {
		t.Fatalf("a grant that issued no token is listed: %d %v", len(grants), err)
	}
	exchange := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {values.Get("client_id")},
		"redirect_uri": {values.Get("redirect_uri")}, "resource": {values.Get("resource")}, "code_verifier": {strings.Repeat("a", 64)}}.Encode()))
	exchange.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	issued := httptest.NewRecorder()
	env.h.oauth.Token(issued, exchange)
	if issued.Code != http.StatusOK {
		t.Fatalf("code exchange status=%d", issued.Code)
	}
	grants, err := env.h.oauth.ListGrants(t.Context(), user.ID)
	if err != nil || len(grants) != 1 || grants[0].ID != consented {
		t.Fatalf("redeemed grant not listed: %d %v", len(grants), err)
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
			if role == model.RolePublic {
				assertLocalOAuthProblem(t, rr, "access_denied")
			} else {
				u := oauthCallbackResponse(t, rr)
				if u.Query().Get("error") != "access_denied" || u.Query().Get("iss") != "https://cms.test" {
					t.Fatalf("denied redirect=%d", rr.Code)
				}
			}
			// A refused or denied consent is single-use: nothing of the pending
			// request may survive in the stored session.
			if pending, _ := env.session(t, cookie)(oauthPending).(string); pending != "" {
				t.Fatal("refused consent left the pending authorization in the session")
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

// TestRevokeOAuthGrantReportsAConnectionThatIsNotTheAccounts pins R1 P3-5: a
// revoke of a connection that does not exist, or belongs to another account,
// used to look like a success, and a storage failure was logged without its
// cause. The first now redirects with an error message and leaves the other
// account's connection alone; the second is a 500 whose log carries the error.
func TestRevokeOAuthGrantReportsAConnectionThatIsNotTheAccounts(t *testing.T) {
	env := newMFATestEnv(t, false)
	enableTestOAuth(t, env)
	owner := env.createUser(t, model.RoleEditor)
	other := env.createUser(t, model.RoleAdmin)
	if _, err := env.db.Exec(`INSERT INTO oauth_clients (id, name, redirect_uris, source, expires_at) VALUES ('client', 'Client', '[]', 'dcr', 9999999999)`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Exec(`INSERT INTO oauth_grants (id, user_id, client_id, scopes, resource, session_version, created_at, expires_at)
		VALUES ('foreign-grant', ?, 'client', 'mcp:access', 'https://cms.test/api/mcp', 0, 1, 9999999999)`, other.ID); err != nil {
		t.Fatal(err)
	}
	cookie, _ := env.signInWithPassword(t, owner)

	for name, grantID := range map[string]string{"unknown": "no-such-grant", "another account's": "foreign-grant"} {
		rr := authRequest(env.router, http.MethodPost, "/account/security/oauth/revoke", url.Values{"grant_id": {grantID}}, cookie)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != RouteAccountSecurity {
			t.Fatalf("%s grant: status=%d location=%q, want a redirect to the security page", name, rr.Code, rr.Header().Get("Location"))
		}
		flashed := env.session(t, sessionCookie(t, rr, env.sm))
		if flashed("flash_type") != "error" || flashed("flash") == "" {
			t.Fatalf("%s grant: revoke was reported as a success (flash %v %v)", name, flashed("flash_type"), flashed("flash"))
		}
	}
	var revoked int64
	if err := env.db.QueryRow(`SELECT revoked_at FROM oauth_grants WHERE id = 'foreign-grant'`).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatalf("another account's connection was revoked: revoked_at=%d err=%v", revoked, err)
	}

	// A storage failure is an internal error, and its cause is logged.
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	if _, err := env.db.Exec(`ALTER TABLE oauth_grants RENAME TO oauth_grants_gone`); err != nil {
		t.Fatal(err)
	}
	rr := authRequest(env.router, http.MethodPost, "/account/security/oauth/revoke", url.Values{"grant_id": {"foreign-grant"}}, cookie)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure status=%d, want 500", rr.Code)
	}
	if !strings.Contains(logs.String(), "oauth_grants") || !strings.Contains(logs.String(), "error=") {
		t.Fatalf("the revoke failure was logged without its error: %q", logs.String())
	}
}
