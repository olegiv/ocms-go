// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/migrator/sources/drupal"
	"github.com/olegiv/ocms-go/modules/migrator/sources/elefant"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// These tests enforce invariants mechanically rather than by review. Each one
// documents the bug state it catches; fixing only the site that first broke an
// invariant leaves the same class of bug reachable through another code path.

// TestTrackedEntityTypesAreDeletable is the highest-value drift test here.
//
// Every source records what it created via TrackImportedItem so that "Delete
// All Imported Content" can undo the import. If a source starts tracking an
// entity type the delete path does not handle, the button silently leaves that
// content behind while reporting success.
//
// Bug state: add tracker.TrackImportedItem(ctx, name, "widget", id) to any
// source without extending deleters() and this fails naming "widget".
func TestTrackedEntityTypesAreDeletable(t *testing.T) {
	tracked := trackedEntityTypeLiterals(t)
	if len(tracked) == 0 {
		t.Fatal("found no TrackImportedItem call sites; the AST walk is broken")
	}

	m := New()
	handled := make(map[string]bool)
	for _, d := range m.deleters(nil) {
		handled[string(d.entityType)] = true
	}

	for entityType, location := range tracked {
		if !handled[entityType] {
			t.Errorf("entity type %q is tracked at %s but has no entry in deleters(); "+
				"deleting imported content would leave it orphaned", entityType, location)
		}
	}
}

