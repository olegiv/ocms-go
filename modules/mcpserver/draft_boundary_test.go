// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"go/ast"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

func TestSharedDraftWriterBoundary(t *testing.T) {
	allowed := map[string]map[string]string{
		// Each MCP write handler uses its corresponding authoring operation.
		".":                           {"createDraftPage": "Create", "updateDraftPage": "Update", "createTranslationDraft": "Translate", "createPreviewLink": "CreatePreviewLink"},
		"../../internal/ai/authoring": {"create": "CreateWith", "Update": "UpdateWith", "CreatePreviewLink": "Issue"},
	}
	for root, expected := range allowed {
		found := map[string]string{}
		pkg := loadDraftWriterPackage(t, root)
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				function := "<package>"
				if fn, ok := declaration.(*ast.FuncDecl); ok {
					function = fn.Name.Name
				}
				for node := range ast.Preorder(declaration) {
					method := draftMethodReference(pkg.TypesInfo, node)
					switch method {
					case "CreatePage", "UpdatePage", "DeletePage", "PublishPage", "PublishScheduledPage", "SetPageAIAccess":
						t.Errorf("%s: direct page mutation %s bypasses shared writer", pkg.Fset.Position(node.Pos()), method)
					default:
						if !slices.Contains(draftWriteMethods, method) && method != "CreateWith" && method != "UpdateWith" && method != "Issue" {
							continue
						}
						if expected[function] != method {
							t.Errorf("%s: unexpected write reference %s in %s", pkg.Fset.Position(node.Pos()), method, function)
						}
						found[function] = method
					}
				}
			}
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
	for _, name := range []string{
		"../../.env.example", "../../.env.example.prod",
		"../../fly.staging.toml", "../../fly.staging.bootstrap.toml",
		"../../docker-compose.yml", "../../internal/config/config.go",
	} {
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
