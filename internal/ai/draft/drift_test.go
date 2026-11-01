// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/ai/draft"
)

func TestEveryIssueHasEnglishRussianAndDocumentation(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/draft-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "i18n", "locales", lang, "messages.json"))
		if err != nil {
			t.Fatal(err)
		}
		var locale struct {
			Messages []struct{ ID, Message, Translation string }
		}
		if err = json.Unmarshal(raw, &locale); err != nil {
			t.Fatal(err)
		}
		messages := make(map[string]string)
		for _, message := range locale.Messages {
			messages[message.ID] = message.Translation
		}
		seen := make(map[string]bool)
		for _, definition := range draft.Catalog() {
			if seen[definition.Code] || definition.Code == "" {
				t.Fatalf("duplicate or empty issue code %q", definition.Code)
			}
			seen[definition.Code] = true
			text := definition.English
			if lang == "ru" {
				text = definition.Russian
			}
			if text.Message == "" || text.Hint == "" || messages["draft.issues."+definition.Code+".message"] != text.Message || messages["draft.issues."+definition.Code+".hint"] != text.Hint {
				t.Errorf("missing or divergent %s issue text for %s", lang, definition.Code)
			}
			if !strings.Contains(string(doc), "| `"+definition.Code+"` |") {
				t.Errorf("issue %s has no docs entry", definition.Code)
			}
			switch definition.Severity {
			case draft.Error, draft.Warning, draft.Info:
			default:
				t.Errorf("invalid severity for %s", definition.Code)
			}
			if issue := draft.NewIssue(definition.Code, "field", lang); issue.Code != definition.Code || issue.Message != text.Message || issue.Hint != text.Hint || issue.Severity != definition.Severity {
				t.Errorf("catalog resolution drift for %s", definition.Code)
			}
		}
	}
	catalog := draft.Catalog()
	catalog[0].Code = "mutated"
	if draft.Catalog()[0].Code == "mutated" || draft.NewIssue("unknown-code", "field", "en").Severity != draft.Error {
		t.Fatal("mutable or open issue catalog")
	}
}

func TestDraftPackagesCannotFetchURLsOrWritePages(t *testing.T) {
	set := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if name == "net/http" {
				t.Errorf("%s: only the OAuth CIMD fetcher may use an HTTP client in the new engine packages", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selected, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selected.Sel.Name {
			case "CreatePage", "UpdatePage", "DeletePage", "PublishPage", "PublishScheduledPage", "SetPageAIAccess":
				t.Errorf("%s: draft normalization must not write pages", set.Position(call.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
