// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// logCapture is a slog.Handler recording every record for assertions.
type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *logCapture) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &boundHandler{capture: c, attrs: attrs}
}

func (c *logCapture) WithGroup(string) slog.Handler { return c }

// boundHandler forwards to the capture with pre-bound attributes.
type boundHandler struct {
	capture *logCapture
	attrs   []slog.Attr
}

func (b *boundHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return b.capture.Enabled(ctx, l)
}

func (b *boundHandler) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
	r.AddAttrs(b.attrs...)
	return b.capture.Handle(ctx, r)
}

func (b *boundHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &boundHandler{capture: b.capture, attrs: append(append([]slog.Attr{}, b.attrs...), attrs...)}
}

func (b *boundHandler) WithGroup(string) slog.Handler { return b }

// find returns the records with the given message.
func (c *logCapture) find(msg string) []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []slog.Record
	for _, r := range c.records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

// text renders every record (message and attributes) for substring checks.
func (c *logCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, r := range c.records {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteByte('\n')
	}
	return b.String()
}

// recordAttr returns the value of an attribute on a record.
func recordAttr(r slog.Record, key string) (slog.Value, bool) {
	var (
		val   slog.Value
		found bool
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val, found = a.Value, true
			return false
		}
		return true
	})
	return val, found
}

// testEnv is an initialized module served over HTTP against a migrated DB.
type testEnv struct {
	t      *testing.T
	db     *sql.DB
	q      *store.Queries
	module *Module
	logs   *logCapture
	server *httptest.Server
	userID int64
}

// newTestEnv builds an initialized module behind a test HTTP server.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	if _, err := store.New(db).UpsertModule(context.Background(), store.UpsertModuleParams{
		Name: ModuleName, IsActive: true,
	}); err != nil {
		t.Fatalf("UpsertModule: %v", err)
	}

	logs := &logCapture{}
	ctx := &module.Context{
		DB:     db,
		Store:  store.New(db),
		Logger: slog.New(logs),
		Config: &config.Config{UploadsDir: t.TempDir()},
		Render: &render.Renderer{},
		Events: service.NewEventService(db),
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	r := chi.NewRouter()
	m.RegisterRoutes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	env := &testEnv{t: t, db: db, q: store.New(db), module: m, logs: logs, server: srv}
	env.userID = env.createUser("author@example.com", "Ada Author")
	return env
}

// endpoint returns the absolute URL of the MCP endpoint.
func (e *testEnv) endpoint() string { return e.server.URL + EndpointPath }

