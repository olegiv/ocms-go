// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"

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

			session := env.connect(token)
			saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Sharing policy changed")))
			args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
			shared := decodeResult[preview.Link](t, call(t, session, "create_preview_link", args))
			shareURL, err := url.Parse(shared.URL)
			if err != nil || shareURL.Fragment == "" {
				t.Fatal("enabled call did not issue a share link")
			}
			disableBeforeIssue.Store(true)
			refused := call(t, session, "create_preview_link", args)
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
			snapshot, err := (preview.Service{Q: env.q}).Resolve(ctx, shareURL.Fragment)
			if err != nil || snapshot.Page.ID != saved.PageID || snapshot.Page.Revision != saved.Revision {
				t.Fatal("previously issued link failed to resolve")
			}
			decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Draft with sharing off")))
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
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("Deferred share issuance")))
	args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
	shared := decodeResult[preview.Link](t, call(t, session, "create_preview_link", args))
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
	if code, _, _ := toolErrorBody(t, call(t, session, "create_preview_link", args)); code != "share_links_disabled" {
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
}

func (c shareLinkTestConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return shareLinkTestConn{Conn: conn, beforeWrite: c.beforeWrite, beforeInsert: c.beforeInsert}, nil
}

type shareLinkTestConn struct {
	driver.Conn
	beforeWrite  func(context.Context) error
	beforeInsert func(context.Context) error
}

func (c shareLinkTestConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !opts.ReadOnly && c.beforeWrite != nil {
		if err := c.beforeWrite(ctx); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c shareLinkTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.beforeInsert != nil && strings.Contains(query, "INSERT INTO preview_links") {
		if err := c.beforeInsert(ctx); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
