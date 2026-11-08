// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestPreviewFailuresAreDistinguishedAndAudited checks the share-link
// handlers: a storage fault is a 503 rather than a 404 that makes a valid
// link look expired, the per-user limit says so, issuing and revoking a link
// are both recorded in the event log, revoking a link that is already gone
// says so, and a claim check that hits a storage fault is not reported as a
// conflict (2026-11-03 review S7, S8, S9). It also checks that only an
// administrator can revoke another author's link (2026-11-05 review TG-4).
//
// Bug state: map Resolve's storage errors to preview.ErrUnavailable, answer
// every Issue error with 409, drop the issue/revoke events or ignore the
// revoked row count, or map pre-revoke lookup faults to 404, and this fails.
func TestPreviewFailuresAreDistinguishedAndAudited(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	// The real migrations: the preview tables are not in the hand-written
	// schema of testHandlerSetup.
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	sm := testSessionManager(t)
	user := createTestAdminUser(t, db)
	q := store.New(db)
	ctx := t.Context()
	createDraftPage(t, db, user.ID)
	page, err := q.GetPageBySlug(ctx, "draft-page")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: user.ID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.UpsertConfig(ctx, store.UpsertConfigParams{Key: model.ConfigKeySiteURL, Value: "https://cms.test", Type: "string", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	countEvents := func(message string) int {
		t.Helper()
		var n int
		if scanErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE message = ?`, message).Scan(&n); scanErr != nil {
			t.Fatal(scanErr)
		}
		return n
	}
	issue := func() preview.Link {
		t.Helper()
		link, issueErr := (preview.Service{Q: q}).Issue(ctx, provenance.Principal{UserID: user.ID, Source: "human"}, page.ID, page.Revision, "https://cms.test", nil)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		return link
	}

	// Anonymous share: a storage fault is a logged 503, not "expired".
	link := issue()
	if got := countEvents("Preview share link created"); got != 1 {
		t.Fatalf("issued link recorded %d events, want 1", got)
	}
	frontend := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	share := chi.NewRouter()
	share.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware)
	share.Post(preview.ShareOpenPath, frontend.PreviewShare)
	open := func() int {
		t.Helper()
		w := httptest.NewRecorder()
		share.ServeHTTP(w, postedShareRequest(t, string(link.URL)))
		return w.Code
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE preview_links RENAME TO preview_links_unavailable`); err != nil {
		t.Fatal(err)
	}
	faulted := open()
	if _, err = db.ExecContext(ctx, `ALTER TABLE preview_links_unavailable RENAME TO preview_links`); err != nil {
		t.Fatal(err)
	}
	if faulted != http.StatusServiceUnavailable {
		t.Errorf("share during storage fault: status %d, want 503", faulted)
	}
	if got := open(); got != http.StatusOK {
		t.Errorf("share after the fault: status %d, want 200", got)
	}

	// Admin handlers.
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	var flash, flashType string
	actor, adminLang := user, "en"
	admin := chi.NewRouter()
	admin.Use(sm.LoadAndSave)
	admin.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sm.Put(r.Context(), render.SessionKeyAdminLang, adminLang)
			next.ServeHTTP(w, withUser(r, actor))
			flash = sm.GetString(r.Context(), "flash")
			flashType = sm.GetString(r.Context(), "flash_type")
		})
	})
	admin.Post("/admin/pages/{id}/claims/{index}", h.CheckClaim)
	admin.Post("/admin/pages/{id}/preview-links", h.CreatePreviewLink)
	admin.Post("/admin/pages/{id}/preview-links/{linkID}/revoke", h.RevokePreviewLink)
	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		flash, flashType = "", ""
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	revision := strconv.FormatInt(page.Revision, 10)

	for range preview.MaxActiveLinks - 1 {
		issue()
	}
	rr := post(fmt.Sprintf("/admin/pages/%d/preview-links", page.ID), url.Values{"acknowledge": {"on"}, "revision": {revision}, "base_revision": {revision}})
	if rr.Code != http.StatusTooManyRequests || !strings.Contains(rr.Body.String(), i18n.T("en", "preview.active_limit", preview.MaxActiveLinks)) {
		t.Errorf("link past the limit: status %d %q, want 429 with the limit message", rr.Code, rr.Body.String())
	}

	var linkID int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM preview_links WHERE revoked_at = 0 ORDER BY id LIMIT 1`).Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	revokedAt := func(t *testing.T) int64 {
		t.Helper()
		var stamp int64
		if scanErr := db.QueryRowContext(ctx, `SELECT revoked_at FROM preview_links WHERE id = ?`, linkID).Scan(&stamp); scanErr != nil {
			t.Fatal(scanErr)
		}
		return stamp
	}
	revoke := fmt.Sprintf("/admin/pages/%d/preview-links/%d/revoke", page.ID, linkID)
	// Follow the healthy request through its helpers, stopping at the write
	// so the same link stays active for each fault injection.
	trace := &previewRevokeTrace{DB: db, lookups: make(map[string]bool)}
	h.queries = store.New(trace)
	traced := post(revoke, nil)
	h.queries = q
	if !trace.reachedWrite || trace.unnamedLookup || len(trace.lookups) == 0 || traced.Code != http.StatusSeeOther {
		t.Fatalf("preview revoke trace did not discover named prerequisites and reach the write: status=%d lookups=%v write=%v unnamed=%v",
			traced.Code, trace.lookups, trace.reachedWrite, trace.unnamedLookup)
	}
	for _, query := range slices.Sorted(maps.Keys(trace.lookups)) {
		for _, lang := range []string{"en", "ru"} {
			for _, missing := range []bool{false, true} {
				// An empty multi-row result is valid for list queries.
				if missing && !trace.lookups[query] {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/missing=%t", query, lang, missing), func(t *testing.T) {
					adminLang = lang
					h.queries = store.New(&conflictQueryFault{DB: db, query: "-- name: " + query + " :", missing: missing})
					t.Cleanup(func() { h.queries, adminLang = q, "en" })
					logs := captureLogs(t)
					response := post(revoke, nil)
					if missing {
						if response.Code != http.StatusNotFound || flash != "" || strings.Contains(logs.String(), "level=ERROR") {
							t.Errorf("missing record: status=%d flash=%q logs=%q", response.Code, flash, logs.String())
						}
					} else {
						message := i18n.T(lang, "preview.revoke_failed")
						if message == "preview.revoke_failed" {
							t.Fatal("missing revocation-failure translation")
						}
						if response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != message || flash != message || flashType != "error" {
							t.Errorf("lookup fault: status=%d body=%q flash=%q type=%q", response.Code, response.Body.String(), flash, flashType)
						}
						for _, field := range []string{"level=ERROR", "failed to revoke preview link", "error=", "unavailable_conflict_data", fmt.Sprintf("page_id=%d", page.ID), fmt.Sprintf("link_id=%d", linkID), fmt.Sprintf("user_id=%d", user.ID)} {
							if !strings.Contains(logs.String(), field) {
								t.Errorf("lookup fault log lacks %q: %s", field, logs.String())
							}
						}
						if strings.Contains(response.Body.String(), "unavailable_conflict_data") {
							t.Error("internal cause leaked into response")
						}
					}
					if response.Header().Get("Location") != "" || countEvents("Preview share link revoked") != 0 {
						t.Error("failed lookup redirected or recorded a successful revocation")
					}
					if code := open(); code != http.StatusOK {
						t.Errorf("refused revocation changed the shared preview: status=%d", code)
					}
				})
			}
		}
	}
	for _, role := range []string{model.RolePublic, model.RoleEditor} {
		t.Run("refused "+role, func(t *testing.T) {
			other, createErr := q.CreateUser(ctx, store.CreateUserParams{Email: "preview-" + role + "@example.test", PasswordHash: user.PasswordHash, Role: role, Name: "Other account", CreatedAt: time.Now(), UpdatedAt: time.Now()})
			if createErr != nil {
				t.Fatal(createErr)
			}
			actor = other
			t.Cleanup(func() { actor = user })
			logs := captureLogs(t)
			response := post(revoke, nil)
			if response.Code != http.StatusNotFound || flash != "" || strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("role/ownership refusal: status=%d flash=%q logs=%q", response.Code, flash, logs.String())
			}
			if stamp := revokedAt(t); stamp != 0 {
				t.Errorf("refused account set revoked_at to %d", stamp)
			}
			if countEvents("Preview share link revoked") != 0 || open() != http.StatusOK {
				t.Error("refused account revoked the link or recorded a revocation")
			}
		})
	}
	actor = createTestUser(t, db, testUser{Email: "preview-other-admin@example.test", Name: "Non-author admin", Role: model.RoleAdmin})
	if rr = post(revoke, nil); rr.Code != http.StatusSeeOther || flash != i18n.T("en", "preview.revoked") {
		t.Errorf("non-author admin revoke: status %d flash %q", rr.Code, flash)
	}
	if stamp := revokedAt(t); stamp <= 0 {
		t.Errorf("non-author admin did not set revoked_at: %d", stamp)
	}
	if got := countEvents("Preview share link revoked"); got != 1 {
		t.Errorf("revoked link recorded %d events, want 1", got)
	}
	if code := open(); code != http.StatusNotFound {
		t.Errorf("share after successful revocation: status=%d, want 404", code)
	}
	if rr = post(revoke, nil); rr.Code != http.StatusSeeOther || flash != i18n.T("en", "preview.revoke_missing") {
		t.Errorf("second revoke: status %d flash %q, want the already-revoked message", rr.Code, flash)
	}
	actor = user

	// Claim check: a storage fault is a 500, a missing claim a 409.
	claim := fmt.Sprintf("/admin/pages/%d/claims/0", page.ID)
	if rr = post(claim, url.Values{"base_revision": {revision}, "checked": {"on"}}); rr.Code != http.StatusConflict {
		t.Errorf("missing claim: status %d, want 409", rr.Code)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE content_provenance RENAME TO content_provenance_unavailable`); err != nil {
		t.Fatal(err)
	}
	rr = post(claim, url.Values{"base_revision": {revision}, "checked": {"on"}})
	if _, err = db.ExecContext(ctx, `ALTER TABLE content_provenance_unavailable RENAME TO content_provenance`); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("claim check during storage fault: status %d, want 500", rr.Code)
	}
}

type previewRevokeTrace struct {
	*sql.DB
	lookups       map[string]bool // named query -> single-row lookup
	reachedWrite  bool
	unnamedLookup bool
}

func (trace *previewRevokeTrace) record(query string, single bool) {
	line, _, _ := strings.Cut(query, "\n")
	fields := strings.Fields(line)
	if len(fields) != 4 || fields[0] != "--" || fields[1] != "name:" {
		trace.unnamedLookup = true
		return
	}
	trace.lookups[fields[2]] = single
}

func (trace *previewRevokeTrace) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	trace.record(query, true)
	return trace.DB.QueryRowContext(ctx, query, args...)
}

func (trace *previewRevokeTrace) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	trace.record(query, false)
	return trace.DB.QueryContext(ctx, query, args...)
}

func (trace *previewRevokeTrace) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	if !strings.HasPrefix(query, "-- name: RevokePagePreviewLink :") {
		return nil, fmt.Errorf("unexpected write while tracing preview revocation")
	}
	trace.reachedWrite = true
	return driver.RowsAffected(0), nil
}
