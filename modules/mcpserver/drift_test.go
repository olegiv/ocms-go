// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// restOperations builds the live REST v2 API and returns its operations by
// operationId, exactly as /api/v2/openapi.json describes them.
func restOperations(t *testing.T) map[string]*huma.Operation {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(db)
	h := apiv2.Register(chi.NewRouter(), apiv2.Deps{DB: db, Queries: queries})
	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, nil, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries, nil))

	ops := make(map[string]*huma.Operation)
	for _, item := range h.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op != nil {
				ops[op.OperationID] = op
			}
		}
	}
	if len(ops) == 0 {
		t.Fatal("no REST operations discovered; the enumeration is broken")
	}
	return ops
}

// TestEveryRESTOperationHasMCPDecision fails when REST v2 gains an
// operation that no tool mirrors and restOperationsNotExposed does not
// explain, or when either list names an operation REST no longer has. New
// REST surface therefore always gets an explicit MCP decision.
func TestEveryRESTOperationHasMCPDecision(t *testing.T) {
	ops := restOperations(t)
	mirrored := make(map[string]string)
	for _, spec := range toolCatalog() {
		for _, opID := range spec.RESTOperations {
			if _, ok := ops[opID]; !ok {
				t.Errorf("tool %s mirrors REST operation %q, which does not exist", spec.Name, opID)
			}
			mirrored[opID] = spec.Name
		}
	}
	for opID, reason := range restOperationsNotExposed {
		if _, ok := ops[opID]; !ok {
			t.Errorf("restOperationsNotExposed names %q, which REST does not have", opID)
		}
		if tool, ok := mirrored[opID]; ok {
			t.Errorf("%q is both mirrored by %s and listed as not exposed", opID, tool)
		}
		if reason == "" {
			t.Errorf("%q is not exposed without a reason", opID)
		}
	}
	for opID, op := range ops {
		_, isMirrored := mirrored[opID]
		_, isExcluded := restOperationsNotExposed[opID]
		if !isMirrored && !isExcluded {
			t.Errorf("REST operation %q (%s %s) has no MCP decision: mirror it with a tool or add it to restOperationsNotExposed",
				opID, op.Method, op.Path)
		}
	}
}

// restParamAliases maps REST parameter names to the MCP argument that
// mirrors them where MCP spells the name more explicitly for agents.
var restParamAliases = map[string]string{
	"category": "category_id",
	"tag":      "tag_id",
	"folder":   "folder_id",
}

// restParamsNotMirrored lists REST parameters a tool deliberately omits.
var restParamsNotMirrored = map[string]string{
	"listPages.include":     "list_pages takes include_taxonomy; authors are part of get_page",
	"getPage.include":       "get_page always returns author, categories and tags",
	"getPageBySlug.include": "get_page always returns author, categories and tags",
	"listMedia.include":     "list_media returns summaries; get_media returns every relation",
	"getMedia.include":      "get_media always returns variants, folder and translations",
}

// constraintKeywords are the JSON Schema keywords that decide what a value
// may be. "format" is excluded: it is an annotation in draft 2020-12.
var constraintKeywords = []string{"type", "enum", "default", "minimum", "maximum", "minLength", "maxLength", "pattern"}

// TestToolInputConstraintsMatchREST fails when an MCP argument and the REST
// parameter it mirrors disagree on any validation keyword, so tightening or
// loosening REST validation cannot leave the MCP surface behind (or the
// other way round).
func TestToolInputConstraintsMatchREST(t *testing.T) {
	ops := restOperations(t)
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	inputs := make(map[string]map[string]any)
	for _, tool := range listed.Tools {
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		inputs[tool.Name] = props
	}

	compared := 0
	for _, spec := range toolCatalog() {
		props := inputs[spec.Name]
		for _, opID := range spec.RESTOperations {
			for _, param := range ops[opID].Parameters {
				if _, skip := restParamsNotMirrored[opID+"."+param.Name]; skip {
					continue
				}
				argName := param.Name
				if alias, ok := restParamAliases[argName]; ok {
					argName = alias
				}
				mcpProp, ok := props[argName].(map[string]any)
				if !ok {
					t.Errorf("%s: REST %s parameter %q has no MCP argument %q (mirror it or list it in restParamsNotMirrored)",
						spec.Name, opID, param.Name, argName)
					continue
				}
				restProp := schemaAsMap(t, param.Schema)
				for _, keyword := range constraintKeywords {
					if !reflect.DeepEqual(restProp[keyword], mcpProp[keyword]) {
						t.Errorf("%s.%s %s = %v, but REST %s.%s has %v",
							spec.Name, argName, keyword, mcpProp[keyword], opID, param.Name, restProp[keyword])
					}
				}
				compared++
			}
		}
	}
	if compared == 0 {
		t.Fatal("no parameters were compared; the test is vacuous")
	}
}

// schemaAsMap renders a huma schema the way it appears in the OpenAPI JSON.
func schemaAsMap(t *testing.T, s *huma.Schema) map[string]any {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal REST schema: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal REST schema: %v", err)
	}
	return out
}

