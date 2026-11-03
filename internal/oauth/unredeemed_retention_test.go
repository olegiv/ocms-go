// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// unredeemedGrantAt inserts a grant, with no token, created at the given time.
func unredeemedGrantAt(t testing.TB, f *fixture, id string, createdAt, revokedAt int64) {
	t.Helper()
	retentionExec(t, f, `INSERT INTO oauth_grants (id,user_id,client_id,scopes,resource,session_version,created_at,expires_at,revoked_at)
		VALUES (?,?,?,'mcp:access',?,?,?,?,?)`, id, f.user.ID, f.values.Get("client_id"), f.s.Resource(), f.user.SessionVersion,
		createdAt, time.Now().Add(24*time.Hour).Unix(), revokedAt)
}

func grantIDs(t testing.TB, f *fixture) []string {
	t.Helper()
	rows, err := f.db.QueryContext(t.Context(), `SELECT id FROM oauth_grants ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestListGrantsOmitsGrantsThatIssuedNoToken pins audit A-3 for the account's
// own connection list: consent creates the grant, and until the code is
// exchanged nothing has connected.
func TestListGrantsOmitsGrantsThatIssuedNoToken(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	f.code(t) // a second consent that is never exchanged either
	if grants, err := f.s.ListGrants(t.Context(), f.user.ID); err != nil || len(grants) != 0 {
		t.Fatalf("unredeemed grants listed: %d %v", len(grants), err)
	}
	f.redeem(t, code)
	grants, err := f.s.ListGrants(t.Context(), f.user.ID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("redeemed grant listed %d times: %v", len(grants), err)
	}
}

// TestUnredeemedGrantsStayCoveredByAccountRevocation shows that hiding a grant
// from the lists removes none of the ways it is revoked: its code is refused
// after a credential-version bump or a role change, and deleting the account
// removes it.
func TestUnredeemedGrantsStayCoveredByAccountRevocation(t *testing.T) {
	for _, tc := range []struct{ name, statement string }{
		{"credential version", `UPDATE users SET session_version = session_version + 1 WHERE id = ?`},
		{"role", `UPDATE users SET role = 'public' WHERE id = ?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			code := f.code(t)
			retentionExec(t, f, tc.statement, f.user.ID)
			if status, _ := f.exchangeCode(code, nil); status != http.StatusBadRequest {
				t.Fatalf("code of an unredeemed grant exchanged after %s change: %d", tc.name, status)
			}
		})
	}
	f := newFixture(t)
	f.code(t)
	retentionExec(t, f, `DELETE FROM users WHERE id = ?`, f.user.ID)
	if retentionCount(t, f, "oauth_grants") != 0 || retentionCount(t, f, "oauth_codes") != 0 {
		t.Fatal("deleting the account left its unredeemed grant behind")
	}
}

