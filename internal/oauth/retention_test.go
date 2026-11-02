// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func retentionExec(t testing.TB, f *fixture, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func retainedGrant(t testing.TB, f *fixture, id string, expiry, revoked int64) {
	t.Helper()
	retentionExec(t, f, `INSERT INTO oauth_grants (id,user_id,client_id,scopes,resource,session_version,created_at,expires_at,revoked_at)
		VALUES (?,?,?,'mcp:access',?,?,1,?,?)`, id, f.user.ID, f.values.Get("client_id"), f.s.Resource(), f.user.SessionVersion, expiry, revoked)
}

func retainedToken(t testing.TB, f *fixture, grantID, id string) {
	t.Helper()
	retentionExec(t, f, `INSERT INTO oauth_tokens (id,grant_id,access_hash,refresh_hash,scopes,created_at,access_expires_at,refresh_used_at)
		VALUES (?,?,?,?,'mcp:access',1,2,3)`, id, grantID, tokenHash(id+"-access"), tokenHash(id+"-refresh"))
}

func retentionCount(t testing.TB, f *fixture, table string) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRetentionBoundariesAndRepeatability(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.Add(-EvidenceRetention).Unix()
	for _, item := range []struct {
		id              string
		expiry, revoked int64
	}{
		{"active", now.Add(time.Hour).Unix(), 0},
		{"expired-boundary", cutoff, 0},
		{"expired-recent", cutoff + 1, 0},
		{"revoked-boundary", now.Add(time.Hour).Unix(), cutoff},
		{"revoked-recent", now.Add(time.Hour).Unix(), cutoff + 1},
	} {
		retainedGrant(t, f, item.id, item.expiry, item.revoked)
		retainedToken(t, f, item.id, item.id+"-token")
	}
	short := now.Add(-CodeRetention).Unix()
	for i, expiry := range []int64{short, short + 1, now.Add(time.Minute).Unix()} {
		retentionExec(t, f, `INSERT INTO oauth_codes (code_hash,grant_id,redirect_uri,challenge,created_at,expires_at)
			VALUES (?,'active','https://client.test/callback','challenge',1,?)`, fmt.Sprint(i), expiry)
	}
	for i, expiry := range []int64{short, short + 1} {
		retentionExec(t, f, `INSERT INTO oauth_clients (id,name,redirect_uris,source,expires_at) VALUES (?, 'Unused','[]','dcr',?)`, fmt.Sprint(i), expiry)
	}
	result, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || result != (CleanupResult{Codes: 1, Tokens: 2, Grants: 2, Clients: 1}) {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if retentionCount(t, f, "oauth_tokens") != 3 || retentionCount(t, f, "oauth_grants") != 3 || retentionCount(t, f, "oauth_codes") != 2 {
		t.Fatal("retention crossed an active/recent boundary")
	}
	again, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || again != (CleanupResult{}) {
		t.Fatalf("repeated cleanup = %+v, %v", again, err)
	}
}

func TestRetentionNeverDropsActiveFamilyReplayEvidence(t *testing.T) {
	f := newFixture(t)
	first := f.redeem(t, f.code(t))
	now := f.s.now().Add(2 * time.Hour)
	// Keep client metadata valid while advancing beyond access-token expiry.
	retentionExec(t, f, `UPDATE oauth_clients SET expires_at=?`, now.Add(time.Hour).Unix())
	f.s.now = func() time.Time { return now }
	status, second := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusOK {
		t.Fatalf("rotation status=%d", status)
	}
	result, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || result.Tokens != 0 || result.Grants != 0 || retentionCount(t, f, "oauth_tokens") != 2 {
		t.Fatalf("active family lost spent evidence: %+v %v", result, err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(second)); err != nil {
		t.Fatal("current access failed after cleanup")
	}
	status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatal("spent refresh replay was not rejected after cleanup")
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(second)); err == nil {
		t.Fatal("replay after cleanup failed to revoke the current family")
	}
}

func TestRetentionDrainsChildrenWithoutUnboundedCascades(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	retainedGrant(t, f, "old", now.Add(-EvidenceRetention-time.Second).Unix(), 0)
	page, err := store.New(f.db).CreatePage(t.Context(), store.CreatePageParams{Title: "Synthetic", Slug: "retention-fixture",
		Body: "<p>Synthetic.</p>", Status: "draft", AuthorID: f.user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := range CleanupBatchSize + 3 {
		retainedToken(t, f, "old", fmt.Sprintf("old-%04d", i))
		retentionExec(t, f, `INSERT INTO preview_links (token_hash,page_id,revision,created_by,session_version,issuing_grant_id,created_at,expires_at)
			VALUES (?,?,1,?,?,'old',1,2)`, tokenHash(fmt.Sprint(i)), page.ID, f.user.ID, f.user.SessionVersion)
	}
	result, err := cleanupAt(t.Context(), f.db, now)
	if err != nil || result.Tokens != CleanupBatchSize || result.Previews != CleanupBatchSize || result.Grants != 0 {
		t.Fatalf("first bounded drain = %+v, %v", result, err)
	}
	if retentionCount(t, f, "oauth_tokens") != 3 || retentionCount(t, f, "preview_links") != 3 || retentionCount(t, f, "pages") != 1 {
		t.Fatal("unbounded cascade or content deletion")
	}
	result, err = cleanupAt(t.Context(), f.db, now)
	if err != nil || result.Tokens != 3 || result.Previews != 3 || result.Grants != 1 {
		t.Fatalf("second drain = %+v, %v", result, err)
	}
	if retentionCount(t, f, "pages") != 1 {
		t.Fatal("family retirement changed content")
	}
}

func TestRetentionRollsBackEveryPhaseAndFailedCommit(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(commitFailure), func(t *testing.T) {
			f := newFixture(t)
			now := time.Now()
			retainedGrant(t, f, "old", now.Add(-EvidenceRetention-time.Second).Unix(), 0)
			retainedToken(t, f, "old", "old-token")
			retentionExec(t, f, `INSERT INTO oauth_codes VALUES ('old-code','old','https://client.test/callback','challenge',1,2,0)`)
			retentionExec(t, f, `UPDATE oauth_clients SET expires_at=1`)
			if commitFailure {
				retentionExec(t, f, `CREATE TABLE retention_commit_guard (grant_id TEXT REFERENCES oauth_grants(id) DEFERRABLE INITIALLY DEFERRED)`)
				retentionExec(t, f, `CREATE TRIGGER retention_failure AFTER DELETE ON oauth_clients BEGIN INSERT INTO retention_commit_guard VALUES ('missing'); END`)
			} else {
				retentionExec(t, f, `CREATE TRIGGER retention_failure BEFORE DELETE ON oauth_clients BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
			}
			result, err := cleanupAt(t.Context(), f.db, now)
			if err == nil || result != (CleanupResult{}) {
				t.Fatalf("failed cleanup = %+v, %v", result, err)
			}
			for _, table := range []string{"oauth_codes", "oauth_tokens", "oauth_grants", "oauth_clients"} {
				if retentionCount(t, f, table) != 1 {
					t.Fatalf("%s deletion survived rollback", table)
				}
			}
			retentionExec(t, f, `DROP TRIGGER retention_failure`)
			if _, err := cleanupAt(t.Context(), f.db, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetentionCancelledContextLeavesEvidenceUntouched(t *testing.T) {
	f := newFixture(t)
	retainedGrant(t, f, "old", 1, 0)
	retainedToken(t, f, "old", "old-token")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Cleanup(ctx, f.db); err == nil || retentionCount(t, f, "oauth_tokens") != 1 {
		t.Fatal("cancelled cleanup changed evidence")
	}
}

func TestRetentionBoundsCodeGrantAndClientBatches(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	for i := range CleanupBatchSize + 3 {
		id := fmt.Sprintf("retire-%04d", i)
		retainedGrant(t, f, id, 1, 0)
		retentionExec(t, f, `INSERT INTO oauth_codes VALUES (?,?, 'https://client.test/callback','challenge',1,2,0)`, id, id)
		retentionExec(t, f, `INSERT INTO oauth_clients VALUES (?, 'Unused','[]','dcr',1)`, id)
	}
	for i, want := range []int64{CleanupBatchSize, 3, 0} {
		result, err := cleanupAt(t.Context(), f.db, now)
		if err != nil || result.Codes != want || result.Grants != want || result.Clients != want {
			t.Fatalf("batch %d: %+v %v", i, result, err)
		}
	}
}

func TestAccessLastUsedIsDurableAndThrottled(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	initial := f.s.now().UTC().Truncate(time.Second)
	now := initial.Add(2 * time.Minute)
	f.s.now = func() time.Time { return now }
	p, err := f.s.VerifyAccess(t.Context(), access(body))
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.s.q.GetOAuthGrant(t.Context(), p.GrantID)
	if err != nil || g.LastUsedAt != now.Unix() {
		t.Fatalf("last-used not updated: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatal(err)
	}
	again, _ := f.s.q.GetOAuthGrant(t.Context(), p.GrantID)
	if again.LastUsedAt != g.LastUsedAt {
		t.Fatal("last-used write was not throttled")
	}
}

func TestRetentionConcurrentWithRefreshPreservesReplayDetection(t *testing.T) {
	f := newFixture(t)
	first := f.redeem(t, f.code(t))
	for i := range 10 {
		id := fmt.Sprintf("retired-%d", i)
		retainedGrant(t, f, id, 1, 0)
		retainedToken(t, f, id, id)
	}
	errors := make(chan error, 4)
	for range 4 {
		go func() { _, err := Cleanup(t.Context(), f.db); errors <- err }()
	}
	status, second := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusOK {
		t.Fatalf("concurrent rotation status=%d", status)
	}
	for range 4 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(second)); err != nil {
		t.Fatal("cleanup invalidated active access", err)
	}
	status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatal("replay evidence lost during concurrent cleanup")
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(second)); err == nil {
		t.Fatal("concurrent cleanup prevented family revocation")
	}
}

func TestRetentionIndexMigrationRollbackPreservesCredentials(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	sql, err := os.ReadFile("../store/migrations/20261102020000_oauth_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(sql), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration rollback")
	}
	retentionExec(t, f, down)
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatal("index rollback changed active credentials", err)
	}
	retentionExec(t, f, up)
	if _, err := Cleanup(t.Context(), f.db); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatal("index reapplication changed active credentials", err)
	}
}

func BenchmarkOAuthRetentionBatch(b *testing.B) {
	f := newFixture(b)
	i := 0
	for b.Loop() {
		b.StopTimer()
		id := fmt.Sprintf("batch-%d", i)
		retainedGrant(b, f, id, 1, 0)
		for n := range CleanupBatchSize {
			retainedToken(b, f, id, fmt.Sprintf("%s-%d", id, n))
		}
		b.StartTimer()
		result, err := Cleanup(b.Context(), f.db)
		if err != nil || result.Tokens != CleanupBatchSize || result.Grants != 1 {
			b.Fatalf("cleanup: %+v %v", result, err)
		}
		i++
	}
}
