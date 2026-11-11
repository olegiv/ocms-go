// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"golang.org/x/net/html"
)

func TestMediaPickerFormatsByContext(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, webp := range []bool{true, false} {
		previous := service.AllowedMimeTypes[model.MimeTypeWebP]
		service.AllowedMimeTypes[model.MimeTypeWebP] = webp
		t.Cleanup(func() { service.AllowedMimeTypes[model.MimeTypeWebP] = previous })
		for _, lang := range []string{"en", "ru"} {
			pc := &PageContext{AdminLang: lang}
			for _, tc := range []struct {
				name      string
				component templ.Component
				allowICO  []bool
			}{
				{"page editor, featured and OG", PageFormPage(pc, PageFormViewData{}), []bool{true, false, false}},
				{"theme", themeMediaPicker(pc), []bool{true}},
			} {
				t.Run(fmt.Sprintf("webp=%t/%s/%s", webp, lang, tc.name), func(t *testing.T) {
					var buf bytes.Buffer
					if err := tc.component.Render(t.Context(), &buf); err != nil {
						t.Fatal(err)
					}
					doc, err := html.Parse(&buf)
					if err != nil {
						t.Fatal(err)
					}
					index := 0
					for element := range doc.Descendants() {
						if mediaPickerAttr(element, "class") != "media-picker-upload" {
							continue
						}
						if index >= len(tc.allowICO) {
							t.Fatal("unexpected upload picker")
						}
						accept := strings.Join(service.ImageUploadExtensions(tc.allowICO[index]), ",")
						hintKey, invalidKey := "media.picker_upload_hint_raster", "media.picker_upload_invalid_raster"
						if tc.allowICO[index] {
							hintKey, invalidKey = "media.picker_upload_hint", "media.picker_upload_invalid"
						}
						for key, want := range map[string]string{
							"data-allowed-formats": accept,
							"data-invalid":         pc.T(invalidKey),
							"data-max-size":        "20971520",
						} {
							if got := mediaPickerAttr(element, key); got != want {
								t.Errorf("picker %d %s = %q, want %q", index, key, got, want)
							}
						}
						var gotAccept, gotHint string
						for child := range element.Descendants() {
							if child.Data == "input" {
								gotAccept = mediaPickerAttr(child, "accept")
							}
							if mediaPickerAttr(child, "class") == "form-hint" && child.FirstChild != nil {
								gotHint = child.FirstChild.Data
							}
						}
						if gotAccept != accept || gotHint != pc.T(hintKey) || gotHint == hintKey {
							t.Errorf("picker %d accept/hint = %q / %q, want %q / %q", index, gotAccept, gotHint, accept, pc.T(hintKey))
						}
						index++
					}
					if index != len(tc.allowICO) {
						t.Fatalf("rendered %d upload pickers, want %d", index, len(tc.allowICO))
					}
				})
			}
		}
	}
}

// JPEG is named JPG in the existing prose; all other raster extensions must
// appear verbatim. Check both hint and refusal text in every shipped locale.
func TestMediaPickerRasterFormatTranslations(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		for _, key := range []string{"media.picker_upload_hint_raster", "media.picker_upload_invalid_raster"} {
			text := strings.ToLower(i18n.T(lang, key))
			for _, extension := range service.ImageUploadExtensions(false) {
				name := strings.TrimPrefix(extension, ".")
				if name == "jpeg" {
					name = "jpg"
				}
				if !strings.Contains(text, name) {
					t.Errorf("%s %s omits server-allowed %s: %s", lang, key, extension, text)
				}
			}
		}
	}
}

func mediaPickerAttr(element *html.Node, key string) string {
	for _, attr := range element.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// Execute the production state machines with deterministic library responses;
// these checks need no listener or browser. Chrome tests cover the DOM wiring.
func TestMediaPickerSelectionRefilter(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	dropzone, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "static", "js", "media-dropzone.js"))
	if err != nil {
		t.Fatal(err)
	}
	var scripts bytes.Buffer
	if err := pageFormScripts(nil, PageFormViewData{}).Render(t.Context(), &scripts); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"dropzone": string(dropzone), "editor": scripts.String()})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"alpine", "editor"} {
		t.Run(kind, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), node, "-e", mediaPickerSelectionCheck, kind)
			cmd.Stdin = bytes.NewReader(payload)
			if output, runErr := cmd.CombinedOutput(); runErr != nil {
				t.Fatalf("picker selection: %v\n%s", runErr, output)
			}
		})
	}
}

