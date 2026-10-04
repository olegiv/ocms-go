// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// ---------------------------------------------------------------------------
// settings
// ---------------------------------------------------------------------------

func TestSettingsRoundTrip(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	moduleutil.RunMigrations(t, db, New().Migrations())

	got, err := loadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if got.AllowDrafts || got.Instructions != "" {
		t.Errorf("defaults = %+v, want drafts off and no instructions", got)
	}

	want := Settings{AllowDrafts: true, Instructions: "Be concise."}
	if err := saveSettings(context.Background(), db, want); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	got, err = loadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if got != want {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

func TestLoadSettingsMissingRow(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	moduleutil.RunMigrations(t, db, New().Migrations())
	if _, err := db.Exec(`DELETE FROM mcp_settings`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := loadSettings(context.Background(), db)
	if err != nil || got != (Settings{}) {
		t.Errorf("missing row: %+v, %v; want defaults", got, err)
	}
	// Saving recreates the row.
	if err := saveSettings(context.Background(), db, Settings{AllowDrafts: true}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	if got, _ := loadSettings(context.Background(), db); !got.AllowDrafts {
		t.Error("save must upsert the missing row")
	}
}

func TestLoadSettingsWithoutTable(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()
	if _, err := loadSettings(context.Background(), db); err == nil {
		t.Error("expected an error without the settings table")
	}
}

func TestNormalizeInstructions(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"trims and keeps newlines", "  Line one\r\nLine two\rLine three\t \n", "Line one\nLine two\nLine three", ""},
		{"drops control characters", "a\x00b\x07c\u200bd", "abc\u200bd", ""},
		{"empty", "   ", "", ""},
		{"at the limit", strings.Repeat("я", maxInstructionsRunes), strings.Repeat("я", maxInstructionsRunes), ""},
		{"over the limit", strings.Repeat("я", maxInstructionsRunes+1), "", errInstructionsTooLong},
		{"invalid utf-8", "bad \xff", "", errInstructionsEncoding},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errKey := normalizeInstructions(tt.in)
			if got != tt.want || errKey != tt.wantErr {
				t.Errorf("normalizeInstructions(%q) = (%q, %q), want (%q, %q)", tt.in, got, errKey, tt.want, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// schemas
// ---------------------------------------------------------------------------

// catalogTypes are the input and output types of every tool.
var catalogTypes = map[string]func() (*jsonschema.Schema, error){
	"SiteInfoInput":        schemaFor[SiteInfoInput],
	"SiteInfo":             schemaFor[SiteInfo],
	"SearchPagesInput":     schemaFor[SearchPagesInput],
	"SearchPagesResult":    schemaFor[SearchPagesResult],
	"ListPagesInput":       schemaFor[ListPagesInput],
	"ListPagesResult":      schemaFor[ListPagesResult],
	"GetPageInput":         schemaFor[GetPageInput],
	"PageDetail":           schemaFor[PageDetail],
	"IDInput":              schemaFor[IDInput],
	"ListMediaInput":       schemaFor[ListMediaInput],
	"ListMediaResult":      schemaFor[ListMediaResult],
	"Media":                schemaFor[media.Media],
	"ListTagsInput":        schemaFor[ListTagsInput],
	"ListTagsResult":       schemaFor[ListTagsResult],
	"TaxonomyTag":          schemaFor[taxonomy.TaxonomyTag],
	"ListCategoriesInput":  schemaFor[ListCategoriesInput],
	"ListCategoriesResult": schemaFor[ListCategoriesResult],
	"TaxonomyCategory":     schemaFor[taxonomy.TaxonomyCategory],
}

func TestSchemasResolve(t *testing.T) {
	for name, build := range catalogTypes {
		s, err := build()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if s.Type != "object" {
			t.Errorf("%s: type %q, want object", name, s.Type)
		}
		if s.AdditionalProperties == nil || s.AdditionalProperties.Not == nil {
			t.Errorf("%s: additionalProperties must be false so misspelled arguments are rejected", name)
		}
	}
}

// TestSchemaRootNotDuplicated verifies the registry's copy of the root type
// is dropped unless the type is recursive.
func TestSchemaRootNotDuplicated(t *testing.T) {
	s, err := schemaFor[PageDetail]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	if _, ok := s.Defs["PageDetail"]; ok {
		t.Error("non-recursive root must not be duplicated in $defs")
	}
	if _, ok := s.Defs["Category"]; !ok {
		t.Errorf("nested types must stay in $defs; got %v", keys(s.Defs))
	}

	cat, err := schemaFor[taxonomy.TaxonomyCategory]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	if _, ok := cat.Defs["TaxonomyCategory"]; !ok {
		t.Error("a recursive root must keep its $defs entry for the children $ref")
	}
}

// TestSchemaConstraintsFromHumaTags verifies huma struct tags become JSON
// Schema keywords the SDK validates.
func TestSchemaConstraintsFromHumaTags(t *testing.T) {
	s, err := schemaFor[ListPagesInput]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	perPage := s.Properties["per_page"]
	if perPage == nil || perPage.Minimum == nil || *perPage.Minimum != 1 || perPage.Maximum == nil || *perPage.Maximum != 100 {
		t.Errorf("per_page = %+v, want minimum 1 maximum 100", perPage)
	}
	if string(perPage.Default) != "20" {
		t.Errorf("per_page default = %s, want 20", perPage.Default)
	}
	if status := s.Properties["status"]; status == nil || len(status.Enum) != 2 {
		t.Errorf("status enum = %+v", status)
	}
	if slices.Contains(s.Required, "per_page") {
		t.Error("omitempty arguments must be optional")
	}

	search, err := schemaFor[SearchPagesInput]()
	if err != nil {
		t.Fatalf("schemaFor: %v", err)
	}
	if !slices.Contains(search.Required, "query") {
		t.Error("query must be required")
	}

	resolved, err := search.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, bad := range []map[string]any{
		{},
		{"query": ""},
		{"query": strings.Repeat("q", 201)},
		{"query": "x", "per_page": 51},
		{"query": "x", "typo": 1},
	} {
		if err := resolved.Validate(bad); err == nil {
			t.Errorf("Validate(%v) succeeded, want an error", bad)
		}
	}
	if err := resolved.Validate(map[string]any{"query": "x", "page": 2}); err != nil {
		t.Errorf("valid arguments rejected: %v", err)
	}
}

func TestSchemaForRejectsNonStructRoots(t *testing.T) {
	if _, err := schemaFor[[]string](); err == nil {
		t.Error("expected an error for a slice root")
	}
	if _, err := schemaFor[struct{ A int }](); err == nil {
		t.Error("expected an error for an anonymous struct root")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

func TestToToolError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCode     string
		wantMessage  string
		wantInternal bool
	}{
		{"validation keeps details", v2.NewValidationError(map[string]string{"slug": "bad"}, "Validation failed"), "validation_error", "Validation failed", false},
		{"not found", v2.NewError(v2.ErrNotFound, "page 3 not found"), "not_found", "page 3 not found", false},
		{"wrapped domain error", fmt.Errorf("ctx: %w", v2.NewError(v2.ErrForbidden, "nope")), "forbidden", "nope", false},
		{"domain internal keeps curated message", v2.NewError(v2.ErrInternal, "Failed to list pages"), "internal_error", "Failed to list pages", true},
		{"plain error is scrubbed", errors.New("open /var/lib/ocms/secret.db: permission denied"), "internal_error", msgInternal, true},
		{"tool error passes through", newValidationError("id", "Give either id or slug"), "validation_error", msgValidation, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			te, internal := toToolError(tt.err)
			if te.Code != tt.wantCode || te.Message != tt.wantMessage || internal != tt.wantInternal {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", te.Code, te.Message, internal, tt.wantCode, tt.wantMessage, tt.wantInternal)
			}
			if strings.Contains(te.Error(), "/var/lib") {
				t.Error("internal details leaked into the tool error")
			}
		})
	}
}

func TestToolErrorRendersRESTEnvelope(t *testing.T) {
	te := newValidationError("slug", "Slug already exists")
	var body v2.ErrorBody
	if err := json.Unmarshal([]byte(te.Error()), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if body.Error.Code != codeValidation || body.Error.Details["slug"] != "Slug already exists" {
		t.Errorf("envelope = %+v", body)
	}
}

func TestIsCancellation(t *testing.T) {
	if !isCancellation(fmt.Errorf("query: %w", context.Canceled)) || !isCancellation(context.DeadlineExceeded) {
		t.Error("context errors must count as cancellation")
	}
	if isCancellation(errors.New("boom")) {
		t.Error("other errors are not cancellation")
	}
}

// ---------------------------------------------------------------------------
// identity
// ---------------------------------------------------------------------------

func TestVerifyValidatedKey(t *testing.T) {
	key := store.ApiKey{ID: 42, Name: "agent", KeyPrefix: "abcd", Permissions: `["mcp:access","pages:read"]`,
		ExpiresAt: sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true}}
	req := httptest.NewRequest("POST", EndpointPath, nil)
	if _, err := verifyValidatedKey(context.Background(), "abcdrest", req); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("no validated key in context: err = %v, want ErrInvalidToken", err)
	}

	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAPIKey, key))
	if _, err := verifyValidatedKey(context.Background(), "zzzzrest", req); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("token prefix mismatch: err = %v, want ErrInvalidToken", err)
	}

	info, err := verifyValidatedKey(context.Background(), "abcdrest", req)
	if err != nil {
		t.Fatalf("verifyValidatedKey: %v", err)
	}
	if info.UserID != "apikey:42" || !slices.Equal(info.Scopes, []string{"mcp:access", "pages:read"}) || info.Expiration.IsZero() {
		t.Errorf("token info = %+v", info)
	}
	id, ok := identityFromTokenInfo(info)
	if !ok || id.key.ID != 42 {
		t.Errorf("identity = %+v, %v", id, ok)
	}
}

func TestIdentityFromRequestMissing(t *testing.T) {
	for name, req := range map[string]*mcp.CallToolRequest{
		"nil request":   nil,
		"no extra":      {},
		"no token info": {Extra: &mcp.RequestExtra{}},
		"foreign extra": {Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{Extra: map[string]any{identityKey: "not an identity"}}}},
	} {
		if _, ok := identityFromRequest(req); ok {
			t.Errorf("%s: identity found, want none", name)
		}
	}
}

