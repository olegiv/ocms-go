// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/web"
)

// Run after building assets with Chrome installed and OCMS_PLAYWRIGHT_CLI
// set to the executable path of playwright-cli (or its local wrapper).
func TestTinyMCEFormPreservesOriginalBodyUntilEdited(t *testing.T) {
	cli := os.Getenv("OCMS_PLAYWRIGHT_CLI")
	if cli == "" {
		t.Skip("set OCMS_PLAYWRIGHT_CLI to run the Chrome form regression")
	}
	const original = "<p>Keep this body.</p>\n<pre class=\"language-go\"><code>fmt.Println(&quot;keep&quot;)</code></pre>\n"
	const edited = "<p>Actual browser edit.</p>"
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	ctx, q := t.Context(), store.New(db)
	user := createTestUser(t, db, testUser{Email: "tinymce@example.test", Name: "TinyMCE reviewer", Role: model.RoleEditor})
	now := time.Now().UTC()
	page, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
		Title: "Original title", Slug: "tinymce-fixture", Body: original, Status: "draft", AuthorID: user.ID,
		LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	router.Group(func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Get("/admin/pages/{id}", h.EditForm)
		r.Post("/admin/pages/{id}", h.Update)
		r.Get("/admin/pages", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	formURL := fmt.Sprintf("%s/admin/pages/%d", server.URL, page.ID)
	listURL := server.URL + "/admin/pages"
	workDir := t.TempDir()
	browser := func(args ...string) {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, cli, append([]string{"--session", fmt.Sprintf("ocms-tinymce-%d", os.Getpid())}, args...)...)
		cmd.Dir = workDir
		out, runErr := cmd.CombinedOutput()
		if runErr != nil || (args[0] == "run-code" && !strings.Contains(string(out), "TG11 browser step passed")) {
			t.Fatalf("playwright %s: %v\n%s", args[0], runErr, out)
		}
	}
	browser("open", formURL, "--browser=chrome", "--idle-timeout=60000")
	t.Cleanup(func() { browser("close") })
	const ready = `await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);`
	const submit = `await Promise.all([page.waitForURL(%s), page.locator('form:has(#editor) button[type=submit]').click()]);`
	browser("run-code", fmt.Sprintf(`async page => {
		const errors = [];
		page.on('pageerror', error => errors.push(error.message));
		const response = await page.reload();
		const policy = (await response.allHeaders())['content-security-policy'];
		if (!policy?.includes("script-src 'self' 'nonce-") || policy.includes("'unsafe-eval'")) {
			throw new Error('Expected production CSP without unsafe-eval');
		}
		%s
		if (await page.evaluate(() => tinymce.get('editor').getContent()) === %s) throw new Error('fixture did not normalize');
		await page.locator('input[name=title]').fill('Metadata-only edit');
		if (await page.locator('input[name=slug]').inputValue() !== 'tinymce-fixture') {
			throw new Error('Editing the title changed the existing slug');
		}
		const aliases = page.locator('details[data-aliases]');
		await aliases.locator('summary').click();
		await aliases.locator('button').click();
		const alias = aliases.locator('input[name="aliases[]"]');
		await alias.fill(' Team Name / Test ');
		if (await alias.inputValue() !== 'team-name/test') throw new Error('Alias input did not normalize');
		await aliases.locator('.alias-item button').click();
		await alias.waitFor({state: 'detached'});
		if (errors.length) throw new Error(errors.join('\n'));
		%s
		return 'TG11 browser step passed';
	}`, ready, strconv.Quote(original), fmt.Sprintf(submit, strconv.Quote(listURL))))
	assertSaved := func(wantBody string, revision int64) {
		t.Helper()
		stored, loadErr := q.GetPageByID(ctx, page.ID)
		if loadErr != nil || stored.Revision != revision || stored.Title != "Metadata-only edit" || stored.Body != wantBody {
			t.Fatalf("saved revision=%d title=%q body=%q, want revision %d and body %q: %v", stored.Revision, stored.Title, stored.Body, revision, wantBody, loadErr)
		}
	}
	assertSaved(original, page.Revision+1)
	browser("run-code", fmt.Sprintf(`async page => {
		await page.goto(%s);
		%s
		await page.evaluate(body => {
			const editor = tinymce.get('editor');
			editor.setContent(body);
			editor.setDirty(true);
			editor.save();
		}, %s);
		%s
		return 'TG11 browser step passed';
	}`, strconv.Quote(formURL), ready, strconv.Quote(edited), fmt.Sprintf(submit, strconv.Quote(listURL))))
	assertSaved(edited, page.Revision+2)
}