// createUser inserts a user and returns its id.
func (e *testEnv) createUser(email, name string) int64 {
	e.t.Helper()
	now := time.Now()
	u, err := e.q.CreateUser(context.Background(), store.CreateUserParams{
		Email: email, PasswordHash: "x", Role: "admin", Name: name, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		e.t.Fatalf("CreateUser: %v", err)
	}
	return u.ID
}

// createKey inserts an active API key with the given permissions and returns
// the raw key.
func (e *testEnv) createKey(perms ...string) string {
	e.t.Helper()
	return e.createKeyWith(func(*store.CreateAPIKeyParams) {}, perms...)
}

// createKeyWith inserts an API key after letting mutate adjust its row.
func (e *testEnv) createKeyWith(mutate func(*store.CreateAPIKeyParams), perms ...string) string {
	e.t.Helper()
	raw, prefix, err := model.GenerateAPIKey()
	if err != nil {
		e.t.Fatalf("GenerateAPIKey: %v", err)
	}
	hash, err := model.HashAPIKey(raw)
	if err != nil {
		e.t.Fatalf("HashAPIKey: %v", err)
	}
	now := time.Now()
	params := store.CreateAPIKeyParams{
		Name:        "Agent key",
		KeyHash:     hash,
		KeyPrefix:   prefix,
		Permissions: model.PermissionsToJSON(perms),
		IsActive:    true,
		CreatedBy:   e.userID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	mutate(&params)
	if _, err := e.q.CreateAPIKey(context.Background(), params); err != nil {
		e.t.Fatalf("CreateAPIKey: %v", err)
	}
	return raw
}

// setConfig upserts a site config value.
func (e *testEnv) setConfig(key, value string) {
	e.t.Helper()
	if _, err := e.q.UpsertConfig(context.Background(), store.UpsertConfigParams{
		Key: key, Value: value, Type: "string", LanguageCode: "en", UpdatedAt: time.Now(),
	}); err != nil {
		e.t.Fatalf("UpsertConfig(%s): %v", key, err)
	}
}

// pageSeed describes a page to insert.
type pageSeed struct {
	title, slug, body, status, lang string
}

// createPage inserts a page and returns its id.
func (e *testEnv) createPage(p pageSeed) int64 {
	e.t.Helper()
	if p.lang == "" {
		p.lang = "en"
	}
	now := time.Now()
	params := store.CreatePageParams{
		Title: p.title, Slug: p.slug, Body: p.body, Summary: p.title + " summary",
		Status: p.status, AuthorID: e.userID, LanguageCode: p.lang, PageType: "post",
		CreatedAt: now, UpdatedAt: now,
	}
	if p.status == model.PageStatusPublished {
		params.PublishedAt = sql.NullTime{Time: now, Valid: true}
	}
	page, err := e.q.CreatePage(context.Background(), params)
	if err != nil {
		e.t.Fatalf("CreatePage(%s): %v", p.slug, err)
	}
	return page.ID
}

// setSettings saves settings and rebuilds the server, as the admin page does.
func (e *testEnv) setSettings(s Settings) {
	e.t.Helper()
	srv, err := e.module.buildServer(s)
	if err != nil {
		e.t.Fatalf("buildServer: %v", err)
	}
	if err := saveSettings(context.Background(), e.db, s); err != nil {
		e.t.Fatalf("saveSettings: %v", err)
	}
	e.module.state.Store(&serverState{settings: s, server: srv})
}

// bearerTransport adds the Authorization header to every request.
type bearerTransport struct {
	key  string
	base http.RoundTripper
}

func (b *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return b.base.RoundTrip(r)
}

// connect opens an MCP client session with the given API key, negotiating
// the newest protocol version the SDK supports.
func (e *testEnv) connect(key string) *mcp.ClientSession {
	e.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "ocms-test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             e.endpoint(),
		HTTPClient:           &http.Client{Transport: &bearerTransport{key: key, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		e.t.Fatalf("Connect: %v", err)
	}
	e.t.Cleanup(func() { _ = session.Close() })
	return session
}

// call invokes a tool and fails the test on a protocol-level error.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// decodeResult decodes a successful tool result's structured content.
func decodeResult[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	if res.IsError {
		t.Fatalf("tool returned an error: %s", resultText(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode structured content: %v\n%s", err, raw)
	}
	return out
}

// resultText concatenates a result's text content.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// toolErrorBody decodes the REST-shaped error envelope from an error result.
func toolErrorBody(t *testing.T, res *mcp.CallToolResult) (code, message string, details map[string]string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("expected a tool error, got success: %s", resultText(res))
	}
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &body); err != nil {
		t.Fatalf("tool error is not the REST envelope: %v\n%s", err, resultText(res))
	}
	return body.Error.Code, body.Error.Message, body.Error.Details
}

// rawPost sends a raw JSON-RPC body to the endpoint.
func (e *testEnv) rawPost(body string, headers map[string]string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.endpoint(), bytes.NewBufferString(body))
	if err != nil {
		e.t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST: %v", err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// readBody reads and returns a response body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// addTool registers an extra tool on the running server, wrapped like every
// catalog tool, for tests that need a tool with controlled behaviour.
func (e *testEnv) addTool(spec toolSpec, fn toolFunc[SiteInfoInput, SiteInfo]) {
	e.t.Helper()
	registerTool(fn)(e.module, e.module.currentServer(), spec)
}

// toolCallOutcomes returns the outcome of every tools/call log line, in order.
func (c *logCapture) toolCallOutcomes() []string {
	var out []string
	for _, r := range c.find("MCP tool call") {
		v, _ := recordAttr(r, "outcome")
		out = append(out, v.String())
	}
	return out
}

// eventually polls cond until it holds or two seconds pass.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
