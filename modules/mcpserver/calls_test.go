// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo/markdown"
)

// TestToolCallCancelledWithRequest verifies that a client going away cancels
// the tool's work on protocols before 2026-07-28, where the SDK hides the
// HTTP request's cancellation from handlers.
func TestToolCallCancelledWithRequest(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	started, stopped := make(chan struct{}), make(chan struct{})
	env.addTool(toolSpec{Name: "block", Title: "Block"}, func(_ *Module, ctx context.Context, _ *toolCall, _ SiteInfoInput) (SiteInfo, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return SiteInfo{}, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.endpoint(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block","arguments":{}}}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the HTTP request must cancel the tool's context")
	}
	eventually(t, "the cancelled call's log line", func() bool {
		return slices.Equal(env.logs.toolCallOutcomes(), []string{outcomeCancelled})
	})
	if line := env.logs.find("MCP tool call")[0]; line.Level != slog.LevelWarn {
		t.Errorf("level = %v, want Warn for a cancelled call", line.Level)
	}
}

// TestCallContextFollowsRequestContext verifies callContext bounds every call
// and re-attaches the HTTP request's cancellation that the SDK hides.
func TestCallContextFollowsRequestContext(t *testing.T) {
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	hidden := doneHidingContext{context.WithValue(context.Background(), requestContextKey{}, reqCtx)}

	ctx, cancel := callContext(hidden)
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > maxToolDuration {
		t.Errorf("deadline = %v, %v; want one within maxToolDuration", deadline, ok)
	}
	cancelReq()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelling the request context must cancel the call context")
	}
}

// doneHidingContext mimics the context go-sdk hands old-protocol handlers:
// values pass through, cancellation does not.
type doneHidingContext struct{ context.Context }

func (doneHidingContext) Done() <-chan struct{}       { return nil }
func (doneHidingContext) Err() error                  { return nil }
func (doneHidingContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// bogusOutput is a tool result that does not match the SiteInfo schema.
type bogusOutput struct {
	Bogus string `json:"bogus"`
}

// TestToolCallLoggedOnceWithRealOutcome verifies every tools/call produces
// exactly one log line with its real outcome, including calls the SDK settles
// before the tool runs (invalid arguments, unknown tool) or after it ran
// (output that fails its schema), and that such failures reach the client in
// a safe form.
func TestToolCallLoggedOnceWithRealOutcome(t *testing.T) {
	env := newTestEnv(t)
	outSchema, err := schemaFor[SiteInfo]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	inSchema, err := schemaFor[SiteInfoInput]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	mcp.AddTool(env.module.currentServer(), &mcp.Tool{Name: "bogus", InputSchema: inSchema, OutputSchema: outSchema},
		wrapTool(env.module, toolSpec{Name: "bogus"}, func(*Module, context.Context, *toolCall, SiteInfoInput) (bogusOutput, error) {
			return bogusOutput{Bogus: "server-side secret"}, nil
		}))
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	call(t, session, "list_tags", nil)

	res := call(t, session, "list_pages", map[string]any{"per_page": 500})
	if code, _, details := toolErrorBody(t, res); code != codeValidation || details["arguments"] == "" {
		t.Errorf("invalid arguments: code %q details %v, want validation_error with an arguments detail", code, details)
	}

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "no_such_tool"}); err == nil {
		t.Error("an unknown tool must be a JSON-RPC error")
	}

	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "bogus"})
	if err == nil || strings.Contains(err.Error(), "server-side secret") || strings.Contains(err.Error(), "bogus") {
		t.Errorf("invalid output: err = %v, want a scrubbed internal error", err)
	}

	want := []string{outcomeOK, outcomeInvalidArguments, outcomeRejected, outcomeInternalError}
	if got := env.logs.toolCallOutcomes(); !slices.Equal(got, want) {
		t.Fatalf("logged outcomes = %v, want %v (one line per call)", got, want)
	}
	lines := env.logs.find("MCP tool call")
	if code, _ := recordAttr(lines[2], "error_code"); code.String() != "invalid_params" {
		t.Errorf("unknown tool error_code = %q, want invalid_params", code.String())
	}
	if lines[3].Level != slog.LevelError {
		t.Errorf("invalid output logged at %v, want Error", lines[3].Level)
	}
	if cause, _ := recordAttr(lines[3], "error"); !strings.Contains(cause.String(), "validating tool output") {
		t.Errorf("invalid output cause = %q, want the SDK's validation error", cause.String())
	}
	for _, line := range lines {
		if id, ok := recordAttr(line, "api_key_id"); !ok || id.Int64() == 0 {
			t.Errorf("%v: every call line must name the API key", line.Message)
		}
	}
}

