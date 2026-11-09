// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"os"
	"os/exec"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/middleware"
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
			sw.off(env.module)
			expectDraftToolsRefused(t, env, session, saved.PageID, saved.Revision, 1)
		})
	}
}

// TestDraftToolsRefuseInDemoMode checks that every draft tool refuses in
// demo mode, whose seeded accounts every visitor shares (2026-11-03 audit
// B-3). IsDemoMode is read once per process, so the check runs in a child
// test process.
//
// Bug state: drop middleware.IsDemoMode() from authorizeDraftCall and this
// fails.
func TestDraftToolsRefuseInDemoMode(t *testing.T) {
	if os.Getenv("OCMS_MCP_DEMO_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDraftToolsRefuseInDemoMode$", "-test.count=1")
		cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_MCP_DEMO_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("demo-mode child: %v\n%s", err, out)
		}
		return
	}
	if !middleware.IsDemoMode() {
		t.Fatal("child process is not in demo mode")
	}
	env := newTestEnv(t)
	enableDraftTools(env)
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	page := env.createPage(pageSeed{title: "Demo page", slug: "demo-page", body: "<p>Demo</p>", status: model.PageStatusDraft})
	expectDraftToolsRefused(t, env, session, page, 1, 0)
}

// expectDraftToolsRefused calls every tool behind the mcp:drafts scope with
// valid arguments for the given page and expects a forbidden refusal and no
// new revision. The catalog drives the list, so a new draft tool must be
// given arguments here or the check fails.
func expectDraftToolsRefused(t *testing.T, env *testEnv, session *mcp.ClientSession, pageID, revision int64, versionsBefore int) {
	t.Helper()
	args := map[string]map[string]any{
		"create_draft_page": draftCreateArgs("Refused"),
		"update_draft_page": {"request_id": uuid.NewString(), "page_id": pageID, "base_revision": revision,
			"changes": map[string]any{"summary": "Refused"}},
		"create_translation_draft": {"request_id": uuid.NewString(), "source_page_id": pageID, "source_revision": revision,
			"language_code": "ru", "draft": draftCreateArgs("Refused")["draft"]},
		"get_my_draft":           {"page_id": pageID},
		"list_my_drafts":         {},
		"get_translation_status": {"page_id": pageID},
		"create_preview_link":    {"page_id": pageID, "revision": revision},
	}
	covered := 0
	for _, spec := range toolCatalog() {
		if spec.scope != model.PermissionMCPDrafts {
			continue
		}
		input, ok := args[spec.Name]
		if !ok {
			t.Fatalf("draft tool %s has no refusal case", spec.Name)
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
	if err := env.db.QueryRow("SELECT COUNT(*) FROM page_versions").Scan(&versions); err != nil || versions != versionsBefore {
		t.Fatalf("refused calls wrote revisions: %d %v", versions, err)
	}
}
