// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

func draftCreateArgs(title string) map[string]any {
	return map[string]any{"request_id": uuid.NewString(), "draft": map[string]any{"title": title, "summary": "Review this draft", "body_markdown": "R15 private draft paragraph with **safe markup**."}}
}

func TestTypedNilSDKResultDoesNotPanic(t *testing.T) {
	env := newTestEnv(t)
	var result *mcp.CallToolResult
	for _, err := range []error{nil, errors.New("SDK output rejected")} {
		_, _ = env.module.finishToolCall(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "create_draft_page"}}, &callRecord{}, result, err, 0)
	}
}

type lostDraftResponse struct {
	base http.RoundTripper
	drop atomic.Bool
}

func (r *lostDraftResponse) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && response.StatusCode == 200 && req.Header.Get("Mcp-Name") == "create_draft_page" && r.drop.CompareAndSwap(true, false) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("simulated lost committed response")
	}
	return response, err
}

func TestLostDraftResponseReplaysAndEscapedBodyFitsWireLimit(t *testing.T) {
	env := newTestEnv(t)
	enableDraftTools(env)
	key := env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
	transport := &lostDraftResponse{base: &bearerTransport{key: key, base: http.DefaultTransport}}
	transport.drop.Store(true)
	client := mcp.NewClient(&mcp.Implementation{Name: "lost-response-client", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: env.endpoint(), HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	args := draftCreateArgs("Lost large response")
	args["draft"].(map[string]any)["body_markdown"] = strings.Repeat(`\`, 140000)
	if _, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_draft_page", Arguments: args}); err == nil {
		t.Fatal("response was not lost")
	}
	replay := decodeResult[authoring.SaveResult](t, call(t, env.connect(key), "create_draft_page", args))
	if replay.PageID <= 0 || replay.Revision != 1 {
		t.Fatalf("lost response replay %+v", replay)
	}
	count, err := env.q.CountPages(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("duplicate or failed large write count=%d %v", count, err)
	}
	var versions int
	if err = env.db.QueryRow("SELECT COUNT(*) FROM page_versions WHERE page_id=?", replay.PageID).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("replay revisions=%d %v", versions, err)
	}
}

func TestQueuedDraftWritesRecheckCredentialAndModulePolicy(t *testing.T) {
	for _, scenario := range []string{"key revoked", "scope removed", "role removed", "module disabled", "draft toggle disabled", "link policy changed", "source restriction changed"} {
		t.Run(scenario, func(t *testing.T) {
			env := newTestEnv(t)
			enableDraftTools(env)
			raw := env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
			keys, err := env.q.GetAPIKeysByPrefix(t.Context(), model.ExtractAPIKeyPrefix(raw))
			if err != nil || len(keys) != 1 {
				t.Fatalf("key fixture %v", err)
			}
			key := keys[0]
			id := &identity{key: key, userID: env.userID, scopes: []string{model.PermissionMCPAccess, model.PermissionMCPDrafts}, sourceIP: "127.0.0.1"}
			svc, actor, err := env.module.authoringService(t.Context(), &toolCall{identity: id, settings: env.module.currentSettings()})
			if err != nil {
				t.Fatal(err)
			}
			original := svc.Authorize
			checks := 0
			svc.Authorize = func(ctx context.Context, q *store.Queries) error {
				if originalErr := original(ctx, q); originalErr != nil {
					return originalErr
				}
				checks++
				if checks != 1 {
					return nil
				}
				// Commit a simulated policy change with preflight, after its fresh
				// authorization. The actual mutation must load this state again.
				var policyErr error
				switch scenario {
				case "key revoked":
					_, policyErr = q.DBHandle().ExecContext(ctx, "UPDATE api_keys SET is_active=0 WHERE id=?", key.ID)
				case "scope removed":
					_, policyErr = q.DBHandle().ExecContext(ctx, `UPDATE api_keys SET permissions='["mcp:access"]' WHERE id=?`, key.ID)
				case "role removed":
					_, policyErr = q.DBHandle().ExecContext(ctx, "UPDATE users SET role='public' WHERE id=?", env.userID)
				case "module disabled":
					_, policyErr = q.DBHandle().ExecContext(ctx, "UPDATE modules SET is_active=0 WHERE name='mcp'")
				case "draft toggle disabled":
					_, policyErr = q.DBHandle().ExecContext(ctx, "UPDATE mcp_settings SET draft_tools_enabled=0 WHERE id=1")
				case "link policy changed":
					_, policyErr = q.DBHandle().ExecContext(ctx, "UPDATE mcp_settings SET external_links='strip' WHERE id=1")
				case "source restriction changed":
					policyErr = q.ReplaceAPIKeySourceCIDRs(ctx, key.ID, []string{"203.0.113.1/32"})
				}
				return policyErr
			}
			in := authoring.CreateInput{RequestID: uuid.NewString()}
			in.Draft.Title = "Queued policy check"
			in.Draft.Summary = "Pending"
			in.Draft.BodyMarkdown = "A safe pending body."
			_, err = svc.Create(t.Context(), actor, in)
			if err == nil {
				t.Fatal("queued mutation ignored changed policy")
			}
			if scenario == "link policy changed" {
				var domain *v2.Error
				if !errors.As(err, &domain) || domain.Code() != "write_retryable" {
					t.Fatalf("policy retry: %v", err)
				}
			}
			count, err := env.q.CountPages(t.Context())
			if err != nil || count != 0 {
				t.Fatalf("unauthorized page count=%d %v", count, err)
			}
		})
	}
}
func enableDraftTools(env *testEnv) {
	env.setSettings(Settings{DraftToolsEnabled: true, ExternalLinks: "keep", AgentShareLinks: true})
	env.setConfig(model.ConfigKeySiteURL, "https://example.test")
}

func TestDraftToolsSDKCreateReadUpdateReplayAndExplicitSharing(t *testing.T) {
	env := newTestEnv(t)
	enableDraftTools(env)
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	args := draftCreateArgs("SDK draft")
	saved := decodeResult[authoring.SaveResult](t, call(t, session, "create_draft_page", args))
	if saved.PageID <= 0 || saved.Revision != 1 || saved.PreviewURL == "" || len(saved.Issues) != 0 {
		t.Fatalf("save %+v", saved)
	}
	detail := decodeResult[authoring.DraftDetail](t, call(t, session, "get_my_draft", map[string]any{"page_id": saved.PageID}))
	if detail.Title != "SDK draft" || !strings.Contains(detail.BodyHTML, "<strong>") || detail.Verified.UserID != env.userID {
		t.Fatalf("read %+v", detail)
	}
	list := decodeResult[authoring.ListResult](t, call(t, session, "list_my_drafts", nil))
	if list.Total != 1 || len(list.Drafts) != 1 {
		t.Fatalf("list %+v", list)
	}
	updatedArgs := map[string]any{"request_id": uuid.NewString(), "page_id": saved.PageID, "base_revision": saved.Revision, "changes": map[string]any{"summary": "Updated summary"}}
	updated := decodeResult[authoring.SaveResult](t, call(t, session, "update_draft_page", updatedArgs))
	replayed := decodeResult[authoring.SaveResult](t, call(t, session, "update_draft_page", updatedArgs))
	if updated.Revision != 2 || !reflect.DeepEqual(updated, replayed) {
		t.Fatal("retry made another revision")
	}
	other := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts))
	continued := decodeResult[authoring.SaveResult](t, call(t, other, "create_draft_page", args))
	if !reflect.DeepEqual(saved, continued) {
		t.Fatal("second app lost original receipt")
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM preview_links").Scan(&count); err != nil || count != 0 {
		t.Fatal("save/read silently issued a token")
	}
	shared := decodeResult[preview.Link](t, call(t, session, "create_preview_link", map[string]any{"page_id": saved.PageID, "revision": updated.Revision}))
	sharedURL, err := url.Parse(shared.URL.Reveal())
	if err != nil || sharedURL.Path != preview.SharePath || sharedURL.RawQuery != "" || len(sharedURL.Fragment) != 43 {
		t.Fatal("MCP issued a proxy-visible bearer")
	}
	secret := sharedURL.Fragment
	snapshot, err := env.previewResolver().Resolve(t.Context(), secret)
	if err != nil || snapshot.Page.ID != saved.PageID || snapshot.Page.Revision != updated.Revision {
		t.Fatal("explicit share failed to pin the requested revision")
	}
	if strings.Contains(env.logs.text(), secret) {
		t.Fatal("share token leaked to logs")
	}
	if !strings.Contains(saved.PreviewURL, "/revisions/1") {
		t.Fatal("save preview is not revision-pinned")
	}
	if strings.Contains(env.logs.text(), "private draft paragraph") {
		t.Fatal("draft body leaked into logs")
	}
}

func TestDraftToolsDisabledScopeAndClosedLifecycleInputs(t *testing.T) {
	env := newTestEnv(t)
	env.setConfig(model.ConfigKeySiteURL, "https://example.test")
	key := env.createKey(model.PermissionMCPAccess, model.PermissionMCPDrafts)
	session := env.connect(key)
	if code, _, _ := toolErrorBody(t, call(t, session, "create_draft_page", draftCreateArgs("Disabled"))); code != "draft_tools_disabled" {
		t.Fatalf("default toggle: %s", code)
	}
	enableDraftTools(env)
	limited := env.connect(env.createKey(model.PermissionMCPAccess))
	if code, _, _ := toolErrorBody(t, call(t, limited, "create_draft_page", draftCreateArgs("Missing scope"))); code != codeForbidden {
		t.Fatalf("scope %s", code)
	}
	for _, field := range []string{"status", "scheduled_at", "published_at", "author_id", "ai_editable", "verified"} {
		args := draftCreateArgs("Forbidden")
		args["draft"].(map[string]any)[field] = "forbidden"
		result := call(t, session, "create_draft_page", args)
		if !result.IsError || result.StructuredContent == nil {
			t.Fatalf("%s accepted or lost structured error", field)
		}
	}
	invalid := draftCreateArgs(" ")
	res := call(t, session, "create_draft_page", invalid)
	if !res.IsError || res.StructuredContent == nil {
		t.Fatal("normalizer error lost structured feedback")
	}
	count, err := env.q.CountPages(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("rejected write persisted %d %v", count, err)
	}
}