// trackedEntityTypeLiterals AST-walks the migrator tree and returns every
// entity type a source can record, keyed to where it was found.
//
// Two shapes are collected, because sources legitimately use both: a literal
// passed straight to TrackImportedItem (as sources/elefant does), and a
// types.EntityXxx constant referenced anywhere in a source package, which is
// how a source that tracks through its own helper spells it (as sources/drupal
// does). Missing the second shape would make this test blind to exactly the
// code style it most needs to police.
func trackedEntityTypeLiterals(t *testing.T) map[string]string {
	t.Helper()
	found := make(map[string]string)
	fset := token.NewFileSet()

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		inSourcePkg := strings.Contains(filepath.ToSlash(path), "sources/")

		ast.Inspect(file, func(n ast.Node) bool {
			// Shape 1: a types.EntityXxx constant referenced in a source package.
			if inSourcePkg {
				if name, ok := entityConstName(n); ok {
					found[name] = fset.Position(n.Pos()).String()
					return true
				}
			}

			// Shape 2: a literal passed to TrackImportedItem.
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "TrackImportedItem" {
				return true
			}
			// Signature: TrackImportedItem(ctx, source, entityType, id)
			if len(call.Args) != 4 {
				return true
			}
			if lit, ok := call.Args[2].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name := strings.Trim(lit.Value, `"`)
				found[name] = fset.Position(call.Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk migrator sources: %v", err)
	}
	return found
}

// entityConstName resolves a types.EntityXxx selector to its string value.
func entityConstName(node ast.Node) (string, bool) {
	sel, ok := node.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "types" {
		return "", false
	}
	for _, entityType := range types.AllEntityTypes {
		if sel.Sel.Name == "Entity"+pascalCase(string(entityType)) {
			return string(entityType), true
		}
	}
	return "", false
}

// pascalCase turns "menu_item" into "MenuItem".
func pascalCase(s string) string {
	var out strings.Builder
	upper := true
	for _, r := range s {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			out.WriteRune(unicode.ToUpper(r))
			upper = false
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// TestDeleteImportedItemsCoversAllEntityTypes checks the delete table is
// complete and well-formed.
//
// Bug state: add an entity type to types.AllEntityTypes without a deleters()
// entry and this fails; give an entry both a deleter and a cascade source (or
// neither) and it fails too.
func TestDeleteImportedItemsCoversAllEntityTypes(t *testing.T) {
	m := New()
	deleters := m.deleters(nil)

	if len(deleters) != len(types.AllEntityTypes) {
		t.Fatalf("deleters() has %d entries, want %d (one per types.AllEntityTypes)",
			len(deleters), len(types.AllEntityTypes))
	}

	for i, d := range deleters {
		if d.entityType != types.AllEntityTypes[i] {
			t.Errorf("deleters()[%d] = %q, want %q — the deletion order must match "+
				"types.AllEntityTypes, which encodes the foreign-key dependencies",
				i, d.entityType, types.AllEntityTypes[i])
		}
		hasDeleter := d.del != nil
		hasCascade := d.cascadesFrom != ""
		if hasDeleter == hasCascade {
			t.Errorf("entity type %q must have exactly one of del or cascadesFrom "+
				"(has del=%v, cascadesFrom=%q)", d.entityType, hasDeleter, d.cascadesFrom)
		}
	}
}

// TestImportOptionsHaveFormCheckboxes proves every import option is reachable
// from the UI and parsed back correctly.
//
// Bug state (a): add a field to ImportOptions without a checkbox and the option
// can never be enabled — a silently dead feature flag. Bug state (b):
// copy-paste a form key in parseImportOptions and two fields flip together.
func TestImportOptionsHaveFormCheckboxes(t *testing.T) {
	optionsType := reflect.TypeOf(types.ImportOptions{})
	markup := renderImportOptions(t)

	for i := 0; i < optionsType.NumField(); i++ {
		field := optionsType.Field(i)
		key := snakeCase(field.Name)

		t.Run(field.Name, func(t *testing.T) {
			if !strings.Contains(markup, `name="`+key+`"`) {
				t.Errorf("ImportOptions.%s has no checkbox named %q in MigratorImportOptions; "+
					"the option could never be enabled from the admin UI", field.Name, key)
			}

			req := httptest.NewRequest("POST", "/?"+key+"=on", nil)
			opts := parseImportOptions(req)

			value := reflect.ValueOf(opts)
			for j := 0; j < optionsType.NumField(); j++ {
				want := j == i
				if got := value.Field(j).Bool(); got != want {
					t.Errorf("submitting only %q set ImportOptions.%s = %v, want %v; "+
						"parseImportOptions maps this form key to the wrong field",
						key, optionsType.Field(j).Name, got, want)
				}
			}
		})
	}
}

// renderImportOptions renders the options component to a string.
//
// supported is nil, meaning "no source-specific restriction", so this still
// asserts that every ImportOptions field has a control somewhere in the
// component. Per-source filtering is covered by
// TestSourcesDeclareTheOptionsTheyRead and TestImportFormHidesUnsupportedOptions.
func renderImportOptions(t *testing.T) string {
	t.Helper()
	return renderImportOptionsFor(t, nil)
}

// renderImportOptionsFor renders the options component for a given support set.
func renderImportOptionsFor(t *testing.T, supported map[string]bool) string {
	t.Helper()
	var sb strings.Builder
	pc := &adminviews.PageContext{}
	if err := MigratorImportOptions(pc, supported).Render(context.Background(), &sb); err != nil {
		t.Fatalf("failed to render import options: %v", err)
	}
	return sb.String()
}

// snakeCase turns "ImportCategories" into "import_categories".
func snakeCase(s string) string {
	var out strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				out.WriteRune('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// TestImportResultCountersCoverAllImportedFields proves Counters() maps every
// *Imported field, so totals, the persisted job blob and the stats panel all
// stay complete.
//
// Bug state: add MenusImported to ImportResult without extending Counters() and
// the count vanishes from the totals and the admin UI while the importer still
// increments it.
func TestImportResultCountersCoverAllImportedFields(t *testing.T) {
	assertCountersCoverFields(t, "Imported", func(r *types.ImportResult) map[types.EntityType]int {
		return r.Counters()
	})
}

// TestImportResultSkippedCountersAreDeclared proves SkippedCounters() only
// reports declared entity types and sums into TotalSkipped.
func TestImportResultSkippedCountersAreDeclared(t *testing.T) {
	result := &types.ImportResult{}
	value := reflect.ValueOf(result).Elem()
	fieldType := value.Type()

	total := 0
	for i := 0; i < fieldType.NumField(); i++ {
		if strings.HasSuffix(fieldType.Field(i).Name, "Skipped") {
			n := (i + 1) * 3
			value.Field(i).SetInt(int64(n))
			total += n
		}
	}

	if got := result.TotalSkipped(); got != total {
		t.Errorf("TotalSkipped() = %d, want %d; a *Skipped field is missing from SkippedCounters()", got, total)
	}
	for entityType := range result.SkippedCounters() {
		if !isDeclaredEntityType(entityType) {
			t.Errorf("SkippedCounters() has key %q, which is not in types.AllEntityTypes", entityType)
		}
	}
}

// assertCountersCoverFields assigns a distinct value to every field with the
// given suffix and asserts the counter map reports each exactly once.
func assertCountersCoverFields(t *testing.T, suffix string, counters func(*types.ImportResult) map[types.EntityType]int) {
	t.Helper()

	result := &types.ImportResult{}
	value := reflect.ValueOf(result).Elem()
	fieldType := value.Type()

	expected := make(map[int]string)
	for i := 0; i < fieldType.NumField(); i++ {
		name := fieldType.Field(i).Name
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		n := (i + 1) * 7
		value.Field(i).SetInt(int64(n))
		expected[n] = name
	}
	if len(expected) == 0 {
		t.Fatalf("found no *%s fields on ImportResult; the reflection is broken", suffix)
	}

	seen := make(map[int]bool)
	for entityType, count := range counters(result) {
		if !isDeclaredEntityType(entityType) {
			t.Errorf("Counters() has key %q, which is not in types.AllEntityTypes", entityType)
		}
		seen[count] = true
	}

	for n, name := range expected {
		if !seen[n] {
			t.Errorf("ImportResult.%s is not reported by Counters(); its count would never "+
				"reach the totals, the job row, or the admin stats panel", name)
		}
	}

	sum := 0
	for n := range expected {
		sum += n
	}
	if got := result.TotalImported(); suffix == "Imported" && got != sum {
		t.Errorf("TotalImported() = %d, want %d", got, sum)
	}
}

// isDeclaredEntityType reports whether an entity type is in AllEntityTypes.
func isDeclaredEntityType(entityType types.EntityType) bool {
	for _, declared := range types.AllEntityTypes {
		if declared == entityType {
			return true
		}
	}
	return false
}

// localeMessages is the on-disk shape of a module translation file.
type localeMessages struct {
	Language string `json:"language"`
	Messages []struct {
		ID          string `json:"id"`
		Translation string `json:"translation"`
	} `json:"messages"`
}

// loadLocale reads and parses one locale file.
func loadLocale(t *testing.T, lang string) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("locales", lang, "messages.json"))
	if err != nil {
		t.Fatalf("failed to read %s locale: %v", lang, err)
	}

	var parsed localeMessages
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("failed to parse %s locale: %v", lang, err)
	}

	out := make(map[string]string, len(parsed.Messages))
	for _, msg := range parsed.Messages {
		if _, dup := out[msg.ID]; dup {
			t.Errorf("%s locale declares %q more than once", lang, msg.ID)
		}
		out[msg.ID] = msg.Translation
	}
	return out
}

// TestLocaleKeyParity keeps the translation files in step.
//
// Bug state: add a key to en and forget ru, and Russian admins see a raw i18n
// key in the UI.
func TestLocaleKeyParity(t *testing.T) {
	en := loadLocale(t, "en")
	ru := loadLocale(t, "ru")

	for key := range en {
		if _, ok := ru[key]; !ok {
			t.Errorf("key %q exists in en but is missing from ru", key)
		}
	}
	for key := range ru {
		if _, ok := en[key]; !ok {
			t.Errorf("key %q exists in ru but is missing from en", key)
		}
	}
}

// TestLocaleFormatVerbsMatch keeps printf verbs aligned across languages.
//
// i18n.T is a bare fmt.Sprintf, so a translation that drops or reorders a verb
// renders as "%!d(MISSING)" for that language only — invisible in English-only
// testing.
func TestLocaleFormatVerbsMatch(t *testing.T) {
	en := loadLocale(t, "en")
	ru := loadLocale(t, "ru")

	for key, enText := range en {
		ruText, ok := ru[key]
		if !ok {
			continue
		}
		enVerbs := formatVerbs(enText)
		ruVerbs := formatVerbs(ruText)
		if !reflect.DeepEqual(enVerbs, ruVerbs) {
			t.Errorf("key %q has verbs %v in en but %v in ru; the mismatched language "+
				"would render a %%!verb(MISSING) placeholder", key, enVerbs, ruVerbs)
		}
	}
}

// formatVerbs extracts the ordered printf verbs from a format string.
func formatVerbs(s string) []string {
	var verbs []string
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) {
			continue
		}
		next := s[i+1]
		if next == '%' {
			i++
			continue
		}
		verbs = append(verbs, "%"+string(next))
		i++
	}
	return verbs
}

// TestLocaleCoversEntityTypesAndJobStatuses proves the UI can label everything
// it may be asked to display.
//
// Bug state: add an entity type or job status without its labels and the admin
// sees the raw key, e.g. "migrator.imported_widget".
func TestLocaleCoversEntityTypesAndJobStatuses(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		messages := loadLocale(t, lang)

		for _, entityType := range types.AllEntityTypes {
			for _, prefix := range []string{"migrator.imported_", "migrator.phase_", "migrator.skipped_"} {
				key := prefix + string(entityType)
				if _, ok := messages[key]; !ok {
					t.Errorf("%s locale is missing %q", lang, key)
				}
			}
		}

		for _, status := range AllJobStatuses {
			key := "migrator.job_" + string(status)
			if _, ok := messages[key]; !ok {
				t.Errorf("%s locale is missing %q", lang, key)
			}
		}
	}
}

