// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func captureOAuthLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	return &logs
}

func assertClientFaultLog(t *testing.T, logged, message, cause, clientID string) {
	t.Helper()
	for _, field := range []string{"level=ERROR", "error=", message, cause, "client_id=" + clientID} {
		if !strings.Contains(logged, field) {
			t.Errorf("client fault log lacks %q: %s", field, logged)
		}
	}
	if got := strings.Count(logged, message); got != 1 {
		t.Errorf("client fault logged %d times, want 1: %s", got, logged)
	}
}

func TestCorruptOAuthClientRedirectsAreLogged(t *testing.T) {
	for _, tc := range []struct{ name, raw, cause string }{
		{"syntax", `["https://client.test/PRIVATE-CACHED-REDIRECT"`, "unexpected end of JSON input"},
		{"type", `{"private":"PRIVATE-CACHED-REDIRECT"}`, "cannot unmarshal object"},
	} {
		for _, operation := range []string{"prepare", "authorization_code", "refresh_token"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				f := newFixture(t)
				clientID := f.values.Get("client_id")
				code := f.code(t)
				values := url.Values{"grant_type": {operation}, "client_id": {clientID}, "resource": {f.s.Resource()},
					"code": {code}, "code_verifier": {f.verifier}, "redirect_uri": {f.values.Get("redirect_uri")}}
				var first map[string]any
				if operation == "refresh_token" {
					first = f.redeem(t, code)
					values = url.Values{"grant_type": {operation}, "client_id": {clientID}, "resource": {f.s.Resource()}, "refresh_token": {refresh(first)}}
				}
				cached, err := f.s.q.GetOAuthClient(t.Context(), clientID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.Exec("UPDATE oauth_clients SET redirect_uris=? WHERE id=?", tc.raw, clientID); err != nil {
					t.Fatal(err)
				}
				var fetches atomic.Int64
				f.s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					fetches.Add(1)
					return nil, errors.New("unexpected metadata fetch")
				})
				request := func() *httptest.ResponseRecorder {
					t.Helper()
					response := httptest.NewRecorder()
					if operation == "prepare" {
						prepared, prepareErr := f.s.Prepare(t.Context(), f.values)
						if prepareErr != nil {
							if !Retryable(prepareErr) || f.s.PreConsentErrorRedirect(prepared, problemCode(prepareErr)) != "" {
								t.Error("corrupt cache was treated as a non-retryable or redirectable authorization error")
							}
							WriteProblem(response, prepareErr)
						} else if prepared.ClientID != clientID || prepared.RedirectURI != f.values.Get("redirect_uri") {
							t.Error("healthy authorization preparation lost the client or redirect")
						}
						return response
					}
					r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					f.s.Token(response, r)
					return response
				}
				logs := captureOAuthLogs(t)
				response := request()
				var problemBody map[string]any
				if err = json.Unmarshal(response.Body.Bytes(), &problemBody); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusServiceUnavailable || problemBody["error"] != "server_error" {
					t.Errorf("corrupt cache: status=%d error=%v", response.Code, problemBody["error"])
				}
				assertClientFaultLog(t, logs.String(), "stored OAuth client has invalid redirect URIs", tc.cause, clientID)
				for _, secret := range []string{"PRIVATE-CACHED-REDIRECT", code, f.verifier, f.values.Get("state"), access(first), refresh(first)} {
					if secret != "" && strings.Contains(logs.String()+response.Body.String(), secret) {
						t.Error("client fault exposed stored redirect data, credentials or state")
					}
				}
				if strings.Contains(response.Body.String(), tc.cause) || access(problemBody) != "" || refresh(problemBody) != "" {
					t.Error("server-error response exposed an internal cause or credentials")
				}
				if operation == "refresh_token" {
					if _, err = f.s.VerifyAccess(t.Context(), access(first)); err != nil {
						t.Fatalf("client cache fault invalidated existing access: %v", err)
					}
				}
				if _, err = f.db.Exec("UPDATE oauth_clients SET redirect_uris=? WHERE id=?", cached.RedirectUris, clientID); err != nil {
					t.Fatal(err)
				}
				logs.Reset()
				response = request()
				if response.Code != http.StatusOK || strings.Contains(logs.String(), "level=ERROR") {
					t.Fatalf("repaired cache: status=%d logs=%q", response.Code, logs.String())
				}
				if operation != "prepare" {
					var credentials map[string]any
					if err = json.Unmarshal(response.Body.Bytes(), &credentials); err != nil {
						t.Fatal(err)
					}
					principal, verifyErr := f.s.VerifyAccess(t.Context(), access(credentials))
					if verifyErr != nil || principal.ClientID != clientID || refresh(credentials) == "" {
						t.Fatalf("retry did not issue usable credentials: %v", verifyErr)
					}
					if operation == "refresh_token" && refresh(credentials) == refresh(first) {
						t.Error("healthy refresh did not rotate its credential")
					}
				}
				if fetches.Load() != 0 {
					t.Error("cached client resolution unexpectedly fetched metadata")
				}
			})
		}
	}
}

