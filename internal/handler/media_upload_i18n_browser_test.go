// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
)

func TestMediaPickerRussianChrome(t *testing.T) {
	cli := requireChromeCLI(t)
	_, base := pickerChromeServer(t)
	artifacts := filepath.Join(repositoryRoot(t), "output", "playwright")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	browser := openAdminChrome(t, cli, "picker-russian", base+"/admin/pages/new")
	for _, kind := range []string{"featured", "og", "editor", "theme"} {
		t.Run(kind, func(t *testing.T) {
			browser(t, "run-code", fmt.Sprintf(`async page => {
                const kind = %s, base = %s;
                const messages = %s;
                const screenshot = %s;
                await page.context().addCookies([{name:%s,value:'ru',url:base}]);
                await page.setViewportSize({width:1680,height:1080});
                await page.goto(base + (kind === 'theme' ? '/admin/themes/starter/settings' : '/admin/pages/new'));
                if (kind !== 'theme') await page.waitForFunction(() => window.tinymce?.get('editor')?.initialized);
                let modal;
                if (kind === 'theme') {
                    modal = page.locator('#media-picker-overlay');
                    await page.locator('.btn-media-browse[data-field-id=hero_image]').click();
                } else if (kind === 'editor') {
                    modal = page.locator('#editor-image-modal');
                    const button = page.getByRole('button', {name:/insert image/i});
                    if (!await button.isVisible()) await page.getByRole('button', {name:'Reveal or hide additional toolbar items'}).click();
                    await button.click();
                } else {
                    if (kind === 'og') await page.locator('details:has([name=og_image_id]) > summary').click();
                    const picker = page.locator('.media-dropzone[data-mode=select]').nth(kind === 'og' ? 1 : 0);
                    modal = picker.locator('.media-dropzone-modal');
                    await picker.locator('.media-dropzone-area').click();
                }
                await modal.waitFor({state:'visible'});
                await modal.getByRole('button', {name:messages.upload,exact:true}).waitFor();
                const input = modal.locator('[data-upload-file]');
                const error = modal.locator('[data-upload-status].form-error');
                const image = {name:'forged.png',mimeType:'image/png',buffer:Buffer.from('invalid image')};
                // Real server refusal must not expose its English API message.
                const refused = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.status() === 400);
                await input.setInputFiles(image);
                await refused;
                await error.waitFor();
                if (await error.innerText() !== messages.invalid) throw new Error('Untranslated server refusal: '+await error.innerText());
                for (const [status, expected] of [[413,messages.large],[403,messages.forbidden],[500,messages.failed],[200,messages.failed]]) {
                    await page.route('**/admin/media/upload', route => status === 200
                        ? route.fulfill({contentType:'text/html',body:'<html>Sign in</html>'})
                        : route.fulfill({status,json:{error:'English server diagnostic'}}));
                    await input.setInputFiles(image);
                    await error.waitFor();
                    if (await error.innerText() !== expected) throw new Error('Untranslated '+status+' response: '+await error.innerText());
                    await page.unroute('**/admin/media/upload');
                }
                await page.setViewportSize({width:390,height:844});
                await modal.screenshot({path:screenshot});
                await page.setViewportSize({width:1680,height:1080});
                if (kind === 'editor') {
                    const grid = page.locator('#editor-image-grid');
                    if (await grid.innerText() !== messages.empty) throw new Error('Untranslated empty editor picker: '+await grid.innerText());
                    await page.route('**/admin/media/api?**', route => route.fulfill({status:500}));
                    await page.locator('#editor-image-search').fill('missing');
                    await grid.getByText(messages.loadFailed,{exact:true}).waitFor();
                    await page.unroute('**/admin/media/api?**');
                }
                await modal.getByRole('button', {name:messages.cancel,exact:true}).click();
                return 'oCMS browser step passed';
            }`, strconv.Quote(kind), strconv.Quote(base), russianPickerMessages(kind == "editor" || kind == "theme"), strconv.Quote(filepath.Join(artifacts, "issue-211-"+kind+"-russian.png")), strconv.Quote(middleware.AdminLanguageCookieName)))
		})
	}
}

func russianPickerMessages(allowICO bool) string {
	invalidKey := "media.picker_upload_invalid_raster"
	if allowICO {
		invalidKey = "media.picker_upload_invalid"
	}
	return fmt.Sprintf(`{"upload":%s,"invalid":%s,"large":%s,"forbidden":%s,"failed":%s,"empty":%s,"loadFailed":%s,"cancel":%s}`,
		strconv.Quote(i18n.T("ru", "media.picker_upload_image")),
		strconv.Quote(i18n.T("ru", invalidKey)),
		strconv.Quote(i18n.T("ru", "media.picker_upload_too_large")),
		strconv.Quote(i18n.T("ru", "media.picker_upload_forbidden")),
		strconv.Quote(i18n.T("ru", "media.picker_upload_error")),
		strconv.Quote(i18n.T("ru", "media.no_media")),
		strconv.Quote(i18n.T("ru", "error.loading_failed")),
		strconv.Quote(i18n.T("ru", "btn.cancel")))
}