const mediaPickerSelectionCheck = `
const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
const items = Array.from({length:13}, (_, i) => ({id:i+1, filename:'image-'+i+'.png'}));
const element = () => ({style:{}, dataset:{}, appendChild(){}, addEventListener(){}, querySelector(){return {};}});
const context = vm.createContext({console, URL, document:{addEventListener(){}, createElement:element},
    fetch:async raw => {
        const params = new URL(raw, 'http://picker.test').searchParams;
        const visible = params.get('q') === 'other' || params.get('type') === 'document' ? [items[12]] : items.slice((Number(params.get('page'))-1)*12, Number(params.get('page'))*12);
        return {ok:true, json:async () => ({items:visible, totalPages:2})};
    }});
const settle = () => new Promise(setImmediate);
(async () => {
    if (process.argv[1] === 'alpine') {
        vm.runInContext(source.dropzone, context);
        const picker = vm.runInContext('mediaDropzone()', context);
        await picker.loadMedia();
        picker.selectItem(items[0]);
        picker.nextPage(); await settle();
        assert.equal(picker.currentPage, 2);
        assert.equal(picker.tempSelected, items[0], 'paging must retain selection');
        picker.prevPage(); await settle();
        assert.equal(picker.tempSelected, items[0]);
        picker.selectedImage = items[12];
        picker.openPicker(); await settle();
        assert.equal(picker.tempSelected, items[12], 'open must retain an image outside page 1');
        for (const filter of ['searchQuery', 'filterType']) {
            picker.selectItem(items[0]); picker.currentPage = 2;
            picker[filter] = filter === 'searchQuery' ? 'other' : 'document';
            picker.refilter(); await settle();
            assert.equal(picker.currentPage, 1);
            assert.equal(picker.tempSelected, null, 'refilter must clear hidden selection');
            picker[filter] = '';
        }
        picker.selectItem(items[0]); picker.refilter(); await settle();
        assert.equal(picker.tempSelected, items[0], 'refilter must retain visible selection');
        let uploaded;
        context.initMediaPickerUpload = (_root, callback) => {uploaded = callback; return {reset(){}};};
        picker.initSelectMode({querySelector(){return null;}, dataset:{}});
        const item = {id:99, filename:'upload.png'};
        uploaded(item); await settle();
        assert.equal(picker.tempSelected, item);
        assert.equal(picker.mediaItems[0], item, 'upload must stay pinned');
    } else {
        const start = source.editor.indexOf('const editorImagePicker =');
        const end = source.editor.indexOf('// Initialize TinyMCE', start);
        vm.runInContext(source.editor.slice(start, end), context);
        const picker = vm.runInContext('editorImagePicker', context);
        Object.assign(picker, {grid:element(), modal:element(), searchInput:{value:''}, optionsPanel:element(), insertBtn:{disabled:false}});
        picker.selectedImage = items[0];
        await picker.loadMedia();
        picker.currentPage = 2; await picker.loadMedia();
        assert.equal(picker.selectedImage, items[0], 'render on page 2 must retain selection');
        picker.currentPage = 1; await picker.loadMedia();
        assert.equal(picker.selectedImage, items[0]);
        picker.refilter(); await settle();
        assert.equal(picker.selectedImage, items[0], 'search must retain visible selection');
        picker.currentPage = 2; picker.searchInput.value = 'other';
        picker.refilter(); await settle();
        assert.equal(picker.currentPage, 1);
        assert.equal(picker.selectedImage, null);
        assert.equal(picker.insertBtn.disabled, true);
        assert.equal(picker.optionsPanel.style.display, 'none');
    }
})().catch(error => {console.error(error); process.exitCode = 1;});
`

// Run the production XHR mapping without a listener. The same translated
// invalid-image key covers type, corrupt-image and dimension-limit refusals.
func TestMediaPickerInvalidImageMessage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "static", "js", "media-dropzone.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		for _, ico := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ico=%v", lang, ico), func(t *testing.T) {
				pc := &PageContext{AdminLang: lang}
				var rendered bytes.Buffer
				if err := mediaPickerUpload(pc, ico).Render(t.Context(), &rendered); err != nil {
					t.Fatal(err)
				}
				doc, err := html.Parse(&rendered)
				if err != nil {
					t.Fatal(err)
				}
				var invalid string
				for element := range doc.Descendants() {
					if mediaPickerAttr(element, "class") == "media-picker-upload" {
						invalid = mediaPickerAttr(element, "data-invalid")
					}
				}
				words := []string{"corrupt", "maximum dimensions"}
				if lang == "ru" {
					words = []string{"повреждено", "максимальные размеры"}
				}
				for _, word := range words {
					if !strings.Contains(invalid, word) {
						t.Fatalf("%s refusal omits %q: %s", lang, word, invalid)
					}
				}
				payload, err := json.Marshal(map[string]string{"source": string(source), "invalid": invalid})
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), node, "-e", mediaPickerInvalidImageCheck)
				cmd.Stdin = bytes.NewReader(payload)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("XHR validation message: %v\n%s", err, output)
				}
			})
		}
	}
}

const mediaPickerInvalidImageCheck = `
const assert = require('node:assert/strict');
const vm = require('node:vm');
const {source,invalid} = JSON.parse(require('node:fs').readFileSync(0,'utf8'));
let change;
const input = {files:[{name:'corrupt.png',size:24}],addEventListener(_event,callback){change=callback;}};
const status = {classList:{add(){},remove(){}}};
const controls = {'[data-upload-file]':input,'[data-upload-choose]':{addEventListener(){}},'[data-upload-progress]':{},'[data-upload-status]':status};
const root = {isConnected:true,dataset:{allowedFormats:'.png',maxSize:'20971520',invalid,error:'retry'},setAttribute(){},querySelector(selector){return controls[selector];}};
class XHR {
    constructor(){this.upload={};this.status=400;this.response={error:'private English decoder failure'};}
    open(){} setRequestHeader(){} getResponseHeader(){return 'application/json';}
    send(){this.onload();}
}
const context = vm.createContext({document:{addEventListener(){}},XMLHttpRequest:XHR,FormData:class{append(){}},root});
vm.runInContext(source,context);
vm.runInContext('initMediaPickerUpload(root, () => {throw new Error("refusal selected an image")})',context);
change();
assert.equal(status.textContent,invalid,'400 must use the translated invalid-image message');
assert.equal(input.disabled,false);
`
