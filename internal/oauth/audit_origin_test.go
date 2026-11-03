// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
)

// lastAuthEvent returns the newest authentication event with the message.
func lastAuthEvent(t testing.TB, f *fixture, message string) auditEvent {
	t.Helper()
	var found *auditEvent
	for _, e := range authEvents(t, f) {
		if e.message == message {
			found = &e
		}
	}
	if found == nil {
		t.Fatalf("no %q audit event", message)
	}
	return *found
}

func (f *fixture) tokenRequest(values url.Values, remote string, header http.Header) {
	r := httptest.NewRequest(http.MethodPost, "https://cms.test/oauth/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = remote
	for key, entries := range header {
		r.Header[key] = entries
	}
	f.s.Token(httptest.NewRecorder(), r)
}

// TestOAuthAuditEventsCarryTheClientAddress pins audit A-10: authorize,
// revoke and reuse-detection events recorded no address and no path, so a
// connection revoked after a replay could not be tied to a source.
func TestOAuthAuditEventsCarryTheClientAddress(t *testing.T) {
	f := newFixture(t)

	// Consent: the handler hands the request to Authorize through WithRequest.
	consent := httptest.NewRequest(http.MethodPost, "/oauth/authorize?state=SECRET-STATE&code_challenge=SECRET-CHALLENGE#SECRET-FRAGMENT", nil)
	consent.RemoteAddr = "203.0.113.9:4711"
	request, err := f.s.Prepare(t.Context(), f.values)
	if err != nil {
		t.Fatal(err)
	}
	code, err := f.s.Authorize(WithRequest(t.Context(), consent), request, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := lastAuthEvent(t, f, "OAuth connection authorized")
	if e.ip != "203.0.113.9" || e.requestURL != "/oauth/authorize" {
		t.Fatalf("authorize event origin = %q %q", e.ip, e.requestURL)
	}

	// Token endpoint: a replayed refresh token revokes the family.
	first := f.redeem(t, code)
	f.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.values.Get("client_id")},
		"resource": {f.s.Resource()}, "refresh_token": {refresh(first)}}, "198.51.100.7:5555", nil)
	f.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.values.Get("client_id")},
		"resource": {f.s.Resource()}, "refresh_token": {refresh(first)}}, "198.51.100.8:5556", nil)
	e = lastAuthEvent(t, f, "OAuth refresh reuse revoked connection")
	if e.ip != "198.51.100.8" || e.requestURL != "/oauth/token" || e.level != "warning" {
		t.Fatalf("refresh reuse event origin = %+v", e)
	}

	// Token endpoint: a replayed authorization code.
	second := f.code(t)
	f.redeem(t, second)
	f.tokenRequest(url.Values{"grant_type": {"authorization_code"}, "client_id": {f.values.Get("client_id")}, "resource": {f.s.Resource()},
		"code": {second}, "redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}}, "198.51.100.9:5557", nil)
	e = lastAuthEvent(t, f, "OAuth authorization code reuse revoked connection")
	if e.ip != "198.51.100.9" || e.requestURL != "/oauth/token" {
		t.Fatalf("code reuse event origin = %+v", e)
	}

	// Revocation endpoint.
	third := f.redeem(t, f.code(t))
	revoke := httptest.NewRequest(http.MethodPost, "https://cms.test/oauth/revoke", strings.NewReader(url.Values{
		"client_id": {f.values.Get("client_id")}, "token": {access(third)}}.Encode()))
	revoke.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	revoke.RemoteAddr = "198.51.100.10:5558"
	f.s.Revoke(httptest.NewRecorder(), revoke)
	e = lastAuthEvent(t, f, "OAuth connection revoked by client")
	if e.ip != "198.51.100.10" || e.requestURL != "/oauth/revoke" {
		t.Fatalf("client revocation event origin = %+v", e)
	}

	// Account revocation, through the same context carrier.
	fourth := f.redeem(t, f.code(t))
	principal, err := f.s.VerifyAccess(t.Context(), access(fourth))
	if err != nil {
		t.Fatal(err)
	}
	account := httptest.NewRequest(http.MethodPost, "/account/security/oauth/revoke?grant_id=SECRET", nil)
	account.RemoteAddr = "198.51.100.11:5559"
	if err = f.s.RevokeGrant(WithRequest(t.Context(), account), f.user.ID, principal.GrantID); err != nil {
		t.Fatal(err)
	}
	e = lastAuthEvent(t, f, "OAuth connection revoked by account")
	if e.ip != "198.51.100.11" || e.requestURL != "/account/security/oauth/revoke" {
		t.Fatalf("account revocation event origin = %+v", e)
	}

	// Without a request the event is still written, with no origin.
	fifth := f.redeem(t, f.code(t))
	principal, err = f.s.VerifyAccess(t.Context(), access(fifth))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeGrant(t.Context(), f.user.ID, principal.GrantID); err != nil {
		t.Fatal(err)
	}
	if e = lastAuthEvent(t, f, "OAuth connection revoked by account"); e.ip != "" || e.requestURL != "" {
		t.Fatalf("event without a request has an origin: %+v", e)
	}

	// Nothing from the query string or fragment is stored.
	for _, e := range authEvents(t, f) {
		for _, secret := range []string{"SECRET-STATE", "SECRET-CHALLENGE", "SECRET-FRAGMENT", "SECRET"} {
			if strings.Contains(e.requestURL, secret) || strings.Contains(e.ip, secret) {
				t.Fatalf("audit event stored request detail %q: %+v", secret, e)
			}
		}
	}
}

// TestOAuthAuditAddressFollowsTrustedProxyRules shows the address comes from
// the project's proxy-aware helper, not from RemoteAddr or from any
// X-Forwarded-For header a client cares to send.
func TestOAuthAuditAddressFollowsTrustedProxyRules(t *testing.T) {
	t.Cleanup(func() {
		if err := middleware.ConfigureTrustedProxies(""); err != nil {
			t.Error(err)
		}
	})
	forwarded := http.Header{"X-Forwarded-For": {"203.0.113.50"}}
	refreshReplay := func(f *fixture, remote string) auditEvent {
		first := f.redeem(t, f.code(t))
		values := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.values.Get("client_id")},
			"resource": {f.s.Resource()}, "refresh_token": {refresh(first)}}
		f.tokenRequest(values, remote, forwarded)
		f.tokenRequest(values, remote, forwarded)
		return lastAuthEvent(t, f, "OAuth refresh reuse revoked connection")
	}

	if err := middleware.ConfigureTrustedProxies(""); err != nil {
		t.Fatal(err)
	}
	if e := refreshReplay(newFixture(t), "198.51.100.20:6000"); e.ip != "198.51.100.20" {
		t.Fatalf("untrusted peer's X-Forwarded-For was believed: %q", e.ip)
	}
	if err := middleware.ConfigureTrustedProxies("198.51.100.0/24"); err != nil {
		t.Fatal(err)
	}
	if e := refreshReplay(newFixture(t), "198.51.100.20:6000"); e.ip != "203.0.113.50" {
		t.Fatalf("trusted proxy's X-Forwarded-For was ignored: %q", e.ip)
	}
}

