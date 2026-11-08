// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This scan supplements the behavioral writer fixtures in docs/page-writes.md.
// It follows transaction parameters and query aliases, including closures; a
// filename or a mention of InTx alone does not exempt an autocommit receiver.
func TestPageWritersUseTransactionQueries(t *testing.T) {
	t.Run("sqlc catalog", checkPageMutationQueryCatalog)

	root := filepath.Join("..", "..")
	set := token.NewFileSet()
	for _, directory := range []string{"internal", "modules", "custom", "cmd", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".sql.go") || strings.HasSuffix(path, "_templ.go") || strings.Contains(filepath.ToSlash(path), "/migrations/") {
				return nil
			}
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			for _, declaration := range file.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				scan := writerScan{t: t, set: set, path: filepath.ToSlash(relative), function: fn.Name.Name, localPackage: file.Name.Name, manual: manualPageTransaction(fn.Body)}
				bindings := make(map[string]string)
				scan.parameters(bindings, fn.Type.Params)
				scan.parameters(bindings, fn.Recv)
				scan.body(fn.Body, bindings)
				for _, diagnostic := range trackedWriterDiagnostics(set, file, fn) {
					t.Error(diagnostic)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

type writerScan struct {
	t            *testing.T
	set          *token.FileSet
	path         string
	function     string
	localPackage string
	manual       bool
}

// The catalog scan requires every generated page mutation to be covered by
// these tracking rules or by a dedicated guard.
var pageMutations = map[string]bool{
	"CreatePage": true, "UpdatePage": true, "UpdatePageCreatedAt": true,
	"PublishPage": true, "PublishUnpublishedPage": true, "UnpublishPage": true,
	"PublishScheduledPage": true, "ClearPageScheduledAt": true, "DeletePage": true,
	"UpdatePageFeaturedImage": true, "ClearPageFeaturedImage": true, "UpdatePageLanguage": true,
	"SetPageAIAccess": true, "AddTagToPage": true, "RemoveTagFromPage": true, "ClearPageTags": true,
	"AddCategoryToPage": true, "RemoveCategoryFromPage": true, "ClearPageCategories": true,
	"CreatePageAlias": true, "ClearPageAliases": true, "DeletePageAlias": true,
	"CreateTranslation": true, "DeleteTranslation": true, "DeleteTranslationsForEntity": true,
	"DeleteTranslationsRelatedToEntity": true, "DeleteTranslationsForEntityAndLanguage": true,
	"DeleteTag": true, "DeleteCategory": true, "DeleteMedia": true,
}

var rawPageMutation = regexp.MustCompile(`(?i)\b(?:UPDATE|INSERT(?:\s+OR\s+\w+)?\s+INTO|DELETE\s+FROM)\s+(?:pages|page_tags|page_categories|page_aliases|translations|page_ai_access)\b`)

func (s writerScan) parameters(bindings map[string]string, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		pointer, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		isWork := false
		switch typ := pointer.X.(type) {
		case *ast.SelectorExpr:
			pkg, ok := typ.X.(*ast.Ident)
			isWork = ok && pkg.Name == "pagewrite" && typ.Sel.Name == "Tx"
		case *ast.Ident:
			isWork = s.localPackage == "pagewrite" && typ.Name == "Tx"
		}
		if isWork {
			for _, name := range field.Names {
				bindings[name.Name] = "work"
			}
		}
	}
}

func (s writerScan) binding(expr ast.Expr, bindings map[string]string) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return bindings[node.Name]
	case *ast.SelectorExpr:
		if node.Sel.Name == "Q" && s.binding(node.X, bindings) == "work" {
			return "queries"
		}
	case *ast.CallExpr:
		if selector, ok := node.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "pagewrite" && selector.Sel.Name == "NewTx" {
				return "work" // NewTx also rejects a non-transaction DB handle at runtime.
			}
			if selector.Sel.Name == "WithTx" && s.manual {
				return "queries"
			}
		}
		if name, ok := node.Fun.(*ast.Ident); ok && s.localPackage == "pagewrite" && name.Name == "NewTx" {
			return "work"
		}
	}
	return ""
}

func (s writerScan) body(body *ast.BlockStmt, bindings map[string]string) {
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			nested := maps.Clone(bindings)
			s.parameters(nested, value.Type.Params)
			s.body(value.Body, nested)
			return false
		case *ast.AssignStmt:
			for index, lhs := range value.Lhs {
				name, ok := lhs.(*ast.Ident)
				if ok && index < len(value.Rhs) {
					bindings[name.Name] = s.binding(value.Rhs[index], bindings)
				}
			}
		case *ast.SelectorExpr:
			if owner, guarded := pageMutationBoundaries[value.Sel.Name]; guarded && (s.path != owner.path || s.function != owner.function) {
				s.t.Errorf("%s: %s is reserved for %s in %s", s.set.Position(node.Pos()), value.Sel.Name, owner.function, owner.path)
			}
		case *ast.CallExpr:
			selected, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || !pageMutations[selected.Sel.Name] || len(value.Args) != 2 || nonPageTranslation(value) {
				return true
			}
			// Demo seeding is a fixed startup fixture in store (which cannot import
			// pagewrite). Its enclosing transaction and complete records are tested.
			seed := s.path == "internal/store/seed_demo.go" && s.function == "seedDemoPages"
			if !seed && s.binding(selected.X, bindings) != "queries" {
				s.t.Errorf("%s: %s uses queries outside the page-write transaction", s.set.Position(node.Pos()), selected.Sel.Name)
			}
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}
			query, err := strconv.Unquote(value.Value)
			languageRename := s.path == "internal/handler/languages.go" && s.function == "updateLanguage" && s.manual
			if err == nil && rawPageMutation.MatchString(query) && !languageRename {
				s.t.Errorf("%s: raw page SQL needs an inventoried transactional writer", s.set.Position(node.Pos()))
			}
		}
		return true
	})
}

func nonPageTranslation(call *ast.CallExpr) bool {
	selected, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selected.Sel.Name != "CreateTranslation" {
		return false
	}
	params, ok := call.Args[1].(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, element := range params.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok || name.Name != "EntityType" {
			continue
		}
		switch value := field.Value.(type) {
		case *ast.BasicLit:
			text, _ := strconv.Unquote(value.Value)
			return text == "tag" || text == "category" || text == "form"
		case *ast.SelectorExpr:
			return value.Sel.Name == "EntityTypeTag" || value.Sel.Name == "EntityTypeCategory" || value.Sel.Name == "EntityTypeForm"
		}
	}
	return false
}

func manualPageTransaction(body *ast.BlockStmt) bool {
	seen := make(map[string]bool)
	ast.Inspect(body, func(node ast.Node) bool {
		if selected, ok := node.(*ast.SelectorExpr); ok {
			seen[selected.Sel.Name] = true
		}
		return true
	})
	return seen["BeginTx"] && seen["NewTx"] && seen["Finish"] && seen["Commit"] && (seen["TrackMedia"] || seen["TrackComponent"])
}
