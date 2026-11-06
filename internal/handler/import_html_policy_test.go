// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/transfer"
)

func TestAdminImportAppliesConfiguredHTMLPolicy(t *testing.T) {
	for _, zipImport := range []bool{false, true} {
		for _, block := range []bool{false, true} {
			t.Run(fmt.Sprintf("zip=%v/block=%v", zipImport, block), func(t *testing.T) {
				db, sm := testHandlerSetup(t)
				user := createTestAdminUser(t, db)
				renderer, err := render.New(render.Config{TemplatesFS: os.DirFS("../../web/templates"), SessionManager: sm, DB: db, IsDev: true})
				if err != nil {
					t.Fatal(err)
				}
				h := NewImportExportHandler(db, renderer, sm)
				h.SetUploadDir(t.TempDir())
				h.SetSanitizePageHTML(true)
				h.SetBlockSuspiciousMarkup(block)
				data := transfer.ExportData{Version: transfer.ExportVersion, Pages: []transfer.ExportPage{{ID: 1, Title: "Policy", Slug: "policy", Body: `<p>Safe</p><script>B2_PRIVATE_BODY()</script>`, Status: "draft", AuthorEmail: user.Email}}}
				payload, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				if zipImport {
					var buffer bytes.Buffer
					writer := zip.NewWriter(&buffer)
					entry, createErr := writer.Create("export.json")
					if createErr != nil {
						t.Fatal(createErr)
					}
					if _, writeErr := entry.Write(payload); writeErr != nil {
						t.Fatal(writeErr)
					}
					if closeErr := writer.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					payload = buffer.Bytes()
				}
				form := url.Values{"import_pages": {"on"}, "conflict_strategy": {"overwrite"}}
				req := httptest.NewRequest(http.MethodPost, "/admin/import", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = addUserToContext(requestWithSession(sm, req), &user)
				sm.Put(req.Context(), "import_is_zip", zipImport)
				if err = h.stageImportPayload(req.Context(), payload); err != nil {
					t.Fatal(err)
				}
				recorder := httptest.NewRecorder()
				h.Import(recorder, req)
				if block {
					if !strings.Contains(sm.GetString(req.Context(), "flash_error"), "blocked by policy") {
						t.Fatal("raw suspicious markup was not refused")
					}
					// The shared importer receives the blocking flag as well as the
					// handler's existing early refusal/audit check.
					result, importErr := h.newImporter().Import(req.Context(), &data, transfer.ImportOptions{ImportPages: true})
					if importErr == nil || result == nil || result.Success {
						t.Fatal("handler did not configure the shared importer blocker")
					}
					return
				}
				stored, loadErr := store.New(db).GetPageBySlug(req.Context(), "policy")
				if recorder.Code != http.StatusOK || loadErr != nil || stored.Body != "<p>Safe</p>" {
					t.Fatalf("admin import failed to sanitize: status=%d body=%q error=%v", recorder.Code, stored.Body, loadErr)
				}
				if !strings.Contains(recorder.Body.String(), "Unsupported HTML was removed from the page body") || strings.Contains(recorder.Body.String(), "B2_PRIVATE_BODY") {
					t.Fatal("removal note absent or body exposed in result")
				}
			})
		}
	}
}

func TestImportHTMLPolicyConfigurationWiring(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "../../cmd/ocms/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	configured := make(map[string]bool)
	for node := range ast.Preorder(parsed) {
		call, callOK := node.(*ast.CallExpr)
		if !callOK || len(call.Args) != 1 {
			continue
		}
		selected, selectedOK := call.Fun.(*ast.SelectorExpr)
		if !selectedOK {
			continue
		}
		receiver, receiverOK := selected.X.(*ast.Ident)
		flag, flagOK := call.Args[0].(*ast.SelectorExpr)
		if receiverOK && receiver.Name == "importExportHandler" && flagOK {
			configured[selected.Sel.Name+":"+flag.Sel.Name] = true
		}
	}
	for _, pair := range []string{"SetSanitizePageHTML:SanitizePageHTML", "SetBlockSuspiciousMarkup:BlockSuspiciousPageHTML"} {
		if !configured[pair] {
			t.Errorf("import handler is missing server policy wiring %s", pair)
		}
	}
}
