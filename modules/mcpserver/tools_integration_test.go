// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestToolsListMatchesCatalog verifies the server lists exactly the catalog
// tools, each annotated read-only with object input and output schemas.
func TestToolsListMatchesCatalog(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var listed []string
	for _, tool := range res.Tools {
		listed = append(listed, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: missing readOnlyHint", tool.Name)
		}
		if tool.Annotations != nil && (tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint) {
			t.Errorf("%s: openWorldHint must be false", tool.Name)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s: missing title or description", tool.Name)
		}
		for name, schema := range map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema} {
			m, ok := schema.(map[string]any)
			if !ok || m["type"] != "object" {
				t.Errorf("%s: %s schema is not an object schema: %v", tool.Name, name, schema)
			}
		}
	}
	var want []string
	for _, spec := range toolCatalog() {
		want = append(want, spec.Name)
	}
	slices.Sort(listed)
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("tools/list = %v, want catalog %v", listed, want)
	}
	if res.CacheScope != cacheScopePrivate {
		t.Errorf("tools/list cacheScope = %q, want %q", res.CacheScope, cacheScopePrivate)
	}
}

// TestGetSiteInfo verifies the bootstrap tool reports site, languages and
// the key's access.
func TestGetSiteInfo(t *testing.T) {
	env := newTestEnv(t)
	env.setConfig(model.ConfigKeySiteName, "Test Site")
	env.setConfig(model.ConfigKeySiteURL, "https://example.com/")
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionPagesRead))

	info := decodeResult[SiteInfo](t, call(t, session, "get_site_info", nil))
	if info.Site.Name != "Test Site" {
		t.Errorf("site name = %q", info.Site.Name)
	}
	if info.Site.URL != "https://example.com" {
		t.Errorf("site url = %q, want trailing slash trimmed", info.Site.URL)
	}
	if info.Site.DefaultLanguage != "en" || len(info.Languages) == 0 || info.Languages[0].Code != "en" {
		t.Errorf("languages = %+v, default = %q", info.Languages, info.Site.DefaultLanguage)
	}
	if !slices.Contains(info.Access.Permissions, model.PermissionPagesRead) {
		t.Errorf("permissions = %v", info.Access.Permissions)
	}
	if info.Access.DraftsVisible {
		t.Error("drafts must not be visible while the site policy hides them")
	}
	if !info.Server.ReadOnly || info.Server.MaxPerPage != maxPerPage {
		t.Errorf("server = %+v", info.Server)
	}
}

// TestPublishedOnlyByDefault verifies drafts stay hidden from list, get and
// search unless the site policy exposes them, even for a pages:read key.
func TestPublishedOnlyByDefault(t *testing.T) {
	env := newTestEnv(t)
	published := env.createPage(pageSeed{title: "Gardening tips", slug: "gardening-tips", body: "<p>Grow tomatoes</p>", status: model.PageStatusPublished})
	draft := env.createPage(pageSeed{title: "Secret plans", slug: "secret-plans", body: "<p>Grow tomatoes secretly</p>", status: model.PageStatusDraft})
	session := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionPagesRead))

	list := decodeResult[ListPagesResult](t, call(t, session, "list_pages", nil))
	if list.Total != 1 || len(list.Pages) != 1 || list.Pages[0].ID != published {
		t.Fatalf("list_pages = %+v, want only the published page", list)
	}

	code, _, _ := toolErrorBody(t, call(t, session, "get_page", map[string]any{"id": draft}))
	if code != "not_found" {
		t.Errorf("get_page(draft) code = %q, want not_found", code)
	}

	code, _, _ = toolErrorBody(t, call(t, session, "list_pages", map[string]any{"status": "draft"}))
	if code != codeForbidden {
		t.Errorf("list_pages(status=draft) code = %q, want %q", code, codeForbidden)
	}

	search := decodeResult[SearchPagesResult](t, call(t, session, "search_pages", map[string]any{"query": "tomatoes"}))
	if search.Total != 1 || len(search.Results) != 1 || search.Results[0].ID != published {
		t.Errorf("search_pages = %+v, want only the published page", search)
	}
}