func TestActorForAppliesDraftsPolicy(t *testing.T) {
	id := &identity{key: store.ApiKey{ID: 1}, scopes: []string{model.PermissionMCPAccess, model.PermissionPagesRead}}

	hidden := actorFor(id, Settings{AllowDrafts: false})
	if draftsVisible(hidden) || hidden.HasPermission(model.PermissionPagesRead) {
		t.Error("pages:read must be withheld while drafts are hidden")
	}
	visible := actorFor(id, Settings{AllowDrafts: true})
	if !draftsVisible(visible) {
		t.Error("pages:read must pass through when drafts are allowed")
	}
	if !slices.Contains(id.scopes, model.PermissionPagesRead) {
		t.Error("actorFor must not mutate the identity's scopes")
	}

	noScope := actorFor(&identity{scopes: []string{model.PermissionMCPAccess}}, Settings{AllowDrafts: true})
	if draftsVisible(noScope) {
		t.Error("drafts need pages:read even when the policy allows them")
	}
}

// ---------------------------------------------------------------------------
// tool wrapper
// ---------------------------------------------------------------------------

// requestWithIdentity builds a CallToolRequest carrying a caller identity.
func requestWithIdentity(scopes ...string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "test"},
		Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{Extra: map[string]any{
			identityKey: &identity{key: store.ApiKey{ID: 9, KeyPrefix: "abcd"}, scopes: scopes},
		}}},
	}
}