// TestGetPageMarkdownTooLarge verifies an oversized body is a validation
// error the agent can act on, not an internal failure.
func TestGetPageMarkdownTooLarge(t *testing.T) {
	env := newTestEnv(t)
	env.createPage(pageSeed{title: "Huge", slug: "huge", body: strings.Repeat("a", markdown.MaxHTMLBytes+1), status: model.PageStatusPublished})
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	res := call(t, session, "get_page", map[string]any{"slug": "huge", "body_format": bodyFormatMarkdown})
	if code, _, details := toolErrorBody(t, res); code != codeValidation || details["body_format"] == "" {
		t.Errorf("code %q details %v, want validation_error on body_format", code, details)
	}
	if res := call(t, session, "get_page", map[string]any{"slug": "huge"}); res.IsError {
		t.Errorf("the HTML body must still be available: %s", resultText(res))
	}
}

// TestSDKLoggerKeepsWarningsOnly verifies the SDK's per-request Info lines
// (a session opened and closed around every stateless request) stay out of
// the log, while its warnings still get through.
func TestSDKLoggerKeepsWarningsOnly(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	call(t, session, "list_tags", nil)
	for _, msg := range []string{"server connecting", "server session connected", "server session disconnected"} {
		if n := len(env.logs.find(msg)); n != 0 {
			t.Errorf("%q logged %d times", msg, n)
		}
	}

	logs := &logCapture{}
	logger := sdkLogger(slog.New(logs), "mcp-server").With("session", "x")
	logger.Info("noise")
	logger.Warn("worth knowing")
	if len(logs.find("noise")) != 0 || len(logs.find("worth knowing")) != 1 {
		t.Errorf("the SDK logger must drop Info and keep Warn:\n%s", logs.text())
	}
	if v, ok := recordAttr(logs.find("worth knowing")[0], "component"); !ok || v.String() != "mcp-server" {
		t.Error("SDK log lines must carry their component")
	}
}

// TestCallLogClipsClientValues verifies client-supplied strings are capped in
// the call log, so a request-sized unknown tool name cannot become a
// request-sized log line.
func TestCallLogClipsClientValues(t *testing.T) {
	if got := clip("list_pages"); got != "list_pages" {
		t.Errorf("clip changed a short value: %q", got)
	}
	long := clip(strings.Repeat("é", 200))
	if !strings.HasSuffix(long, "…") || len(long) > maxLoggedClientValue+len("…") || !utf8.ValidString(long) {
		t.Errorf("clip(long) = %d bytes, valid UTF-8 %v", len(long), utf8.ValidString(long))
	}

	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: strings.Repeat("x", 4096)}); err == nil {
		t.Fatal("an unknown tool must be a JSON-RPC error")
	}
	lines := env.logs.find("MCP tool call")
	if len(lines) != 1 {
		t.Fatalf("call log lines = %d, want 1", len(lines))
	}
	if tool, _ := recordAttr(lines[0], "tool"); len(tool.String()) > maxLoggedClientValue+len("…") {
		t.Errorf("logged tool name is %d bytes, want it clipped", len(tool.String()))
	}
}
