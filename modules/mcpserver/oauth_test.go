// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
)

func enableOAuth(t *testing.T, env *testEnv, scratch bool) *oauth.Service {
	t.Helper()
	s, err := oauth.New(env.db, oauth.Options{Issuer: "https://cms.test", AllowDCR: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := env.module.ctx
	ctx.OAuth = s
	ctx.Config.Env = "development"
	ctx.Config.MCPSpikeTools = scratch
	if err := env.module.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func oauthToken(t *testing.T, s *oauth.Service, userID int64, scopes string) (string, oauth.Principal) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"Integration client","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Register(w, r)
	if w.Code != 201 {
		t.Fatalf("DCR=%d", w.Code)
	}
	var c struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(verifier))
	v := url.Values{"client_id": {c.ID}, "redirect_uri": {"https://client.test/callback"}, "response_type": {"code"},
		"scope": {scopes}, "resource": {s.Resource()}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
	req, err := s.Prepare(t.Context(), v)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.Authorize(t.Context(), req, userID)
	if err != nil {
		t.Fatal(err)
	}
	v.Set("code", code)
	v.Set("code_verifier", verifier)
	v.Set("grant_type", "authorization_code")
	r = httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.Token(w, r)
	if w.Code != 200 {
		t.Fatalf("exchange=%d", w.Code)
	}
	var body struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	p, err := s.VerifyAccess(t.Context(), body.Access)
	if err != nil {
		t.Fatal(err)
	}
	return body.Access, p
}

func TestOAuthMCPPrincipalScratchRevocationAndKeyCompatibility(t *testing.T) {
	env := newTestEnv(t)
	s := enableOAuth(t, env, true)
	token, p := oauthToken(t, s, env.userID, "mcp:access mcp:drafts")
	session := env.connect(token)
	info := decodeResult[SiteInfo](t, call(t, session, "get_site_info", nil))
	if info.Access.UserID != env.userID || info.Access.AuthType != "oauth" || info.Access.GrantID != p.GrantID || info.Access.ClientID != p.ClientID || info.Access.ExpiresAt == nil || info.Server.ReadOnly {
		t.Fatalf("wrong verified principal: %+v", info.Access)
	}
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name == "spike_save_draft" {
			found = true
			if tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || tool.Meta["securitySchemes"] == nil {
				t.Fatal("scratch write misclassified")
			}
		}
	}
	if !found {
		t.Fatal("scratch tool not discovered")
	}
	var pagesBefore, pagesAfter int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&pagesBefore); err != nil {
		t.Fatal(err)
	}
	entry := decodeResult[scratchEntry](t, call(t, session, "spike_save_draft", map[string]any{"title": "Scratch", "body": "Never published"}))
	got := decodeResult[scratchEntry](t, call(t, session, "spike_get_draft", map[string]any{"id": entry.ID}))
	if got.Body != entry.Body {
		t.Fatal("scratch read differs")
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&pagesAfter); err != nil || pagesAfter != pagesBefore {
		t.Fatal("scratch changed CMS pages")
	}
	otherUser := env.createUser("other@example.test", "Other")
	otherToken, _ := oauthToken(t, s, otherUser, "mcp:access mcp:drafts")
	foreign := call(t, env.connect(otherToken), "spike_get_draft", map[string]any{"id": entry.ID})
	if code, _, _ := toolErrorBody(t, foreign); code != "not_found" {
		t.Fatal("foreign scratch entry disclosed")
	}
	limitedToken, _ := oauthToken(t, s, env.userID, "mcp:access")
	denied := call(t, env.connect(limitedToken), "spike_save_draft", map[string]any{"title": "Denied", "body": ""})
	if !denied.IsError || denied.Meta["mcp/www_authenticate"] == nil {
		t.Fatal("missing tool-level OAuth scope challenge")
	}
	if err := s.RevokeGrant(t.Context(), env.userID, p.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_site_info", Arguments: map[string]any{}}); err == nil {
		t.Fatal("existing client session survived revocation")
	}
	resp := env.rawPost(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("revoked token missing discovery challenge")
	}
	key := env.createKey(model.PermissionMCPAccess)
	keyInfo := decodeResult[SiteInfo](t, call(t, env.connect(key), "get_site_info", nil))
	if keyInfo.Access.AuthType != "api_key" || keyInfo.Access.UserID != env.userID {
		t.Fatal("legacy bearer principal changed")
	}
	logs := env.logs.text()
	for _, secret := range []string{token, key, "Never published"} {
		if strings.Contains(logs, secret) {
			t.Fatal("MCP log leaked credential/content")
		}
	}
	if !strings.Contains(logs, "grant_id="+p.GrantID) || !strings.Contains(logs, fmt.Sprintf("user_id=%d", env.userID)) {
		t.Fatal("verified audit identity missing")
	}
}

