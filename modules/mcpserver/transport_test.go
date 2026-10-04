// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

const listToolsBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

// apiErrorCode decodes the REST error envelope of an HTTP error response.
func apiErrorCode(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v\n%s", err, body)
	}
	return envelope.Error.Code
}

// TestEndpointRejectsNonPOST verifies non-POST methods get 405 with Allow,
// without any API key work.
func TestEndpointRejectsNonPOST(t *testing.T) {
	env := newTestEnv(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut, http.MethodOptions} {
		req, err := http.NewRequest(method, env.endpoint(), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
			t.Errorf("%s: status %d Allow %q, want 405 Allow POST", method, resp.StatusCode, resp.Header.Get("Allow"))
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", method, resp.Header.Get("Cache-Control"))
		}
	}
}

// TestEndpointRequiresAPIKey verifies missing and invalid keys get 401 with a
// bearer challenge that does not invite an OAuth flow.
func TestEndpointRequiresAPIKey(t *testing.T) {
	env := newTestEnv(t)
	for name, headers := range map[string]map[string]string{
		"missing": nil,
		"invalid": {"Authorization": "Bearer ocms_not-a-real-key"},
		"basic":   {"Authorization": "Basic dXNlcjpwYXNz"},
	} {
		resp := env.rawPost(listToolsBody, headers)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401: %s", name, resp.StatusCode, body)
			continue
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != bearerRealm {
			t.Errorf("%s: WWW-Authenticate = %q, want %q", name, got, bearerRealm)
		}
		if strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
			t.Errorf("%s: challenge must not advertise OAuth metadata", name)
		}
	}
}

// TestEndpointRequiresMCPAccess verifies a valid key without mcp:access is
// refused, so REST integration keys never reach the MCP server implicitly.
func TestEndpointRequiresMCPAccess(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionPagesRead, model.PermissionPagesWrite)

	resp := env.rawPost(listToolsBody, map[string]string{"Authorization": "Bearer " + key})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || apiErrorCode(t, body) != codeForbidden {
		t.Fatalf("status %d body %s, want 403 forbidden", resp.StatusCode, body)
	}
	if len(env.logs.find("MCP request rejected: API key lacks mcp:access")) != 1 {
		t.Error("the denial must be logged")
	}
}

// TestEndpointRejectsInactiveAndExpiredKeys verifies API key policies apply.
func TestEndpointRejectsInactiveAndExpiredKeys(t *testing.T) {
	env := newTestEnv(t)
	inactive := env.createKeyWith(func(p *store.CreateAPIKeyParams) { p.IsActive = false }, model.PermissionMCPAccess)
	expired := env.createKeyWith(func(p *store.CreateAPIKeyParams) {
		p.ExpiresAt = sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
	}, model.PermissionMCPAccess)
	for name, key := range map[string]string{"inactive": inactive, "expired": expired} {
		resp := env.rawPost(listToolsBody, map[string]string{"Authorization": "Bearer " + key})
		_ = readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s key: status %d, want 401", name, resp.StatusCode)
		}
	}
}

// TestEndpointRejectsCrossOriginBrowserRequests verifies browser requests
// from another origin are refused while non-browser clients are not.
func TestEndpointRejectsCrossOriginBrowserRequests(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	auth := "Bearer " + key

	resp := env.rawPost(listToolsBody, map[string]string{"Authorization": auth, "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || apiErrorCode(t, body) != codeForbidden {
		t.Errorf("cross-site browser request: status %d body %s, want 403", resp.StatusCode, body)
	}

	resp = env.rawPost(listToolsBody, map[string]string{"Authorization": auth, "Origin": "https://evil.example"})
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Origin without Sec-Fetch-Site: status %d, want 403", resp.StatusCode)
	}

	resp = env.rawPost(listToolsBody, map[string]string{"Authorization": auth, "Sec-Fetch-Site": "same-origin"})
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("same-origin request: status %d, want 200", resp.StatusCode)
	}
}

// TestEndpointBodyLimit verifies oversized requests are refused.
func TestEndpointBodyLimit(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	padding := strings.Repeat("x", maxRequestBodyBytes)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_pages","arguments":{"query":"` + padding + `"}}}`

	resp := env.rawPost(body, map[string]string{"Authorization": "Bearer " + key})
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", resp.StatusCode)
	}
}

