// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

type revokeAuditReadFault struct {
	*sql.DB
	query   string
	missing bool
}

func (db *revokeAuditReadFault) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "-- name: "+db.query+" :") {
		if db.missing {
			return db.DB.QueryRowContext(ctx, "SELECT 1 WHERE 0")
		}
		return db.DB.QueryRowContext(ctx, "SELECT * FROM unavailable_revoke_audit_data")
	}
	return db.DB.QueryRowContext(ctx, query, args...)
}

// TestRevokeGrantAuditReadFailuresAreLogged keeps committed revocations
// successful while making a lost audit event visible. It discovers the
// post-write audit queries so a new read also receives fault coverage.
func TestRevokeGrantAuditReadFailuresAreLogged(t *testing.T) {
	type auditCase struct {
		name, query         string
		missing, writeFault bool
	}
	cases := []auditCase{
		{name: "normal"},
		{name: "write failure", writeFault: true},
	}
	for _, query := range revokeGrantAuditLookups(t) {
		for _, missing := range []bool{false, true} {
			cases = append(cases, auditCase{name: fmt.Sprintf("%s/missing=%t", query, missing), query: query, missing: missing})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			code := f.code(t)
			body := f.redeem(t, code)
			principal, err := f.s.VerifyAccess(t.Context(), access(body))
			if err != nil {
				t.Fatal(err)
			}
			if tc.query != "" {
				f.s.q = store.New(&revokeAuditReadFault{DB: f.db, query: tc.query, missing: tc.missing})
			}
			if tc.writeFault {
				if _, err = f.db.Exec(`CREATE TRIGGER fail_owned_revoke BEFORE UPDATE OF revoked_at ON oauth_grants BEGIN SELECT RAISE(ABORT, 'revocation_write_blocked'); END`); err != nil {
					t.Fatal(err)
				}
			}
			var logs bytes.Buffer
			previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() {
				slog.SetDefault(previous)
				log.SetOutput(writer)
				log.SetFlags(flags)
			})

			err = f.s.RevokeGrant(t.Context(), f.user.ID, principal.GrantID)
			if tc.writeFault {
				if err == nil || !strings.Contains(err.Error(), "revocation_write_blocked") {
					t.Errorf("write failure = %v, want the write error", err)
				}
			} else if err != nil {
				t.Errorf("committed revocation returned an error: %v", err)
			}
			warning := "OAuth connection revoked without audit event"
			wantWarnings := 0
			if tc.query != "" {
				wantWarnings = 1
				cause := "unavailable_revoke_audit_data"
				if tc.missing {
					cause = sql.ErrNoRows.Error()
				}
				for _, field := range []string{"level=WARN", "error=", cause, "grant_id=" + principal.GrantID, fmt.Sprintf("user_id=%d", f.user.ID)} {
					if !strings.Contains(logs.String(), field) {
						t.Errorf("audit-read warning lacks %q: %s", field, logs.String())
					}
				}
			}
			if got := strings.Count(logs.String(), warning); got != wantWarnings {
				t.Errorf("lost-audit warnings=%d, want %d: %s", got, wantWarnings, logs.String())
			}
			for _, secret := range []string{code, access(body), refresh(body), f.verifier, f.values.Get("state")} {
				if strings.Contains(logs.String(), secret) {
					t.Error("warning leaked a credential or client state")
				}
			}
			var revoked int64
			if err = f.db.QueryRowContext(t.Context(), "SELECT revoked_at FROM oauth_grants WHERE id=?", principal.GrantID).Scan(&revoked); err != nil {
				t.Fatal(err)
			}
			if (revoked == 0) != tc.writeFault {
				t.Errorf("revoked_at=%d, write failure=%t", revoked, tc.writeFault)
			}
			countAudits := func() int {
				t.Helper()
				count := 0
				for _, event := range authEvents(t, f) {
					if event.message == "OAuth connection revoked by account" {
						count++
					}
				}
				return count
			}
			wantAudits := 1
			if tc.query != "" || tc.writeFault {
				wantAudits = 0
			}
			if got := countAudits(); got != wantAudits {
				t.Errorf("audit events=%d, want %d", got, wantAudits)
			}

			// Read faults cannot undo the committed write. Once the fault is
			// cleared, access/refresh credentials must still be rejected.
			f.s.q = store.New(f.db)
			_, err = f.s.VerifyAccess(t.Context(), access(body))
			if tc.writeFault {
				if err != nil {
					t.Errorf("failed write invalidated the access token: %v", err)
				}
				if _, err = f.db.Exec("DROP TRIGGER fail_owned_revoke"); err != nil {
					t.Fatal(err)
				}
			} else {
				if problemCode(err) != "invalid_token" {
					t.Errorf("access after committed revoke: %v", err)
				}
				if status, response := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}}); status != http.StatusBadRequest || response["error"] != "invalid_grant" {
					t.Errorf("refresh after committed revoke: status=%d error=%v", status, response["error"])
				}
			}
			logs.Reset()
			if err = f.s.RevokeGrant(t.Context(), f.user.ID, principal.GrantID); err != nil {
				t.Fatalf("healthy retry: %v", err)
			}
			if strings.Contains(logs.String(), warning) {
				t.Errorf("healthy retry reported a lost audit: %s", logs.String())
			}
			if tc.writeFault {
				wantAudits = 1
			}
			if got := countAudits(); got != wantAudits {
				t.Errorf("audit events after retry=%d, want %d", got, wantAudits)
			}
		})
	}
}

// revokeGrantAuditLookups inventories direct query calls between the
// revocation write and its audit. Adding an unchecked audit read must add a
// failing runtime case above without maintaining a separate query list.
func revokeGrantAuditLookups(t *testing.T) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "RevokeGrant" {
			continue
		}
		var queries []string
		wrote := false
		for node := range ast.Preorder(fn.Body) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				continue
			}
			if wrote && calleeName(call) == "audit" && len(queries) > 0 {
				return queries
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			receiver, ok := method.X.(*ast.SelectorExpr)
			if !ok || receiver.Sel.Name != "q" {
				continue
			}
			if method.Sel.Name == "RevokeOwnedOAuthGrant" {
				wrote = true
			} else if wrote {
				queries = append(queries, method.Sel.Name)
			}
		}
	}
	t.Fatal("missing revoke write, audit lookup or audit call in RevokeGrant")
	return nil
}
