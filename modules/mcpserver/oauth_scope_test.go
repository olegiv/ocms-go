// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestOAuthToolScopeHTTPChallenge(t *testing.T) {
	env := newTestEnv(t)
	s := enableOAuth(t, env, true)
	token, _ := oauthToken(t, s, env.userID, "mcp:access")
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, name := range []string{"spike_save_draft", "spike_get_draft"} {
		args := `{"id":"unknown"}`
		if name == "spike_save_draft" {
			args = `{"title":"Denied","body":"Scratch only"}`
		}
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		resp := env.rawPost(body, headers)
		challenge := resp.Header.Get("WWW-Authenticate")
		if resp.StatusCode != http.StatusForbidden ||
			!strings.Contains(challenge, `error="insufficient_scope"`) ||
			!strings.Contains(challenge, `scope="mcp:access mcp:drafts"`) ||
			!strings.Contains(challenge, `resource_metadata="https://cms.test/.well-known/oauth-protected-resource/api/mcp"`) {
			t.Fatalf("%s: status=%d challenge=%q; want HTTP scope step-up", name, resp.StatusCode, challenge)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("scope challenge may be cached")
		}
	}
	if len(env.module.scratch.entries) != 0 {
		t.Fatal("insufficient-scope request executed a scratch write")
	}
	info := decodeResult[SiteInfo](t, call(t, env.connect(token), "get_site_info", nil))
	if info.Access.AuthType != "oauth" || len(info.Access.Permissions) != 1 {
		t.Fatal("read-only OAuth access changed")
	}
	// A separately consented grant can retry the operation normally.
	upgraded, _ := oauthToken(t, s, env.userID, "mcp:access mcp:drafts")
	session := env.connect(upgraded)
	saved := decodeResult[scratchEntry](t, call(t, session, "spike_save_draft", map[string]any{"title": "Scope retry", "body": "Scratch only"}))
	got := decodeResult[scratchEntry](t, call(t, session, "spike_get_draft", map[string]any{"id": saved.ID}))
	if got.Title != saved.Title || got.Body != saved.Body {
		t.Fatal("authorized scratch retry did not preserve content")
	}
}

func TestOAuthScopeChallengePreservesOtherRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		spike      bool
		status     int
	}{
		{"list", listToolsBody, true, http.StatusOK},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`, true, http.StatusOK},
		{"disabled scratch", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spike_save_draft","arguments":{"title":"Denied"}}}`, false, http.StatusOK},
		{"malformed", `{"method":"tools/call",`, true, http.StatusBadRequest},
		{"batch", `[` + listToolsBody + `]`, true, http.StatusBadRequest},
		{"oversized", strings.Repeat("x", maxRequestBodyBytes+1), true, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			s := enableOAuth(t, env, tc.spike)
			token, _ := oauthToken(t, s, env.userID, "mcp:access")
			resp := env.rawPost(tc.body, map[string]string{"Authorization": "Bearer " + token})
			if resp.StatusCode != tc.status || resp.Header.Get("WWW-Authenticate") != "" {
				t.Fatalf("status=%d challenge=%q, want %d without scope upgrade", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), tc.status)
			}
		})
	}
	env := newTestEnv(t)
	enableOAuth(t, env, true)
	key := env.createKey(model.PermissionMCPAccess)
	denied := call(t, env.connect(key), "spike_save_draft", map[string]any{"title": "Denied", "body": ""})
	if !denied.IsError || denied.Meta["mcp/www_authenticate"] != nil {
		t.Fatal("API-key request was routed to OAuth scope upgrade")
	}
	key = env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
	saved := decodeResult[scratchEntry](t, call(t, env.connect(key), "spike_save_draft", map[string]any{"title": "Legacy key", "body": ""}))
	if saved.Title != "Legacy key" {
		t.Fatal("scoped API-key scratch write changed")
	}
}