// TestFinishJobPersistsSkippedCounters checks that a finished job records what
// it declined to import, not only what it imported.
//
// ImportResult has carried SkippedCounters all along, and finishJob's own
// comment claims the result "alone carries the skipped counts" — but only
// Counters() was ever written to the row, so a file skipped for its type
// vanished with no number anywhere to account for it.
//
// Bug state: drop the skipped column from the UPDATE in finishJob and this
// fails with a zero count.
func TestFinishJobPersistsSkippedCounters(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "tester@example.com", 0, ImportOptions{ImportMedia: true})
	if err != nil {
		t.Fatalf("startJob: %v", err)
	}

	result := &ImportResult{MediaImported: 3, MediaSkipped: 5}
	if err := m.finishJob(ctx, jobID, JobCompleted, result, nil); err != nil {
		t.Fatalf("finishJob: %v", err)
	}

	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatalf("latestJob: %v", err)
	}
	if job == nil {
		t.Fatal("latestJob returned no job")
	}

	if got := job.SkippedCount(types.EntityMedia); got != 5 {
		t.Errorf("SkippedCount(media) = %d, want 5", got)
	}
	if got := job.Count(types.EntityMedia); got != 3 {
		t.Errorf("Count(media) = %d, want 3", got)
	}
}

// TestSourceLabelsAreTranslated proves every registered source's admin-facing
// strings resolve.
//
// Bug state: ship a source whose ConfigField.Label is a key with no translation
// and the form renders "drupal.field_mysql_host" as its label.
func TestSourceLabelsAreTranslated(t *testing.T) {
	en := loadLocale(t, "en")
	ru := loadLocale(t, "ru")

	// Init registers every built-in source into the shared registry.
	_ = testModule(t)

	for _, src := range ListSources() {
		keys := []string{src.Description()}
		for _, field := range src.ConfigFields() {
			keys = append(keys, field.Label)
			if field.Placeholder != "" {
				keys = append(keys, field.Placeholder)
			}
		}

		for _, key := range keys {
			if _, ok := en[key]; !ok {
				t.Errorf("source %q uses i18n key %q with no en translation", src.Name(), key)
			}
			if _, ok := ru[key]; !ok {
				t.Errorf("source %q uses i18n key %q with no ru translation", src.Name(), key)
			}
		}
	}
}

