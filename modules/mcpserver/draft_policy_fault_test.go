// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestDraftAccountReadFaultsAreRetryable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reads int
		seed  bool
		share bool
	}{
		{"create", 6, false, false},
		{"replay", 3, true, false},
		{"preview", 3, true, true},
	} {
		for read := 1; read <= tc.reads; read++ {
			t.Run(fmt.Sprintf("%s/read_%d", tc.name, read), func(t *testing.T) {
				env, id := draftKeyFixture(t)
				ctx := t.Context()
				call := &toolCall{identity: id, settings: env.module.currentSettings()}
				in := authoring.CreateInput{
					RequestID: uuid.NewString(),
					Draft:     draft.PageDraft{Title: "Account checks", Summary: "Review", BodyMarkdown: "Safe content."},
				}
				var seed authoring.SaveResult
				if tc.seed {
					var err error
					seed, err = env.module.createDraftPage(ctx, call, in)
					if err != nil {
						t.Fatal(err)
					}
				}
				fault := errors.New("injected draft account read fault")
				reads, inject := 0, true
				alternate := openDraftFaultDB(t, env, "immediate", func(query string) error {
					if strings.Contains(query, "FROM users WHERE id") {
						reads++
						if inject && reads == read {
							return fault
						}
					}
					return nil
				})
				env.module.ctx.DB = alternate
				env.module.svc.queries = store.New(alternate)
				env.module.svc.pages = pages.NewService(alternate, store.New(alternate), nil, nil, pages.Policy{})
				previous := slog.Default()
				slog.SetDefault(slog.New(env.logs))
				t.Cleanup(func() { slog.SetDefault(previous) })

				issue := func() (int64, error) {
					if tc.share {
						link, err := env.module.createPreviewLink(ctx, call, authoring.PreviewInput{PageID: seed.PageID, Revision: seed.Revision})
						if link.URL.Reveal() == "" {
							return 0, err
						}
						return link.PageID, err
					}
					saved, err := env.module.createDraftPage(ctx, call, in)
					return saved.PageID, err
				}
				pageID, err := issue()
				if pageID != 0 || !errors.Is(err, fault) || reads != read {
					t.Fatalf("account read %d returned page %d, error %v after %d reads", read, pageID, err, reads)
				}
				requireDraftRetryCode(t, err)
				assertDraftAccountDiagnostic(t, env, fault, seed.PageID, tc.share)
				draftsBefore := 0
				if tc.seed {
					draftsBefore = 1
				}
				assertDraftPolicyRows(t, env, draftsBefore)
				assertPreviewIssuanceRows(t, env, 0)

				inject, reads = false, 0
				pageID, err = issue()
				if err != nil || pageID <= 0 || (tc.seed && pageID != seed.PageID) || reads != tc.reads {
					t.Fatalf("recovery returned page %d, error %v after %d reads, want %d reads", pageID, err, reads, tc.reads)
				}
				assertDraftPolicyRows(t, env, 1)
				links := 0
				if tc.share {
					links = 1
				}
				assertPreviewIssuanceRows(t, env, links)
				assertDraftAccountDiagnostic(t, env, fault, seed.PageID, tc.share)
			})
		}
	}
}

func assertDraftAccountDiagnostic(t *testing.T, env *testEnv, fault error, pageID int64, share bool) {
	t.Helper()
	if got := strings.Count(env.logs.text(), fault.Error()); got != 1 {
		t.Fatalf("want one account diagnostic, got %d: %s", got, env.logs.text())
	}
	records := env.logs.find("failed to re-check draft account")
	if share || len(records) != 0 {
		if len(records) != 1 {
			t.Fatalf("account diagnostic missing: %s", env.logs.text())
		}
		user, _ := recordAttr(records[0], "user_id")
		page, found := recordAttr(records[0], "page_id")
		if !found || user.Int64() != env.userID || (share && page.Int64() != pageID) || (!share && page.Int64() != 0) {
			t.Fatalf("account diagnostic has incorrect IDs: %v", records[0])
		}
	}
}

