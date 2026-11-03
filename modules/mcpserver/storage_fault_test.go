// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"database/sql"
	"go/ast"
	"net/http"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// withTableUnavailable runs fn while table cannot be read, the way the OAuth
// storage-fault tests simulate a database error, and puts the table back.
func withTableUnavailable(t *testing.T, db *sql.DB, table string, fn func()) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_unavailable`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(`ALTER TABLE ` + table + `_unavailable RENAME TO ` + table); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

// requireRetryableFault fails unless err is the retryable, scrubbed answer for
// a storage fault: not unauthorized, flagged retryable, no leaked cause.
func requireRetryableFault(t *testing.T, err error) {
	t.Helper()
	toolErr, _ := toToolError(err)
	if toolErr == nil || toolErr.Code == codeUnauthorized || !toolErr.Retryable {
		t.Fatalf("storage fault answered %+v (err %v), want a retryable error that is not unauthorized", toolErr, err)
	}
	if strings.Contains(toolErr.Message, "api_keys") || strings.Contains(toolErr.Message, "no such table") {
		t.Fatalf("storage fault leaked its cause: %q", toolErr.Message)
	}
}

// requireUnauthorized fails unless err is the plain, non-retryable 401.
func requireUnauthorized(t *testing.T, what string, err error) {
	t.Helper()
	toolErr, _ := toToolError(err)
	if toolErr == nil || toolErr.Code != codeUnauthorized || toolErr.Retryable {
		t.Fatalf("%s answered %+v (err %v), want unauthorized", what, toolErr, err)
	}
}

// draftKeyFixture is an environment with draft tools on and one API key.
func draftKeyFixture(t *testing.T) (*testEnv, *identity) {
	t.Helper()
	env := newTestEnv(t)
	enableDraftTools(env)
	raw := env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
	keys, err := env.q.GetAPIKeysByPrefix(t.Context(), model.ExtractAPIKeyPrefix(raw))
	if err != nil || len(keys) != 1 {
		t.Fatalf("key fixture %v", err)
	}
	return env, &identity{key: keys[0], userID: env.userID, scopes: []string{model.PermissionMCPAccess, model.PermissionMCPDrafts}, sourceIP: "127.0.0.1"}
}

func requireFaultLogged(t *testing.T, env *testEnv, msg string) {
	t.Helper()
	if logs := env.logs.text(); !strings.Contains(logs, msg) || !strings.Contains(logs, "no such table") {
		t.Fatalf("the fault was not logged with its cause (%q): %q", msg, logs)
	}
}

// TestDraftAuthorizationStorageFaultIsRetryableNotUnauthorized pins the MCP
// half of S12: authorizeDraftCall answered "Credential unavailable" (401) for
// any error loading the key, so a storage fault during a draft call told the
// agent its key was revoked. A key that is gone or is not the caller's stays
// 401; a fault is a logged, retryable error.
func TestDraftAuthorizationStorageFaultIsRetryableNotUnauthorized(t *testing.T) {
	env, id := draftKeyFixture(t)
	authorize := func(id *identity) error { return env.module.authorizeDraftCall(t.Context(), env.q, id) }
	if err := authorize(id); err != nil {
		t.Fatalf("healthy authorization refused: %v", err)
	}

	var faulted error
	withTableUnavailable(t, env.db, "api_keys", func() { faulted = authorize(id) })
	requireRetryableFault(t, faulted)
	requireFaultLogged(t, env, "failed to re-check the MCP API key")

	stranger := *id
	stranger.userID++
	requireUnauthorized(t, "another owner's credential", authorize(&stranger))
	if _, err := env.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id.key.ID); err != nil {
		t.Fatal(err)
	}
	requireUnauthorized(t, "a deleted key", authorize(id))
}

// TestQueuedWritePolicyCheckSharesTheCredentialLookup covers the queued
// write's own re-check (authoring.Service.CheckPolicy), which used to carry a
// second copy of the lookup that returned the raw error. Both now go through
// credentialScopes, so a fault is the same retryable answer and a missing key
// the same 401.
func TestQueuedWritePolicyCheckSharesTheCredentialLookup(t *testing.T) {
	env, id := draftKeyFixture(t)
	svc, _, err := env.module.authoringService(t.Context(), &toolCall{identity: id, settings: env.module.currentSettings()})
	if err != nil {
		t.Fatal(err)
	}
	check := func() error { return svc.CheckPolicy(t.Context(), env.q) }
	if err = check(); err != nil {
		t.Fatalf("healthy policy check refused: %v", err)
	}

	var faulted error
	withTableUnavailable(t, env.db, "api_keys", func() { faulted = check() })
	requireRetryableFault(t, faulted)
	requireFaultLogged(t, env, "failed to re-check the MCP API key")

	if _, err = env.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id.key.ID); err != nil {
		t.Fatal(err)
	}
	requireUnauthorized(t, "a deleted key", check())
}

// TestDraftServicesLookUpScopesOnlyThroughCredentialScopes is the drift test
// for that sharing: no other function in tools_drafts.go reads the API key row
// or re-checks the OAuth grant, so the authorization and the queued write's
// policy check cannot diverge again.
func TestDraftServicesLookUpScopesOnlyThroughCredentialScopes(t *testing.T) {
	_, files := packageFiles(t)
	callers := map[string][]string{}
	for _, decl := range files["tools_drafts.go"].Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "GetAPIKeyByID" || sel.Sel.Name == "RecheckAccess") {
					callers[sel.Sel.Name] = append(callers[sel.Sel.Name], fn.Name.Name)
				}
			}
			return true
		})
	}
	for _, lookup := range []string{"GetAPIKeyByID", "RecheckAccess"} {
		if got := callers[lookup]; len(got) != 1 || got[0] != "credentialScopes" {
			t.Errorf("%s is called from %v in tools_drafts.go, want only credentialScopes", lookup, got)
		}
	}
}

// TestAccountLookupStorageFaultIsNotUnauthorized covers the other half of S12
// in this module: keyAccountGuard answered 401 "API key required" when loading
// the key owner failed for any reason. A missing owner is still 401; a storage
// fault is a logged 500 that does not leak its cause, so the client keeps a key
// that may be fine.
func TestAccountLookupStorageFaultIsNotUnauthorized(t *testing.T) {
	env := newTestEnv(t)
	raw := env.createKey(model.PermissionMCPAccess)
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	bearer := map[string]string{"Authorization": "Bearer " + raw}
	if resp := env.rawPost(list, bearer); resp.StatusCode != http.StatusOK {
		t.Fatalf("healthy request answered %d", resp.StatusCode)
	}

	var status int
	var body string
	withTableUnavailable(t, env.db, "users", func() {
		resp := env.rawPost(list, bearer)
		status, body = resp.StatusCode, readBody(t, resp)
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("account lookup fault answered %d, want 500 (a 401 tells the agent to drop a valid key)", status)
	}
	if strings.Contains(body, "users") || strings.Contains(body, "no such table") {
		t.Fatalf("storage fault leaked its cause: %s", body)
	}
	requireFaultLogged(t, env, "failed to load the account of an MCP API key")

	// A key whose owner no longer exists is still unauthorized.
	if _, err := env.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Exec(`UPDATE api_keys SET created_by = created_by + 1000`); err != nil {
		t.Fatal(err)
	}
	if resp := env.rawPost(list, bearer); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a key without an owner answered %d, want 401", resp.StatusCode)
	}
}
