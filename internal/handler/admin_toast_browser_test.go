// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/web"
)

// Run with freshly built assets and OCMS_PLAYWRIGHT_CLI set to playwright-cli.
func TestAdminDeleteToastsInChrome(t *testing.T) {
	cli := os.Getenv("OCMS_PLAYWRIGHT_CLI")
	if cli == "" {
		t.Skip("set OCMS_PLAYWRIGHT_CLI to run the Chrome toast regression")
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	admin := createTestAdminUser(t, db)
	user := createTestUser(t, db, testUser{Email: "toast@example.test", Name: "Toast fixture", Role: model.RoleEditor})
	q, now := store.New(db), time.Now().UTC()
	webhook, err := q.CreateWebhook(t.Context(), store.CreateWebhookParams{
		Name: "Toast webhook", Url: "https://example.test/webhook", Secret: "fixture-secret", Events: "[]",
		Headers: "{}", IsActive: true, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{
		Name: "Toast key", KeyHash: "fixture-hash", KeyPrefix: "fixture", Permissions: "[]",
		IsActive: true, CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	users := NewUsersHandler(db, renderer, sm)
	webhooks := NewWebhooksHandler(db, renderer, sm)
	keys := NewAPIKeysHandler(db, renderer, sm)
	cases := []struct {
		Path, OpenLabel, Success, Refusal string
		ID                                int64
		list, remove                      http.HandlerFunc
	}{
		{"/admin/users", i18n.T("en", "btn.delete"), "User deleted successfully", "User not found", user.ID, users.List, users.Delete},
		{"/admin/webhooks", i18n.T("en", "btn.delete"), "Webhook deleted successfully", "Webhook not found", webhook.ID, webhooks.List, webhooks.Delete},
		{"/admin/api-keys", i18n.T("en", "api_keys.revoke"), "API key revoked successfully", "API key not found", key.ID, keys.List, keys.Delete},
	}

	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	router.Group(func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				sm.Put(req.Context(), render.SessionKeyAdminLang, "en")
				next.ServeHTTP(w, withUser(req, admin))
			})
		})
		for _, tc := range cases {
			r.Get(tc.Path, tc.list)
			r.Delete(tc.Path+"/{id}", tc.remove)
		}
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	scenarios, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	browser := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		session := fmt.Sprintf("ocms-admin-toast-%d", os.Getpid())
		cmd := exec.CommandContext(ctx, cli, append([]string{"--session", session}, args...)...)
		cmd.Dir = workDir
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("playwright %s: %v\n%s", args[0], runErr, output)
		}
		return string(output)
	}
	browser("open", server.URL+"/admin/users", "--browser=chrome", "--idle-timeout=60000")
	t.Cleanup(func() { browser("close") })
	output := browser("run-code", fmt.Sprintf(`async page => {
		const origin = new URL(page.url()).origin;
		const toasts = page.locator('#toast-container .toast');
		async function checkToast(type, message) {
			const toast = toasts.filter({hasText: message});
			await toast.waitFor({state: 'visible', timeout: 3000});
			if (await toasts.count() !== 1 || await toast.textContent() !== message ||
				!await toast.evaluate((el, type) => el.classList.contains('toast-' + type), type)) {
				throw new Error('Expected exactly one ' + type + ' toast: ' + message + '. Found ' + JSON.stringify(await toasts.allTextContents()));
			}
		}
		for (const tc of %s) {
			await page.goto(origin + tc.Path);
			const action = tc.Path + '/' + tc.ID;
			const confirm = page.locator('[hx-delete="' + action + '"]');
			const row = page.locator(await confirm.getAttribute('hx-target'));
			const missing = tc.Path + '/99999';
			const refused = page.waitForResponse(response =>
				response.request().method() === 'DELETE' && new URL(response.url()).pathname === missing);
			await confirm.evaluate((button, path) => htmx.ajax('DELETE', path, {source: button}), missing);
			if ((await refused).status() !== 400 || !await row.isVisible()) {
				throw new Error(tc.Path + ': refusal must preserve the row');
			}
			await checkToast('error', tc.Refusal);

			// Reload to isolate the success toast from the refusal above.
			await page.reload();
			await row.locator('button[title="' + tc.OpenLabel + '"]').click();
			const completed = page.waitForResponse(response =>
				response.request().method() === 'DELETE' && new URL(response.url()).pathname === action);
			await confirm.click();
			if ((await completed).status() !== 200) throw new Error(tc.Path + ': delete failed');
			await row.waitFor({state: 'detached', timeout: 3000});
			await checkToast('success', tc.Success);
		}

		await page.reload();
		await page.evaluate(() => {
			htmx.trigger(document.createElement('button'), 'showToast', {value: 'Saved after swap'});
		});
		await checkToast('success', 'Saved after swap');
		return 'Admin delete toast checks passed';
	}`, scenarios))
	if !strings.Contains(output, "Admin delete toast checks passed") {
		t.Fatalf("unexpected browser result:\n%s", output)
	}
}
