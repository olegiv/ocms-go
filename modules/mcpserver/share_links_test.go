// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestAgentShareLinksPolicyAndAdminRevocation checks the share-link control
// for AI clients (2026-11-03 audit B-5): sharing is on for an existing site
// after the migration, an administrator can turn it off so
// create_preview_link refuses, and the MCP admin page lists the usable links
// agents issued and revokes one with an audit event.
//
// Bug state: drop the AgentShareLinks check from createPreviewLink, or the
// share link list or revoke route from the admin page, and this fails.
func TestAgentShareLinksPolicyAndAdminRevocation(t *testing.T) {
	env := newTestEnv(t)
	if settings, err := loadSettings(t.Context(), env.db); err != nil || !settings.AgentShareLinks {
		t.Fatalf("migrated site does not allow agent share links: %+v %v", settings, err)
	}
	enableDraftTools(env)
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Shared draft")))
	shared := decodeResult[preview.Link](t, call(t, session, "create_preview_link", map[string]any{"page_id": saved.PageID, "revision": saved.Revision}))
	if shared.URL == "" {
		t.Fatal("share link not issued while allowed")
	}

	env.setSettings(Settings{DraftToolsEnabled: true, ExternalLinks: "keep", AgentShareLinks: false})
	if code, _, _ := toolErrorBody(t, call(t, session, "create_preview_link", map[string]any{"page_id": saved.PageID, "revision": saved.Revision})); code != "share_links_disabled" {
		t.Errorf("share link with agent sharing off: code %q", code)
	}
	var issued int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM preview_links`).Scan(&issued); err != nil || issued != 1 {
		t.Fatalf("refused call issued a link: %d %v", issued, err)
	}

	admin, err := env.q.GetUserByID(t.Context(), env.userID)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/admin", env.module.RegisterAdminRoutes)
	request := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(url.Values{}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	var linkID int64
	if err = env.db.QueryRow(`SELECT id FROM preview_links`).Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	revoke := fmt.Sprintf("%s/share-links/%d/revoke", adminPath, linkID)
	if w := request(http.MethodGet, adminPath); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Shared draft") || !strings.Contains(w.Body.String(), `action="`+revoke+`"`) {
		t.Fatalf("admin page does not list the agent's share link: %d", w.Code)
	}
	if w := request(http.MethodPost, revoke); w.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", w.Code)
	}
	var revokedAt int64
	if err = env.db.QueryRow(`SELECT revoked_at FROM preview_links WHERE id = ?`, linkID).Scan(&revokedAt); err != nil || revokedAt == 0 {
		t.Fatalf("link not revoked: %d %v", revokedAt, err)
	}
	var events int
	if err = env.db.QueryRow(`SELECT COUNT(*) FROM events WHERE message = 'Preview share link revoked'`).Scan(&events); err != nil || events != 1 {
		t.Errorf("revocation recorded %d events: %v", events, err)
	}
	if w := request(http.MethodGet, adminPath); strings.Contains(w.Body.String(), `action="`+revoke+`"`) {
		t.Error("a revoked link is still listed")
	}
	if revoked, revokeErr := store.New(env.db).RevokeAgentPreviewLink(t.Context(), store.RevokeAgentPreviewLinkParams{RevokedAt: 1, ID: linkID}); revokeErr != nil || revoked != 0 {
		t.Errorf("a revoked link was revoked again: %d %v", revoked, revokeErr)
	}
}