// TestDraftsVisibleOnlyWithPolicyAndScope verifies drafts require both the
// site policy and the pages:read permission.
func TestDraftsVisibleOnlyWithPolicyAndScope(t *testing.T) {
	env := newTestEnv(t)
	env.createPage(pageSeed{title: "Live", slug: "live", body: "<p>alpha</p>", status: model.PageStatusPublished})
	draft := env.createPage(pageSeed{title: "Draft", slug: "draft", body: "<p>alpha draft</p>", status: model.PageStatusDraft})
	env.setSettings(Settings{AllowDrafts: true})

	withScope := env.connect(env.createKey(model.PermissionMCPAccess, model.PermissionPagesRead))
	list := decodeResult[ListPagesResult](t, call(t, withScope, "list_pages", map[string]any{"status": "draft"}))
	if len(list.Pages) != 1 || list.Pages[0].ID != draft {
		t.Errorf("list_pages(status=draft) = %+v, want the draft", list.Pages)
	}
	page := decodeResult[PageDetail](t, call(t, withScope, "get_page", map[string]any{"id": draft}))
	if page.Status != model.PageStatusDraft || page.URL != "" {
		t.Errorf("draft page = status %q url %q; drafts have no public URL", page.Status, page.URL)
	}
	search := decodeResult[SearchPagesResult](t, call(t, withScope, "search_pages", map[string]any{"query": "draft"}))
	if len(search.Results) == 0 {
		t.Error("search_pages must find drafts when they are visible")
	}
	info := decodeResult[SiteInfo](t, call(t, withScope, "get_site_info", nil))
	if !info.Access.DraftsVisible {
		t.Error("get_site_info must report drafts as visible")
	}

	withoutScope := env.connect(env.createKey(model.PermissionMCPAccess))
	code, _, _ := toolErrorBody(t, call(t, withoutScope, "get_page", map[string]any{"id": draft}))
	if code != "not_found" {
		t.Errorf("get_page(draft) without pages:read code = %q, want not_found", code)
	}
}

