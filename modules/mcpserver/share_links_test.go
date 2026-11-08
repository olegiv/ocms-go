// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/session"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
)

func TestExistingAgentPreviewLinksHonorDisableSwitches(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t)
	oauthTokenValue, _ := oauthToken(t, enableOAuth(t, env, true), env.userID, "mcp:access mcp:drafts")
	enableDraftTools(env)
	keyClient := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	const marker = "Shared preview policy marker"
	saved := decodeResult[authoring.SaveResult](t, call(t, keyClient, "create_draft_page", draftCreateArgs(marker)))
	args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
	keyLink := decodeResult[issuedPreviewLink](t, call(t, keyClient, "create_preview_link", args))
	grantLink := decodeResult[issuedPreviewLink](t, call(t, env.connect(oauthTokenValue), "create_preview_link", args))
	humanLink, err := env.previewResolver().Issue(t.Context(), provenance.Principal{UserID: env.userID, Source: "human", Transport: "admin"},
		saved.PageID, saved.Revision, "https://cms.test", nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg := env.module.ctx.Config
	logger := env.module.ctx.Logger
	var emptyFS embed.FS
	frontend := handler.NewFrontendHandler(env.db, theme.NewManager(emptyFS, "", logger), nil, logger, nil, nil)
	policy := AgentSharePolicy(cfg)
	frontend.SetAgentSharePolicy(policy)
	assertLinks := func(t *testing.T, agentStatus int) {
		t.Helper()
		for _, tc := range []struct {
			name   string
			rawURL string
			status int
		}{
			{"API key", keyLink.URL, agentStatus},
			{"OAuth grant", grantLink.URL, agentStatus},
			{"human", humanLink.URL.Reveal(), http.StatusOK},
		} {
			parsed, parseErr := url.Parse(tc.rawURL)
			if parseErr != nil || parsed.Fragment == "" {
				t.Fatalf("%s share URL missing a bearer", tc.name)
			}
			form := url.Values{"token": {parsed.Fragment}}
			r := httptest.NewRequest(http.MethodPost, preview.ShareOpenPath, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			frontend.PreviewShare(w, r)
			if hasDraft := strings.Contains(w.Body.String(), marker); w.Code != tc.status || hasDraft != (tc.status == http.StatusOK) {
				t.Errorf("%s preview: status %d, draft visible %t, want status %d", tc.name, w.Code, hasDraft, tc.status)
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Errorf("%s preview lost its no-store policy", tc.name)
			}
		}
	}
	assertLinks(t, http.StatusOK)
	for _, tc := range []struct{ name, update string }{
		{"sharing off", "UPDATE mcp_settings SET agent_share_links = ? WHERE id = 1"},
		{"draft tools off", "UPDATE mcp_settings SET draft_tools_enabled = ? WHERE id = 1"},
		{"module inactive", "UPDATE modules SET is_active = ? WHERE name = 'mcp'"},
		{"AI disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnabled := func(enabled bool) {
				t.Helper()
				if tc.update == "" {
					cfg.AIDisabled = !enabled
					return
				}
				if _, updateErr := env.db.ExecContext(t.Context(), tc.update, enabled); updateErr != nil {
					t.Fatal(updateErr)
				}
			}
			setEnabled(false)
			assertLinks(t, http.StatusNotFound)
			setEnabled(true)
			assertLinks(t, http.StatusOK)
		})
	}
	t.Run("policy missing", func(t *testing.T) {
		frontend.SetAgentSharePolicy(nil)
		assertLinks(t, http.StatusNotFound)
		frontend.SetAgentSharePolicy(policy)
	})
	t.Run("policy storage fault", func(t *testing.T) {
		withTableUnavailable(t, env.db, "mcp_settings", func() { assertLinks(t, http.StatusServiceUnavailable) })
	})
	assertLinks(t, http.StatusOK)

	t.Run("revocation before re-enable", func(t *testing.T) {
		ctx := t.Context()
		if _, disableErr := env.db.ExecContext(ctx, "UPDATE mcp_settings SET agent_share_links = 0"); disableErr != nil {
			t.Fatal(disableErr)
		}
		reenabled := false
		frontend.SetAgentSharePolicy(func(ctx context.Context, q *store.Queries) (bool, error) {
			if !reenabled {
				// Commit revocation and re-enable before reading the policy.
				// This request must not combine the earlier link with that policy.
				if _, revokeErr := env.db.ExecContext(ctx, "UPDATE preview_links SET revoked_at = 1 WHERE issuing_key_id IS NOT NULL OR issuing_grant_id IS NOT NULL"); revokeErr != nil {
					t.Fatal(revokeErr)
				}
				if _, enableErr := env.db.ExecContext(ctx, "UPDATE mcp_settings SET agent_share_links = 1"); enableErr != nil {
					t.Fatal(enableErr)
				}
				reenabled = true
			}
			return policy(ctx, q)
		})
		assertLinks(t, http.StatusNotFound)
		if !reenabled {
			t.Fatal("request did not reach the policy read")
		}
		frontend.SetAgentSharePolicy(policy)
		assertLinks(t, http.StatusNotFound)
	})
}

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
	client := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	saved := decodeResult[authoring.SaveResult](t, call(t, client, "create_draft_page", draftCreateArgs("Shared draft")))
	shared := decodeResult[issuedPreviewLink](t, call(t, client, "create_preview_link", map[string]any{"page_id": saved.PageID, "revision": saved.Revision}))
	if shared.URL == "" {
		t.Fatal("share link not issued while allowed")
	}

	env.setSettings(Settings{DraftToolsEnabled: true, ExternalLinks: "keep", AgentShareLinks: false})
	if code, _, _ := toolErrorBody(t, call(t, client, "create_preview_link", map[string]any{"page_id": saved.PageID, "revision": saved.Revision})); code != "share_links_disabled" {
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
	if _, err = (preview.Service{Q: env.q}).Issue(t.Context(), provenance.Principal{UserID: admin.ID, Source: "human"}, saved.PageID, saved.Revision, "https://cms.test", nil); err != nil {
		t.Fatal(err)
	}
	sm := session.New(env.db, true)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: env.db})
	if err != nil {
		t.Fatal(err)
	}
	env.module.ctx.Render = renderer
	r := chi.NewRouter()
	r.Route("/admin", env.module.RegisterAdminRoutes)
	var flash, flashType string
	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(url.Values{}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx, loadErr := sm.Load(req.Context(), "")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		req = req.WithContext(context.WithValue(ctx, middleware.ContextKeyUser, admin))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		flash, flashType = sm.GetString(ctx, "flash"), sm.GetString(ctx, "flash_type")
		return w
	}
	var linkID, humanLinkID int64
	if err = env.db.QueryRow(`SELECT id FROM preview_links WHERE issuing_key_id IS NOT NULL`).Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	if err = env.db.QueryRow(`SELECT id FROM preview_links WHERE issuing_key_id IS NULL AND issuing_grant_id IS NULL`).Scan(&humanLinkID); err != nil {
		t.Fatal(err)
	}
	revoke := fmt.Sprintf("%s/share-links/%d/revoke", adminPath, linkID)
	humanRevoke := fmt.Sprintf("%s/share-links/%d/revoke", adminPath, humanLinkID)
	if w := request(http.MethodGet, adminPath); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Shared draft") || !strings.Contains(w.Body.String(), `action="`+revoke+`"`) || strings.Contains(w.Body.String(), `action="`+humanRevoke+`"`) {
		t.Fatalf("admin page does not list the agent's share link: %d", w.Code)
	}
	if w := request(http.MethodPost, humanRevoke); w.Code != http.StatusSeeOther || flashType != "info" || flash != i18n.T("en", "mcp.share_revoke_missing") {
		t.Fatalf("editor-issued link revoke: status=%d type=%q message=%q", w.Code, flashType, flash)
	}
	if _, err = env.db.Exec(`CREATE TRIGGER fail_agent_revoke BEFORE UPDATE OF revoked_at ON preview_links
		BEGIN SELECT RAISE(ABORT, 'agent revoke fixture fault'); END`); err != nil {
		t.Fatal(err)
	}
	if w := request(http.MethodPost, revoke); w.Code != http.StatusSeeOther || flashType != "error" || flash != i18n.T("en", "mcp.share_revoke_error") ||
		!strings.Contains(env.logs.text(), "agent revoke fixture fault") {
		t.Fatalf("agent revoke fault: status=%d type=%q message=%q", w.Code, flashType, flash)
	}
	var changed, failedEvents int
	if err = env.db.QueryRow(`SELECT (SELECT COUNT(*) FROM preview_links WHERE revoked_at <> 0),
		(SELECT COUNT(*) FROM events WHERE message = 'Preview share link revoked')`).Scan(&changed, &failedEvents); err != nil || changed != 0 || failedEvents != 0 {
		t.Fatalf("denied revocations changed %d links and wrote %d success events: %v", changed, failedEvents, err)
	}
	if _, err = env.db.Exec(`DROP TRIGGER fail_agent_revoke`); err != nil {
		t.Fatal(err)
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

// A disable committed just before the issuance transaction must prevent both
// the new bearer link and its audit event for either MCP credential type.
func TestAgentShareLinksRechecksPolicyInIssuanceTransaction(t *testing.T) {
	for _, credential := range []string{"API key", "OAuth"} {
		t.Run(credential, func(t *testing.T) {
			env := newTestEnv(t)
			var token string
			if credential == "OAuth" {
				token, _ = oauthToken(t, enableOAuth(t, env, true), env.userID, "mcp:access mcp:drafts")
			} else {
				token = env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
			}
			enableDraftTools(env)
			ctx := t.Context()
			settings, err := loadSettings(ctx, env.db)
			if err != nil || !settings.AgentShareLinks {
				t.Fatalf("load enabled share policy: %v", err)
			}
			settings.AgentShareLinks = false

			var dbPath string
			if err = env.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&dbPath); err != nil {
				t.Fatal(err)
			}
			connector, err := sqlite.NewConnector(dbPath + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_txlock=immediate&_time_format=sqlite&_timezone=UTC")
			if err != nil {
				t.Fatal(err)
			}
			var disableBeforeIssue atomic.Bool
			db := sql.OpenDB(shareLinkTestConnector{Connector: connector, beforeWrite: func(ctx context.Context) error {
				if disableBeforeIssue.CompareAndSwap(true, false) {
					// The admin write commits on the other pool after preflight
					// and before issuance can take SQLite's write lock.
					return saveSettings(ctx, env.db, settings)
				}
				return nil
			}})
			t.Cleanup(func() {
				if closeErr := db.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			env.module.svc.queries = store.New(db)

			client := env.connect(token)
			saved := decodeResult[authoring.SaveResult](t, call(t, client, "create_draft_page", draftCreateArgs("Sharing policy changed")))
			args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
			shared := decodeResult[issuedPreviewLink](t, call(t, client, "create_preview_link", args))
			shareURL, err := url.Parse(shared.URL)
			if err != nil || shareURL.Fragment == "" {
				t.Fatal("enabled call did not issue a share link")
			}
			disableBeforeIssue.Store(true)
			refused := call(t, client, "create_preview_link", args)
			settingsAfter, err := loadSettings(ctx, env.db)
			if err != nil || disableBeforeIssue.Load() || settingsAfter.AgentShareLinks {
				t.Fatalf("share policy change did not commit before issuance: %v", err)
			}
			var links, events int
			if err = env.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM preview_links),
				(SELECT COUNT(*) FROM events WHERE message = 'Preview share link created')`).Scan(&links, &events); err != nil {
				t.Fatal(err)
			}
			if links != 1 || events != 1 {
				t.Errorf("after the disable, got %d links and %d issuance events, want one each", links, events)
			}
			if !refused.IsError {
				t.Fatal("create_preview_link succeeded after the disable committed")
			}
			if code, _, _ := toolErrorBody(t, refused); code != "share_links_disabled" {
				t.Fatalf("queued share link with sharing disabled: code %q", code)
			}
			if _, err = env.previewResolver().Resolve(ctx, shareURL.Fragment); !errors.Is(err, preview.ErrUnavailable) {
				t.Fatalf("previously issued link stayed available after sharing was disabled: %v", err)
			}
			decodeResult[authoring.SaveResult](t, call(t, client, "create_draft_page", draftCreateArgs("Draft with sharing off")))
		})
	}
}

// A deferred, read-uncommitted connection must reserve the writer before its
// policy read, so a disable cannot commit between that read and the insert.
func TestAgentShareLinksSerializeDisableWithDeferredIssuance(t *testing.T) {
	env := newTestEnv(t)
	enableDraftTools(env)
	ctx := t.Context()
	settings, err := loadSettings(ctx, env.db)
	if err != nil {
		t.Fatal(err)
	}
	disabled := settings
	disabled.AgentShareLinks = false
	var dbPath string
	if err = env.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	// These URI options override the normal immediate transaction mode.
	// The last parameter leaves room for NewDB's appended pragma options.
	dsn := "file:" + dbPath + "?cache=shared&_txlock=deferred&_pragma=read_uncommitted(1)&_unused=1"
	admin, err := store.NewDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := admin.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	connector, err := sqlite.NewConnector(dsn + "&_pragma=foreign_keys(ON)&_time_format=sqlite&_timezone=UTC")
	if err != nil {
		t.Fatal(err)
	}
	disableAttempt := make(chan error, 1)
	db := sql.OpenDB(shareLinkTestConnector{Connector: connector, beforeInsert: func(ctx context.Context) error {
		// Attempt the real settings write after all policy reads. The issuer
		// holds its transaction open until this attempt finishes.
		writeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		disableAttempt <- saveSettings(writeCtx, admin, disabled)
		return nil
	}})
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	env.module.svc.queries = store.New(db)
	client := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	saved := decodeResult[authoring.SaveResult](t, call(t, client, "create_draft_page", draftCreateArgs("Deferred share issuance")))
	args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
	shared := decodeResult[issuedPreviewLink](t, call(t, client, "create_preview_link", args))
	if shared.URL == "" || shared.PageID != saved.PageID || shared.Revision != saved.Revision {
		t.Fatal("enabled issuance did not create the requested link")
	}
	select {
	case disableErr := <-disableAttempt:
		if !errors.Is(disableErr, context.DeadlineExceeded) {
			t.Fatalf("disable was not blocked until issuance committed: %v", disableErr)
		}
	default:
		t.Fatal("issuance did not reach the insert")
	}
	if current, loadErr := loadSettings(ctx, admin); loadErr != nil || current != settings {
		t.Fatalf("issuance or blocked disable changed the settings: %v", loadErr)
	}
	// Once issuance commits, the same settings write succeeds and stops
	// further links through the normal tool entry point.
	if err = saveSettings(ctx, admin, disabled); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := toolErrorBody(t, call(t, client, "create_preview_link", args)); code != "share_links_disabled" {
		t.Fatalf("share link after the disable committed: code %q", code)
	}
	var links, events int
	if err = env.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM preview_links),
		(SELECT COUNT(*) FROM events WHERE message = 'Preview share link created')`).Scan(&links, &events); err != nil || links != 1 || events != 1 {
		t.Fatalf("got %d links and %d issuance events, want one each: %v", links, events, err)
	}
}

type shareLinkTestConnector struct {
	driver.Connector
	beforeWrite  func(context.Context) error
	beforeInsert func(context.Context) error
	beforeSQL    func(string) error
}

func (c shareLinkTestConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return shareLinkTestConn{Conn: conn, hooks: c}, nil
}

type shareLinkTestConn struct {
	driver.Conn
	hooks shareLinkTestConnector
}

func (c shareLinkTestConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !opts.ReadOnly && c.hooks.beforeWrite != nil {
		if err := c.hooks.beforeWrite(ctx); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c shareLinkTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.hooks.beforeSQL != nil {
		if err := c.hooks.beforeSQL(query); err != nil {
			return nil, err
		}
	}
	if c.hooks.beforeInsert != nil && strings.Contains(query, "INSERT INTO preview_links") {
		if err := c.hooks.beforeInsert(ctx); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c shareLinkTestConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.hooks.beforeSQL != nil {
		if err := c.hooks.beforeSQL(query); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
