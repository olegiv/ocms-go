// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"testing"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/model"
)

// TestDraftToolsRefuseWhenAuthoringIsOff checks that every tool behind the
// mcp:drafts scope refuses while draft authoring is switched off by
// OCMS_AI_DISABLED, and that a module context without configuration counts
// as switched off. The catalog drives the list, so a new draft tool must be
// given arguments here or the test fails.
//
// Bug state: drop the AIDisabled condition from authorizeDraftCall, or let a
// nil Config skip it, and this fails.
func TestDraftToolsRefuseWhenAuthoringIsOff(t *testing.T) {
	switches := []struct {
		name string
		off  func(*Module)
	}{
		{"ai_disabled", func(m *Module) { m.ctx.Config.AIDisabled = true }},
		{"nil_config", func(m *Module) { m.ctx.Config = nil }},
	}
	for _, sw := range switches {
		t.Run(sw.name, func(t *testing.T) {
			env := newTestEnv(t)
			enableDraftTools(env)
			session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
			saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Kill switch")))
			args := map[string]map[string]any{
				"create_draft_page": draftCreateArgs("Refused"),
				"update_draft_page": {"request_id": uuid.NewString(), "page_id": saved.PageID, "base_revision": saved.Revision,
					"changes": map[string]any{"summary": "Refused"}},
				"create_translation_draft": {"request_id": uuid.NewString(), "source_page_id": saved.PageID, "source_revision": saved.Revision,
					"language_code": "ru", "draft": draftCreateArgs("Refused")["draft"]},
				"get_my_draft":           {"page_id": saved.PageID},
				"list_my_drafts":         {},
				"get_translation_status": {"page_id": saved.PageID},
				"create_preview_link":    {"page_id": saved.PageID, "revision": saved.Revision},
			}
			sw.off(env.module)
			covered := 0
			for _, spec := range toolCatalog() {
				if spec.scope != model.PermissionMCPDrafts {
					continue
				}
				input, ok := args[spec.Name]
				if !ok {
					t.Fatalf("draft tool %s has no kill-switch case", spec.Name)
				}
				if code, _, _ := toolErrorBody(t, call(t, session, spec.Name, input)); code != codeForbidden {
					t.Errorf("%s with authoring off: code %q", spec.Name, code)
				}
				covered++
			}
			if covered != len(args) {
				t.Fatalf("covered %d draft tools, test lists %d", covered, len(args))
			}
			var versions int
			if err := env.db.QueryRow("SELECT COUNT(*) FROM page_versions").Scan(&versions); err != nil || versions != 1 {
				t.Fatalf("refused calls wrote revisions: %d %v", versions, err)
			}
		})
	}
}
