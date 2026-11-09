// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/model"
)

func TestOAuthToolScopeInBandChallenge(t *testing.T) {
	env := newTestEnv(t)
	s := enableOAuth(t, env, true)
	token, _ := oauthToken(t, s, env.userID, model.PermissionMCPAccess)

	// Exercise the SDK's in-band result without the outer HTTP scope preflight.
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return env.module.currentServer()
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	handler := env.module.mixedAuthentication(http.NotFoundHandler(), sdk)
	req := httptest.NewRequest(http.MethodPost, EndpointPath, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_my_draft","arguments":{"page_id":1}}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var response struct {
		Result struct {
			IsError bool                `json:"isError"`
			Meta    map[string][]string `json:"_meta"`
		} `json:"result"`
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 with an in-band tool error: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	challenges := response.Result.Meta["mcp/www_authenticate"]
	const want = `Bearer resource_metadata="https://cms.test/.well-known/oauth-protected-resource/api/mcp", error="insufficient_scope", scope="mcp:access mcp:drafts"`
	if !response.Result.IsError || len(challenges) != 1 || challenges[0] != want {
		t.Fatalf("in-band result=%+v, want a tool error with challenge %q", response.Result, want)
	}
}

func TestOAuthToolScopeHTTPChallenge(t *testing.T) {
	env := newTestEnv(t)
	s := enableOAuth(t, env, true)
	token, _ := oauthToken(t, s, env.userID, "mcp:access")
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, name := range []string{"create_draft_page", "get_my_draft"} {
		args := `{"page_id":1}`
		if name == "create_draft_page" {
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
	if total, err := env.q.CountPages(t.Context()); err != nil || total != 0 {
		t.Fatal("insufficient-scope request executed a content write")
	}
	info := decodeResult[SiteInfo](t, call(t, env.connect(token), "get_site_info", nil))
	if info.Access.AuthType != "oauth" || len(info.Access.Permissions) != 1 {
		t.Fatal("read-only OAuth access changed")
	}
	// A separately consented grant can retry the operation normally.
	upgraded, _ := oauthToken(t, s, env.userID, "mcp:access mcp:drafts")
	session := env.connect(upgraded)
	saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Scope retry")))
	got := decodeResult[authoring.DraftDetail](t, call(t, session, "get_my_draft", map[string]any{"page_id": saved.PageID}))
	if got.Title != "Scope retry" || got.BodyHTML == "" {
		t.Fatal("authorized draft retry did not preserve content")
	}

}

func TestOAuthScopeChallengePreservesOtherRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		drafts     bool
		status     int
	}{
		{"list", listToolsBody, true, http.StatusOK},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`, true, http.StatusOK},
		{"disabled tools list", listToolsBody, false, http.StatusOK},
		{"malformed", `{"method":"tools/call",`, true, http.StatusBadRequest},
		{"batch", `[` + listToolsBody + `]`, true, http.StatusBadRequest},
		{"oversized", strings.Repeat("x", maxRequestBodyBytes+1), true, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			s := enableOAuth(t, env, tc.drafts)
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
	denied := call(t, env.connect(key), "create_draft_page", draftCreateArgs("Denied"))
	if !denied.IsError || denied.Meta["mcp/www_authenticate"] != nil {
		t.Fatal("API-key request was routed to OAuth scope upgrade")
	}
	key = env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
	saved := decodeResult[authoring.SaveResult](t, call(t, env.connect(key), "create_draft_page", draftCreateArgs("Legacy key")))
	if saved.PageID <= 0 {
		t.Fatal("scoped API-key draft write failed")
	}
}