// TestCatalogSpecsWellFormed fails on catalog entries clients or the drift
// tests could not rely on.
func TestCatalogSpecsWellFormed(t *testing.T) {
	validName := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	seen := make(map[string]bool)
	for _, spec := range toolCatalog() {
		if !validName.MatchString(spec.Name) {
			t.Errorf("tool name %q must be lower snake_case", spec.Name)
		}
		if seen[spec.Name] {
			t.Errorf("duplicate tool name %q", spec.Name)
		}
		seen[spec.Name] = true
		if spec.Title == "" || len(spec.Description) < 40 {
			t.Errorf("%s: needs a title and a description agents can act on", spec.Name)
		}
		if (len(spec.RESTOperations) == 0) == (spec.MCPOnlyReason == "") {
			t.Errorf("%s: declare either RESTOperations or MCPOnlyReason (exactly one)", spec.Name)
		}
		if spec.register == nil {
			t.Errorf("%s: no register function", spec.Name)
		}
	}
}

// TestEveryRegisteredToolIsReadOnly guards the scope of the read-only
// release: no tool may reach the server without readOnlyHint, whether added
// to the catalog or registered directly. Write tools need their own policy
// layer (publish and delete gates) before this test may be relaxed.
func TestEveryRegisteredToolIsReadOnly(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed.Tools) != len(toolCatalog()) {
		t.Errorf("server registers %d tools, catalog has %d", len(listed.Tools), len(toolCatalog()))
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s is not read-only", tool.Name)
		}
	}
}

// messageKeyPattern matches translation keys referenced from module code.
var messageKeyPattern = regexp.MustCompile(`"((?:mcp|nav|label|api_keys)\.[a-z0-9_]+)"`)

// TestTranslationsCompleteAndUsedKeysExist fails when the module's en and ru
// catalogs drift apart, or when code or templates reference a message key
// that no catalog defines (the page would render the raw key).
func TestTranslationsCompleteAndUsedKeysExist(t *testing.T) {
	catalogs := make(map[string]map[string]bool)
	for _, lang := range []string{"en", "ru"} {
		raw, err := localesFS.ReadFile("locales/" + lang + "/messages.json")
		if err != nil {
			t.Fatalf("read %s: %v", lang, err)
		}
		var file struct {
			Language string `json:"language"`
			Messages []struct {
				ID          string `json:"id"`
				Translation string `json:"translation"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatalf("parse %s: %v", lang, err)
		}
		if file.Language != lang {
			t.Errorf("%s catalog declares language %q", lang, file.Language)
		}
		catalogs[lang] = make(map[string]bool)
		for _, m := range file.Messages {
			if catalogs[lang][m.ID] {
				t.Errorf("%s: duplicate key %s", lang, m.ID)
			}
			if !strings.HasPrefix(m.ID, ModuleName+".") {
				t.Errorf("%s: key %s must use the %q prefix", lang, m.ID, ModuleName+".")
			}
			if strings.TrimSpace(m.Translation) == "" {
				t.Errorf("%s: empty translation for %s", lang, m.ID)
			}
			catalogs[lang][m.ID] = true
		}
	}
	for key := range catalogs["en"] {
		if !catalogs["ru"][key] {
			t.Errorf("key %s is missing from ru", key)
		}
	}
	for key := range catalogs["ru"] {
		if !catalogs["en"][key] {
			t.Errorf("key %s is missing from en", key)
		}
	}

	if err := i18n.Init(nil); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}
	if err := i18n.LoadTranslationsFromFS(localesFS, ""); err != nil {
		t.Fatalf("load module translations: %v", err)
	}
	used := usedMessageKeys(t)
	if len(used) == 0 {
		t.Fatal("no message keys found in module sources; the scan is broken")
	}
	for _, key := range used {
		if strings.HasPrefix(key, ModuleName+".") && !catalogs["en"][key] {
			t.Errorf("module code uses %s, which the module catalogs do not define", key)
		}
		for _, lang := range []string{"en", "ru"} {
			if i18n.T(lang, key) == key {
				t.Errorf("%s: no translation for %s", lang, key)
			}
		}
	}
}

// usedMessageKeys scans the module's Go and templ sources for message keys.
func usedMessageKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, "_test.go") || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".templ")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range messageKeyPattern.FindAllStringSubmatch(string(src), -1) {
			if !slices.Contains(keys, m[1]) {
				keys = append(keys, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan sources: %v", err)
	}
	return keys
}

// TestServerCardMatchesInitialize fails when the server card and the live
// server disagree about who they are: clients that read the card and then
// connect compare the two.
func TestServerCardMatchesInitialize(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	init := session.InitializeResult()
	if init == nil || init.ServerInfo == nil {
		t.Fatal("no initialize result")
	}
	card := ServerCardEndpoint()
	live := init.ServerInfo
	if live.Name != card.Name || live.Title != card.Title || live.Version != card.Version {
		t.Errorf("server card says %s/%s/%s, initialize says %s/%s/%s",
			card.Name, card.Title, card.Version, live.Name, live.Title, live.Version)
	}
}