// TestOAuthAuditCallersPassTheRequest keeps the address from being lost again
// when an audited call is added or moved (A-10). Audit events read the
// address from the context, so a caller that has a request must put it there:
//   - a Service method that takes an *http.Request and audits must call
//     WithRequest;
//   - the handler's calls to the Service methods that audit without a request
//     of their own (Authorize, RevokeGrant) must pass oauth.WithRequest.
//
// Bug state: pass r.Context() instead of oauth.WithRequest(...) to
// h.oauth.Authorize in internal/handler/oauth.go, and this names the call.
func TestOAuthAuditCallersPassTheRequest(t *testing.T) {
	fset := token.NewFileSet()
	parse := func(pattern string) []*ast.File {
		names, err := filepath.Glob(pattern)
		if err != nil || len(names) == 0 {
			t.Fatalf("no files match %s: %v", pattern, err)
		}
		var files []*ast.File
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
		return files
	}

	audited := 0
	for _, file := range parse("*.go") {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !takesHTTPRequest(fn) {
				continue
			}
			var audits, scoped bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch callee := calleeName(call); callee {
					case "audit":
						audits = true
					case "WithRequest":
						scoped = true
					}
				}
				return true
			})
			if audits {
				audited++
				if !scoped {
					t.Errorf("%s audits without WithRequest, so its events carry no client address", fset.Position(fn.Pos()))
				}
			}
		}
	}
	if audited < 2 {
		t.Fatalf("found %d request-taking audited functions, want Token and Revoke; the scan is broken", audited)
	}

	wrapped := 0
	for _, file := range parse(filepath.Join("..", "handler", "*.go")) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeName(call)
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if (name != "Authorize" && name != "RevokeGrant") || !isSelector || !isOAuthField(selector.X) {
				return true
			}
			wrapped++
			if len(call.Args) == 0 || !isWithRequestCall(call.Args[0]) {
				t.Errorf("%s: %s must be called with oauth.WithRequest(ctx, r) so the audit event carries the client address", fset.Position(call.Pos()), name)
			}
			return true
		})
	}
	if wrapped < 2 {
		t.Fatalf("found %d handler calls to Authorize/RevokeGrant, want 2; the scan is broken", wrapped)
	}
}

func takesHTTPRequest(fn *ast.FuncDecl) bool {
	for _, field := range fn.Type.Params.List {
		if star, ok := field.Type.(*ast.StarExpr); ok {
			if selector, ok := star.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "Request" {
				return true
			}
		}
	}
	return false
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

// isOAuthField reports whether expr is a selector ending in ".oauth", the
// handler's *oauth.Service field.
func isOAuthField(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "oauth"
}

func isWithRequestCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && calleeName(call) == "WithRequest"
}
