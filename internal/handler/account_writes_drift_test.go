// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// accountWriteMethods are the store.Queries methods that set a user's
// email, role or password: the ones a stolen administrator session could
// use to take an account over.
var accountWriteMethods = map[string]bool{
	"CreateUser":             true,
	"UpdateUser":             true,
	"UpdateUserPassword":     true,
	"UpdateUserPasswordHash": true,
}

// accountWriteSites are the functions allowed to call accountWriteMethods
// with a role other than the public one, with what keeps each from handing
// an account to whoever holds a session.
var accountWriteSites = map[string]string{
	"internal/handler/users.go (*UsersHandler).Create":       "a new administrator needs the acting administrator's password (checkActorPassword)",
	"internal/handler/users.go (*UsersHandler).Update":       "actorPasswordNeeded asks for the acting administrator's password for a new password, email or admin role",
	"internal/handler/auth.go (*AuthHandler).Login":          "rehashes the password the user has just signed in with",
	"internal/transfer/importer.go (*Importer).importUsers":  "importRole never grants or removes the admin role; new users get a random password",
	"internal/store/seed.go Seed":                            "startup seeding of the default administrator, opt-in by OCMS_DO_SEED",
	"internal/store/seed_demo.go RotateDemoAdminPassword":    "demo mode startup; the password is random and shown only in the informer bar",
	"internal/store/seed_demo.go RotateDefaultAdminPassword": "demo mode startup; replaces the well-known default password with a random one",
	"internal/store/seed_demo.go seedDemoUsers":              "demo mode startup seeding of the demo accounts",
}

// TestAccountWritesAreGated fails when code outside accountWriteSites sets a
// user's email, role or password, other than creating a public user.
//
// The user form asks for the acting administrator's password before such
// changes, but the content import overwrote roles from the archive with no
// check, so a stolen session could create an editor it knew the password of
// and import it as an administrator (pre-PR review of the two-factor
// branch). A new path that writes these fields needs the same protection,
// and an entry here saying what it is.
//
// Bug state: have importUsers write the archive's role on overwrite again
// and drop its entry here, and this names it.
func TestAccountWritesAreGated(t *testing.T) {
	fset, files := parseRepoGoFiles(t)
	used := map[string]bool{}
	for rel, file := range files {
		for _, w := range accountWrites(file) {
			key := rel + " " + w.where
			if _, ok := accountWriteSites[key]; ok {
				used[key] = true
				continue
			}
			t.Errorf("%s: %s sets a user's email, role or password; ask for the acting administrator's password like UsersHandler.Update, then list it in accountWriteSites", fset.Position(w.pos), w.where)
		}
	}
	for key := range accountWriteSites {
		if !used[key] {
			t.Errorf("accountWriteSites lists %s, which no longer writes user accounts; drop the entry", key)
		}
	}
}

// TestAccountWriteScan checks the scan behind TestAccountWritesAreGated on a
// fixture: creating a public user is allowed anywhere, every other account
// write is found.
func TestAccountWriteScan(t *testing.T) {
	const src = `package p
func f() {
	q.CreateUser(ctx, store.CreateUserParams{Email: e, Role: model.RolePublic})
	q.CreateUser(ctx, store.CreateUserParams{Email: e, Role: "public"})
	q.CreateUser(ctx, store.CreateUserParams{Email: e, Role: role})
	q.CreateUser(ctx, params)
	q.UpdateUser(ctx, store.UpdateUserParams{Role: model.RolePublic})
	q.UpdateUserPassword(ctx, p)
	st.queries.UpdateUserPasswordHash(ctx, p)
	q.GetUser(ctx, 1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountWrites(file); len(got) != 5 {
		for _, w := range got {
			t.Logf("found %s", fset.Position(w.pos))
		}
		t.Fatalf("found %d account writes, want 5", len(got))
	}
}

// accountWrite is a call to one of accountWriteMethods.
type accountWrite struct {
	pos   token.Pos
	where string
}

// accountWrites returns the calls in file to accountWriteMethods, except a
// CreateUser whose parameters set the public role.
func accountWrites(file *ast.File) []accountWrite {
	var writes []accountWrite
	for _, decl := range file.Decls {
		where := "a package-level declaration"
		if fn, ok := decl.(*ast.FuncDecl); ok {
			where = funcName(fn)
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !accountWriteMethods[sel.Sel.Name] {
				return true
			}
			if sel.Sel.Name == "CreateUser" && len(call.Args) == 2 && createsPublicUser(call.Args[1]) {
				return true
			}
			writes = append(writes, accountWrite{pos: call.Pos(), where: where})
			return true
		})
	}
	return writes
}

// createsPublicUser reports whether params is a composite literal whose Role
// is model.RolePublic or "public".
func createsPublicUser(params ast.Expr) bool {
	lit, ok := ast.Unparen(params).(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Role" {
			continue
		}
		if sel, ok := kv.Value.(*ast.SelectorExpr); ok && sel.Sel.Name == "RolePublic" {
			return true
		}
		value, ok := stringValue(kv.Value)
		return ok && value == "public"
	}
	return false
}
