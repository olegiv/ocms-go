// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestDemoModeGuardsWebhookDeliveriesAndOAuthRevoke checks that demo mode
// refuses the webhook test and retry actions, which queue outbound
// deliveries, and the legacy account OAuth revoke route, as it already
// refuses the other webhook and connected-app changes (2026-11-03 audit B-2,
// A-12). IsDemoMode is read once per process, so the check runs in a child
// test process.
//
// Bug state: drop the demoGuard call from WebhooksHandler.Test,
// RetryDelivery or AuthHandler.RevokeOAuthGrant and this fails.
func TestDemoModeGuardsWebhookDeliveriesAndOAuthRevoke(t *testing.T) {
	if os.Getenv("OCMS_HANDLER_DEMO_GUARD_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDemoModeGuardsWebhookDeliveriesAndOAuthRevoke$", "-test.count=1")
		cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_HANDLER_DEMO_GUARD_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("demo-mode child: %v\n%s", err, out)
		}
		return
	}
	if !middleware.IsDemoMode() {
		t.Fatal("child process is not in demo mode")
	}

	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	queries := store.New(db)
	now := time.Now()
	webhook, err := queries.CreateWebhook(t.Context(), store.CreateWebhookParams{Name: "Hook", Url: "https://example.com/hook", Secret: "secret123",
		Events: `["page.created"]`, IsActive: true, Headers: "{}", CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := queries.CreateWebhookDelivery(t.Context(), store.CreateWebhookDeliveryParams{WebhookID: webhook.ID, Event: "page.created", Payload: "{}", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE webhook_deliveries SET status = 'failed' WHERE id = ?`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	h := NewWebhooksHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Post(RouteWebhooksID+"/test", h.Test)
	router.Post(RouteWebhooksID+"/deliveries/{did}/retry", h.RetryDelivery)
	for _, target := range []string{
		fmt.Sprintf("/webhooks/%d/test", webhook.ID),
		fmt.Sprintf("/webhooks/%d/deliveries/%d/retry", webhook.ID, delivery.ID),
	} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, withUser(httptest.NewRequest(http.MethodPost, target, nil), user))
		if rr.Code != http.StatusSeeOther {
			t.Errorf("%s in demo mode: status %d", target, rr.Code)
		}
	}
	var deliveries int
	var status string
	if err = db.QueryRow(`SELECT COUNT(*), MAX(CASE WHEN id = ? THEN status END) FROM webhook_deliveries`, delivery.ID).Scan(&deliveries, &status); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || status != "failed" {
		t.Errorf("demo mode queued deliveries: %d rows, retried delivery status %q", deliveries, status)
	}
	pages := NewPagesHandler(db, renderer, sm)
	pagesRouter := chi.NewRouter()
	pagesRouter.Use(sm.LoadAndSave)
	pagesRouter.Post("/admin/pages/{id}/translation-review", pages.ReviewTranslation)
	pagesRouter.Post("/admin/pages/{id}/claims/{index}", pages.CheckClaim)
	for _, target := range []string{"/admin/pages/1/translation-review", "/admin/pages/1/claims/0"} {
		form := strings.NewReader("acknowledge=on&checked=on&base_revision=1&source_page_id=2&source_revision=1")
		req := httptest.NewRequest(http.MethodPost, target, form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		pagesRouter.ServeHTTP(rr, withUser(req, user))
		if rr.Code != http.StatusForbidden || strings.TrimSpace(rr.Body.String()) != "Read only" {
			t.Errorf("%s in demo mode: status=%d body=%q, want the read-only refusal", target, rr.Code, rr.Body.String())
		}
	}

	env := newMFATestEnv(t, false)
	enableTestOAuth(t, env)
	account := env.createUser(t, "editor")
	req := httptest.NewRequest(http.MethodPost, "/account/security/oauth/revoke", strings.NewReader("grant_id=probe"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = addUserToContext(requestWithSession(env.sm, req), &account)
	rr := httptest.NewRecorder()
	env.h.RevokeOAuthGrant(rr, req)
	if want := middleware.DemoModeMessageDetailed(middleware.RestrictionAPIKeys); rr.Code != http.StatusSeeOther || env.sm.GetString(req.Context(), "flash") != want {
		t.Errorf("legacy OAuth revoke in demo mode: status %d flash %q, want the demo refusal", rr.Code, env.sm.GetString(req.Context(), "flash"))
	}
}