func TestWrapToolRecoversPanics(t *testing.T) {
	env := newTestEnv(t)
	spec := toolSpec{Name: "panicky"}
	handler := wrapTool(env.module, spec, func(*Module, context.Context, *toolCall, IDInput) (SiteInfo, error) {
		panic("kaboom")
	})
	res, _, err := handler(context.Background(), requestWithIdentity(model.PermissionMCPAccess), IDInput{ID: 1})
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	var te *toolError
	if !errors.As(err, &te) || te.Code != codeInternal || strings.Contains(te.Error(), "kaboom") {
		t.Errorf("err = %v, want a scrubbed internal tool error", err)
	}
	panics := env.logs.find("MCP tool panicked")
	if len(panics) != 1 {
		t.Fatalf("panic log lines = %d, want 1", len(panics))
	}
	if stack, ok := recordAttr(panics[0], "stack"); !ok || stack.String() == "" {
		t.Error("the panic log must carry a stack trace")
	}
	calls := env.logs.find("MCP tool call")
	if len(calls) != 1 {
		t.Fatalf("call log lines = %d, want 1", len(calls))
	}
	if v, _ := recordAttr(calls[0], "outcome"); v.String() != outcomeInternalError {
		t.Errorf("outcome = %q, want %q", v.String(), outcomeInternalError)
	}
}