// TestEndpointSuccessIsNotCacheable verifies successful responses are marked
// no-store.
func TestEndpointSuccessIsNotCacheable(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	resp := env.rawPost(listToolsBody, map[string]string{"Authorization": "Bearer " + key})
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("status %d Cache-Control %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}

// TestEndpointPerIPRateLimit verifies requests beyond the per-IP burst are
// refused before any API key verification.
func TestEndpointPerIPRateLimit(t *testing.T) {
	env := newTestEnv(t)
	limited := false
	for range ipRateLimitBurst + 10 {
		resp := env.rawPost(listToolsBody, nil)
		_ = readBody(t, resp)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Errorf("no 429 after %d rapid requests from one address", ipRateLimitBurst+10)
	}
}

// TestNullArgumentsDoNotCrash is the regression test for go-sdk v1.8.0
// panicking on "arguments": null for a tool with schema defaults. The call
// must succeed as an argument-less call instead of killing the process.
func TestNullArgumentsDoNotCrash(t *testing.T) {
	env := newTestEnv(t)
	env.createPage(pageSeed{title: "Hello", slug: "hello", body: "<p>x</p>", status: model.PageStatusPublished})
	key := env.createKey(model.PermissionMCPAccess)

	resp := env.rawPost(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_pages","arguments":null}}`,
		map[string]string{"Authorization": "Bearer " + key})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"hello"`) || strings.Contains(body, `"isError":true`) {
		t.Fatalf("status %d body %s, want a successful list", resp.StatusCode, body)
	}

	// The Go SDK client sends null for a typed-nil argument map.
	var nilArgs map[string]any
	res := call(t, env.connect(key), "list_pages", nilArgs)
	if res.IsError {
		t.Errorf("typed-nil arguments: %s", resultText(res))
	}
}

// TestRequestGuardRecoversPanics verifies a panic anywhere in the SDK request
// pipeline becomes a JSON-RPC internal error instead of a process crash.
func TestRequestGuardRecoversPanics(t *testing.T) {
	env := newTestEnv(t)
	handler := env.module.requestGuard(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic("boom")
	})
	result, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "list_pages"}})
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) || wireErr.Code != jsonrpc.CodeInternalError || strings.Contains(wireErr.Message, "boom") {
		t.Errorf("err = %v, want a scrubbed internal JSON-RPC error", err)
	}
	if len(env.logs.find("MCP request panicked")) != 1 {
		t.Error("the panic must be logged")
	}
}

// TestLimitInFlight verifies requests beyond capacity get 503 and Retry-After
// instead of queueing.
func TestLimitInFlight(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	h := limitInFlight(testutil.TestLoggerSilent(), 1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, EndpointPath, nil))
		done <- rec.Code
	}()
	<-entered

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, EndpointPath, nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("over capacity: status %d Retry-After %q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first request status %d, want 200", code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, EndpointPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("slot not released: status %d", rec.Code)
	}
}

// TestServeEndpointBeforeInit verifies a registered but never initialized
// module answers 503 instead of panicking on a nil chain.
func TestServeEndpointBeforeInit(t *testing.T) {
	m := New()
	r := chi.NewRouter()
	m.RegisterRoutes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, EndpointPath, strings.NewReader(listToolsBody)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

// TestLateActivationEnforcesFullChain covers the registry path: the module
// is opt-in, so its route is registered while inactive (404) and Init runs
// only on activation. The endpoint must then enforce the complete chain,
// which a handler captured at registration time would have missed.
func TestLateActivationEnforcesFullChain(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	logger := testutil.TestLoggerSilent()
	registry := module.NewRegistry(logger)
	m := New()
	if err := registry.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.InitAll(&module.Context{DB: db, Store: store.New(db), Logger: logger}); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if registry.IsActive(ModuleName) {
		t.Fatal("the MCP module must be inactive after first registration")
	}
	r := chi.NewRouter()
	registry.RouteAll(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	post := func() *http.Response {
		resp, err := http.Post(srv.URL+EndpointPath, "application/json", strings.NewReader(listToolsBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	if resp := post(); resp.StatusCode != http.StatusNotFound {
		t.Errorf("inactive: status %d, want 404", resp.StatusCode)
	}

	if err := registry.SetActive(ModuleName, true); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	resp := post()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != bearerRealm {
		t.Errorf("after activation: status %d, want 401 from the auth chain", resp.StatusCode)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("after activation the response must come through the full chain")
	}
}
