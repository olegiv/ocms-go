// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/session"
	"github.com/olegiv/ocms-go/internal/store"
)

// openShortBusyDB opens a migrated file database whose connections give up on
// a locked database after 300 ms instead of 5 s, with the production
// transaction mode (BEGIN IMMEDIATE), so a lock held across a response shows
// up as a quick failure instead of a slow one.
func openShortBusyDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "lock.db") +
		"?_pragma=busy_timeout(300)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate&_time_format=sqlite&_timezone=UTC"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestAPIKeyUpdateFailureRedirectsWithoutHoldingTheWriteLock pins R1 P3-3: the
// update ran inside a write transaction whose rollback was deferred, and the
// error paths flashed and redirected first. The session commit that happens as
// the redirect is written uses another connection, which waited on the SQLite
// write lock until busy_timeout and then failed the response with a 500. A
// failed update has to give the lock up before it answers.
func TestAPIKeyUpdateFailureRedirectsWithoutHoldingTheWriteLock(t *testing.T) {
	db := openShortBusyDB(t)
	user := createTestAdminUser(t, db)
	now := time.Now()
	key, err := store.New(db).CreateAPIKey(t.Context(), store.CreateAPIKeyParams{
		Name: "Lock fixture", KeyHash: "hash", KeyPrefix: "lock_", Permissions: `["pages:read"]`,
		IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A storage fault inside the transaction, after it has taken the lock.
	if _, err = db.ExecContext(t.Context(), `CREATE TRIGGER fail_key_update BEFORE UPDATE ON api_keys
		BEGIN SELECT RAISE(ABORT, 'simulated storage fault'); END`); err != nil {
		t.Fatal(err)
	}

	sm := session.New(db, true)
	h := NewAPIKeysHandler(db, newStatusTestRenderer(t, db, sm), sm)
	id := strconv.FormatInt(key.ID, 10)
	handler := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = requestWithURLParams(addUserToContext(r, &user), map[string]string{"id": id})
		h.Update(w, r)
	}))
	form := url.Values{"name": {"Changed"}, "permissions": {"pages:read"}, "is_active": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/api-keys/"+id, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: a failed update must redirect with its flash message "+
			"instead of failing the session commit while its transaction is open (body %q)", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/api-keys/"+id; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	var name string
	if err = db.QueryRowContext(t.Context(), `SELECT name FROM api_keys WHERE id = ?`, key.ID).Scan(&name); err != nil || name != "Lock fixture" {
		t.Fatalf("failed update changed the key: name=%q err=%v", name, err)
	}
}

// isResponseCall reports the helpers and calls that answer the request, which
// makes the session middleware commit its store on another connection.
func isResponseCall(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		switch {
		case strings.HasPrefix(fn.Name, "flash"),
			fn.Name == "logAndHTTPError", fn.Name == "logAndInternalError", fn.Name == "renderTempl":
			return fn.Name, true
		}
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "http" {
			switch fn.Sel.Name {
			case "Redirect", "Error", "NotFound":
				return "http." + fn.Sel.Name, true
			}
		}
	}
	return "", false
}

func isBeginTxCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "BeginTx" || (sel.Sel.Name == "Begin" && len(call.Args) == 0))
}

// TestFunctionsThatBeginATransactionDoNotAnswerTheRequest enforces the
// invariant behind R1 P3-3 for the whole package: a function that opens a
// database transaction (BeginTx) must not itself flash, redirect or render. The
// session commit that the response triggers needs the SQLite write lock the
// open transaction holds, and a deferred rollback runs only after the response
// was written. The transaction belongs in a helper that returns an error, as in
// LanguagesHandler.updateLanguage and APIKeysHandler.updateAPIKey, and the
// handler answers after the helper has committed or rolled back.
func TestFunctionsThatBeginATransactionDoNotAnswerTheRequest(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	beginners := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			begins := false
			var answers []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isBeginTxCall(call) {
					begins = true
				}
				if name, ok := isResponseCall(call); ok {
					answers = append(answers, name+" at "+fset.Position(call.Pos()).String())
				}
				return true
			})
			if !begins {
				continue
			}
			beginners++
			for _, a := range answers {
				violations = append(violations, fn.Name.Name+" begins a transaction and calls "+a)
			}
		}
	}
	if beginners == 0 {
		t.Fatal("found no function that begins a transaction; the scan is not looking at the handlers")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}