// informationalPhrases mark a message describing an expected outcome rather
// than a failure. A message containing one of these belongs in Notices.
var informationalPhrases = []string{
	"not found in source database",
	"were not imported",
	"was not imported",
	"no ocms equivalent",
	"no files path configured",
	"deliberately",
}

// failurePhrases mark a message describing something that went wrong. A message
// containing one of these belongs in Errors.
var failurePhrases = []string{
	"failed to",
}

// TestImportMessagesAreClassifiedCorrectly enforces the split between things
// that failed and things the operator simply needs to know.
//
// A real migration reported "3 errors" for a healthy import: an optional source
// table the site does not have, a second one, and translations that are out of
// scope by design. Fixing the three call sites alone would leave the next one
// free to regress, so the rule is enforced over the source text.
//
// Bug state: change any AddNotice describing an expected outcome back to
// AddError and this names the file, line and message.
func TestImportMessagesAreClassifiedCorrectly(t *testing.T) {
	calls := resultMessageCalls(t)
	if len(calls) == 0 {
		t.Fatal("found no AddError/AddNotice call sites; the AST walk is broken")
	}

	for _, call := range calls {
		lower := strings.ToLower(call.format)

		if call.method == "AddError" {
			for _, phrase := range informationalPhrases {
				if strings.Contains(lower, phrase) {
					t.Errorf("%s: AddError(%q) describes an expected outcome (%q); "+
						"use AddNotice, or a healthy import reports it as a failure",
						call.pos, call.format, phrase)
				}
			}
			continue
		}

		for _, phrase := range failurePhrases {
			if strings.Contains(lower, phrase) {
				t.Errorf("%s: AddNotice(%q) describes a failure (%q); "+
					"use AddError, or a real problem is hidden as an informational message",
					call.pos, call.format, phrase)
			}
		}
	}
}

