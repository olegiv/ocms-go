// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestMetadataIssuerAndPermissionContract(t *testing.T) {
	f := newFixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	r.Host = "attacker.test"
	r.Header.Set("X-Forwarded-Host", "attacker.test")
	f.s.Discovery(w, r)
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["issuer"] != "https://cms.test" || m["client_id_metadata_document_supported"] != true || m["authorization_response_iss_parameter_supported"] != true || m["registration_endpoint"] != nil {
		t.Fatal("discovery is not the configured opt-in protocol")
	}
	for name, want := range map[string]string{"response_types_supported": "code", "code_challenge_methods_supported": "S256", "token_endpoint_auth_methods_supported": "none"} {
		values, ok := m[name].([]any)
		if !ok || len(values) != 1 || values[0] != want {
			t.Errorf("%s=%v", name, m[name])
		}
	}
	for _, role := range []string{model.RoleAdmin, model.RoleEditor} {
		for _, scope := range RoleScopes(role) {
			if !slices.Contains(model.AllPermissions(), scope) || strings.Contains(scope, "openid") {
				t.Fatalf("scope has no permission: %s", scope)
			}
		}
	}
	if len(RoleScopes(model.RolePublic)) != 0 || len(RoleScopes("unknown")) != 0 {
		t.Fatal("unprivileged role has OAuth access")
	}
	w = httptest.NewRecorder()
	f.s.ProtectedResourceMetadata().ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "attacker.test") || !strings.Contains(w.Body.String(), f.s.Resource()) {
		t.Fatal("protected resource metadata is not bound to configured site")
	}
	for _, issuer := range []string{"http://cms.test", "http://localhost:8080", "https://cms.test/path", "https://cms.test?x=1", "https://cms.test?", "https://user@cms.test"} {
		if _, err := New(f.db, Options{Issuer: issuer, Development: true}); err == nil {
			t.Errorf("unsafe issuer accepted: %s", issuer)
		}
	}
	if _, err := New(f.db, Options{Issuer: "http://127.0.0.1:8080", Development: true}); err != nil {
		t.Fatal(err)
	}
}

func TestTokenEndpointRejectsUnsupportedAndAmbiguousRequests(t *testing.T) {
	f := newFixture(t)
	for _, grant := range []string{"password", "client_credentials", "implicit", "urn:ietf:params:oauth:grant-type:jwt-bearer"} {
		status, body := f.exchange(url.Values{"grant_type": {grant}})
		if status != 400 || body["error"] != "unsupported_grant_type" {
			t.Errorf("grant=%s status=%d", grant, status)
		}
	}
	for _, tc := range []struct{ method, query, contentType, body, authorization string }{
		{method: "GET"},
		{method: "POST", query: "?code=never-in-query", contentType: "application/x-www-form-urlencoded", body: "grant_type=authorization_code"},
		{method: "POST", contentType: "application/x-www-form-urlencoded-bogus", body: "grant_type=authorization_code"},
		{method: "POST", contentType: "application/x-www-form-urlencoded", body: "grant_type=authorization_code&grant_type=refresh_token"},
		{method: "POST", contentType: "application/x-www-form-urlencoded", body: strings.Repeat("x", (16<<10)+1)},
		{method: "POST", contentType: "application/x-www-form-urlencoded", body: "grant_type=authorization_code", authorization: "Basic denied"},
	} {
		r := httptest.NewRequest(tc.method, "/oauth/token"+tc.query, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set("Authorization", tc.authorization)
		w := httptest.NewRecorder()
		f.s.Token(w, r)
		if w.Code != 400 && w.Code != 405 {
			t.Errorf("ambiguous request status=%d", w.Code)
		}
	}
}

func TestTokenClientRedirectResourceAndExpiryBindings(t *testing.T) {
	for _, field := range []string{"redirect_uri", "resource", "client_id", "code_verifier", "expired_code"} {
		t.Run(field, func(t *testing.T) {
			f := newFixture(t)
			code := f.code(t)
			v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}}
			switch field {
			case "expired_code":
				if _, err := f.db.Exec(`UPDATE oauth_codes SET expires_at=0`); err != nil {
					t.Fatal(err)
				}
			case "code_verifier":
				v.Set(field, "short")
			default:
				v.Set(field, "https://attacker.test/other")
			}
			status, body := f.exchange(v)
			if status != 400 || access(body) != "" {
				t.Fatalf("invalid %s accepted: %d", field, status)
			}
		})
	}
}

func TestGrantScopeReductionAppliesToAccessAndRefresh(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	if _, err := f.db.Exec(`UPDATE oauth_grants SET scopes='mcp:access'`); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.VerifyAccess(t.Context(), access(body))
	if err != nil || !slices.Equal(p.Scopes, []string{"mcp:access"}) {
		t.Fatal("access ignored grant scope reduction")
	}
	status, denied := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}, "scope": {"mcp:drafts"}})
	if status != 400 || denied["error"] != "invalid_scope" {
		t.Fatal("refresh ignored grant scope reduction")
	}
	status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}})
	if status != 200 || next["scope"] != "mcp:access" {
		t.Fatalf("refresh resurrected old scope: %d", status)
	}
}
