// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func refreshBudgetRequest(s *Service, clientID, token string) *httptest.ResponseRecorder {
	values := url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {token},
		"client_id": {clientID}, "resource": {s.Resource()},
	}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Token(w, r)
	return w
}

func checkRefreshBudgetRefusal(t *testing.T, w *httptest.ResponseRecorder, retryAfter string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || body["error"] != "temporarily_unavailable" ||
		w.Header().Get("Retry-After") != retryAfter || w.Header().Get("Cache-Control") != "no-store" ||
		access(body) != "" || refresh(body) != "" {
		t.Fatalf("refresh refusal status=%d error=%v Retry-After=%q", w.Code, body["error"], w.Header().Get("Retry-After"))
	}
}

func TestRefreshGrantBudgetSurvivesRestartAndAllowsRetry(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	clientID := f.values.Get("client_id")
	if _, err := f.db.Exec("UPDATE oauth_clients SET expires_at = ? WHERE id = ?", now.Add(2*time.Hour).Unix(), clientID); err != nil {
		t.Fatal(err)
	}
	first := f.redeem(t, f.code(t))
	current := first
	for range 8 {
		status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(current)}})
		if status != http.StatusOK {
			t.Fatalf("refresh below limit returned %d", status)
		}
		current = body
	}
	checkRefreshBudgetRefusal(t, refreshBudgetRequest(f.s, clientID, refresh(current)), "3601")
	row, err := f.s.q.GetOAuthTokenByRefresh(t.Context(), tokenHash(refresh(current)))
	if err != nil || row.RefreshUsedAt != 0 || row.AccessRevokedAt != 0 {
		t.Fatalf("refusal changed the current token: used=%d revoked=%d err=%v", row.RefreshUsedAt, row.AccessRevokedAt, err)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM oauth_tokens WHERE grant_id = ?", row.GrantID).Scan(&count); err != nil || count != 9 {
		t.Fatalf("refusal changed the family size: rows=%d err=%v", count, err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(current)); err != nil {
		t.Fatalf("access failed after refusal: %v", err)
	}

	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewDB(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(db, f.s.options)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	now = now.Add(time.Hour)
	checkRefreshBudgetRefusal(t, refreshBudgetRequest(s, clientID, refresh(current)), "1")
	now = now.Add(time.Second)
	w := refreshBudgetRequest(s, clientID, refresh(current))
	var next map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil || w.Code != http.StatusOK || refresh(next) == "" {
		t.Fatalf("retry after window returned %d, err=%v", w.Code, err)
	}
	if w := refreshBudgetRequest(s, clientID, refresh(first)); w.Code != http.StatusBadRequest {
		t.Fatalf("spent-token replay returned %d", w.Code)
	}
	if _, err := s.VerifyAccess(t.Context(), access(next)); err == nil {
		t.Fatal("replay no longer revokes the family")
	}
}

func TestRefreshAccountBudgetSerializesAcrossGrants(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	rotate := func(f *fixture, token map[string]any) map[string]any {
		t.Helper()
		status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(token)}})
		if status != http.StatusOK {
			t.Fatalf("refresh below account limit returned %d", status)
		}
		return body
	}
	var spent, latest map[string]any
	for i := range 10 {
		first := f.redeem(t, f.code(t))
		current := first
		rotations := 8
		if i == 9 {
			rotations = 7 // Leave one account slot for the concurrent exchanges.
		}
		for range rotations {
			current = rotate(f, current)
		}
		if i == 0 {
			p, err := f.s.VerifyAccess(t.Context(), access(current))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.q.RevokeOAuthGrant(t.Context(), store.RevokeOAuthGrantParams{ID: p.GrantID, RevokedAt: now.Unix()}); err != nil {
				t.Fatal(err)
			}
		}
		spent, latest = first, current
	}

	otherClient := *f
	otherClient.values = maps.Clone(f.values)
	const otherID = "https://client.test/other"
	otherClient.values.Set("client_id", otherID)
	if err := f.s.saveClient(t.Context(), registeredClient{
		ID: otherID, Name: "Other client", AuthMethod: "none",
		Redirects: []string{f.values.Get("redirect_uri")},
	}, "cimd", now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	first := f.redeem(t, f.code(t))
	second := otherClient.redeem(t, otherClient.code(t))

	db, err := store.NewDB(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	other, err := New(db, f.s.options)
	if err != nil {
		t.Fatal(err)
	}
	other.now = f.s.now
	type result struct {
		response *httptest.ResponseRecorder
		token    string
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	go func() {
		<-start
		token := refresh(first)
		results <- result{refreshBudgetRequest(f.s, f.values.Get("client_id"), token), token}
	}()
	go func() {
		<-start
		token := refresh(second)
		results <- result{refreshBudgetRequest(other, otherID, token), token}
	}()
	close(start)
	admitted, refused := 0, 0
	for range 2 {
		result := <-results
		if result.response.Code == http.StatusOK {
			admitted++
			continue
		}
		refused++
		checkRefreshBudgetRefusal(t, result.response, "3601")
		row, err := f.s.q.GetOAuthTokenByRefresh(t.Context(), tokenHash(result.token))
		if err != nil || row.RefreshUsedAt != 0 || row.AccessRevokedAt != 0 {
			t.Fatalf("account limit consumed the refused token: err=%v", err)
		}
	}
	if admitted != 1 || refused != 1 {
		t.Fatalf("concurrent account refreshes: admitted=%d refused=%d", admitted, refused)
	}

	// A fresh grant still redeems its code, but cannot reset account usage.
	fresh := f.redeem(t, f.code(t))
	checkRefreshBudgetRefusal(t, refreshBudgetRequest(f.s, f.values.Get("client_id"), refresh(fresh)), "3601")
	w := refreshBudgetRequest(f.s, f.values.Get("client_id"), refresh(spent))
	var replay map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &replay); err != nil ||
		w.Code != http.StatusBadRequest || replay["error"] != "invalid_grant" || w.Header().Get("Retry-After") != "" {
		t.Fatalf("account budget masked a replay: status=%d err=%v", w.Code, err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(latest)); err == nil {
		t.Fatal("replay under account limit did not revoke the family")
	}

	otherAccount := *f
	user, err := f.s.q.CreateUser(t.Context(), store.CreateUserParams{
		Email: "another@example.test", PasswordHash: "unused", Name: "Another author",
		Role: "editor", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAccount.user = user
	rotate(&otherAccount, otherAccount.redeem(t, otherAccount.code(t)))
}

func TestRefreshBudgetUsesLimitthNewestLegacyRotation(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	current := f.redeem(t, f.code(t))
	row, err := f.s.q.GetOAuthTokenByRefresh(t.Context(), tokenHash(refresh(current)))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../store/migrations/20261109160000_oauth_refresh_budget.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(sql), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration rollback")
	}
	retentionExec(t, f, down)
	// Simulate rotations already present when the budget was deployed.
	for i := range 10 {
		when := now.Add(-time.Duration(i) * time.Minute).Unix()
		if _, err := f.db.Exec(`INSERT INTO oauth_tokens
			(id, grant_id, access_hash, refresh_hash, scopes, created_at, access_expires_at, refresh_used_at)
			VALUES (?, ?, ?, ?, '', ?, ?, ?)`,
			randomCredential(""), row.GrantID, randomCredential(""), randomCredential(""), when, when, when); err != nil {
			t.Fatal(err)
		}
	}
	retentionExec(t, f, up)
	usedAt, err := f.s.q.GetOAuthAccountRefreshBudget(t.Context(), store.GetOAuthAccountRefreshBudgetParams{
		UserID: f.user.ID, Cutoff: now.Add(-time.Hour).Unix(), BudgetOffset: 7,
	})
	if err != nil || usedAt != now.Add(-7*time.Minute).Unix() {
		t.Fatalf("account query omitted legacy rotations after migration: usedAt=%d err=%v", usedAt, err)
	}
	checkRefreshBudgetRefusal(t, refreshBudgetRequest(f.s, f.values.Get("client_id"), refresh(current)), "3181")
}

func TestRefreshBudgetAllowsShortAccessTTL(t *testing.T) {
	for _, tc := range []struct {
		ttl   time.Duration
		grant int64
	}{{time.Hour, 8}, {15 * time.Minute, 8}, {14 * time.Minute, 9}, {time.Minute, 120}} {
		grant, account := refreshBudgetLimits(tc.ttl)
		if grant != tc.grant || account != 10*tc.grant {
			t.Errorf("access TTL %v got grant=%d account=%d", tc.ttl, grant, account)
		}
	}
}

func TestRefreshBudgetOnlyChargesCommittedRotations(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	clientID := f.values.Get("client_id")
	current := f.redeem(t, f.code(t))
	for range 7 {
		status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(current)}})
		if status != http.StatusOK {
			t.Fatalf("refresh below limit returned %d", status)
		}
		current = next
	}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_refresh_insert BEFORE INSERT ON oauth_tokens
		BEGIN SELECT RAISE(ABORT, 'test refresh insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := refreshBudgetRequest(f.s, clientID, refresh(current))
	if failed.Code != http.StatusServiceUnavailable || failed.Header().Get("Retry-After") != "" {
		t.Fatalf("storage failure returned %d with Retry-After=%q", failed.Code, failed.Header().Get("Retry-After"))
	}
	row, err := f.s.q.GetOAuthTokenByRefresh(t.Context(), tokenHash(refresh(current)))
	if err != nil || row.RefreshUsedAt != 0 || row.AccessRevokedAt != 0 {
		t.Fatalf("failed rotation changed current token: used=%d revoked=%d err=%v", row.RefreshUsedAt, row.AccessRevokedAt, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER fail_refresh_insert"); err != nil {
		t.Fatal(err)
	}
	status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(current)}})
	if status != http.StatusOK {
		t.Fatalf("retry of the last available slot returned %d", status)
	}
	checkRefreshBudgetRefusal(t, refreshBudgetRequest(f.s, clientID, refresh(next)), "3601")
}