// messageCall is one ImportResult.AddError / AddNotice call site.
type messageCall struct {
	method string
	format string
	pos    string
}

// resultMessageCalls AST-walks the migrator tree and returns every AddError and
// AddNotice call whose format string is a literal.
func resultMessageCalls(t *testing.T) []messageCall {
	t.Helper()
	var calls []messageCall
	fset := token.NewFileSet()

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "AddError" && sel.Sel.Name != "AddNotice" {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			calls = append(calls, messageCall{
				method: sel.Sel.Name,
				format: strings.Trim(lit.Value, `"`),
				pos:    fset.Position(call.Pos()).String(),
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk migrator sources: %v", err)
	}
	return calls
}

// TestSourcesDeclareTheOptionsTheyRead ties each source's declared
// SupportedImportOptions to the ImportOptions fields its code actually reads.
//
// Bug state: the import form rendered all eight checkboxes for every source,
// with import_menus and import_categories checked by default, while
// elefant.Source.Import consults neither. An Elefant run therefore promised
// menus and categories, imported neither, and still reported "Completed".
// Fixing only Elefant's declaration would leave the next source free to drift
// the same way, so the invariant is enforced over the source tree rather than
// at the one site that broke.
//
// It fails in both directions: an option offered but never read, and an option
// read but not declared (which would hide a working feature from the UI).
func TestSourcesDeclareTheOptionsTheyRead(t *testing.T) {
	// Every source package must appear here. A new source with no entry fails
	// the test rather than silently escaping it.
	declared := map[string]Source{
		"drupal":  drupal.NewSource(),
		"elefant": elefant.NewSource(),
	}

	optionFields := make(map[string]bool)
	optionsType := reflect.TypeOf(types.ImportOptions{})
	for i := 0; i < optionsType.NumField(); i++ {
		optionFields[optionsType.Field(i).Name] = true
	}

	read := optionFieldsReadPerSource(t, optionFields)

	for pkg := range read {
		if _, ok := declared[pkg]; !ok {
			t.Errorf("source package %q reads ImportOptions but is not listed in this test; "+
				"add it so its declared options stay tied to the code", pkg)
		}
	}

	for pkg, src := range declared {
		t.Run(pkg, func(t *testing.T) {
			supporter, ok := src.(types.OptionSupporter)
			if !ok {
				// Not implementing the interface means "supports everything",
				// which is only honest if the source really does read them all.
				for key := range read[pkg] {
					_ = key
				}
				for _, key := range types.ImportOptionKeys() {
					if !read[pkg][key] {
						t.Errorf("%s does not implement OptionSupporter, so the form offers %q, "+
							"but no code in the package reads it", pkg, key)
					}
				}
				return
			}

			declaredKeys := make(map[string]bool, len(supporter.SupportedImportOptions()))
			for _, key := range supporter.SupportedImportOptions() {
				declaredKeys[key] = true
			}

			for _, key := range types.ImportOptionKeys() {
				switch {
				case declaredKeys[key] && !read[pkg][key]:
					t.Errorf("%s declares support for %q but no code in the package reads it; "+
						"the form would offer content the import silently drops", pkg, key)
				case !declaredKeys[key] && read[pkg][key]:
					t.Errorf("%s reads %q but does not declare it; "+
						"the form hides an option the import honours", pkg, key)
				}
			}
		})
	}
}

// optionFieldsReadPerSource AST-walks modules/migrator/sources and returns, per
// source package, the set of ImportOptions form keys its non-test code reads.
func optionFieldsReadPerSource(t *testing.T, optionFields map[string]bool) map[string]map[string]bool {
	t.Helper()
	read := make(map[string]map[string]bool)
	fset := token.NewFileSet()

	root := "sources"
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		pkg := filepath.Dir(rel)
		if pkg == "." || pkg == "shared" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !optionFields[sel.Sel.Name] {
				return true
			}
			// Only count reads off an options value: "opts.ImportMenus" and
			// "st.opts.ImportMenus", not an unrelated struct that happens to
			// carry a field of the same name.
			if !isOptionsReceiver(sel.X) {
				return true
			}
			if read[pkg] == nil {
				read[pkg] = make(map[string]bool)
			}
			read[pkg][types.ImportOptionFormKey(sel.Sel.Name)] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk migrator sources: %v", err)
	}
	return read
}

// isOptionsReceiver reports whether an expression names an ImportOptions value.
func isOptionsReceiver(expr ast.Expr) bool {
	var name string
	switch recv := expr.(type) {
	case *ast.Ident:
		name = recv.Name
	case *ast.SelectorExpr:
		name = recv.Sel.Name
	default:
		return false
	}
	name = strings.ToLower(name)
	return strings.Contains(name, "opts") || strings.Contains(name, "options")
}

// TestImportFormHidesUnsupportedOptions checks the rendered form, not just the
// declaration: the capability set has to reach the markup.
func TestImportFormHidesUnsupportedOptions(t *testing.T) {
	for _, tc := range []struct {
		source Source
		name   string
	}{
		{drupal.NewSource(), "drupal"},
		{elefant.NewSource(), "elefant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported := types.SupportedImportOptionSet(tc.source)
			markup := renderImportOptionsFor(t, supported)

			for _, key := range types.ImportOptionKeys() {
				present := strings.Contains(markup, `name="`+key+`"`)
				if present != supported[key] {
					t.Errorf("checkbox %q present = %v, want %v for source %q",
						key, present, supported[key], tc.name)
				}
			}
		})
	}
}

