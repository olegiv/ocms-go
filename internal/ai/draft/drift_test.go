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
	for _, root := range []string{"..", "../../contentpath", "../../editorial"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
}

// This checks RenderMarkdown's direct HTML pipeline. It deliberately requires
// an explicit final sanitizer assignment, not interprocedural data-flow proof.
func TestMarkdownSanitizationFollowsLinkRewriting(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "markdown.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var render *ast.FuncDecl
	for _, decl := range file.Decls {
		if function, ok := decl.(*ast.FuncDecl); ok && function.Name.Name == "RenderMarkdown" {
			render = function
		}
	}
	if render == nil {
		t.Fatal("RenderMarkdown is missing")
	}
	var rewrites, sanitizers, outputs int
	var rewrittenAt, sanitizedAt token.Pos
	var htmlName, sanitizedName, sanitizerInput string
	var assignments []*ast.AssignStmt
	ast.Inspect(render.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		assignments = append(assignments, assignment)
		if len(assignment.Rhs) != 1 {
			return true
		}
		call, ok := assignment.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		output, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		switch function := call.Fun.(type) {
		case *ast.Ident:
			if function.Name == "applyLinks" {
				rewrites++
				htmlName, rewrittenAt = output.Name, assignment.Pos()
			}
		case *ast.SelectorExpr:
			pkg, qualified := function.X.(*ast.Ident)
			if qualified && pkg.Name == "security" && function.Sel.Name == "SanitizePageHTML" {
				sanitizers++
				sanitizedName, sanitizedAt = output.Name, assignment.Pos()
				if len(call.Args) == 1 {
					if input, direct := call.Args[0].(*ast.Ident); direct {
						sanitizerInput = input.Name
					}
				}
			}
		}
		return true
	})
	if rewrites != 1 || sanitizers != 1 || rewrittenAt >= sanitizedAt || htmlName != sanitizedName || htmlName != sanitizerInput {
		t.Fatal("link rewriting must feed one final sanitizer assignment")
	}
	for _, assignment := range assignments {
		for index, left := range assignment.Lhs {
			if name, ok := left.(*ast.Ident); ok && name.Name == htmlName && assignment.Pos() > sanitizedAt {
				t.Fatal("HTML must not be rewritten after final sanitization")
			}
			if field, ok := left.(*ast.SelectorExpr); ok && field.Sel.Name == "HTML" {
				outputs++
				if len(assignment.Rhs) != len(assignment.Lhs) || assignment.Pos() <= sanitizedAt {
					t.Fatal("Rendered.HTML must receive the final sanitizer output")
				}
				value, direct := assignment.Rhs[index].(*ast.Ident)
				if !direct || value.Name != htmlName {
					t.Fatal("Rendered.HTML must receive the final sanitizer output")
				}
			}
		}
	}
	if outputs != 1 {
		t.Fatalf("expected one sanitized HTML output, got %d", outputs)
	}
}
