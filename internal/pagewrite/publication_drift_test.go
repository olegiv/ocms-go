// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// publicationCover names a function that must call callee: the proof that a
// page write reaches review.ValidatePublication. Functions are found by name in
// the named file, whatever their receiver.
type publicationCover struct{ file, function, callee string }

// publicationSite is the disposition of one runtime function that can write a
// page's content or publication state: either the calls that cover it, or the
// reason it cannot publish an AI draft.
type publicationSite struct {
	covers []publicationCover
	exempt string
}

const (
	newPageOnly   = "inserts a new page: no AI access or authoring history exists before its first revision, and ValidatePublication passes such pages"
	draftOnly     = "creates a draft with no lifecycle fields"
	startupDemo   = "fixed demo content written once at startup; skips existing pages"
	validateInUse = "ValidatePublication"
)

func validatedBy(file, function string) publicationSite {
	return publicationSite{covers: []publicationCover{{file, function, validateInUse}}}
}

// publicationSites inventories every runtime function that calls a page query
// able to set content or publication state (CreatePage, UpdatePage,
// PublishUnpublishedPage, PublishScheduledPage, PublishPage) or pagewrite.Insert.
// Keys are "path:function". A function absent from this table fails the test,
// and so does an entry that no longer exists.
var publicationSites = map[string]publicationSite{
	// Admin create and edit: the guarded writers run formWrite, whose Related
	// callback validates a published or scheduled page (callers are checked below).
	"internal/handler/pages.go:createPageGuarded": validatedBy("internal/handler/pages_write.go", "formWrite"),
	"internal/handler/pages.go:updatePageGuarded": validatedBy("internal/handler/pages_write.go", "formWrite"),
	"internal/handler/pages.go:TogglePublish":     validatedBy("internal/handler/pages.go", "TogglePublish"),
	"internal/handler/pages.go:Translate":         {exempt: draftOnly},
	"internal/pagewrite/restore.go:Restore": {covers: []publicationCover{
		{"internal/handler/pages.go", "RestoreVersion", "Restore"},
		{"internal/handler/pages.go", "RestoreVersion", validateInUse},
	}},
	"internal/pagewrite/queries.go:Insert":        {exempt: newPageOnly},
	"internal/scheduler/scheduler.go:publishPage": validatedBy("internal/scheduler/scheduler.go", "publishPage"),
	// REST v2 and the agent draft tools: create and update are reachable only
	// through Service.write (checked below), which validates before Finish.
	"internal/api/v2/pages/service.go:create": validatedBy("internal/api/v2/pages/write.go", "write"),
	"internal/api/v2/pages/service.go:update": validatedBy("internal/api/v2/pages/write.go", "write"),
	// oCMS JSON/ZIP import: an overwrite can publish an AI draft, so
	// importPages validates the pages it overwrote.
	"internal/transfer/importer.go:updateExistingPage": {covers: []publicationCover{
		{"internal/transfer/importer.go", "importPages", "validateOverwrittenPublication"},
		{"internal/transfer/importer.go", "validateOverwrittenPublication", validateInUse},
	}},
	"internal/transfer/importer.go:createNewPage": {exempt: newPageOnly},
	"internal/store/seed_demo.go:seedDemoPages":   {exempt: startupDemo},
	// Importers and generators insert new pages through pagewrite.Insert.
	"modules/developer/generator.go:generatePages":                  {exempt: newPageOnly},
	"modules/migrator/sources/drupal/importer.go:importNode":        {exempt: newPageOnly},
	"modules/migrator/sources/elefant/importer.go:importPosts":      {exempt: newPageOnly},
	"modules/migrator/sources/elefant/importer.go:importPages":      {exempt: newPageOnly},
	"modules/migrator/sources/phpnuke/stages.go:importStories":      {exempt: newPageOnly},
	"modules/migrator/sources/phpnuke/stages.go:importStaticPages":  {exempt: newPageOnly},
	"modules/migrator/sources/phpnuke/stages.go:importEncyclopedia": {exempt: newPageOnly},
}

var publicationQueries = map[string]bool{
	"CreatePage": true, "UpdatePage": true, "PublishUnpublishedPage": true,
	"PublishScheduledPage": true, "PublishPage": true,
}

