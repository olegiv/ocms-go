// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/web"
)

func pickerChromeServer(t *testing.T) (*MediaHandler, string) {
	t.Helper()
	h, user := pickerUploadFixture(t)
	pages := NewPagesHandler(h.db, h.renderer, h.sessionManager)
	m := theme.NewManager(embed.FS{}, t.TempDir(), testutil.TestLoggerSilent())
	m.SetFuncMap(testutil.MinimalThemeFuncMap())
	if _, err := m.ImportArchive(bytes.NewReader(starterThemeArchive(t))); err != nil {
		t.Fatal(err)
	}
	themes := NewThemesHandler(h.db, h.renderer, h.sessionManager, m, nil, nil, nil)
	user.Role = "admin"
	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	router.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(h.uploadDir))))
	router.Group(func(r chi.Router) {
		r.Use(h.sessionManager.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Use(middleware.RequireEditor())
		r.Get("/admin/pages/new", pages.NewForm)
		r.Post("/admin/pages", pages.Create)
		r.Get("/admin/pages/{id}", pages.EditForm)
		r.Post("/admin/pages/{id}", pages.Update)
		r.Get("/admin/pages", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		r.Get("/admin/media/api", h.API)
		r.Post("/admin/media/upload", h.Upload)
		r.Get("/admin/themes/{name}/settings", themes.Settings)
		r.Post("/admin/themes/{name}/settings", themes.SaveSettings)
		r.Get("/admin/themes", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return h, server.URL
}

// Drive the actual forms, production CSP and multipart endpoint against a fresh
// database/upload directory. The browser CLI helper makes these mandatory in CI.
func TestMediaPickerInlineUploadChrome(t *testing.T) {
	cli := requireChromeCLI(t)
	h, base := pickerChromeServer(t)
	imagePath := filepath.Join(t.TempDir(), "picker.png")
	if err := os.WriteFile(imagePath, pickerPNG(t, 1200, 800), 0o600); err != nil {
		t.Fatal(err)
	}
	browser := openAdminChrome(t, cli, "picker-upload", base+"/admin/pages/new")
	for _, kind := range []string{"post", "page"} {
		t.Run("create "+kind, func(t *testing.T) {
			browser(t, "run-code", fmt.Sprintf(`async page => {
                const errors = [];
                page.on('pageerror', error => errors.push(error.message));
                const response = await page.goto(%s + '/admin/pages/new');
                const policy = (await response.allHeaders())['content-security-policy'];
                if (!policy.includes("script-src 'self' 'nonce-") || policy.includes("'unsafe-eval'")) throw new Error('Production CSP missing');
                await page.setViewportSize({width: 1440, height: 1000});
                await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
                const formURL = page.url();
                await page.locator('[name=title]').fill('Inline %s');
                await page.locator('[name=slug]').fill('inline-%s');
                await page.locator('[name=summary]').fill('Unsaved summary');
                const type = page.locator('[name=page_type]');
                await type.evaluate((input, value) => { input.value = value; input.dispatchEvent(new Event('change', {bubbles:true})); }, %s);
                await page.evaluate(() => { const editor = tinymce.get('editor'); editor.setContent('<p>Unsaved body.</p>'); editor.setDirty(true); editor.save(); });
                const featured = page.locator('.media-dropzone[data-mode=select]').first();
                await featured.locator('.media-dropzone-area').click();
                const modal = featured.locator('.media-dropzone-modal');
                await modal.waitFor({state:'visible'});
                const uploaded = page.waitForResponse(response => response.url().endsWith('/admin/media/upload') && response.status() === 201);
                await modal.locator('[data-upload-file]').setInputFiles(%s);
                const item = (await (await uploaded).json()).item;
                await modal.locator('.is-selected').waitFor();
                if (page.url() !== formURL || await featured.locator('[name=featured_image_id]').inputValue()) throw new Error('Upload navigated or applied before confirmation');
                await modal.getByRole('button', {name:'Select Image', exact:true}).click();
                if (await featured.locator('[name=featured_image_id]').inputValue() !== String(item.id)) throw new Error('Featured selection lost');
                if (await page.locator('[name=title]').inputValue() !== 'Inline %s' || await page.locator('[name=slug]').inputValue() !== 'inline-%s' || await page.locator('[name=summary]').inputValue() !== 'Unsaved summary' || !await page.evaluate(() => tinymce.get('editor').getContent().includes('Unsaved body.'))) throw new Error('Unsaved draft changed');
                if (errors.length) throw new Error(errors.join('\n'));
                await Promise.all([page.waitForURL(%s + '/admin/pages'), page.locator('form:has(#editor) button[type=submit]').click()]);
                return 'oCMS browser step passed';
            }`, strconv.Quote(base), kind, kind, strconv.Quote(kind), strconv.Quote(imagePath), kind, kind, strconv.Quote(base)))
			stored, err := h.queries.GetPageBySlug(t.Context(), "inline-"+kind)
			if err != nil || stored.PageType != kind || stored.Title != "Inline "+kind || stored.Summary != "Unsaved summary" || !strings.Contains(stored.Body, "Unsaved body.") || !stored.FeaturedImageID.Valid {
				t.Fatalf("saved draft: %+v %v", stored, err)
			}
		})
	}
	t.Run("edit featured OG and TinyMCE", func(t *testing.T) {
		// This subtest also runs alone: it must not depend on create_post.
		user, err := h.queries.GetUserByEmail(t.Context(), "picker@example.test")
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.Upload(w, withUser(pickerUploadRequest(t, "file", []string{"existing.png"}, pickerPNG(t, 1200, 800)), user))
		var response struct{ Item mediaAPIItem }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusCreated {
			t.Fatalf("edit fixture upload: %d %s %v", w.Code, w.Body.String(), err)
		}
		now := time.Now().UTC()
		stored, err := pagewrite.Insert(t.Context(), h.queries, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
			Title: "Edit fixture", Slug: "inline-edit", Body: "<p>Unsaved body.</p>", Status: "draft", AuthorID: user.ID,
			LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
			FeaturedImageID: sql.NullInt64{Int64: response.Item.ID, Valid: true},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		browser(t, "run-code", fmt.Sprintf(`async page => {
            await page.goto(%s + '/admin/pages/' + %d);
            await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
            await page.locator('[name=summary]').fill('Edited summary');
            const featured = page.locator('.media-dropzone[data-mode=select]').first();
            const original = await featured.locator('[name=featured_image_id]').inputValue();
            await featured.getByRole('button', {name:'Change', exact:true}).click();
            const fm = featured.locator('.media-dropzone-modal');
            await fm.locator('input[type=text]').fill('no-match');
            await fm.locator('select').selectOption('document');
            const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
            await fm.locator('[data-upload-file]').setInputFiles(%s);
            await upload;
            await fm.locator('.is-selected').waitFor();
            if (await fm.locator('input[type=text]').inputValue() || await fm.locator('select').inputValue()) throw new Error('Upload did not reset library filters');
            await fm.getByRole('button', {name:'Select Image',exact:true}).click();
            const chosen = await featured.locator('[name=featured_image_id]').inputValue();
            if (chosen === original) throw new Error('Editing did not change image');
            await page.locator('details:has([name=og_image_id]) > summary').click();
            const og = page.locator('.media-dropzone[data-mode=select]').nth(1);
            await og.locator('.media-dropzone-area').click();
            const modal = og.locator('.media-dropzone-modal');
            const pending = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
            await modal.locator('[data-upload-file]').setInputFiles(%s);
            const item = (await (await pending).json()).item;
            await modal.locator('.is-selected').waitFor();
            await modal.getByRole('button', {name:'Select Image', exact:true}).click();
            if (await og.locator('[name=og_image_id]').inputValue() !== String(item.id) || await featured.locator('[name=featured_image_id]').inputValue() !== chosen) throw new Error('OG affected featured');
            
            const imageButton = page.getByRole('button', {name:/insert image/i});
            if (!await imageButton.isVisible()) await page.getByRole('button', {name:'Reveal or hide additional toolbar items'}).click();
            await imageButton.click();
            const editor = page.locator('#editor-image-modal');
            await page.locator('#editor-image-search').fill('no-match');
            const inserted = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
            await editor.locator('[data-upload-file]').setInputFiles(%s);
            await inserted;
            await editor.locator('.is-selected').waitFor();
            if (await page.locator('#editor-image-search').inputValue()) throw new Error('Editor search was not reset');
            await page.locator('#editor-image-alt').fill('Uploaded illustration');
            await page.locator('#editor-image-insert-btn').click();
            if (!await page.evaluate(() => {const body = tinymce.get('editor').getContent(); return body.includes('Uploaded illustration') && body.includes('Unsaved body.');})) throw new Error('Image insert lost content or alt');
            await Promise.all([page.waitForURL(%s + '/admin/pages'), page.locator('form:has(#editor) button[type=submit]').click()]);
            return 'oCMS browser step passed';
        }`, strconv.Quote(base), stored.ID, strconv.Quote(imagePath), strconv.Quote(imagePath), strconv.Quote(imagePath), strconv.Quote(base)))
		fresh, loadErr := h.queries.GetPageByID(t.Context(), stored.ID)
		if loadErr != nil || fresh.FeaturedImageID == stored.FeaturedImageID || !fresh.OgImageID.Valid || fresh.OgImageID == fresh.FeaturedImageID || !strings.Contains(fresh.Body, "Uploaded illustration") || fresh.Summary != "Edited summary" {
			t.Fatalf("saved edited images: %+v %v", fresh, loadErr)
		}
	})
	t.Run("theme settings", func(t *testing.T) {
		browser(t, "run-code", fmt.Sprintf(`async page => {
            await page.goto(%s + '/admin/themes/starter/settings');
            await page.locator('#hero_image_alt').fill('Unsaved theme text');
            await page.locator('.btn-media-browse[data-field-id=hero_image]').click();
            const modal = page.locator('#media-picker-overlay');
            const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
            await modal.locator('[data-upload-file]').setInputFiles(%s);
            const item = (await (await upload).json()).item;
            await modal.locator('.is-selected').waitFor();
            if (await page.locator('#hero_image').inputValue()) throw new Error('Theme image applied before confirm');
            await page.locator('#media-picker-confirm').click();
            if (await page.locator('#hero_image').inputValue() !== item.filepath || await page.locator('#hero_image_alt').inputValue() !== 'Unsaved theme text') throw new Error('Theme state lost');
            await Promise.all([page.waitForURL(%s + '/admin/themes'), page.getByRole('button', {name:'Save Settings',exact:true}).click()]);
            return 'oCMS browser step passed';
        }`, strconv.Quote(base), strconv.Quote(imagePath), strconv.Quote(base)))
		config, loadErr := h.queries.GetConfig(t.Context(), "theme_settings_starter")
		if loadErr != nil || !strings.Contains(config.Value, "Unsaved theme text") || !strings.Contains(config.Value, "/uploads/originals/") {
			t.Fatalf("saved theme settings: %+v %v", config, loadErr)
		}
	})
	t.Run("small featured image still fails validation", func(t *testing.T) {
		small := filepath.Join(t.TempDir(), "small.png")
		if err := os.WriteFile(small, pickerPNG(t, 30, 30), 0o600); err != nil {
			t.Fatal(err)
		}
		browser(t, "run-code", fmt.Sprintf(`async page => {
            await page.goto(%s + '/admin/pages/new');
            await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
            await page.locator('[name=title]').fill('Small image draft');
            await page.locator('[name=slug]').fill('small-image-draft');
            await page.locator('[name=summary]').fill('Keep this summary');
            const picker = page.locator('.media-dropzone[data-mode=select]').first();
            await picker.locator('.media-dropzone-area').click();
            const modal = picker.locator('.media-dropzone-modal');
            const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
            await modal.locator('[data-upload-file]').setInputFiles(%s);
            await upload;
            await modal.locator('.is-selected').waitFor();
            await modal.getByRole('button', {name:'Select Image',exact:true}).click();
            await Promise.all([page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/pages')), page.locator('form:has(#editor) button[type=submit]').click()]);
            await page.locator('.form-error').filter({hasText:/1200/}).waitFor();
            if (await page.locator('[name=title]').inputValue() !== 'Small image draft' || await page.locator('[name=summary]').inputValue() !== 'Keep this summary') throw new Error('Validation lost draft');
            return 'oCMS browser step passed';
        }`, strconv.Quote(base), strconv.Quote(small)))
		if _, loadErr := h.queries.GetPageBySlug(t.Context(), "small-image-draft"); loadErr == nil {
			t.Fatal("undersized featured image was saved")
		}
	})
}

func TestMediaPickerUploadFailuresChrome(t *testing.T) {
	cli := requireChromeCLI(t)
	h, base := pickerChromeServer(t)
	imagePath := filepath.Join(t.TempDir(), "retry.png")
	if err := os.WriteFile(imagePath, pickerPNG(t, 1200, 800), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(repositoryRoot(t), "output", "playwright")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	browser := openAdminChrome(t, cli, "picker-errors", base+"/admin/pages/new")
	ranPickers := 0
	for _, kind := range []string{"featured", "og", "editor", "theme"} {
		t.Run(kind, func(t *testing.T) {
			ranPickers++
			browser(t, "run-code", fmt.Sprintf(`async page => {
                const kind = %s, base = %s, file = %s, screenshot = %s;
                const errors = [];
                page.on('pageerror', error => errors.push(error.message));
                await page.setViewportSize({width:1440,height:1000});
                await page.goto(base + (kind === 'theme' ? '/admin/themes/starter/settings' : '/admin/pages/new'));
                if (kind !== 'theme') await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
                if (kind === 'og') await page.locator('details:has([name=og_image_id]) > summary').click();
                let modal, open, value;
                if (kind === 'theme') {
                    modal = page.locator('#media-picker-overlay');
                    open = () => page.locator('.btn-media-browse[data-field-id=hero_image]').click();
                    value = () => page.locator('#hero_image').inputValue();
                    await page.locator('#hero_image_alt').fill('Still unsaved');
                } else if (kind === 'editor') {
                    modal = page.locator('#editor-image-modal');
                    open = async () => {
                        const button = page.getByRole('button', {name:/insert image/i});
                        if (!await button.isVisible()) await page.getByRole('button', {name:'Reveal or hide additional toolbar items'}).click();
                        await button.click();
                    };
                    value = () => page.evaluate(() => tinymce.get('editor').getContent());
                } else {
                    const picker = page.locator('.media-dropzone[data-mode=select]').nth(kind === 'og' ? 1 : 0);
                    modal = picker.locator('.media-dropzone-modal');
                    open = () => picker.locator('.media-dropzone-area').click();
                    value = () => picker.locator(kind === 'og' ? '[name=og_image_id]' : '[name=featured_image_id]').inputValue();
                }
                const show = open;
                open = async () => {await show(); await modal.waitFor({state:'visible'});};
                const original = await value(), url = page.url();
                const status = modal.locator('[data-upload-status]');
                const input = modal.locator('[data-upload-file]');
                const close = () => modal.getByRole('button', {name:'Cancel',exact:true}).click();
                const waitError = () => modal.locator('[data-upload-status].form-error').filter({hasText:/\S/}).waitFor();
                // Every picker exposes uploads for an empty and a populated library.
                await page.route('**/admin/media/api?**', route => route.fulfill({json:{items:[]}}));
                await open();
                await modal.waitFor({state:'visible'});
                await modal.getByText(/No (media|images)/i).first().waitFor();
                if (!await modal.locator('[data-upload-choose]').isVisible()) throw new Error('Empty picker has no upload');
                await page.unroute('**/admin/media/api?**');
                await input.setInputFiles({name:'bad.svg',mimeType:'image/svg+xml',buffer:Buffer.from('<svg/>')});
                await waitError();
                await input.setInputFiles({name:'huge.png',mimeType:'image/png',buffer:Buffer.alloc(20*1024*1024+1)});
                await status.filter({hasText:/20 MB/}).waitFor();
                const failed = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 400);
                await input.setInputFiles({name:'forged.png',mimeType:'image/png',buffer:Buffer.from('invalid')});
                await failed;
                await waitError();
                await page.route('**/admin/media/upload', route => route.fulfill({contentType:'text/html',body:'<html>Sign in</html>'}));
                await input.setInputFiles(file);
                await status.filter({hasText:/Upload failed/}).waitFor();
                await page.unroute('**/admin/media/upload');
                await page.route('**/admin/media/upload', route => route.abort('failed'));
                await input.setInputFiles(file);
                await status.filter({hasText:/Upload failed/}).waitFor();
                await page.unroute('**/admin/media/upload');
                const retry = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
                await input.setInputFiles(file);
                await retry;
                await modal.locator('.is-selected').waitFor();
                if (await value() !== original || page.url() !== url) throw new Error('Retry applied selection or navigated');
                await close();
                await open();
                await modal.locator('[data-upload-choose]').waitFor();
                if (await status.innerText()) throw new Error('Reopen retained upload state');
                await close();
                // Closing aborts a pending upload; its late response cannot apply.
                let releaseUpload, started;
                const seen = new Promise(resolve => {started=resolve;});
                const hold = new Promise(resolve => {releaseUpload=resolve;});
                await page.route('**/admin/media/upload', async route => {
                    started(); await hold;
                    await route.fulfill({status:201,json:{item:{id:99999,filename:'late.png',mimetype:'image/png',filepath:'/uploads/late.png'}}}).catch(() => {});
                });
                await open();
                await input.setInputFiles(file);
                await seen;
                if (!await modal.locator('[data-upload-choose]').isDisabled()) throw new Error('Duplicate upload allowed');
                await modal.locator('[data-upload-progress]').waitFor({state:'visible'});
                await close();
                releaseUpload();
                await page.unrouteAll({behavior:'wait'});
                await open();
                if (await value() !== original || await status.innerText()) throw new Error('Closed upload changed state');
                await close();
                // An old library request finishing after POST must not hide the image.
                let releaseList, listStarted, first = true;
                const listSeen = new Promise(resolve => {listStarted=resolve;});
                const listHold = new Promise(resolve => {releaseList=resolve;});
                await page.route('**/admin/media/api?**', async route => {
                    if (!first) {await route.continue(); return;}
                    first=false; listStarted(); await listHold;
                    await route.fulfill({json:{items:[]}}).catch(() => {});
                });
                await open();
                await listSeen;
                const final = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
                await input.setInputFiles(file);
                await final;
                await modal.locator('.is-selected').waitFor();
                releaseList();
                await page.unrouteAll({behavior:'wait'});
                await modal.locator('.is-selected').waitFor();
                await modal.screenshot({path:screenshot+'.png'});
                await page.setViewportSize({width:390,height:844});
                await modal.locator('[data-upload-choose]').scrollIntoViewIfNeeded();
                await modal.screenshot({path:screenshot+'-mobile.png'});
                await close();
                if (await value() !== original || page.url() !== url) throw new Error('Cancel changed form state');
                if (kind === 'theme' && await page.locator('#hero_image_alt').inputValue() !== 'Still unsaved') throw new Error('Other settings lost');
                if (errors.length) throw new Error(errors.join('\n'));
                return 'oCMS browser step passed';
            }`, strconv.Quote(kind), strconv.Quote(base), strconv.Quote(imagePath), strconv.Quote(filepath.Join(artifacts, "issue-211-"+kind))))
		})
	}
	if count, err := h.queries.CountMedia(t.Context()); err != nil || count != int64(2*ranPickers) {
		t.Fatalf("cancelled selection should keep %d completed uploads, not aborted uploads: %d %v", 2*ranPickers, count, err)
	}
}

// Review regressions use the real picker scripts and endpoint. Search results
// are controlled so a selected image can disappear into an empty or populated
// result independently of library ordering.
func TestMediaPickerReviewRegressionsChrome(t *testing.T) {
	cli := requireChromeCLI(t)
	h, base := pickerChromeServer(t)
	imagePath := filepath.Join(t.TempDir(), "review.png")
	if err := os.WriteFile(imagePath, pickerPNG(t, 1200, 800), 0o600); err != nil {
		t.Fatal(err)
	}
	iconBytes, err := web.Static.ReadFile("static/dist/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	iconPath := filepath.Join(t.TempDir(), "review.ico")
	if err := os.WriteFile(iconPath, iconBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := h.queries.GetUserByEmail(t.Context(), "picker@example.test")
	if err != nil {
		t.Fatal(err)
	}
	var library []mediaAPIItem
	for i := range 13 {
		name := fmt.Sprintf("library-%02d.png", i)
		w := httptest.NewRecorder()
		h.Upload(w, withUser(pickerUploadRequest(t, "file", []string{name}, pickerPNG(t, 30, 30)), user))
		var response struct{ Item mediaAPIItem }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusCreated {
			t.Fatalf("fixture: %d %s %v", w.Code, w.Body.String(), err)
		}
		library = append(library, response.Item)
	}
	items, err := json.Marshal(library)
	if err != nil {
		t.Fatal(err)
	}
	browser := openAdminChrome(t, cli, "picker-review", base+"/admin/pages/new")
	for _, kind := range []string{"featured", "og", "editor", "theme"} {
		t.Run(kind, func(t *testing.T) {
			browser(t, "run-code", fmt.Sprintf(`async page => {
                const kind = %s, base = %s, png = %s, ico = %s, items = %s;
                await page.goto(base + (kind === 'theme' ? '/admin/themes/starter/settings' : '/admin/pages/new'));
                if (kind !== 'theme') await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
                if (kind === 'theme') {
                    await page.locator('.btn-media-browse[data-field-id=hero_image]').click();
                    const modal = page.locator('#media-picker-overlay');
                    await modal.locator('button.media-picker-item').first().waitFor();
                    if (!(await modal.locator('[data-upload-file]').getAttribute('accept')).split(',').includes('.ico')) throw new Error('Theme input lost ICO');
                    if (!(await modal.locator('[data-media-picker-upload] .form-hint').innerText()).includes('ICO')) throw new Error('Theme hint lost ICO');
                    await page.evaluate(items => renderThemeMedia(items), items);
                    const first = modal.locator('button.media-picker-item').first();
                    const second = modal.locator('button.media-picker-item').nth(1);
                    const original = await first.elementHandle();
                    await first.focus();
                    await page.keyboard.press('Enter');
                    if (!await original.evaluate(button => button.isConnected && document.activeElement === button && button.classList.contains('is-selected') && button.getAttribute('aria-pressed') === 'true')) throw new Error('Keyboard selection replaced or unfocused button');
                    await second.focus();
                    await page.keyboard.press('Space');
                    if (await first.getAttribute('aria-pressed') !== 'false' || await second.getAttribute('aria-pressed') !== 'true' || await first.evaluate(button => button.classList.contains('is-selected'))) throw new Error('Selection state not toggled in place');
                    await page.route('**/admin/media/api?**', route => route.fulfill({json:{items:[items[0]]}}));
                    await page.evaluate(() => loadThemeMedia());
                    if (!await page.locator('#media-picker-confirm').isDisabled()) throw new Error('Theme refresh retained hidden selection');
                    await page.unroute('**/admin/media/api?**');
                    const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
                    await modal.locator('[data-upload-file]').setInputFiles(ico);
                    const icon = (await (await upload).json()).item;
                    await modal.locator('.is-selected').waitFor();
                    await page.locator('#media-picker-confirm').click();
                    if (await page.locator('#hero_image').inputValue() !== icon.filepath) throw new Error('Theme ICO stopped working');
                    return 'oCMS browser step passed';
                }
                if (kind === 'editor') {
                    await page.evaluate(() => editorImagePicker.open((src) => { window.reviewInsertedImage = src; }));
                    const modal = page.locator('#editor-image-modal');
                    if (!(await modal.locator('[data-upload-file]').getAttribute('accept')).split(',').includes('.ico')) throw new Error('Editor input lost ICO');
                    await modal.locator('.editor-image-item').first().click();
                    const selected = await page.evaluate(() => editorImagePicker.selectedImage.id);
                    await page.evaluate(async () => { editorImagePicker.currentPage = 2; await editorImagePicker.loadMedia(); });
                    if (await modal.locator('.editor-image-item').count() === 0 || await page.evaluate(() => editorImagePicker.selectedImage?.id) !== selected || await page.locator('#editor-image-insert-btn').isDisabled()) throw new Error('Editor page 2 lost selection');
                    await page.evaluate(async () => { editorImagePicker.currentPage = 1; await editorImagePicker.loadMedia(); });
                    if (await modal.locator('.is-selected').getAttribute('data-id') !== String(selected)) throw new Error('Editor page 1 lost selection');
                    const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
                    await modal.locator('[data-upload-file]').setInputFiles(ico);
                    const icon = (await (await upload).json()).item;
                    if (icon.thumbnail) throw new Error('ICO fixture has variants');
                    await modal.locator('.is-selected').waitFor();
                    await page.route('**/admin/media/api?**', route => route.fulfill({json:{items:[items[0]]}}));
                    await page.locator('#editor-image-search').fill('different');
                    await page.waitForFunction(() => editorImagePicker.currentItems.length === 1 && editorImagePicker.selectedImage === null && editorImagePicker.insertBtn.disabled);
                    if (await page.locator('#editor-image-options').isVisible()) throw new Error('Hidden selection options remain');
                    await page.unroute('**/admin/media/api?**');
                    await page.locator('#editor-image-search').fill('');
                    await modal.locator('.editor-image-item[data-id="'+icon.id+'"]').waitFor();
                    await modal.locator('.editor-image-item[data-id="'+icon.id+'"]').click();
                    await modal.locator('.is-selected').waitFor();
                    await page.route('**/admin/media/api?**', route => route.fulfill({json:{items:[]}}));
                    await page.locator('#editor-image-search').fill('empty');
                    await page.waitForFunction(() => editorImagePicker.currentItems.length === 0 && editorImagePicker.selectedImage === null && editorImagePicker.insertBtn.disabled);
                    await page.unroute('**/admin/media/api?**');
                    await page.locator('#editor-image-search').fill('');
                    await modal.locator('.editor-image-item[data-id="'+icon.id+'"]').waitFor();
                    await modal.locator('.editor-image-item[data-id="'+icon.id+'"]').click();
                    await modal.locator('.is-selected').waitFor();
                    if (await page.locator('#editor-image-size input[data-tui-selectbox-hidden-input]').inputValue() !== 'medium') throw new Error('Fixture must exercise default medium');
                    await page.locator('#editor-image-insert-btn').click();
                    if (await page.evaluate(() => window.reviewInsertedImage) !== icon.filepath) throw new Error('Default medium invented an ICO variant');
                    return 'oCMS browser step passed';
                }
                if (kind === 'og') await page.locator('details:has([name=og_image_id]) > summary').click();
                const picker = page.locator('.media-dropzone[data-mode=select]').nth(kind === 'og' ? 1 : 0);
                await picker.locator('.media-dropzone-area').click();
                const modal = picker.locator('.media-dropzone-modal');
                const hint = await modal.locator('[data-media-picker-upload] .form-hint').innerText();
                if (hint !== 'JPG, PNG, GIF, WebP · Max 20 MB') throw new Error('Wrong raster upload hint');
                const input = modal.locator('[data-upload-file]');
                if ((await input.getAttribute('accept')).split(',').includes('.ico')) throw new Error('Raster input accepts ICO');
                let posts = 0;
                await page.route('**/admin/media/upload', route => { posts++; return route.fulfill({status:400,json:{error:'Unexpected ICO upload'}}); });
                await input.setInputFiles(ico);
                await modal.locator('[data-upload-status]').filter({hasText:'Choose a JPG, PNG, GIF or WebP image.'}).waitFor();
                if (posts !== 0) throw new Error('Raster ICO selection sent a POST');
                await page.unroute('**/admin/media/upload');
                await modal.locator('.media-dropzone-modal-item').first().click();
                const selected = await picker.evaluate(el => Alpine.$data(el).tempSelected.id);
                await modal.getByRole('button', {name:'Next',exact:true}).click();
                await picker.evaluate(el => new Promise(resolve => {
                    const check = () => { const state = Alpine.$data(el); if (!state.loading && state.currentPage === 2) resolve(); else setTimeout(check, 20); }; check();
                }));
                if (await picker.evaluate(el => Alpine.$data(el).tempSelected?.id) !== selected || await modal.getByRole('button',{name:'Select Image',exact:true}).isDisabled()) throw new Error('Page 2 lost selection');
                await modal.getByRole('button', {name:'Previous',exact:true}).click();
                await modal.locator('.is-selected').waitFor();
                if (await picker.evaluate(el => Alpine.$data(el).tempSelected?.id) !== selected) throw new Error('Page 1 lost selection');
                const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 201);
                await modal.locator('[data-upload-file]').setInputFiles(png);
                const uploaded = (await (await upload).json()).item;
                await modal.locator('.is-selected').waitFor();
                // Pin merge happens before reconciliation, including a failed refresh.
                await page.route('**/admin/media/api?**', route => route.fulfill({status:500}));
                await picker.evaluate(el => Alpine.$data(el).loadMedia());
                if (await modal.getByRole('button',{name:'Select Image',exact:true}).isDisabled()) throw new Error('Unfiltered refresh lost pin selection');
                await page.unroute('**/admin/media/api?**');
                await page.route('**/admin/media/api?**', route => route.fulfill({json:{items:[items[0]]}}));
                await modal.locator('input[type=text]').fill('different');
                await picker.evaluate(el => new Promise(resolve => {
                    const check = () => { const state = Alpine.$data(el); if (!state.loading && state.mediaItems.length === 1 && state.tempSelected === null) resolve(); else setTimeout(check, 20); }; check();
                }));
                if (!await modal.getByRole('button',{name:'Select Image',exact:true}).isDisabled()) throw new Error('Search retained hidden selection');
                await page.unroute('**/admin/media/api?**');
                await modal.locator('input[type=text]').fill('');
                await page.waitForFunction(({index, id}) => {const state = Alpine.$data(document.querySelectorAll('.media-dropzone[data-mode=select]')[index]); return !state.loading && !state.searchQuery && state.mediaItems.some(item => item.id === id);}, {index:kind === 'og' ? 1 : 0, id:uploaded.id});
                await picker.evaluate((el, id) => {const state = Alpine.$data(el); state.selectItem(state.mediaItems.find(item => item.id === id));}, uploaded.id);
                await modal.locator('select').selectOption('document');
                await page.waitForFunction(index => {const state = Alpine.$data(document.querySelectorAll('.media-dropzone[data-mode=select]')[index]); return !state.loading && state.mediaItems.length === 0 && state.tempSelected === null;}, kind === 'og' ? 1 : 0);
                if (!await modal.getByRole('button',{name:'Select Image',exact:true}).isDisabled()) throw new Error('Filter retained hidden selection');
                return 'oCMS browser step passed';
            }`, strconv.Quote(kind), strconv.Quote(base), strconv.Quote(imagePath), strconv.Quote(iconPath), string(items)))
		})
	}
}