func TestDraftAuthorizationPolicyFaultsAreRetryable(t *testing.T) {
	for _, credential := range []string{"api_key", "oauth"} {
		for _, stage := range []string{"initial", "write authorization", "write policy"} {
			for _, tc := range []struct {
				table, column, message, denySQL, restoreSQL, deniedCode string
			}{
				{"modules", "is_active", "failed to check MCP draft module status", "UPDATE modules SET is_active=0 WHERE name='mcp'", "UPDATE modules SET is_active=1 WHERE name='mcp'", codeForbidden},
				{"mcp_settings", "instructions", "failed to load MCP draft authorization settings", "UPDATE mcp_settings SET draft_tools_enabled=0 WHERE id=1", "UPDATE mcp_settings SET draft_tools_enabled=1 WHERE id=1", "draft_tools_disabled"},
				{"api_key_source_cidrs", "cidr", "failed to re-check MCP API key policy", "INSERT INTO api_key_source_cidrs (api_key_id, cidr) SELECT id, '203.0.113.0/24' FROM api_keys", "DELETE FROM api_key_source_cidrs WHERE cidr='203.0.113.0/24'", codeUnauthorized},
			} {
				if stage == "write policy" && tc.table != "mcp_settings" || credential == "oauth" && tc.table == "api_key_source_cidrs" {
					continue
				}
				t.Run(credential+"/"+stage+"/"+tc.table, func(t *testing.T) {
					env := newTestEnv(t)
					enableDraftTools(env)
					var bearer string
					if credential == "oauth" {
						bearer, _ = oauthToken(t, enableOAuth(t, env, true), env.userID, "mcp:access mcp:drafts")
					} else {
						bearer = env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
					}
					message := tc.message
					if stage == "write policy" {
						message = "failed to re-check MCP draft write settings"
					}
					breakSQL := "ALTER TABLE " + tc.table + " RENAME COLUMN " + tc.column + " TO sf10_unavailable"
					repairSQL := "ALTER TABLE " + tc.table + " RENAME COLUMN sf10_unavailable TO " + tc.column
					var phase, hits atomic.Int32
					phase.Store(1)
					tool := "create_draft_page"
					if stage == "initial" {
						// Transport authentication and stored-state checks must pass
						// before the actual draft handler meets the injected fault.
						env.module.currentServer().AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
							return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
								if method != "tools/call" {
									return next(ctx, method, req)
								}
								selected := phase.Swap(0)
								if selected == 0 {
									return next(ctx, method, req)
								}
								change, restore := breakSQL, repairSQL
								if selected == 2 {
									change, restore = tc.denySQL, tc.restoreSQL
								}
								if _, changeErr := env.db.ExecContext(ctx, change); changeErr != nil {
									return nil, changeErr
								}
								hits.Add(1)
								result, callErr := next(ctx, method, req)
								if _, restoreErr := env.db.Exec(restore); restoreErr != nil {
									return nil, restoreErr
								}
								return result, callErr
							}
						})
					} else {
						// A test-only tool wraps the real service callbacks. DDL in
						// the write transaction fails its next lookup and rolls back
						// with the refused mutation; production gets no test hook.
						tool = "queued_policy_probe"
						registerTool(func(m *Module, ctx context.Context, call *toolCall, in authoring.CreateInput) (authoring.SaveResult, error) {
							svc, actor, err := m.authoringService(ctx, call)
							if err != nil {
								return authoring.SaveResult{}, err
							}
							inject := func(ctx context.Context, q *store.Queries) error {
								if !phase.CompareAndSwap(1, 0) {
									return nil
								}
								if _, ok := q.DBHandle().(*sql.Tx); !ok {
									return fmt.Errorf("fixture did not reach a write transaction")
								}
								if _, changeErr := q.DBHandle().ExecContext(ctx, breakSQL); changeErr != nil {
									return changeErr
								}
								hits.Add(1)
								return nil
							}
							if stage == "write policy" {
								original := svc.CheckPolicy
								svc.CheckPolicy = func(ctx context.Context, q *store.Queries) error {
									if injectErr := inject(ctx, q); injectErr != nil {
										return injectErr
									}
									return original(ctx, q)
								}
							} else {
								original := svc.Authorize
								checks := 0
								svc.Authorize = func(ctx context.Context, q *store.Queries) error {
									checks++
									if checks == 2 { // Let ReplayDraft preflight succeed first.
										if injectErr := inject(ctx, q); injectErr != nil {
											return injectErr
										}
									}
									return original(ctx, q)
								}
							}
							return svc.Create(ctx, actor, in)
						})(env.module, env.module.currentServer(), toolSpec{Name: tool, Title: "Queued policy fixture", scope: model.PermissionMCPDrafts, mutation: mutationCreate})
					}
					session := env.connect(bearer)
					args := draftCreateArgs("SF10_PRIVATE_DRAFT_TITLE")
					failed := call(t, session, tool, args)
					if hits.Load() != 1 {
						t.Fatal("fixture did not reach the intended authorization boundary")
					}
					assertDraftPolicyError(t, failed, "write_retryable", true)
					lines := env.logs.find(message)
					if len(lines) != 1 {
						t.Fatalf("fault diagnostics=%d, want 1: %s", len(lines), env.logs.text())
					}
					cause, _ := recordAttr(lines[0], "error")
					user, _ := recordAttr(lines[0], "user_id")
					if lines[0].Level != slog.LevelError || !strings.Contains(cause.String(), "no such column") || user.Int64() != env.userID {
						t.Errorf("fault lacks cause or actor: %s", env.logs.text())
					}
					if tc.table == "api_key_source_cidrs" {
						if key, ok := recordAttr(lines[0], "api_key_id"); !ok || key.Int64() <= 0 {
							t.Error("source-policy fault lacks key ID")
						}
					}
					calls := env.logs.find("MCP tool call")
					if len(calls) != 1 {
						t.Fatalf("call records=%d, want 1", len(calls))
					}
					outcome, _ := recordAttr(calls[0], "outcome")
					if outcome.String() != outcomeToolError || calls[0].Level != slog.LevelInfo {
						t.Errorf("retryable failure misclassified by the call logger: %v", calls[0])
					}
					if _, duplicated := recordAttr(calls[0], "error"); duplicated || strings.Count(env.logs.text(), "no such column") != 1 {
						t.Error("storage cause was logged more than once")
					}
					assertDraftPolicyRows(t, env, 0)
					for _, private := range []string{bearer, "SF10_PRIVATE_DRAFT_TITLE", "R15 private draft paragraph"} {
						if strings.Contains(resultText(failed)+env.logs.text(), private) {
							t.Error("policy failure exposed credentials or draft input")
						}
					}
					saved := decodeResult[authoring.SaveResult](t, call(t, session, tool, args))
					replayed := decodeResult[authoring.SaveResult](t, call(t, session, tool, args))
					if saved.PageID <= 0 || saved.Revision != 1 || replayed.PageID != saved.PageID || replayed.Revision != saved.Revision {
						t.Fatalf("repair/replay did not preserve one draft: %+v %+v", saved, replayed)
					}
					assertDraftPolicyRows(t, env, 1)
					if stage == "initial" {
						phase.Store(2)
						assertDraftPolicyError(t, call(t, session, tool, args), tc.deniedCode, false)
						if hits.Load() != 2 {
							t.Error("genuine policy refusal was not injected")
						}
						assertDraftPolicyRows(t, env, 1)
					}
					if len(env.logs.find(message)) != 1 || strings.Count(env.logs.text(), "no such column") != 1 {
						t.Error("recovery or genuine refusal logged another storage fault")
					}
				})
			}
		}
	}
}