// TestEveryPublicationPathIsInventoriedAndValidated is the drift test for the
// publication boundary (2026-11-03 review R2 P3-12): every runtime function
// that can change a page's status, or its content once published, must either
// reach review.ValidatePublication or be a documented exemption. It is
// complementary to TestPageWritersUseTransactionQueries, which checks that
// page queries run on transaction handles and says nothing about validation.
//
// Bug state: delete the ValidatePublication call from TogglePublish, the
// scheduler, the REST write or the import, or add a new page-publishing
// function without an entry here, and this fails.
func TestEveryPublicationPathIsInventoriedAndValidated(t *testing.T) {
	root := filepath.Join("..", "..")
	set := token.NewFileSet()
	found := make(map[string][]string)
	for _, directory := range []string{"internal", "modules", "custom", "cmd"} {
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
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selected, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, _ := selected.X.(*ast.Ident)
					insert := pkg != nil && pkg.Name == "pagewrite" && selected.Sel.Name == "Insert"
					query := publicationQueries[selected.Sel.Name] && len(call.Args) == 2
					if insert || query {
						key := filepath.ToSlash(relative) + ":" + fn.Name.Name
						found[key] = append(found[key], selected.Sel.Name)
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for key, calls := range found {
		if _, ok := publicationSites[key]; !ok {
			t.Errorf("%s calls %v, which can publish or change a page, and is not in publicationSites: cover it with review.ValidatePublication or document why it cannot publish an AI draft", key, calls)
		}
	}
	for key, site := range publicationSites {
		if _, ok := found[key]; !ok {
			t.Errorf("publicationSites lists %s, which no longer calls a page-writing query: remove the entry", key)
		}
		if (len(site.covers) == 0) == (site.exempt == "") {
			t.Errorf("%s needs exactly one of covers and exempt", key)
		}
		// An exemption says the function only creates pages. Hold it to that: a
		// later UpdatePage or publish call in the same function needs a cover.
		if site.exempt != "" {
			for _, call := range found[key] {
				if call != "CreatePage" && call != "Insert" {
					t.Errorf("%s is exempt (%s) but calls %s: cover it with review.ValidatePublication", key, site.exempt, call)
				}
			}
		}
		for _, cover := range site.covers {
			if !functionCalls(t, set, filepath.Join(root, filepath.FromSlash(cover.file)), cover.function, cover.callee) {
				t.Errorf("%s depends on %s calling %s in %s, and it does not", key, cover.function, cover.callee, cover.file)
			}
		}
	}
}

// TestPageFormWritersAlwaysCarryTheFormWrite requires every runtime call of the
// guarded admin writers to pass h.formWrite(...) as their required context.
// A pageFormWrite literal satisfies the signature but can omit the Related
// callback that validates publication, so this guard is still needed.
func TestPageFormWritersAlwaysCarryTheFormWrite(t *testing.T) {
	set := token.NewFileSet()
	entries, err := filepath.Glob(filepath.Join("..", "handler", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			continue
		}
		file, parseErr := parser.ParseFile(set, path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selected, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selected.Sel.Name != "createPageGuarded" && selected.Sel.Name != "updatePageGuarded") {
				return true
			}
			checked++
			last, ok := call.Args[len(call.Args)-1].(*ast.CallExpr)
			var name string
			if ok {
				if option, isSelector := last.Fun.(*ast.SelectorExpr); isSelector {
					name = option.Sel.Name
				}
			}
			if name != "formWrite" {
				t.Errorf("%s: %s is called without h.formWrite(...), which skips publication validation", set.Position(call.Pos()), selected.Sel.Name)
			}
			return true
		})
	}
	if checked < 2 {
		t.Fatalf("found %d guarded writer calls, want the admin create and edit", checked)
	}
}

// TestRESTPageMutationsOnlyRunInsideTheValidatingWrite requires the v2 service's
// create and update, the only functions there that write pages, to be called
// only from write.go, where Service.write validates publication before Finish.
func TestRESTPageMutationsOnlyRunInsideTheValidatingWrite(t *testing.T) {
	set := token.NewFileSet()
	entries, err := filepath.Glob(filepath.Join("..", "api", "v2", "pages", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	callers := make(map[string][]string)
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(set, path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selected, ok := call.Fun.(*ast.SelectorExpr); ok && (selected.Sel.Name == "create" || selected.Sel.Name == "update") {
					callers[selected.Sel.Name] = append(callers[selected.Sel.Name], filepath.Base(path)+":"+fn.Name.Name)
				}
				return true
			})
		}
	}
	for _, name := range []string{"create", "update"} {
		want := []string{"write.go:CreateWith"}
		if name == "update" {
			want = []string{"write.go:UpdateWith"}
		}
		if !slices.Equal(callers[name], want) {
			t.Errorf("Service.%s is called from %v, want only %v (the closure passed to Service.write)", name, callers[name], want)
		}
	}
}

// functionCalls reports whether the named function in file calls callee.
func functionCalls(t *testing.T, set *token.FileSet, path, function, callee string) bool {
	t.Helper()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function || fn.Body == nil {
			continue
		}
		called := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				called = called || fun.Sel.Name == callee
			case *ast.Ident:
				called = called || fun.Name == callee
			}
			return true
		})
		return called
	}
	return false
}
