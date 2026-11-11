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
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This scan supplements the behavioral writer fixtures in docs/page-writes.md.
// It follows transaction parameters and query aliases, including closures; a
// filename or a mention of InTx alone does not exempt an autocommit receiver.
func TestPageWritersUseTransactionQueries(t *testing.T) {
	t.Run("sqlc catalog", checkPageMutationQueryCatalog)
	for _, site := range []writerLocation{{"internal/pagewrite/validation.go", "FeaturedImageSize"}, {"internal/ai/draft/normalize.go", "resolveAssignments"}} {
		if !functionCalls(t, token.NewFileSet(), filepath.Join("..", "..", site.path), site.function, "CheckFeaturedImage") {
			t.Errorf("%s:%s must call CheckFeaturedImage", site.path, site.function)
		}
	}

	if !functionCalls(t, token.NewFileSet(), filepath.Join("..", "api", "v2", "pages", "service.go"), "applyUpdate", "FeaturedImageSize") {
		t.Error("REST applyUpdate must call FeaturedImageSize")
	}
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
				scan := writerScan{t: t, set: set, path: filepath.ToSlash(relative), localPackage: file.Name.Name}
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok {
					scan.body(declaration, nil)
					continue
				}
				if fn.Body == nil {
					continue
				}
				scan.function = fn.Name.Name
				scan.manual = manualPageTransaction(fn.Body)
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

// Import/seed paths preserve imported content and have their own contracts;
// Translate copies the current assignment. Interactive assignments must reach
// the shared format-and-dimensions helper, including Restore.
var featuredWriterChecks = map[writerLocation]string{
	{"internal/handler/pages.go", "createPageGuarded"}:      "FeaturedImageSize",
	{"internal/handler/pages.go", "updatePageGuarded"}:      "FeaturedImageSize",
	{"internal/api/v2/pages/service.go", "create"}:          "FeaturedImageSize",
	{"internal/api/v2/pages/service.go", "update"}:          "applyUpdate",
	{"internal/pagewrite/restore.go", "Restore"}:            "CheckFeaturedImage",
	{"internal/handler/pages.go", "Translate"}:              "",
	{"internal/pagewrite/queries.go", "Insert"}:             "",
	{"internal/transfer/importer.go", "createNewPage"}:      "",
	{"internal/transfer/importer.go", "updateExistingPage"}: "",
	{"internal/store/seed_demo.go", "seedDemoPages"}:        "",
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

var rawPageMutation = regexp.MustCompile(`(?i)\b(?:UPDATE(?:\s+OR\s+\w+)?|(?:INSERT(?:\s+OR\s+\w+)?|REPLACE)\s+INTO|DELETE\s+FROM)\s+(?:main\s*\.\s*)?["'\x60\[]?(?:` + strings.Join(pageMutationTables, "|") + `)\b`)

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

func (s writerScan) body(body ast.Node, bindings map[string]string) {
	ast.PreorderStack(body, nil, func(node ast.Node, stack []ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if value.Name == "CheckFeaturedImageDimensions" {
				s.t.Errorf("%s: size-only featured validation bypasses format checks", s.set.Position(value.Pos()))
			}
		case *ast.FuncLit:
			nested := maps.Clone(bindings)
			if nested == nil {
				nested = make(map[string]string)
			}
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
			if owners, guarded := pageMutationBoundaries[value.Sel.Name]; guarded &&
				!slices.Contains(owners, writerLocation{s.path, s.function}) && !s.nonQueryEntryPoint(value) {
				s.t.Errorf("%s: %s is outside its inventoried writer", s.set.Position(node.Pos()), value.Sel.Name)
			}
			if pageMutations[value.Sel.Name] && !s.nonQueryEntryPoint(value) {
				var parent ast.Node
				if len(stack) > 0 {
					parent = stack[len(stack)-1]
				}
				call, direct := parent.(*ast.CallExpr)
				_, methodExpression := ast.Unparen(value.X).(*ast.StarExpr)
				if !direct || call.Fun != value || methodExpression {
					s.t.Errorf("%s: %s must be called directly on tracked queries", s.set.Position(node.Pos()), value.Sel.Name)
				}
			}
		case *ast.CallExpr:
			selected, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || !pageMutations[selected.Sel.Name] || len(value.Args) != 2 || nonPageTranslation(value) {
				return true
			}
			if selected.Sel.Name == "CreatePage" || selected.Sel.Name == "UpdatePage" || selected.Sel.Name == "UpdatePageFeaturedImage" {
				check, inventoried := featuredWriterChecks[writerLocation{s.path, s.function}]
				if !inventoried {
					s.t.Errorf("%s: featured-image writer needs shared validation or a documented preservation exemption", s.set.Position(node.Pos()))
				} else if check != "" && !functionCalls(s.t, s.set, filepath.Join("..", "..", s.path), s.function, check) {
					s.t.Errorf("%s: featured-image writer bypasses %s", s.set.Position(node.Pos()), check)
				}
			}
			// Demo seeding is a fixed startup fixture in store (which cannot import
			// pagewrite). Its enclosing transaction and complete records are tested.
			seed := s.path == "internal/store/seed_demo.go" && s.function == "seedDemoPages"
			if !seed && s.binding(selected.X, bindings) != "queries" {
				s.t.Errorf("%s: %s uses queries outside the page-write transaction", s.set.Position(node.Pos()), selected.Sel.Name)
			}
		case *ast.BasicLit, *ast.BinaryExpr:
			query, ok := literalSQL(value.(ast.Expr))
			languageRename := s.path == "internal/handler/languages.go" && s.function == "updateLanguage" && s.manual
			if ok && rawPageMutation.MatchString(query) && !languageRename {
				s.t.Errorf("%s: raw page SQL needs an inventoried transactional writer", s.set.Position(node.Pos()))
				return false
			}
		}
		return true
	})
}

func literalSQL(expr ast.Expr) (string, bool) {
	switch node := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			text, err := strconv.Unquote(node.Value)
			return text, err == nil
		}
	case *ast.BinaryExpr:
		if node.Op == token.ADD {
			left, leftOK := literalSQL(node.X)
			right, rightOK := literalSQL(node.Y)
			return left + right, leftOK && rightOK
		}
	}
	return "", false
}

func (s writerScan) nonQueryEntryPoint(selector *ast.SelectorExpr) bool {
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	if s.path == "cmd/ocms/main.go" && s.function == "run" {
		return receiver.Name == "pagesHandler" && selector.Sel.Name == "CreatePreviewLink" ||
			receiver.Name == "taxonomyHandler" && (selector.Sel.Name == "DeleteTag" || selector.Sel.Name == "DeleteCategory")
	}
	return s.path == "modules/mcpserver/tools_drafts.go" && s.function == "createPreviewLink" &&
		receiver.Name == "s" && selector.Sel.Name == "CreatePreviewLink"
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