func TestWrapToolRefusesUnauthenticatedCalls(t *testing.T) {
	env := newTestEnv(t)
	ran := false
	handler := wrapTool(env.module, toolSpec{Name: "guarded"}, func(*Module, context.Context, *toolCall, IDInput) (SiteInfo, error) {
		ran = true
		return SiteInfo{}, nil
	})
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, IDInput{})
	var te *toolError
	if !errors.As(err, &te) || te.Code != codeUnauthorized {
		t.Errorf("err = %v, want unauthorized", err)
	}
	if ran {
		t.Error("the tool must not run without an identity")
	}
}

func TestWrapToolLogsInternalErrors(t *testing.T) {
	env := newTestEnv(t)
	handler := wrapTool(env.module, toolSpec{Name: "failing"}, func(*Module, context.Context, *toolCall, IDInput) (SiteInfo, error) {
		return SiteInfo{}, errors.New("database exploded at /var/lib/ocms")
	})
	_, _, err := handler(context.Background(), requestWithIdentity(model.PermissionMCPAccess), IDInput{})
	if strings.Contains(err.Error(), "/var/lib") {
		t.Errorf("internal detail leaked: %v", err)
	}
	calls := env.logs.find("MCP tool call")
	if len(calls) != 1 {
		t.Fatalf("call log lines = %d, want 1", len(calls))
	}
	if calls[0].Level != slog.LevelError {
		t.Errorf("level = %v, want Error for an internal failure", calls[0].Level)
	}
	if v, _ := recordAttr(calls[0], "outcome"); v.String() != outcomeInternalError {
		t.Errorf("outcome = %q, want %q", v.String(), outcomeInternalError)
	}
	if v, _ := recordAttr(calls[0], "error"); !strings.Contains(v.String(), "database exploded") {
		t.Error("the server log must keep the original error")
	}
}

func TestNewPagination(t *testing.T) {
	if p := newPagination(45, 2, 20); p.TotalPages != 3 || p.Page != 2 || p.PerPage != 20 {
		t.Errorf("pagination = %+v", p)
	}
	if p := newPagination(0, 1, 20); p.TotalPages != 0 {
		t.Errorf("empty pagination = %+v", p)
	}
}

func TestNormalizePaging(t *testing.T) {
	if page, perPage := normalizePaging(0, 0, 20); page != 1 || perPage != 20 {
		t.Errorf("defaults = %d, %d", page, perPage)
	}
	if page, perPage := normalizePaging(3, 500, 20); page != 3 || perPage != 20 {
		t.Errorf("over max = %d, %d", page, perPage)
	}
	if page, perPage := normalizePaging(2, 50, 20); page != 2 || perPage != 50 {
		t.Errorf("valid = %d, %d", page, perPage)
	}
}

// ---------------------------------------------------------------------------
// server
// ---------------------------------------------------------------------------

func TestServerInstructions(t *testing.T) {
	hidden := serverInstructions(Settings{})
	if !strings.Contains(hidden, "not returned over MCP") || strings.Contains(hidden, "Guidance from the site administrator") {
		t.Errorf("default instructions:\n%s", hidden)
	}
	shown := serverInstructions(Settings{AllowDrafts: true, Instructions: "Prefer recent posts."})
	if !strings.Contains(shown, "pages:read") || !strings.Contains(shown, "Prefer recent posts.") {
		t.Errorf("custom instructions:\n%s", shown)
	}
}