// Done is evaluated by resolve's select only after joining the shared flight.
// Keep the database on the parent context so its own cancellation checks do
// not trigger this test barrier. Cancellation and deadlines are preserved.
type clientResolutionProbeContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *clientResolutionProbeContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

type clientResolutionProbeDB struct{ *sql.DB }

func (db clientResolutionProbeDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if probe, ok := ctx.(*clientResolutionProbeContext); ok {
		ctx = probe.Context
	}
	return db.DB.QueryRowContext(ctx, query, args...)
}

func TestUnexpectedOAuthClientFlightResultsAreLogged(t *testing.T) {
	const clientID = "https://client.test/shared-result"
	const privateResult = "PRIVATE-SHARED-RESULT"
	upstreamErr := errors.New("upstream metadata failure")
	for _, tc := range []struct {
		name, cause string
		value       any
		err         error
	}{
		{name: "wrong type", cause: "unexpected result type string", value: privateResult},
		{name: "nil", cause: "unexpected result type <nil>"},
		{name: "normal", value: registeredClient{ID: clientID, Name: "Client", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}}},
		{name: "upstream failure", err: upstreamErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.s.options.AllowedHosts = []string{"client.test"}
			f.s.q = store.New(clientResolutionProbeDB{DB: f.db})
			var fetches atomic.Int64
			f.s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				fetches.Add(1)
				return nil, errors.New("unexpected metadata fetch")
			})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			flight := f.s.clients.group.DoChan(clientID, func() (any, error) {
				<-release
				return tc.value, tc.err
			})
			ctx := &clientResolutionProbeContext{Context: t.Context(), joined: make(chan struct{})}
			type outcome struct {
				client registeredClient
				err    error
			}
			completed := make(chan outcome, 1)
			logs := captureOAuthLogs(t)
			go func() {
				client, resolveErr := f.s.clients.resolve(ctx, clientID)
				completed <- outcome{client: client, err: resolveErr}
			}()
			select {
			case <-ctx.joined:
			case <-time.After(5 * time.Second):
				t.Fatal("resolver did not join the shared flight")
			}
			unblock()
			var got outcome
			select {
			case got = <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("resolver did not return the shared result")
			}
			if result := <-flight; !result.Shared || fetches.Load() != 0 {
				t.Fatal("fixture did not exercise the real shared-result path")
			}
			if tc.cause != "" {
				if problemCode(got.err) != "server_error" || !Retryable(got.err) {
					t.Fatalf("unexpected shared result: %v", got.err)
				}
				assertClientFaultLog(t, logs.String(), "OAuth client lookup returned an unexpected result", tc.cause, clientID)
				response := httptest.NewRecorder()
				WriteProblem(response, got.err)
				if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), tc.cause) {
					t.Errorf("shared-result error response: status=%d body=%q", response.Code, response.Body.String())
				}
				if strings.Contains(logs.String()+response.Body.String(), privateResult) {
					t.Error("unexpected result contents leaked into logs or response")
				}
			} else {
				if !errors.Is(got.err, tc.err) {
					t.Errorf("shared error changed: %v, want %v", got.err, tc.err)
				}
				if tc.err == nil && (got.client.ID != clientID || !got.client.acceptsRedirect("https://client.test/callback")) {
					t.Error("valid shared client was not returned")
				}
				if strings.Contains(logs.String(), "level=ERROR") {
					t.Errorf("normal result or propagated failure was logged again: %s", logs.String())
				}
			}
		})
	}
}

// Every internal client-resolution server_error needs a cause and client ID.
// Protocol refusals and cancellation retain their existing classifications.
func TestOAuthClientServerErrorsUseDiagnostics(t *testing.T) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, "clients.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for node := range ast.Preorder(parsed) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			continue
		}
		name := calleeName(call)
		codeIndex := 0
		if name == "failure" {
			codeIndex = 1
		} else if name != "problem" {
			continue
		}
		if len(call.Args) <= codeIndex {
			continue
		}
		code, ok := call.Args[codeIndex].(*ast.BasicLit)
		if !ok || code.Value != `"server_error"` {
			continue
		}
		checked++
		if name == "problem" {
			t.Errorf("%s: server_error must use failure with a cause and client_id", positions.Position(call.Pos()))
			continue
		}
		if len(call.Args) < 6 {
			t.Errorf("%s: missing diagnostic cause or client_id", positions.Position(call.Pos()))
			continue
		}
		if cause, ok := call.Args[3].(*ast.Ident); ok && cause.Name == "nil" {
			t.Errorf("%s: server_error has no cause", positions.Position(call.Pos()))
		}
		clientID := false
		for i := 4; i+1 < len(call.Args); i += 2 {
			if key, ok := call.Args[i].(*ast.BasicLit); ok && key.Value == `"client_id"` {
				clientID = true
			}
		}
		if !clientID {
			t.Errorf("%s: server_error has no client_id", positions.Position(call.Pos()))
		}
	}
	if checked == 0 {
		t.Fatal("no client server-error paths found; update the diagnostic coverage")
	}
}
