// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// conflict (2026-11-03 review S7, S8, S9).
//
// Bug state: map Resolve's storage errors to preview.ErrUnavailable, answer
// every Issue error with 409, drop the issue/revoke events or ignore the
// revoked row count, and this fails.
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
		share.ServeHTTP(w, postedShareRequest(t, link.URL))
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
	var flash string
	admin := chi.NewRouter()
	admin.Use(sm.LoadAndSave)
	admin.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, withUser(r, user))
			flash = sm.GetString(r.Context(), "flash")
		})
	})
	admin.Post("/admin/pages/{id}/claims/{index}", h.CheckClaim)
	admin.Post("/admin/pages/{id}/preview-links", h.CreatePreviewLink)
	admin.Post("/admin/pages/{id}/preview-links/{linkID}/revoke", h.RevokePreviewLink)
	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		flash = ""
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
	if rr.Code != http.StatusTooManyRequests || !strings.Contains(rr.Body.String(), i18n.T("en", "preview.limit", preview.MaxActiveLinks, preview.MaxIssuedPerHour)) {
		t.Errorf("link past the limit: status %d %q, want 429 with the limit message", rr.Code, rr.Body.String())
	}

	var linkID int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM preview_links WHERE revoked_at = 0 ORDER BY id LIMIT 1`).Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	revoke := fmt.Sprintf("/admin/pages/%d/preview-links/%d/revoke", page.ID, linkID)
	if rr = post(revoke, nil); rr.Code != http.StatusSeeOther || flash != i18n.T("en", "preview.revoked") {
		t.Errorf("revoke: status %d flash %q", rr.Code, flash)
	}
	if got := countEvents("Preview share link revoked"); got != 1 {
		t.Errorf("revoked link recorded %d events, want 1", got)
	}
	if rr = post(revoke, nil); rr.Code != http.StatusSeeOther || flash != i18n.T("en", "preview.revoke_missing") {
		t.Errorf("second revoke: status %d flash %q, want the already-revoked message", rr.Code, flash)
	}

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