func assertDraftPolicyRows(t *testing.T, env *testEnv, want int) {
	t.Helper()
	var pages, versions, receipts int
	err := env.db.QueryRow(`SELECT (SELECT COUNT(*) FROM pages), (SELECT COUNT(*) FROM page_versions), (SELECT COUNT(*) FROM idempotency_keys)`).Scan(&pages, &versions, &receipts)
	if err != nil || pages != want || versions != want || receipts != want {
		t.Fatalf("draft state pages=%d revisions=%d receipts=%d, want %d: %v", pages, versions, receipts, want, err)
	}
}

func assertDraftPolicyError(t *testing.T, result *mcp.CallToolResult, code string, retryable bool) {
	t.Helper()
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{resultText(result), string(structured)} {
		var envelope struct {
			Error     toolError `json:"error"`
			Retryable *bool     `json:"retryable"`
		}
		if err = json.Unmarshal([]byte(encoded), &envelope); err != nil {
			t.Fatal(err)
		}
		if !result.IsError || envelope.Error.Code != code || envelope.Retryable == nil || *envelope.Retryable != retryable {
			t.Errorf("policy result=%s, want %s retryable=%t", encoded, code, retryable)
		}
		if strings.Contains(encoded, "no such column") || strings.Contains(encoded, "sf10_unavailable") {
			t.Error("internal storage cause leaked into the tool response")
		}
	}
}

