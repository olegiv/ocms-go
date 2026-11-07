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
		r.Get("/admin/media/api", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, writeErr := fmt.Fprint(w, `{"items":[{"id":7,"filename":"picker-fixture.ico","filepath":"/static/dist/favicon.ico","alt":"Picker fixture"}]}`); writeErr != nil {
				t.Error(writeErr)
			}
		})
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	formURL := fmt.Sprintf("%s/admin/pages/%d", server.URL, page.ID)
	listURL := server.URL + "/admin/pages"
	browser := openAdminChrome(t, cli, "tinymce", formURL)
	const ready = `await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);`
	const submit = `await Promise.all([page.waitForURL(%s), page.locator('form:has(#editor) button[type=submit]').click()]);`
	browser(t, "run-code", fmt.Sprintf(`async page => {
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
		return 'oCMS browser step passed';
	}`, ready, strconv.Quote(original), fmt.Sprintf(submit, strconv.Quote(listURL))))
	assertSaved := func(wantBody string, revision int64) {
		t.Helper()
		stored, loadErr := q.GetPageByID(ctx, page.ID)
		if loadErr != nil || stored.Revision != revision || stored.Title != "Metadata-only edit" || stored.Body != wantBody {
			t.Fatalf("saved revision=%d title=%q body=%q, want revision %d and body %q: %v", stored.Revision, stored.Title, stored.Body, revision, wantBody, loadErr)
		}
	}
	assertSaved(original, page.Revision+1)
	browser(t, "run-code", fmt.Sprintf(`async page => {
		await page.goto(%s);
		%s
		await page.evaluate(body => {
			const editor = tinymce.get('editor');
			editor.setContent(body);
			editor.setDirty(true);
			editor.save();
		}, %s);
		%s
		return 'oCMS browser step passed';
	}`, strconv.Quote(formURL), ready, strconv.Quote(edited), fmt.Sprintf(submit, strconv.Quote(listURL))))
	assertSaved(edited, page.Revision+2)

	t.Run("image picker selection and insert", func(t *testing.T) {
		browser(t, "run-code", fmt.Sprintf(`async page => {
			const errors = [];
			page.on('console', message => {
				if (message.type() === 'error' && message.text().includes('Failed to load media:')) errors.push(message.text());
			});
			await page.setViewportSize({width: 1680, height: 1080});
			await page.goto(%s);
			%s
			const imageButton = page.getByRole('button', {name: /insert image/i});
			if (!await imageButton.isVisible()) {
				const more = page.getByRole('button', {name: 'Reveal or hide additional toolbar items'});
				if (await more.isVisible()) await more.click();
			}
			if (!await imageButton.isVisible()) {
				throw new Error('Image toolbar button is absent: ' + JSON.stringify(await page.locator('.tox-editor-header button').evaluateAll(buttons => buttons.map(button => [button.title, button.getAttribute('aria-label'), button.textContent]))));
			}
			await imageButton.click();
			const grid = page.locator('#editor-image-grid');
			const item = grid.locator('.editor-image-item[data-id="7"]');
			const checkSelection = async () => {
				await grid.locator('.editor-image-item, .editor-image-empty').first().waitFor();
				if (!(await item.evaluateAll(items => items.length === 1 && items[0].classList.contains('is-selected')))) {
					throw new Error('Picker selection did not survive media reload: ' + await grid.innerText());
				}
			};
			await item.click();
			await checkSelection();
			await Promise.all([
				page.waitForResponse(response => {
					const url = new URL(response.url());
					return url.pathname === '/admin/media/api' && url.searchParams.get('q') === 'picker';
				}),
				page.locator('#editor-image-search').fill('picker'),
			]);
			await checkSelection();
			if (errors.length) throw new Error(errors.join('\n'));
			await page.locator('#editor-image-insert-btn').click();
			await page.locator('#editor-image-modal').waitFor({state: 'hidden'});
			await page.waitForFunction(() => {
				const image = tinymce.get('editor').getBody().querySelector('img');
				return image?.alt === 'Picker fixture' && image.src.endsWith('/static/dist/favicon.ico');
			});
			return 'oCMS browser step passed';
		}`, strconv.Quote(formURL), ready))
		t.Log("Chrome selected a TinyMCE image, kept it selected after reload and search, and inserted it")
	})
}

func openAdminChrome(t *testing.T, cli, name, url string) func(*testing.T, ...string) {
	t.Helper()
	workDir := t.TempDir()
	browser := func(t *testing.T, args ...string) {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, cli, append([]string{"--session", fmt.Sprintf("ocms-%s-%d", name, os.Getpid())}, args...)...)
		cmd.Dir = workDir
		out, runErr := cmd.CombinedOutput()
		if runErr != nil || (args[0] == "run-code" && !strings.Contains(string(out), "oCMS browser step passed")) {
			t.Fatalf("playwright %s: %v\n%s", args[0], runErr, out)
		}
	}
	browser(t, "open", url, "--browser=chrome", "--idle-timeout=60000")
	t.Cleanup(func() { browser(t, "close") })
	return browser
}