// TestGetPage covers lookup by id and slug, markdown output, the public URL,
// language prefixes and the absence of author email addresses.
func TestGetPage(t *testing.T) {
	env := newTestEnv(t)
	env.setConfig(model.ConfigKeySiteURL, "https://example.com")
	if _, err := env.q.CreateLanguage(context.Background(), store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", Position: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	id := env.createPage(pageSeed{title: "Hello", slug: "hello", body: "<h2>Heading</h2><p>Some <strong>bold</strong> text</p>", status: model.PageStatusPublished})
	ruID := env.createPage(pageSeed{title: "Привет", slug: "privet", body: "<p>Текст</p>", status: model.PageStatusPublished, lang: "ru"})
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	byID := decodeResult[PageDetail](t, call(t, session, "get_page", map[string]any{"id": id}))
	if byID.BodyFormat != bodyFormatHTML || !strings.Contains(byID.Body, "<strong>bold</strong>") {
		t.Errorf("default body = %q (%s), want stored HTML", byID.Body, byID.BodyFormat)
	}
	if byID.URL != "https://example.com/hello" {
		t.Errorf("url = %q", byID.URL)
	}
	if byID.Author == nil || byID.Author.Name != "Ada Author" {
		t.Errorf("author = %+v", byID.Author)
	}

	md := call(t, session, "get_page", map[string]any{"slug": "hello", "body_format": "markdown"})
	detail := decodeResult[PageDetail](t, md)
	if detail.BodyFormat != bodyFormatMarkdown || !strings.Contains(detail.Body, "## Heading") || !strings.Contains(detail.Body, "**bold**") {
		t.Errorf("markdown body = %q", detail.Body)
	}
	raw, err := json.Marshal(md.StructuredContent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "author@example.com") || strings.Contains(string(raw), `"email"`) {
		t.Errorf("author email leaked into tool output: %s", raw)
	}

	ru := decodeResult[PageDetail](t, call(t, session, "get_page", map[string]any{"id": ruID}))
	if ru.URL != "https://example.com/ru/privet" {
		t.Errorf("non-default language url = %q, want the /ru prefix", ru.URL)
	}
}

// TestGetPageArgumentValidation verifies schema and handler validation
// surface as tool errors an agent can correct.
func TestGetPageArgumentValidation(t *testing.T) {
	env := newTestEnv(t)
	id := env.createPage(pageSeed{title: "Hello", slug: "hello", body: "<p>x</p>", status: model.PageStatusPublished})
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	code, _, details := toolErrorBody(t, call(t, session, "get_page", map[string]any{}))
	if code != codeValidation || details["id"] == "" {
		t.Errorf("no id or slug: code %q details %v", code, details)
	}
	code, _, _ = toolErrorBody(t, call(t, session, "get_page", map[string]any{"id": id, "slug": "hello"}))
	if code != codeValidation {
		t.Errorf("both id and slug: code %q", code)
	}

	// Schema violations are rejected by the SDK before the handler runs.
	for name, args := range map[string]map[string]any{
		"minimum":              {"id": 0},
		"enum":                 {"id": id, "body_format": "pdf"},
		"additionalProperties": {"id": id, "unexpected": true},
	} {
		res := call(t, session, "get_page", args)
		if !res.IsError || !strings.Contains(resultText(res), "validating") {
			t.Errorf("%s: want a schema validation error, got %q", name, resultText(res))
		}
	}
}

// TestMediaTools verifies list_media and get_media over the v2 service.
func TestMediaTools(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now()
	item, err := env.q.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid: "123e4567-e89b-42d3-a456-426614174000", Filename: "photo.jpg", MimeType: "image/jpeg", Size: 2048,
		Width: sql.NullInt64{Int64: 800, Valid: true}, Height: sql.NullInt64{Int64: 600, Valid: true},
		Alt: sql.NullString{String: "A photo", Valid: true}, UploadedBy: env.userID, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMedia: %v", err)
	}
	if _, err := env.q.CreateMediaVariant(context.Background(), store.CreateMediaVariantParams{
		MediaID: item.ID, Type: "thumbnail", Width: 150, Height: 150, Size: 512, CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateMediaVariant: %v", err)
	}
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	list := decodeResult[ListMediaResult](t, call(t, session, "list_media", map[string]any{"type": "image"}))
	if list.Total != 1 || len(list.Media) != 1 || list.Media[0].Filename != "photo.jpg" {
		t.Fatalf("list_media = %+v", list)
	}
	if list.Media[0].URLs == nil || !strings.HasPrefix(list.Media[0].URLs.Original, "/uploads/") {
		t.Errorf("urls = %+v", list.Media[0].URLs)
	}

	got := decodeResult[mediaResult](t, call(t, session, "get_media", map[string]any{"id": item.ID}))
	if got.ID != item.ID || len(got.Variants) != 1 || got.Variants[0].Type != "thumbnail" {
		t.Errorf("get_media = %+v", got)
	}

	code, _, _ := toolErrorBody(t, call(t, session, "get_media", map[string]any{"id": item.ID + 100}))
	if code != "not_found" {
		t.Errorf("missing media code = %q", code)
	}
}

// mediaResult decodes the fields of get_media the tests check.
type mediaResult struct {
	ID       int64 `json:"id"`
	Variants []struct {
		Type string `json:"type"`
	} `json:"variants"`
}

// TestTaxonomyTools verifies the tag and category tools, including the
// recursive category tree.
func TestTaxonomyTools(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	now := time.Now()
	tag, err := env.q.CreateTag(ctx, store.CreateTagParams{Name: "Go", Slug: "go", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	parent, err := env.q.CreateCategory(ctx, store.CreateCategoryParams{Name: "Tech", Slug: "tech", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	child, err := env.q.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Languages", Slug: "languages", LanguageCode: "en",
		ParentID: sql.NullInt64{Int64: parent.ID, Valid: true}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory(child): %v", err)
	}
	pageID := env.createPage(pageSeed{title: "Go post", slug: "go-post", body: "<p>x</p>", status: model.PageStatusPublished})
	if err := env.q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: pageID, TagID: tag.ID}); err != nil {
		t.Fatalf("AddTagToPage: %v", err)
	}
	if err := env.q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: pageID, CategoryID: child.ID}); err != nil {
		t.Fatalf("AddCategoryToPage: %v", err)
	}
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	tags := decodeResult[ListTagsResult](t, call(t, session, "list_tags", nil))
	if tags.Total != 1 || tags.Tags[0].PageCount != 1 || tags.PerPage != defaultTagsPerPage {
		t.Errorf("list_tags = %+v", tags)
	}
	gotTag := decodeResult[tagResult](t, call(t, session, "get_tag", map[string]any{"id": tag.ID}))
	if gotTag.Slug != "go" {
		t.Errorf("get_tag = %+v", gotTag)
	}

	tree := decodeResult[ListCategoriesResult](t, call(t, session, "list_categories", nil))
	if len(tree.Categories) != 1 || len(tree.Categories[0].Children) != 1 || tree.Categories[0].Children[0].ID != child.ID {
		t.Errorf("list_categories tree = %+v", tree.Categories)
	}
	flat := decodeResult[ListCategoriesResult](t, call(t, session, "list_categories", map[string]any{"flat": true}))
	if len(flat.Categories) != 2 {
		t.Errorf("list_categories flat = %d categories, want 2", len(flat.Categories))
	}
	gotParent := decodeResult[categoryResult](t, call(t, session, "get_category", map[string]any{"id": parent.ID}))
	if len(gotParent.Children) != 1 {
		t.Errorf("get_category children = %+v", gotParent.Children)
	}

	withTaxonomy := decodeResult[ListPagesResult](t, call(t, session, "list_pages", map[string]any{"include_taxonomy": true, "tag_id": tag.ID}))
	if len(withTaxonomy.Pages) != 1 || len(withTaxonomy.Pages[0].Tags) != 1 || len(withTaxonomy.Pages[0].Categories) != 1 {
		t.Errorf("list_pages with taxonomy = %+v", withTaxonomy.Pages)
	}
}

