// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestRevokeClientMismatchWarnsWithoutRevocation(t *testing.T) {
	for _, scenario := range []string{"live access", "live refresh", "spent refresh", "rotated access", "expired access", "unknown access", "unknown refresh", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			code := f.code(t)
			first := f.redeem(t, code)
			live := first
			if scenario == "spent refresh" || scenario == "rotated access" {
				status, rotated := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
				if status != http.StatusOK || access(rotated) == "" {
					t.Fatalf("fixture rotation: status=%d", status)
				}
				live = rotated
			}
			supplied := access(first)
			warn := scenario == "live access" || scenario == "live refresh" || scenario == "spent refresh"
			switch scenario {
			case "live refresh", "spent refresh":
				supplied = refresh(first)
			case "expired access":
				if _, err := f.db.Exec("UPDATE oauth_tokens SET access_expires_at=? WHERE access_hash=?", f.s.now().Unix(), tokenHash(supplied)); err != nil {
					t.Fatal(err)
				}
			case "unknown access":
				supplied = randomCredential(AccessPrefix)
			case "unknown refresh":
				supplied = randomCredential(refreshPrefix)
			case "malformed":
				supplied = "SF14_MALFORMED_CREDENTIAL"
			}
			var rows []store.OauthToken
			for _, raw := range []string{access(first), access(live)} {
				row, err := f.s.q.GetOAuthTokenByAccess(t.Context(), tokenHash(raw))
				if err != nil {
					t.Fatal(err)
				}
				rows = append(rows, row)
			}
			grant, err := f.s.q.GetOAuthGrant(t.Context(), rows[0].GrantID)
			if err != nil {
				t.Fatal(err)
			}
			events := authEvents(t, f)
			var fetches atomic.Int64
			f.s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				fetches.Add(1)
				return nil, errors.New("unexpected metadata fetch")
			})
			logs := captureOAuthLogs(t)
			const otherClient = "https://client.test/SF14_PRIVATE_CLIENT?secret=SF14_PRIVATE_VALUE"
			status, response := f.revoke(otherClient, supplied)
			if status != http.StatusOK || len(response) != 0 {
				t.Fatalf("mismatch/probe response: status=%d, fields=%d", status, len(response))
			}
			wantWarnings := 0
			if warn {
				wantWarnings = 1
				for _, field := range []string{"level=WARN", "grant_id=" + grant.ID, fmt.Sprintf("user_id=%d", f.user.ID)} {
					if !strings.Contains(logs.String(), field) {
						t.Errorf("mismatch diagnostic lacks %s", field)
					}
				}
			}
			if strings.Count(logs.String(), "OAuth revocation client mismatch") != wantWarnings || strings.Count(logs.String(), "level=WARN") != wantWarnings || strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("wrong diagnostic count/severity; expected %d warning", wantWarnings)
			}
			for _, secret := range []string{otherClient, "SF14_PRIVATE_", supplied, code, access(first), refresh(first), access(live), refresh(live), f.verifier, f.values.Get("state")} {
				if secret != "" && strings.Contains(logs.String(), secret) {
					t.Error("revocation diagnostic exposed request data or a credential")
				}
			}
			after, err := f.s.q.GetOAuthGrant(t.Context(), grant.ID)
			if err != nil || !reflect.DeepEqual(after, grant) || !reflect.DeepEqual(authEvents(t, f), events) {
				t.Fatal("refused revocation changed the grant or its audit events")
			}
			for _, before := range rows {
				row, rowErr := f.s.q.GetOAuthTokenByID(t.Context(), before.ID)
				if rowErr != nil || !reflect.DeepEqual(row, before) {
					t.Fatal("refused revocation changed a token row")
				}
			}
			if scenario != "expired access" {
				if _, err = f.s.VerifyAccess(t.Context(), access(live)); err != nil {
					t.Fatal("mismatch invalidated current access", err)
				}
			}
			status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(live)}})
			if status != http.StatusOK || access(next) == "" {
				t.Fatalf("mismatch invalidated the refresh family: status=%d", status)
			}
			if _, err = f.s.VerifyAccess(t.Context(), access(next)); err != nil {
				t.Fatal("refreshed access is unusable", err)
			}
			// Correct-client access and refresh revocation retain their normal
			// effect, with one audit event and no mismatch diagnostic on replay.
			correctToken := refresh(next)
			if strings.Contains(scenario, "access") {
				correctToken = access(next)
			}
			logs.Reset()
			for range 2 {
				status, response = f.revoke(f.values.Get("client_id"), correctToken)
				if status != http.StatusOK || len(response) != 0 {
					t.Fatalf("normal revocation: status=%d", status)
				}
			}
			if strings.Contains(logs.String(), "OAuth revocation client mismatch") || strings.Contains(logs.String(), "level=WARN") {
				t.Error("correct-client revocation was reported as a mismatch")
			}
			if _, err = f.s.VerifyAccess(t.Context(), access(next)); problemCode(err) != "invalid_token" {
				t.Fatal("correct-client revocation left access valid")
			}
			revocations := 0
			for _, event := range authEvents(t, f) {
				if event.message == "OAuth connection revoked by client" {
					revocations++
				}
			}
			if revocations != 1 || fetches.Load() != 0 {
				t.Errorf("revocation audit count=%d, metadata fetches=%d", revocations, fetches.Load())
			}
		})
	}
}

