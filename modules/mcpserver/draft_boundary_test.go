// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

func TestSharedDraftWriterBoundary(t *testing.T) {
	allowed := map[string]map[string]string{
		// Each MCP write handler calls its authoring operation directly.
		".":                           {"createDraftPage": "Create", "updateDraftPage": "Update", "createTranslationDraft": "Translate", "createPreviewLink": "CreatePreviewLink"},
		"../../internal/ai/authoring": {"create": "CreateWith", "Update": "UpdateWith", "CreatePreviewLink": "Issue"},
	}
	for root, expected := range allowed {
		found := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, declaration := range file.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					method := selector.Sel.Name
					switch method {
					case "CreatePage", "UpdatePage", "DeletePage", "PublishPage", "PublishScheduledPage", "SetPageAIAccess":
						t.Errorf("%s: direct page mutation %s bypasses shared writer", path, method)
					default:
						if !slices.Contains(draftWriteMethods, method) && method != "CreateWith" && method != "UpdateWith" && method != "Issue" {
							return true
						}
						if expected[fn.Name.Name] != method {
							t.Errorf("%s: unexpected write call %s in %s", path, method, fn.Name.Name)
						}
						found[fn.Name.Name] = method
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, found) {
			t.Errorf("%s: adapter inventory changed: %v", root, found)
		}
	}
}

func TestCompatibilityProbeIsRetired(t *testing.T) {
	if _, err := os.Stat("scratch.go"); !os.IsNotExist(err) {
		t.Fatal("retired scratch runtime file exists")
	}
	if _, ok := reflect.TypeFor[config.Config]().FieldByName("MCPSpikeTools"); ok {
		t.Fatal("retired probe configuration remains")
	}
	for _, name := range []string{"../../fly.staging.toml", "../../fly.staging.bootstrap.toml", "../../docker-compose.yml", "../../internal/config/config.go"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "OCMS_MCP_"+"SPIKE_TOOLS") {
			t.Errorf("%s: retired probe environment flag remains", name)
		}
	}
	for _, spec := range toolCatalog() {
		if strings.Contains(spec.Name, "scratch") {
			t.Errorf("retired probe tool %s", spec.Name)
		}
	}
}