// TestSourceFormKeepsPollingWhenJobReadFails checks the initial render, not just
// the status endpoint.
//
// The page passed a literal nil for the job lookup error, so a transient failure
// while it loaded rendered the idle card — no hx-trigger — and an import running
// in the background stayed invisible until someone reloaded by hand. The status
// endpoint already treats an unknown state as "keep polling"; the first render
// has to agree, or there is never a second one.
func TestSourceFormKeepsPollingWhenJobReadFails(t *testing.T) {
	pc := &adminviews.PageContext{}
	data := MigratorSourceFormViewData{
		SourceName: "drupal",
		JobReadErr: errors.New("database is locked"),
	}

	var sb strings.Builder
	if err := MigratorSourceFormPage(pc, data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("failed to render source form: %v", err)
	}
	markup := sb.String()

	if !strings.Contains(markup, `hx-trigger="every 2s"`) {
		t.Error("the status card does not poll after a failed job lookup; a running " +
			"import would stay invisible until a manual reload")
	}

	// And the opposite: a clean read with no job renders the idle card, which
	// must not poll forever.
	var idle strings.Builder
	if err := MigratorSourceFormPage(pc, MigratorSourceFormViewData{
		SourceName: "drupal",
	}).Render(context.Background(), &idle); err != nil {
		t.Fatalf("failed to render idle source form: %v", err)
	}
	if strings.Contains(idle.String(), `hx-trigger="every 2s"`) {
		t.Error("the idle card polls; only an unknown or running state should")
	}
}