// Discover direct client-ID comparisons in Revoke so an added mismatch branch
// cannot silently omit its diagnostic. This does not follow aliases/dataflow.
func TestRevocationClientMismatchBranchesLog(t *testing.T) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "Revoke" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("Revoke missing; update mismatch drift coverage")
	}
	checked := 0
	for node := range ast.Preorder(body) {
		branch, branchOK := node.(*ast.IfStmt)
		if !branchOK {
			continue
		}
		comparison, comparisonOK := branch.Cond.(*ast.BinaryExpr)
		if !comparisonOK || comparison.Op != token.EQL && comparison.Op != token.NEQ {
			continue
		}
		var stored *ast.SelectorExpr
		for _, pair := range [][2]ast.Expr{{comparison.X, comparison.Y}, {comparison.Y, comparison.X}} {
			selected, selectedOK := pair[0].(*ast.SelectorExpr)
			supplied, suppliedOK := pair[1].(*ast.Ident)
			if selectedOK && selected.Sel.Name == "ClientID" && suppliedOK && supplied.Name == "clientID" {
				stored = selected
			}
		}
		if stored == nil {
			continue
		}
		checked++
		mismatch := branch.Body
		if comparison.Op == token.EQL {
			mismatch, _ = branch.Else.(*ast.BlockStmt)
		}
		logged := false
		if mismatch != nil {
			for nested := range ast.Preorder(mismatch) {
				call, callOK := nested.(*ast.CallExpr)
				if !callOK || calleeName(call) != "WarnContext" || len(call.Args) < 2 {
					continue
				}
				keys := make(map[string]bool)
				for i := 2; i+1 < len(call.Args); i += 2 {
					if literal, literalOK := call.Args[i].(*ast.BasicLit); literalOK {
						keys[literal.Value] = true
					}
				}
				logged = logged || keys[`"grant_id"`] && keys[`"user_id"`]
				for _, arg := range call.Args[2:] {
					for value := range ast.Preorder(arg) {
						if name, named := value.(*ast.Ident); named && (name.Name == "raw" || name.Name == "clientID" || name.Name == "r") {
							t.Errorf("%s: mismatch diagnostic logs request data", positions.Position(name.Pos()))
						}
					}
				}
			}
		}
		if !logged {
			t.Errorf("%s: client mismatch needs a warning with grant and user IDs", positions.Position(branch.Pos()))
		}
	}
	if checked == 0 {
		t.Fatal("no client comparisons found; update mismatch drift coverage")
	}
}