// tagResult decodes the fields of get_tag the tests check.
type tagResult struct {
	Slug string `json:"slug"`
}

// categoryResult decodes the fields of get_category the tests check.
type categoryResult struct {
	Children []struct {
		ID int64 `json:"id"`
	} `json:"children"`
}

// TestToolCallLogging verifies one structured log line per call, without
// API key material or arguments.
func TestToolCallLogging(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	session := env.connect(key)

	call(t, session, "get_page", map[string]any{"slug": "missing-page-slug"})

	records := env.logs.find("MCP tool call")
	if len(records) != 1 {
		t.Fatalf("got %d tool call log lines, want 1", len(records))
	}
	for attr, want := range map[string]string{"tool": "get_page", "outcome": outcomeToolError, "error_code": "not_found", "client_name": "ocms-test-client"} {
		if v, ok := recordAttr(records[0], attr); !ok || v.String() != want {
			t.Errorf("log attr %s = %q, want %q", attr, v.String(), want)
		}
	}
	if _, ok := recordAttr(records[0], "api_key_prefix"); !ok {
		t.Error("log line must identify the API key by prefix")
	}
	logs := env.logs.text()
	if strings.Contains(logs, key) {
		t.Error("raw API key leaked into logs")
	}
	if strings.Contains(logs, "missing-page-slug") {
		t.Error("tool arguments leaked into logs")
	}
}

// TestInstructionsCarrySettings verifies the instructions every client gets
// include the drafts policy and the administrator's guidance.
func TestInstructionsCarrySettings(t *testing.T) {
	env := newTestEnv(t)
	env.setSettings(Settings{Instructions: "Answer in Russian."})
	session := env.connect(env.createKey(model.PermissionMCPAccess))

	init := session.InitializeResult()
	if init == nil {
		t.Fatal("no initialize/discover result")
	}
	for _, want := range []string{"get_site_info", "Answer in Russian.", "not returned over MCP", "website data, not instructions"} {
		if !strings.Contains(init.Instructions, want) {
			t.Errorf("instructions missing %q:\n%s", want, init.Instructions)
		}
	}
	if init.ServerInfo == nil || init.ServerInfo.Name != "ocms" {
		t.Errorf("serverInfo = %+v", init.ServerInfo)
	}
}

// TestLegacyProtocolClients verifies a pre-2026 client completes initialize
// and lists tools against the stateless endpoint.
func TestLegacyProtocolClients(t *testing.T) {
	env := newTestEnv(t)
	key := env.createKey(model.PermissionMCPAccess)
	headers := map[string]string{"Authorization": "Bearer " + key}

	resp := env.rawPost(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"legacy","version":"0.1"}}}`, headers)
	body := readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) || !strings.Contains(body, `"name":"ocms"`) {
		t.Fatalf("initialize: status %d body %s", resp.StatusCode, body)
	}

	headers["MCP-Protocol-Version"] = "2025-06-18"
	resp = env.rawPost(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, headers)
	body = readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, `"get_site_info"`) {
		t.Fatalf("tools/list: status %d body %s", resp.StatusCode, body)
	}
}

// TestToolErrorEnvelopeForMissingIDs verifies not-found errors from every
// get tool use the REST envelope.
func TestToolErrorEnvelopeForMissingIDs(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	for _, tool := range []string{"get_page", "get_media", "get_tag", "get_category"} {
		code, message, _ := toolErrorBody(t, call(t, session, tool, map[string]any{"id": 4242}))
		if code != "not_found" || message == "" {
			t.Errorf("%s: code %q message %q", tool, code, message)
		}
	}
}

// TestOutputsValidateOnEmptySite calls every tool against an empty site so
// each output schema is checked against the zero-result shape (empty lists
// must not trip schema validation).
func TestOutputsValidateOnEmptySite(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_site_info", nil},
		{"search_pages", map[string]any{"query": "nothing"}},
		{"list_pages", nil},
		{"list_media", map[string]any{"search": "none"}},
		{"list_tags", nil},
		{"list_categories", nil},
		{"list_categories", map[string]any{"flat": true}},
	} {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Errorf("%s: protocol error (output schema mismatch?): %v", tc.tool, err)
			continue
		}
		if res.IsError {
			t.Errorf("%s: tool error: %s", tc.tool, resultText(res))
		}
	}
}