func TestPageURLs(t *testing.T) {
	urls := pageURLs{base: "https://example.com", isDefault: map[string]bool{"en": true, "ru": false}}
	tests := []struct {
		status, slug, lang, want string
	}{
		{model.PageStatusPublished, "hello", "en", "https://example.com/hello"},
		{model.PageStatusPublished, "privet", "ru", "https://example.com/ru/privet"},
		{model.PageStatusDraft, "hello", "en", ""},
		{model.PageStatusPublished, "hola", "es", ""}, // inactive language
	}
	for _, tt := range tests {
		if got := urls.forPage(tt.status, tt.slug, tt.lang); got != tt.want {
			t.Errorf("forPage(%s, %s, %s) = %q, want %q", tt.status, tt.slug, tt.lang, got, tt.want)
		}
	}
	if got := (pageURLs{}).forPage(model.PageStatusPublished, "x", "en"); got != "" {
		t.Errorf("no site URL: %q, want empty", got)
	}
}

func TestSiteURLValidation(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct {
		value, want string
		status      siteURLStatus
	}{
		{"https://example.com/", "https://example.com", siteURLValid},
		{" http://example.com ", "http://example.com", siteURLValid},
		{"", "", siteURLUnset},
		{"javascript:alert(1)", "", siteURLInvalid},
		{"example.com", "", siteURLInvalid},
		{"example.com", "", siteURLInvalid}, // unchanged: not logged again
		{"ftp://example.com/pub", "", siteURLInvalid},
	} {
		env.setConfig(model.ConfigKeySiteURL, tc.value)
		got, status, err := env.module.resolveSiteURL(context.Background())
		if err != nil || got != tc.want || status != tc.status {
			t.Errorf("resolveSiteURL(%q) = %q, %v, %v; want %q, %v", tc.value, got, status, err, tc.want, tc.status)
		}
	}
	if warned := env.logs.find("configured site URL is not an absolute http(s) URL; MCP results omit page URLs"); len(warned) != 3 {
		t.Errorf("invalid site URL warnings = %d, want one per change of value (3)", len(warned))
	}
}

// ---------------------------------------------------------------------------
// module
// ---------------------------------------------------------------------------

func TestModuleMetadata(t *testing.T) {
	m := New()
	if m.Name() != ModuleName || m.Version() != moduleVersion || m.AdminURL() != adminPath || m.SidebarLabel() == "" {
		t.Errorf("metadata = %q %q %q %q", m.Name(), m.Version(), m.AdminURL(), m.SidebarLabel())
	}
	if m.ActiveByDefault() {
		t.Error("the MCP module must be opt-in")
	}
	for _, lang := range []string{"en", "ru"} {
		if _, err := m.TranslationsFS().ReadFile("locales/" + lang + "/messages.json"); err != nil {
			t.Errorf("missing %s translations: %v", lang, err)
		}
	}
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestMigrationsUpDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	migrations := New().Migrations()
	moduleutil.AssertMigrations(t, migrations, 1)
	moduleutil.RunMigrations(t, db, migrations)
	var allow int
	if err := db.QueryRow(`SELECT allow_drafts FROM mcp_settings WHERE id = 1`).Scan(&allow); err != nil || allow != 0 {
		t.Errorf("seeded row: allow_drafts=%d err=%v, want 0", allow, err)
	}
	moduleutil.RunMigrationsDown(t, db, migrations)
	moduleutil.AssertTableNotExists(t, db, "mcp_settings")
}

func TestServerCardEndpoint(t *testing.T) {
	ep := ServerCardEndpoint()
	if ep.Path != EndpointPath || ep.Version != moduleVersion || len(ep.ProtocolVersions) == 0 {
		t.Errorf("endpoint = %+v", ep)
	}
	if !slices.Contains(ep.ProtocolVersions, "2026-07-28") {
		t.Errorf("protocol versions %v must include 2026-07-28", ep.ProtocolVersions)
	}
}

// TestInitDefaultsWithoutOptionalContext verifies Init tolerates a context
// without Store, Config, Cache or Events, as tests and embedders provide.
func TestInitDefaultsWithoutOptionalContext(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	ctx.Config = nil
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.currentServer() == nil || m.handler.Load() == nil {
		t.Error("Init must build the server and the HTTP chain")
	}
}
