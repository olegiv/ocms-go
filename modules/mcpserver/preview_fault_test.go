// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestPreviewIssuanceFaultsAreRetryable(t *testing.T) {
	for _, tc := range []struct {
		name, diagnostic string
		busy, immediate  bool
	}{
		{"preflight policy", "failed to check MCP preview sharing policy", false, false},
		{"writer reservation", "failed to reserve MCP preview issuance writer", false, false},
		{"transactional policy", "failed to check MCP preview sharing policy", false, false},
		{"deferred writer contention", "failed to reserve MCP preview issuance writer", true, false},
		{"immediate transaction contention", "preview link issuance is busy", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, id := draftKeyFixture(t)
			ctx := t.Context()
			call := &toolCall{identity: id, settings: env.module.currentSettings()}
			draftResult, err := env.module.createDraftPage(ctx, call, authoring.CreateInput{
				RequestID: uuid.NewString(),
				Draft:     draft.PageDraft{Title: "Share draft", Summary: "Review", BodyMarkdown: "Safe content."},
			})
			if err != nil {
				t.Fatal(err)
			}
			in := authoring.PreviewInput{PageID: draftResult.PageID, Revision: draftResult.Revision}
			fault := errors.New("injected preview storage failure")
			var reads int
			var reserved bool
			inject := true
			mode := "deferred"
			if tc.immediate {
				mode = "immediate"
			}
			alternate := openDraftFaultDB(t, env, mode, func(query string) error {
				if !inject {
					return nil
				}
				if strings.Contains(query, "FROM mcp_settings WHERE") {
					reads++
					// Initial draft authorization performs the first read.
					if tc.name == "preflight policy" && reads == 2 || tc.name == "transactional policy" && reserved {
						return fault
					}
				}
				if strings.Contains(query, "UPDATE mcp_settings SET agent_share_links = agent_share_links WHERE 0") {
					reserved = true
					if tc.name == "writer reservation" {
						return fault
					}
				}
				return nil
			})
			env.module.ctx.DB = alternate
			env.module.svc.queries = store.New(alternate)
			previous := slog.Default()
			slog.SetDefault(slog.New(env.logs))
			t.Cleanup(func() { slog.SetDefault(previous) })

			if tc.busy {
				tx, beginErr := env.db.BeginTx(ctx, nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				t.Cleanup(func() {
					if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
						t.Error(rollbackErr)
					}
				})
				// Take the real SQLite writer before the issuance request.
				if _, writeErr := tx.ExecContext(ctx, "UPDATE mcp_settings SET agent_share_links=agent_share_links WHERE id=1"); writeErr != nil {
					t.Fatal(writeErr)
				}
				link, issueErr := env.module.createPreviewLink(ctx, call, in)
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Fatal(rollbackErr)
				}
				if !store.IsSQLiteBusy(issueErr) || reserved == tc.immediate || link.URL.Reveal() != "" {
					t.Fatalf("did not reach the intended SQLite lock boundary: reservation=%t, error=%v", reserved, issueErr)
				}
				requireDraftRetryCode(t, issueErr)
			} else {
				link, issueErr := env.module.createPreviewLink(ctx, call, in)
				if !errors.Is(issueErr, fault) || link.URL.Reveal() != "" {
					t.Fatalf("did not reach %s: error=%v", tc.name, issueErr)
				}
				requireDraftRetryCode(t, issueErr)
			}
			diagnostics := env.logs.find(tc.diagnostic)
			if len(diagnostics) != 1 {
				t.Fatalf("want one fault diagnostic for %s, got %d: %s", tc.name, len(diagnostics), env.logs.text())
			}
			cause, _ := recordAttr(diagnostics[0], "error")
			pageID, _ := recordAttr(diagnostics[0], "page_id")
			userID, _ := recordAttr(diagnostics[0], "user_id")
			faultMessage := fault.Error()
			if tc.busy {
				faultMessage = "locked"
			}
			if diagnostics[0].Level != slog.LevelError || !strings.Contains(cause.String(), faultMessage) ||
				pageID.Int64() != in.PageID || userID.Int64() != env.userID {
				t.Fatalf("issuance diagnostic omitted the cause or IDs: %v", diagnostics[0])
			}
			assertPreviewIssuanceRows(t, env, 0)

			inject = false
			link, err := env.module.createPreviewLink(ctx, call, in)
			if err != nil || link.URL.Reveal() == "" || link.PageID != in.PageID || link.Revision != in.Revision {
				t.Fatalf("same issuance after recovery failed: %v", err)
			}
			assertPreviewIssuanceRows(t, env, 1)
			settings := call.settings
			settings.AgentShareLinks = false
			if err = saveSettings(ctx, env.db, settings); err != nil {
				t.Fatal(err)
			}
			link, err = env.module.createPreviewLink(ctx, call, in)
			refusal, _ := toToolError(err)
			if link.URL.Reveal() != "" || refusal == nil || refusal.Code != "share_links_disabled" || refusal.Retryable {
				t.Fatalf("sharing-disabled refusal = %+v", refusal)
			}
			if len(env.logs.find(tc.diagnostic)) != 1 {
				t.Fatal("recovery or a definitive refusal logged another fault")
			}
			assertPreviewIssuanceRows(t, env, 1)
		})
	}
}

func openDraftFaultDB(t *testing.T, env *testEnv, mode string, beforeSQL func(string) error) *sql.DB {
	t.Helper()
	var path string
	if err := env.db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqlite.NewConnector(path + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(0)&_time_format=sqlite&_timezone=UTC&_txlock=" + mode)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(shareLinkTestConnector{Connector: connector, beforeSQL: beforeSQL})
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return db
}

func assertPreviewIssuanceRows(t *testing.T, env *testEnv, want int) {
	t.Helper()
	var links, events int
	err := env.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM preview_links),
		(SELECT count(*) FROM events WHERE message='Preview share link created')`).Scan(&links, &events)
	if err != nil || links != want || events != want {
		t.Fatalf("preview links/quota rows=%d, audit events=%d, want %d each: %v", links, events, want, err)
	}
}

func requireDraftRetryCode(t *testing.T, err error) {
	t.Helper()
	mapped, internal := toToolError(err)
	if mapped == nil || internal || mapped.Code != "write_retryable" || !mapped.Retryable || mapped.RetryAfterSeconds != 1 {
		t.Fatalf("storage fault answered %+v, want write_retryable with a delay: %v", mapped, err)
	}
	if strings.Contains(mapped.Message, "injected") || strings.Contains(mapped.Message, "no such table") {
		t.Fatal("retryable response exposed the storage cause")
	}
}