// Every fallible lookup in draft authorization and its write-policy callback
// must classify its error. credentialScopes already returns classified errors.
func TestDraftAuthorizationLookupsClassifyFaults(t *testing.T) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, "tools_drafts.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	type lookupRoot struct {
		body       *ast.BlockStmt
		account    bool
		classifier string
	}
	var roots []lookupRoot
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "authorizeDraftCall", "createPreviewLink", "checkAgentShareLinks":
			roots = append(roots, lookupRoot{body: fn.Body})
		}
		if fn.Name.Name == "authoringService" {
			for node := range ast.Preorder(fn.Body) {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
					continue
				}
				selected, ok := assignment.Lhs[0].(*ast.SelectorExpr)
				if !ok || selected.Sel.Name != "CheckPolicy" {
					continue
				}
				if callback, ok := assignment.Rhs[0].(*ast.FuncLit); ok {
					roots = append(roots, lookupRoot{body: callback.Body})
				}
			}
		}
	}
	if len(roots) != 4 {
		t.Fatal("missing draft authorization, write-policy or preview issuance checks; update fault coverage")
	}
	for _, target := range []struct{ file, method, classifier string }{
		{"../../internal/ai/authoring/service.go", "authorize", "accountLookupError"},
		{"../../internal/preview/service.go", "Issue", "IssuerLookupError"},
	} {
		source, parseErr := parser.ParseFile(positions, target.file, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range source.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == target.method {
				roots = append(roots, lookupRoot{body: fn.Body, account: true, classifier: target.classifier})
			}
		}
	}
	if len(roots) != 6 {
		t.Fatal("missing authoring or preview account lookup root")
	}
	checked := 0
	check := func(root lookupRoot, assignment *ast.AssignStmt, branch *ast.IfStmt) {
		if len(assignment.Rhs) != 1 || len(assignment.Lhs) == 0 {
			return
		}
		call, ok := assignment.Rhs[0].(*ast.CallExpr)
		if !ok {
			return
		}
		if selected, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
			switch selected.Sel.Name {
			case "credentialScopes", "authoringService", "checkAgentShareLinks":
				return // These helpers already classify their errors.
			}
		}
		if root.account {
			selected, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selected.Sel.Name != "GetUserByID" && selected.Sel.Name != "RequireEditor") {
				return
			}
		}
		bound, ok := assignment.Lhs[len(assignment.Lhs)-1].(*ast.Ident)
		if !ok || (bound.Name != "err" && !strings.HasSuffix(bound.Name, "Err")) {
			return
		}
		if branch == nil {
			t.Errorf("%s: lookup error needs an explicit classification branch", positions.Position(assignment.Pos()))
			return
		}
		checked++
		if root.account {
			for node := range ast.Preorder(branch.Body) {
				if name, ok := node.(*ast.Ident); ok && name.Name == root.classifier {
					return
				}
			}
			t.Errorf("%s: account lookup must use %s", positions.Position(assignment.Pos()), root.classifier)
			return
		}
		for inner := range ast.Preorder(branch.Body) {
			if returned, ok := inner.(*ast.ReturnStmt); ok {
				for _, value := range returned.Results {
					if name, ok := value.(*ast.Ident); ok && name.Name == bound.Name {
						t.Errorf("%s: raw %s must be classified as a retryable authorization fault", positions.Position(returned.Pos()), bound.Name)
					}
				}
			}
		}
	}
	for _, root := range roots {
		before := checked
		for node := range ast.Preorder(root.body) {
			if branch, ok := node.(*ast.IfStmt); ok {
				if assignment, ok := branch.Init.(*ast.AssignStmt); ok {
					check(root, assignment, branch)
				}
			}
			block, ok := node.(*ast.BlockStmt)
			if !ok {
				continue
			}
			for i, statement := range block.List {
				assignment, ok := statement.(*ast.AssignStmt)
				if !ok {
					continue
				}
				var branch *ast.IfStmt
				if i+1 < len(block.List) {
					branch, _ = block.List[i+1].(*ast.IfStmt)
				}
				check(root, assignment, branch)
			}
		}
		if checked == before {
			t.Error("no authorization lookups found in a guarded root")
		}
	}
}