// TestRetentionDrainsUnredeemedGrants pins the cleanup half of A-3: a grant
// that never issued a token is deleted once it is older than a day, together
// with its code, and nothing that did issue a token (or a preview) is touched.
func TestRetentionDrainsUnredeemedGrants(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.Add(-CodeRetention).Unix()

	unredeemedGrantAt(t, f, "boundary", cutoff, 0)
	unredeemedGrantAt(t, f, "recent", cutoff+1, 0)
	unredeemedGrantAt(t, f, "revoked-recent", 1, now.Unix())
	unredeemedGrantAt(t, f, "with-code", cutoff, 0)
	retentionExec(t, f, `INSERT INTO oauth_codes VALUES ('live-code','with-code','https://client.test/callback','challenge',?,?,0)`, now.Unix(), now.Add(time.Minute).Unix())
	unredeemedGrantAt(t, f, "with-token", 1, 0)
	retainedToken(t, f, "with-token", "kept-token")
	unredeemedGrantAt(t, f, "with-preview", 1, 0)
	page, err := store.New(f.db).CreatePage(t.Context(), store.CreatePageParams{Title: "Synthetic", Slug: "unredeemed-fixture",
		Body: "<p>Synthetic.</p>", Status: "draft", AuthorID: f.user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	retentionExec(t, f, `INSERT INTO preview_links (token_hash,page_id,revision,created_by,session_version,issuing_grant_id,created_at,expires_at)
		VALUES ('p',?,1,?,?,'with-preview',1,2)`, page.ID, f.user.ID, f.user.SessionVersion)

	result, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || result != (CleanupResult{Unredeemed: 3}) {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if got := strings.Join(grantIDs(t, f), " "); got != "recent with-preview with-token" {
		t.Fatalf("remaining grants = %q", got)
	}
	if retentionCount(t, f, "oauth_codes") != 0 {
		t.Fatal("the deleted grant's code was left behind")
	}
	if retentionCount(t, f, "oauth_tokens") != 1 || retentionCount(t, f, "preview_links") != 1 {
		t.Fatal("cleanup removed a token or preview of a surviving grant")
	}
	again, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || again != (CleanupResult{}) {
		t.Fatalf("repeated cleanup = %+v, %v", again, err)
	}
}

func TestRetentionBoundsUnredeemedGrantBatches(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	for i := range CleanupBatchSize + 3 {
		unredeemedGrantAt(t, f, fmt.Sprintf("pending-%04d", i), 1, 0)
	}
	for i, want := range []int64{CleanupBatchSize, 3, 0} {
		result, err := cleanupAt(t.Context(), f.db, now)
		if err != nil || result.Unredeemed != want {
			t.Fatalf("batch %d: %+v %v", i, result, err)
		}
	}
}

// TestAbandonedConsentIsCleanedUpEndToEnd runs the real flow: a consent whose
// code is never exchanged is removed a day later, its code with it, while the
// redeemed connection next to it keeps working.
func TestAbandonedConsentIsCleanedUpEndToEnd(t *testing.T) {
	f := newFixture(t)
	abandoned := f.code(t)
	body := f.redeem(t, f.code(t))
	result, err := cleanupAt(t.Context(), f.db, time.Now().Add(CodeRetention+time.Hour))
	if err != nil || result.Unredeemed != 1 {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if retentionCount(t, f, "oauth_grants") != 1 {
		t.Fatal("cleanup removed the redeemed grant")
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatalf("redeemed connection stopped working: %v", err)
	}
	if status, _ := f.exchangeCode(abandoned, nil); status != http.StatusBadRequest {
		t.Fatalf("a cleaned-up code was exchanged: %d", status)
	}
}

func (f *fixture) registerDCR(t testing.TB, name string) string {
	t.Helper()
	body := `{"client_name":"` + name + `","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}`
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.Register(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("register %s: status %d", name, w.Code)
	}
	var registered struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	return registered.ID
}

func clientExpiry(t testing.TB, f *fixture, id string) int64 {
	t.Helper()
	var expiry int64
	if err := f.db.QueryRowContext(t.Context(), `SELECT expires_at FROM oauth_clients WHERE id = ?`, id).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	return expiry
}

// TestDCRClientsAreShortLivedUntilUsed pins audit A-6: dynamic registration is
// unauthenticated, and its clients lived 90 days whether or not anyone ever
// used them. Now a client that never gets a grant lapses after a day and is
// removed by retention; consent gives a used client the full lifetime.
func TestDCRClientsAreShortLivedUntilUsed(t *testing.T) {
	f := newFixture(t)
	f.s.options.AllowDCR = true
	start := time.Now().UTC().Truncate(time.Second)
	clock := start
	f.s.now = func() time.Time { return clock }

	unused := f.registerDCR(t, "Never used")
	used := f.registerDCR(t, "Used")
	for _, id := range []string{unused, used} {
		if got, want := clientExpiry(t, f, id), start.Add(DCRUnusedClientLifetime).Unix(); got != want {
			t.Fatalf("registration expiry = %d, want %d (one day)", got, want)
		}
	}

	// Consent is the first use: only that client gets the full lifetime.
	clock = start.Add(time.Hour)
	f.values.Set("client_id", used)
	f.redeem(t, f.code(t))
	if got, want := clientExpiry(t, f, used), clock.Add(DCRClientLifetime).Unix(); got != want {
		t.Fatalf("used client expiry = %d, want %d", got, want)
	}
	if got, want := clientExpiry(t, f, unused), start.Add(DCRUnusedClientLifetime).Unix(); got != want {
		t.Fatalf("unused client was extended: %d", got)
	}
	// A later consent never shortens it.
	retentionExec(t, f, `UPDATE oauth_clients SET expires_at = ? WHERE id = ?`, start.Add(1000*24*time.Hour).Unix(), used)
	f.code(t)
	if got := clientExpiry(t, f, used); got != start.Add(1000*24*time.Hour).Unix() {
		t.Fatalf("a later consent changed the expiry to %d", got)
	}
	retentionExec(t, f, `UPDATE oauth_clients SET expires_at = ? WHERE id = ?`, clock.Add(DCRClientLifetime).Unix(), used)

	// A day and a bit later the unused client no longer resolves, the used one does.
	clock = start.Add(DCRUnusedClientLifetime + time.Hour)
	if got := problemCode(second(f.s.clients.resolve(t.Context(), unused))); got != "invalid_client" {
		t.Fatalf("expired unused client resolves: %q", got)
	}
	if _, err := f.s.clients.resolve(t.Context(), used); err != nil {
		t.Fatalf("used client stopped resolving: %v", err)
	}
	clock = start.Add(60 * 24 * time.Hour)
	if _, err := f.s.clients.resolve(t.Context(), used); err != nil {
		t.Fatalf("used client lapsed within its lifetime: %v", err)
	}

	// Retention keeps its usual day of grace after expiry, then removes the
	// unused client and never the used one (it has grants).
	clientRows := func() (usedRows, unusedRows int) {
		for id, rows := range map[string]*int{used: &usedRows, unused: &unusedRows} {
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_clients WHERE id = ?`, id).Scan(rows); err != nil {
				t.Fatal(err)
			}
		}
		return usedRows, unusedRows
	}
	if _, err := cleanupAt(t.Context(), f.db, start.Add(DCRUnusedClientLifetime+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if usedRows, unusedRows := clientRows(); usedRows != 1 || unusedRows != 1 {
		t.Fatalf("cleanup inside the grace period left used=%d unused=%d, want 1 and 1", usedRows, unusedRows)
	}
	if _, err := cleanupAt(t.Context(), f.db, start.Add(DCRUnusedClientLifetime+CodeRetention+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if usedRows, unusedRows := clientRows(); usedRows != 1 || unusedRows != 0 {
		t.Fatalf("cleanup left used=%d unused=%d, want the used client only", usedRows, unusedRows)
	}
}

// TestDCRFloodClearsWithinADay is the attack in A-6: a full registry of
// clients that nobody uses stops registrations, but only for a day.
func TestDCRFloodClearsWithinADay(t *testing.T) {
	f := newFixture(t)
	f.s.options.AllowDCR = true
	start := time.Now().UTC().Truncate(time.Second)
	clock := start
	f.s.now = func() time.Time { return clock }
	retentionExec(t, f, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
		INSERT INTO oauth_clients SELECT 'flood' || x, 'Flood', '["https://client.test/callback"]', 'dcr', ? FROM n`, start.Add(DCRUnusedClientLifetime).Unix())

	register := func() int {
		r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"Honest","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.s.Register(w, r)
		return w.Code
	}
	clock = start.Add(DCRUnusedClientLifetime - time.Hour)
	if got := register(); got != http.StatusServiceUnavailable {
		t.Fatalf("registration into a full registry = %d, want 503", got)
	}
	clock = start.Add(DCRUnusedClientLifetime + time.Hour)
	if got := register(); got != http.StatusCreated {
		t.Fatalf("registration after the flood lapsed = %d, want 201", got)
	}
}

// TestDCRRegistrationsUseTheShortLifetime keeps a second registration path
// from restoring the 90-day lifetime for clients nobody has used (A-6): every
// saveClient call for source "dcr" in runtime code must pass
// DCRUnusedClientLifetime. Consent extends the client in Authorize.
//
// Bug state: pass DCRClientLifetime in Register (protocol.go) and this names
// the call.
func TestDCRRegistrationsUseTheShortLifetime(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || calleeName(call) != "saveClient" || len(call.Args) != 4 {
				return true
			}
			source, ok := call.Args[2].(*ast.BasicLit)
			if !ok || source.Value != `"dcr"` {
				return true
			}
			calls++
			uses := false
			ast.Inspect(call.Args[3], func(n ast.Node) bool {
				if ident, ok := n.(*ast.Ident); ok && ident.Name == "DCRUnusedClientLifetime" {
					uses = true
				}
				return !uses
			})
			if !uses {
				t.Errorf("%s: a dcr client is saved with a lifetime other than DCRUnusedClientLifetime", fset.Position(call.Pos()))
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("no saveClient call for source dcr found; the scan is broken")
	}
}
