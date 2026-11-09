// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
	"github.com/olegiv/ocms-go/internal/store"
)

func assertLocalOAuthProblem(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" ||
		response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("expected local OAuth problem, got status %d and content type %q", response.Code, response.Header().Get("Content-Type"))
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] != code || len(body) != 2 {
		t.Fatalf("expected bounded OAuth problem %q: %v", code, err)
	}
	for _, unwanted := range []string{"oauth-continue", "window.location", "browser-state", "client.test", "127.0.0.1"} {
		if strings.Contains(response.Body.String(), unwanted) {
			t.Fatalf("local problem contains callback navigation or request data: %s", unwanted)
		}
	}
}

func oauthConsentPolicyClient(t *testing.T, env *mfaTestEnv, kind string) url.Values {
	t.Helper()
	values := enableTestOAuth(t, env)
	options := oauth.Options{Issuer: "https://cms.test"}
	switch kind {
	case "dcr":
		return values
	case "dcr-loopback":
		// A stored native registration permits a different callback port. Its
		// provenance, rather than the callback spelling, controls error redirects.
		if _, err := env.db.Exec(`UPDATE oauth_clients SET redirect_uris = ? WHERE id = ?`,
			`["http://127.0.0.1:8100/callback?native=yes"]`, values.Get("client_id")); err != nil {
			t.Fatal(err)
		}
		values.Set("redirect_uri", "http://127.0.0.1:9271/callback?native=yes")
		return values
	case "dcr-registration-disabled":
		// Existing DCR rows remain dynamic when new registration is disabled.
	case "cimd", "pinned":
		const id = "https://client.test/metadata"
		values.Set("client_id", id)
		if kind == "cimd" {
			err := store.New(env.db).UpsertOAuthClient(t.Context(), store.UpsertOAuthClientParams{
				ID: id, Name: "Cached metadata client", Source: "cimd",
				RedirectUris: `["https://client.test/callback"]`, ExpiresAt: time.Now().Add(time.Hour).Unix(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return values
		}
		options.AllowedHosts = []string{"client.test"}
		options.PreRegisteredClients = `[{"client_id":"https://client.test/metadata","client_name":"Pinned client","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}]`
	default:
		t.Fatalf("unknown client kind %s", kind)
	}
	service, err := oauth.New(env.db, options)
	if err != nil {
		t.Fatal(err)
	}
	env.h.SetOAuthService(service)
	return values
}

func TestOAuthPreConsentErrorsRespectClientSource(t *testing.T) {
	for _, kind := range []string{"dcr", "dcr-loopback", "dcr-registration-disabled", "cimd", "pinned"} {
		for _, scenario := range []string{"public", "public-mfa", "allow", "deny", "downgraded-after-consent"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				env := newMFATestEnv(t, false)
				values := oauthConsentPolicyClient(t, env, kind)
				role := model.RoleEditor
				preConsentDenial := strings.HasPrefix(scenario, "public")
				if preConsentDenial {
					role = model.RolePublic
				} else if scenario == "deny" {
					role = model.RoleAdmin
				}
				user := env.createUser(t, role)
				var secret string
				if scenario == "public-mfa" {
					secret, _ = env.enroll(t, user)
				}
				target := "/oauth/authorize?" + values.Encode()
				response := authRequest(env.router, http.MethodGet, target, nil)
				if response.Header().Get("Location") != RouteLogin {
					t.Fatal("authorization did not require fresh sign-in")
				}
				cookie := sessionCookie(t, response, env.sm)
				response = authRequest(env.router, http.MethodPost, RouteLogin,
					url.Values{"email": {user.Email}, "password": {mfaTestPassword}}, cookie)
				cookie = sessionCookie(t, response, env.sm)
				if secret != "" {
					if response.Header().Get("Location") != RouteLoginMFA {
						t.Fatal("sign-in skipped MFA")
					}
					response = authRequest(env.router, http.MethodPost, RouteLoginMFA, url.Values{"code": {totpCode(t, secret, 0)}}, cookie)
					cookie = sessionCookie(t, response, env.sm)
				}
				if response.Header().Get("Location") != target {
					t.Fatal("completed sign-in did not resume authorization")
				}
				response = authRequest(env.router, http.MethodGet, target, nil, cookie)
				if !preConsentDenial {
					cookie = sessionCookie(t, response, env.sm)
					nonce, _ := env.session(t, cookie)(oauthNonce).(string)
					if response.Code != http.StatusOK || nonce == "" || oauthHTMLAttribute(t, response.Body.String(), "input", "name", "consent_token", "value") != nonce {
						t.Fatal("eligible account did not receive bound consent")
					}
					if scenario == "downgraded-after-consent" {
						if _, err := env.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, model.RolePublic, user.ID); err != nil {
							t.Fatal(err)
						}
					}
					decision := "allow"
					if scenario == "deny" {
						decision = "deny"
					}
					response = authRequest(env.router, http.MethodPost, "/oauth/authorize", url.Values{"consent_token": {nonce}, "decision": {decision}}, cookie)
				}
				if preConsentDenial && strings.HasPrefix(kind, "dcr") {
					assertLocalOAuthProblem(t, response, "access_denied")
				} else {
					callback := oauthCallbackResponse(t, response)
					expected, err := url.Parse(values.Get("redirect_uri"))
					if err != nil {
						t.Fatal(err)
					}
					query := callback.Query()
					if callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path ||
						query.Get("native") != expected.Query().Get("native") || query.Get("state") != values.Get("state") || query.Get("iss") != "https://cms.test" {
						t.Fatal("callback lost its validated destination or bindings")
					}
					if scenario == "allow" {
						if query.Get("code") == "" || query.Get("error") != "" {
							t.Fatal("explicit consent did not issue a code")
						}
						exchange := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(url.Values{
							"grant_type": {"authorization_code"}, "code": {query.Get("code")}, "client_id": {values.Get("client_id")},
							"redirect_uri": {values.Get("redirect_uri")}, "resource": {values.Get("resource")}, "code_verifier": {strings.Repeat("a", 64)},
						}.Encode()))
						exchange.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						issued := httptest.NewRecorder()
						env.h.oauth.Token(issued, exchange)
						if issued.Code != http.StatusOK {
							t.Fatalf("normal code exchange failed: %d", issued.Code)
						}
					} else if query.Get("error") != "access_denied" || query.Get("code") != "" {
						t.Fatal("refusal did not retain its denial callback")
					}
				}
				cookie = sessionCookie(t, response, env.sm)
				readSession := env.session(t, cookie)
				for _, key := range []string{oauthPending, oauthNonce, middleware.SessionKeyOAuthReturnTo, middleware.SessionKeyOAuthReauthAfter} {
					if readSession(key) != nil {
						t.Fatalf("consent result retained session key %s", key)
					}
				}
				if readSession(middleware.SessionKeyUserID) != user.ID || readSession(middleware.SessionKeyAuthenticatedAt) == nil {
					t.Fatal("consent result discarded the authenticated account")
				}
				if scenario != "allow" {
					var writes int
					err := env.db.QueryRow(`SELECT (SELECT COUNT(*) FROM oauth_grants) + (SELECT COUNT(*) FROM oauth_codes) + (SELECT COUNT(*) FROM oauth_tokens)`).Scan(&writes)
					if err != nil || writes != 0 {
						t.Fatal("refusal persisted grant/code/token state", err)
					}
				}
				if preConsentDenial {
					retry := authRequest(env.router, http.MethodGet, target, nil, cookie)
					if retry.Header().Get("Location") != RouteLogin {
						t.Fatal("refused request reused its completed sign-in")
					}
				}
			})
		}
	}
}
