// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// passwordGatedModule is a module whose activation needs a password, like
// DB Manager.
type passwordGatedModule struct{ module.BaseModule }

func (*passwordGatedModule) ActiveByDefault() bool         { return false }
func (*passwordGatedModule) ActivationNeedsPassword() bool { return true }

// TestModuleActivationNeedsPassword fails when a stolen administrator
// session can switch on a module whose activation needs a password (DB
// Manager, whose raw SQL reaches every table) without the administrator's
// current password, or when wrong passwords stop counting toward the lockout.
// Switching it off and toggling other modules need none.
//
// Bug state: have ToggleActive call ActivateConfirmed without
// confirmActivation, and this fails.
func TestModuleActivationNeedsPassword(t *testing.T) {
	env := newMFATestEnv(t, false)
	lp := env.withLockout(t, 2)
	admin := env.createUser(t, model.RoleAdmin)
	logger := testutil.TestLoggerSilent()
	registry := module.NewRegistry(logger)
	for _, m := range []module.Module{
		&passwordGatedModule{module.NewBaseModule("sqlconsole", "1.0.0", "SQL console")},
		&passwordGatedModule{module.NewBaseModule("sqlconsole2", "1.0.0", "SQL console")},
	} {
		if err := registry.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	plain := module.NewBaseModule("plain", "1.0.0", "Plain")
	if err := registry.Register(&plain); err != nil {
		t.Fatal(err)
	}
	if err := registry.InitAll(&module.Context{DB: env.db, Logger: logger}); err != nil {
		t.Fatal(err)
	}
	h := NewModulesHandler(env.db, env.h.renderer, env.sm, registry, module.NewHookRegistry(logger))
	h.SetPasswordConfirmation(lp, service.NewEventService(env.db))

	actor := &admin
	toggle := func(name string, active bool, password string) (int, string) {
		body, err := json.Marshal(map[string]any{"active": active, "password": password})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/admin/modules/"+name+"/toggle", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req = addUserToContext(requestWithSession(env.sm, requestWithURLParams(req, map[string]string{"name": name})), actor)
		rr := httptest.NewRecorder()
		h.ToggleActive(rr, req)
		var resp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("toggle %s: response %q is not JSON: %v", name, rr.Body.String(), err)
		}
		return rr.Code, resp.Error
	}

	for _, tc := range []struct{ password, message string }{
		{"", "Enter your current password to activate this module."},
		{"Not-the-password!", "Your current password is incorrect."},
	} {
		if code, msg := toggle("sqlconsole", true, tc.password); code != http.StatusForbidden || msg != tc.message {
			t.Errorf("activation with password %q = %d %q, want 403 %q", tc.password, code, msg, tc.message)
		}
		if registry.IsActive("sqlconsole") {
			t.Fatalf("activation with password %q switched the module on", tc.password)
		}
	}
	if code, _ := toggle("sqlconsole", true, mfaTestPassword); code != http.StatusOK || !registry.IsActive("sqlconsole") {
		t.Fatalf("activation with the right password = %d, active %t", code, registry.IsActive("sqlconsole"))
	}
	if code, _ := toggle("sqlconsole", false, ""); code != http.StatusOK || registry.IsActive("sqlconsole") {
		t.Fatalf("deactivation = %d, active %t; want it allowed without a password", code, registry.IsActive("sqlconsole"))
	}
	if code, _ := toggle("plain", false, ""); code != http.StatusOK {
		t.Fatalf("toggling an ungated module = %d", code)
	}
	if code, _ := toggle("plain", true, ""); code != http.StatusOK || !registry.IsActive("plain") {
		t.Fatalf("activating an ungated module = %d", code)
	}

	// The earlier wrong password still counts after the right one: a second
	// wrong one locks the account, and then even the right one is refused.
	if code, msg := toggle("sqlconsole2", true, "Not-the-password-2!"); code != http.StatusForbidden || msg != "Too many failed attempts; try again later." {
		t.Fatalf("second wrong password = %d %q, want the lock message", code, msg)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if locked, _ := lp.IsAccountLocked(admin.Email, middleware.GetClientIP(req)); !locked {
		t.Fatal("wrong activation passwords do not count toward the lockout")
	}
	if code, msg := toggle("sqlconsole2", true, mfaTestPassword); code != http.StatusForbidden || msg != "Too many failed attempts; try again later." || registry.IsActive("sqlconsole2") {
		t.Fatalf("right password while locked = %d %q, active %t", code, msg, registry.IsActive("sqlconsole2"))
	}
	// A stored hash that cannot be checked refuses the activation without
	// counting it, and says so.
	actor = &store.User{ID: admin.ID, Email: "unverifiable@example.com", Role: model.RoleAdmin, PasswordHash: "not-a-hash"}
	if code, msg := toggle("sqlconsole2", true, mfaTestPassword); code != http.StatusForbidden || msg != "Your password could not be checked; the module stays off." || registry.IsActive("sqlconsole2") {
		t.Fatalf("activation with an unverifiable hash = %d %q, active %t", code, msg, registry.IsActive("sqlconsole2"))
	}

	for message, want := range map[string]int{
		"Module activation refused: password could not be verified": 1,
		"Module activation refused: no password":                    1,
		"Module activation refused: wrong current password":         2,
		"Module activated with password confirmation":               1,
		"Module activation refused: account locked":                 1,
	} {
		var got int
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM events WHERE message = ? AND category = ?`, message, model.EventCategorySecurity).Scan(&got); err != nil || got != want {
			t.Errorf("security events %q = %d, %v; want %d", message, got, err, want)
		}
	}
}

// TestActivateConfirmedOnlyAfterPassword fails when code other than
// ModulesHandler.ToggleActive calls Registry.ActivateConfirmed: that would
// switch on DB Manager without the password its activation needs. That
// ToggleActive checks the password before the call is
// TestModuleActivationNeedsPassword's job.
//
// Bug state: call registry.ActivateConfirmed from ToggleSidebar, and this
// names it.
func TestActivateConfirmedOnlyAfterPassword(t *testing.T) {
	checkGatedCalls(t, map[string]string{
		"internal/handler/modules.go (*ModulesHandler).ToggleActive": "calls it only after confirmActivation accepts the password",
	}, func(file *ast.File) []gatedCall {
		return gatedCalls(file, map[string]bool{"ActivateConfirmed": true}, nil)
	}, "activates a password-gated module; confirm the administrator's password first like ModulesHandler.ToggleActive")
}

// moduleActiveWriteSQL matches SQL that can set a module's active flag:
// an UPDATE of modules that assigns is_active (one that only returns or
// filters on it may be flagged too, which errs on the safe side), or any
// INSERT or REPLACE into modules, whatever the conflict clause, quoting or
// schema prefix.
var moduleActiveWriteSQL = regexp.MustCompile(`(?is)\bupdate(?:\s+or\s+\w+)?\s+(?:[\x60"\[]?\w+[\x60"\]]?\.)?[\x60"\[]?modules[\x60"\]]?\s+set\b[^;]*\bis_active\s*=` +
	`|\b(?:replace|insert(?:\s+or\s+\w+)?)\s+into\s+(?:[\x60"\[]?\w+[\x60"\]]?\.)?[\x60"\[]?modules\b`)

// moduleActiveWriteSites are where a module's active flag may be written,
// with what keeps each from switching a password-gated module on: Go
// functions and package-level constants, and named sqlc queries.
var moduleActiveWriteSites = map[string]string{
	"internal/module/registry.go (*Registry).setActive":        "refuses a password-gated module unless ActivateConfirmed called it",
	"internal/module/registry.go (*Registry).loadActiveStatus": "inserts a module's first row, inactive for a password-gated module",
	"internal/store/modules.sql.go const upsertModule":         "sqlc's UpsertModule, whose callers this test checks too",
	"internal/store/queries/modules.sql UpsertModule":          "the query behind upsertModule",
}

// TestModuleActiveFlagWritesAreGated fails when code outside
// moduleActiveWriteSites writes a module's active flag: a call to the
// store's UpsertModule (which overwrites it on conflict), a Go string or
// constant holding such SQL (literals joined with + included), or a named
// sqlc query or migration doing so. Any of these would switch DB Manager on
// without the password Registry.SetActive asks for. SQL assembled at run
// time from variables or fmt is out of its reach.
//
// Bug state: restore the SetModuleActive query removed from
// internal/store/queries/modules.sql (and regenerate), and this names both
// the query and its constant; or call UpsertModule from a handler.
func TestModuleActiveFlagWritesAreGated(t *testing.T) {
	for query, want := range map[string]bool{
		"UPDATE modules SET is_active = ?, updated_at = ? WHERE name = ?":                                true,
		"update modules\nset updated_at = now, is_active = 1":                                            true,
		"UPDATE OR IGNORE modules SET is_active = 1":                                                     true,
		`UPDATE "modules" SET is_active = 1`:                                                             true,
		"UPDATE main.modules SET is_active = 1":                                                          true,
		"INSERT INTO modules (name, is_active) VALUES (?, ?)":                                            true,
		"INSERT OR REPLACE INTO modules (name) VALUES (?)":                                               true,
		"REPLACE INTO modules (name, is_active) VALUES (?, 1)":                                           true,
		"INSERT INTO `modules` (name) VALUES (?)":                                                        true,
		"UPDATE modules SET show_in_sidebar = ? WHERE name = ?":                                          false,
		"UPDATE modules SET show_in_sidebar = ? WHERE name = ? RETURNING id, is_active, show_in_sidebar": false,
		"SELECT is_active FROM modules WHERE name = ?":                                                   false,
		"INSERT INTO modules_migrations (module, version) VALUES (?, ?)":                                 false,
		"UPDATE modules_migrations SET is_active = 1":                                                    false,
	} {
		if moduleActiveWriteSQL.MatchString(query) != want {
			t.Errorf("moduleActiveWriteSQL on %q = %t, want %t", query, !want, want)
		}
	}

	used := scanGatedCalls(t, moduleActiveWriteSites, func(file *ast.File) []gatedCall {
		calls := gatedCalls(file, map[string]bool{"UpsertModule": true}, nil)
		return append(calls, sqlLiterals(file, moduleActiveWriteSQL)...)
	}, "writes a module's active flag; switch modules through Registry.SetActive")
	for _, dir := range []string{"internal/store/queries", "internal/store/migrations"} {
		for _, q := range sqlFileStatements(t, dir) {
			if !moduleActiveWriteSQL.MatchString(q.sql) {
				continue
			}
			if _, ok := moduleActiveWriteSites[q.where]; ok {
				used[q.where] = true
				continue
			}
			t.Errorf("%s writes a module's active flag; switch modules through Registry.SetActive, then list it with its guard", q.where)
		}
	}
	for key := range moduleActiveWriteSites {
		if !used[key] {
			t.Errorf("moduleActiveWriteSites lists %s, which no longer writes a module's active flag; drop the entry", key)
		}
	}
}

// TestSQLLiteralScan checks the scan behind TestModuleActiveFlagWritesAreGated
// on a fixture: it names package-level constants and variables, joins
// literals split with +, and leaves other SQL alone.
func TestSQLLiteralScan(t *testing.T) {
	const src = `package p
const setActive = "UPDATE modules SET is_active = ? WHERE name = ?"
var split = "UPDATE modules SET " + "is_active = 1"
const sidebar = "UPDATE modules SET show_in_sidebar = ?"
func f() { db.Exec("REPLACE INTO modules (name) VALUES (?)"); db.Exec("SELECT 1") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range sqlLiterals(file, moduleActiveWriteSQL) {
		got = append(got, w.where)
	}
	want := []string{"const setActive", "var split", "f"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("found %v, want %v", got, want)
	}
}

// sqlLiterals returns the string expressions in file that pattern matches:
// literals, and literals joined with +. A match in a package-level constant
// or variable is named after it ("const upsertModule").
func sqlLiterals(file *ast.File, pattern *regexp.Regexp) []gatedCall {
	var found []gatedCall
	scan := func(node ast.Node, where string) {
		ast.Inspect(node, func(n ast.Node) bool {
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			switch expr.(type) {
			case *ast.BasicLit, *ast.BinaryExpr:
			default:
				return true
			}
			value, ok := stringValue(expr)
			if !ok {
				return true // not a constant string: look at its parts
			}
			if pattern.MatchString(value) {
				found = append(found, gatedCall{pos: expr.Pos(), where: where})
			}
			return false
		})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			scan(d, funcName(d))
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, value := range vs.Values {
					where := "a package-level declaration"
					if i < len(vs.Names) {
						where = d.Tok.String() + " " + vs.Names[i].Name
					}
					scan(value, where)
				}
			}
		}
	}
	return found
}

// sqlStatement is a named sqlc query, or a whole migration file.
type sqlStatement struct {
	where string // "internal/store/queries/modules.sql UpsertModule"
	sql   string
}

// sqlFileStatements reads the .sql files in dir, a directory relative to
// the repository root: a file with "-- name:" headers yields one statement
// per query, any other file one statement.
func sqlFileStatements(t *testing.T, dir string) []sqlStatement {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", dir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no .sql files in %s; the scan no longer sees them", dir)
	}
	var statements []sqlStatement
	for _, sqlPath := range paths {
		src, err := os.ReadFile(sqlPath) // #nosec G304 -- repository files
		if err != nil {
			t.Fatal(err)
		}
		rel := dir + "/" + filepath.Base(sqlPath)
		parts := sqlcQueryName.Split(string(src), -1)
		names := sqlcQueryName.FindAllStringSubmatch(string(src), -1)
		if len(names) == 0 {
			statements = append(statements, sqlStatement{where: rel, sql: string(src)})
			continue
		}
		for i, name := range names {
			statements = append(statements, sqlStatement{where: rel + " " + name[1], sql: parts[i+1]})
		}
	}
	return statements
}

// sqlcQueryName matches an sqlc query header, "-- name: GetModule :one".
var sqlcQueryName = regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)
