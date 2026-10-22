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
// and an entry here saying what it is. What an allowed site does with its
// write is the job of that site's tests (the import's role rule is
// TestImportOverwriteNeverChangesAdminRole).
//
// The scan sees direct method calls only: a method value
// (f := q.UpdateUser) or raw SQL that updates users outside internal/store
// is out of its reach, and so are lockout counters and session versions.
//
// Bug state: add a q.UpdateUser call to any handler not listed here, and
// this names it.
func TestAccountWritesAreGated(t *testing.T) {
	checkGatedCalls(t, accountWriteSites, func(file *ast.File) []gatedCall {
		return gatedCalls(file, accountWriteMethods, func(call *ast.CallExpr, method string) bool {
			return method == "CreateUser" && len(call.Args) == 2 && createsPublicUser(call.Args[1])
		})
	}, "sets a user's email, role or password; ask for the acting administrator's password like UsersHandler.Update")
}

// secondFactorRemovalMethods remove a user's second factor: the
// mfa.Service method and the store methods behind it.
var secondFactorRemovalMethods = map[string]bool{
	"Disable":             true,
	"DeleteUserTOTP":      true,
	"DeleteRecoveryCodes": true,
}

// secondFactorRemovalSites are the functions allowed to call
// secondFactorRemovalMethods, with what guards each.
var secondFactorRemovalSites = map[string]string{
	"internal/handler/users.go (*UsersHandler).ResetMFA": "asks for the acting administrator's password (checkActorPassword)",
	"internal/handler/mfa.go (*AuthHandler).DisableTOTP": "asks for a current code from the user's own second factor",
	"cmd/ocms/mfa.go runMFAReset":                        "ocms mfa-reset, run on the server",
	"internal/mfa/service.go (*Service).Disable":         "the service method the sites above call",
	"internal/mfa/service.go replaceRecoveryCodes":       "replaces recovery codes with new ones, behind a current code or enrollment",
}

// TestSecondFactorRemovalIsGated fails when code outside
// secondFactorRemovalSites removes a user's second factor. Removing it
// leaves an account to whoever has its password, so an administrator's
// reset asks for that administrator's password (pre-PR review of the
// two-factor branch), and any new path needs a guard and an entry here.
//
// Bug state: call h.mfa.Disable from another UsersHandler method, and this
// names it.
func TestSecondFactorRemovalIsGated(t *testing.T) {
	checkGatedCalls(t, secondFactorRemovalSites, func(file *ast.File) []gatedCall {
		return gatedCalls(file, secondFactorRemovalMethods, nil)
	}, "removes a user's second factor; ask for the acting administrator's password like UsersHandler.ResetMFA")
}

// checkGatedCalls fails for every call scan finds outside sites, and for
// every entry of sites that no longer makes one.
func checkGatedCalls(t *testing.T, sites map[string]string, scan func(*ast.File) []gatedCall, problem string) {
	t.Helper()
	used := scanGatedCalls(t, sites, scan, problem)
	for key := range sites {
		if !used[key] {
			t.Errorf("the allowed sites list %s, which no longer makes such a call; drop the entry", key)
		}
	}
}

// scanGatedCalls fails for every call scan finds outside sites, and returns
// the entries of sites it found calls in.
func scanGatedCalls(t *testing.T, sites map[string]string, scan func(*ast.File) []gatedCall, problem string) map[string]bool {
	t.Helper()
	fset, files := parseRepoGoFiles(t)
	used := map[string]bool{}
	for rel, file := range files {
		for _, c := range scan(file) {
			key := rel + " " + c.where
			if _, ok := sites[key]; ok {
				used[key] = true
				continue
			}
			t.Errorf("%s: %s %s, then list it with its guard", fset.Position(c.pos), c.where, problem)
		}
	}
	return used
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
	got := gatedCalls(file, accountWriteMethods, func(call *ast.CallExpr, method string) bool {
		return method == "CreateUser" && len(call.Args) == 2 && createsPublicUser(call.Args[1])
	})
	if len(got) != 5 {
		for _, w := range got {
			t.Logf("found %s", fset.Position(w.pos))
		}
		t.Fatalf("found %d account writes, want 5", len(got))
	}
}

// gatedCall is a call to a method a gated-call test watches.
type gatedCall struct {
	pos   token.Pos
	where string
}

// gatedCalls returns the calls in file to methods, except those allowed
// reports true for (allowed may be nil).
func gatedCalls(file *ast.File, methods map[string]bool, allowed func(call *ast.CallExpr, method string) bool) []gatedCall {
	var calls []gatedCall
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
			if !ok || !methods[sel.Sel.Name] {
				return true
			}
			if allowed != nil && allowed(call, sel.Sel.Name) {
				return true
			}
			calls = append(calls, gatedCall{pos: call.Pos(), where: where})
			return true
		})
	}
	return calls
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