func TestMCPRejectsChangedAccountAndInvalidOAuthWithoutKeyFallback(t *testing.T) {
	env := newTestEnv(t)
	s := enableOAuth(t, env, false)
	token, _ := oauthToken(t, s, env.userID, "mcp:access")
	session := env.connect(token)
	keySession := env.connect(env.createKey(model.PermissionMCPAccess))
	if _, err := env.db.Exec(`UPDATE users SET role='public' WHERE id=?`, env.userID); err != nil {
		t.Fatal(err)
	}
	for _, client := range []*mcp.ClientSession{session, keySession} {
		if _, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_site_info", Arguments: map[string]any{}}); err == nil {
			t.Fatal("changed account retained MCP access")
		}
	}
	resp := env.rawPost(`{}`, map[string]string{"Authorization": "Bearer ocms.oauth.at.invalid"})
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("invalid OAuth fell into API-key verifier")
	}
}

func TestScratchBoundsExpiryAndStartupGates(t *testing.T) {
	env := newTestEnv(t)
	enableOAuth(t, env, true)
	m := env.module
	id := &identity{userID: env.userID, scopes: []string{"mcp:drafts"}}
	call := &toolCall{identity: id}
	if _, err := m.saveScratch(t.Context(), call, scratchInput{Title: "X", Body: strings.Repeat("x", 8192)}); err == nil {
		t.Fatal("oversized scratch accepted")
	}
	first, err := m.saveScratch(t.Context(), call, scratchInput{Title: "One"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.saveScratch(t.Context(), call, scratchInput{Title: "Two"})
	if err != nil || len(m.scratch.entries) != 1 {
		t.Fatal("more than one entry per user")
	}
	if _, err := m.getScratch(t.Context(), call, scratchID{ID: first.ID}); err == nil {
		t.Fatal("replaced ID still valid")
	}
	for user := int64(2); user <= 100; user++ {
		if _, err := m.saveScratch(t.Context(), &toolCall{identity: &identity{userID: user, scopes: id.scopes}}, scratchInput{Title: "Bounded"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.saveScratch(t.Context(), &toolCall{identity: &identity{userID: 101, scopes: id.scopes}}, scratchInput{Title: "Overflow"}); err == nil {
		t.Fatal("scratch capacity exceeded")
	}
	m.scratch.now = func() time.Time { return second.ExpiresAt.Add(time.Second) }
	if _, err := m.getScratch(t.Context(), call, scratchID{ID: second.ID}); err == nil {
		t.Fatal("expired scratch readable")
	}
	if _, err := m.saveScratch(t.Context(), call, scratchInput{Title: "After expiry"}); err != nil || len(m.scratch.entries) != 1 {
		t.Fatal("expired entries not removed")
	}
	m.ctx.Config.AIDisabled = true
	if len(m.catalog()) != len(toolCatalog()) {
		t.Fatal("kill switch left scratch tools listed")
	}
	if _, err := m.saveScratch(t.Context(), call, scratchInput{Title: "Disabled"}); err == nil {
		t.Fatal("kill switch left writes enabled")
	}
	m.ctx.Config.Env = "production"
	if err := New().Init(m.ctx); err == nil {
		t.Fatal("production scratch flag accepted")
	}
}
