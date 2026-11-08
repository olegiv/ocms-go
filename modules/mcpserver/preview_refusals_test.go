// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestMCPPreviewRefusalsExplainRecovery(t *testing.T) {
	for _, credential := range []string{"api_key", "oauth"} {
		for _, scenario := range []string{"invalid URL", "unset URL", "active", "hourly", "unavailable"} {
			t.Run(credential+"/"+scenario, func(t *testing.T) {
				env := newTestEnv(t)
				var bearer string
				if credential == "oauth" {
					bearer, _ = oauthToken(t, enableOAuth(t, env, true), env.userID, "mcp:access mcp:drafts")
				} else {
					bearer = env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
				}
				enableDraftTools(env)
				session := env.connect(bearer)
				saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", draftCreateArgs("SF13_PRIVATE_TITLE")))
				args := map[string]any{"page_id": saved.PageID, "revision": saved.Revision}
				user, err := env.q.GetUserByID(t.Context(), env.userID)
				if err != nil {
					t.Fatal(err)
				}
				now, count, wantCode := time.Now().UTC(), 0, "preview_limit_reached"
				switch scenario {
				case "invalid URL":
					env.setConfig(model.ConfigKeySiteURL, "http://SF13_PRIVATE_HOST.test")
					wantCode = "site_url_required"
				case "unset URL":
					env.setConfig(model.ConfigKeySiteURL, "")
					wantCode = "site_url_required"
				case "active":
					count = preview.MaxActiveLinks
				case "hourly":
					count, wantCode = preview.MaxIssuedPerHour, "preview_rate_limited"
				case "unavailable":
					args["revision"] = 999
					wantCode = "preview_unavailable"
				}
				for i := range count {
					if _, err = env.q.CreatePreviewLink(t.Context(), store.CreatePreviewLinkParams{TokenHash: fmt.Sprintf("%064d", i), PageID: saved.PageID, Revision: saved.Revision, CreatedBy: user.ID, SessionVersion: user.SessionVersion, CreatedAt: now.Add(-20 * time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix()}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "hourly" {
					if _, err = env.db.Exec("UPDATE preview_links SET revoked_at=1"); err != nil {
						t.Fatal(err)
					}
				}
				result := call(t, session, "create_preview_link", args)
				structured, err := json.Marshal(result.StructuredContent)
				if err != nil {
					t.Fatal(err)
				}
				for _, encoded := range []string{resultText(result), string(structured)} {
					var envelope struct {
						Error             toolError `json:"error"`
						Retryable         *bool     `json:"retryable"`
						RetryAfterSeconds int64     `json:"retry_after_seconds"`
					}
					if err = json.Unmarshal([]byte(encoded), &envelope); err != nil {
						t.Fatal(err)
					}
					if !result.IsError || envelope.Error.Code != wantCode || envelope.Retryable == nil || *envelope.Retryable != (scenario == "hourly") {
						t.Errorf("wrong refusal envelope: %s", encoded)
					}
					if scenario == "hourly" {
						if envelope.RetryAfterSeconds < 2395 || envelope.RetryAfterSeconds > 2401 || !strings.Contains(envelope.Error.Message, "Revoking links does not reset") {
							t.Errorf("hourly limit lacks useful retry guidance: %s", encoded)
						}
					} else if envelope.RetryAfterSeconds != 0 {
						t.Error("non-hourly refusal advertised a delay")
					}
					if scenario == "active" && !strings.Contains(envelope.Error.Message, "Ask the editor") {
						t.Error("agent was told to revoke a link itself")
					}
					if wantCode == "site_url_required" && !strings.Contains(envelope.Error.Message, "HTTPS") {
						t.Error("configuration refusal does not explain the URL requirement")
					}
					if strings.Contains(encoded, "SF13_PRIVATE_") || strings.Contains(encoded, "issue preview for page") {
						t.Error("refusal leaked private content/configuration or internal context")
					}
				}
				faults := 0
				for _, record := range env.logs.find("MCP tool call") {
					if record.Level != slog.LevelError {
						continue
					}
					faults++
					cause, _ := recordAttr(record, "error")
					actor, _ := recordAttr(record, "user_id")
					if !strings.Contains(cause.String(), fmt.Sprintf("page %d revision %d", saved.PageID, saved.Revision)) || !strings.Contains(cause.String(), "preview_site_url_required") || actor.Int64() != env.userID {
						t.Errorf("configuration fault lacks cause/identity: %s", env.logs.text())
					}
				}
				wantFaults := 0
				if wantCode == "site_url_required" {
					wantFaults = 1
				}
				if faults != wantFaults || strings.Contains(env.logs.text(), "SF13_PRIVATE_") || strings.Contains(env.logs.text(), bearer) {
					t.Error("configuration diagnostic count/privacy changed")
				}
				var links, events int
				if err = env.db.QueryRow("SELECT (SELECT COUNT(*) FROM preview_links), (SELECT COUNT(*) FROM events WHERE message='Preview share link created')").Scan(&links, &events); err != nil || links != count || events != 0 {
					t.Fatalf("refusal created links/events: %d/%d %v", links, events, err)
				}
				env.setConfig(model.ConfigKeySiteURL, "https://example.test")
				args["revision"] = saved.Revision
				if scenario == "active" || scenario == "hourly" {
					if _, err = env.db.Exec("UPDATE preview_links SET revoked_at=1, created_at=?", now.Add(-2*time.Hour).Unix()); err != nil {
						t.Fatal(err)
					}
				}
				shared := decodeResult[preview.Link](t, call(t, session, "create_preview_link", args))
				parsed, parseErr := url.Parse(shared.URL.Reveal())
				if parseErr != nil || parsed.Fragment == "" {
					t.Fatal("recovery did not issue a share URL")
				}
				if _, err = (preview.Service{Q: env.q}).Resolve(t.Context(), parsed.Fragment); err != nil {
					t.Fatal("recovered share does not resolve", err)
				}
				if err = env.db.QueryRow("SELECT (SELECT COUNT(*) FROM preview_links), (SELECT COUNT(*) FROM events WHERE message='Preview share link created')").Scan(&links, &events); err != nil || links != count+1 || events != 1 {
					t.Fatalf("recovery did not create one link/event: %d/%d %v", links, events, err)
				}
				testutil.AssertPageRevision(t, env.q, saved.PageID, saved.Revision)
			})
		}
	}
}
