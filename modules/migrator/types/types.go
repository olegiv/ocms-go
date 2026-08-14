// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package types defines shared types for the migrator module.
// This package is separate to avoid import cycles between migrator and source implementations.
package types

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

// EntityType identifies a kind of imported entity. Values are the strings
// stored in migrator_imported_items.entity_type.
type EntityType string

const (
	EntityMenuItem EntityType = "menu_item"
	EntityMenu     EntityType = "menu"
	EntityRedirect EntityType = "redirect"
	EntityAlias    EntityType = "alias"
	EntityPost     EntityType = "post"
	EntityPage     EntityType = "page"
	EntityTag      EntityType = "tag"
	EntityCategory EntityType = "category"
	EntityMedia    EntityType = "media"
	EntityUser     EntityType = "user"
)

// AllEntityTypes lists every trackable entity type in dependency-safe deletion
// order: dependents first, dependencies last. The order is derived from the
// foreign keys in 00003_create_pages.sql (pages.author_id ... ON DELETE
// RESTRICT, so pages must go before users), 00011_create_menus.sql
// (menu_items.menu_id ... ON DELETE CASCADE) and 00008_create_categories.sql
// (categories.parent_id ... ON DELETE SET NULL, so categories are unordered).
var AllEntityTypes = []EntityType{
	EntityMenuItem,
	EntityMenu,
	EntityRedirect,
	EntityAlias,
	EntityPost,
	EntityPage,
	EntityTag,
	EntityCategory,
	EntityMedia,
	EntityUser,
}

// ImportTracker tracks imported items for later deletion.
type ImportTracker interface {
	// TrackImportedItem records an imported item.
	TrackImportedItem(ctx context.Context, source, entityType string, entityID int64) error
}

// MediaCleanupQueuer is an optional capability of an ImportTracker. Sources
// use it when compensating an import-time media failure could not remove the
// files immediately. The durable queue keeps that orphan discoverable across
// process restarts without extending ImportTracker and breaking third-party
// implementations.
type MediaCleanupQueuer interface {
	QueueMediaCleanup(ctx context.Context, source, uploadRoot, mediaUUID string) error
}

// Progress is a live progress sample published by a source during Import.
// Total is 0 when the source cannot know the denominator up front.
type Progress struct {
	Source    string
	Phase     EntityType
	Processed int
	Total     int
}

// ProgressReporter is an optional capability of an ImportTracker. Sources must
// not depend on it directly — call Report, which is a no-op when the tracker
// does not implement it.
//
// It is deliberately a separate interface rather than a method on
// ImportTracker: adding a method there would break every implementer,
// including test doubles and any out-of-tree module.
type ProgressReporter interface {
	ReportProgress(ctx context.Context, p Progress)
}

// Report publishes progress when the tracker supports it. Nil-safe and a no-op
// for trackers that do not implement ProgressReporter, so sources written
// against the original interface keep working unchanged.
func Report(ctx context.Context, tracker ImportTracker, p Progress) {
	reporter, ok := tracker.(ProgressReporter)
	if !ok {
		return
	}
	reporter.ReportProgress(ctx, p)
}

// Source defines the interface that all migration sources must implement.
type Source interface {
	// Name returns the unique identifier for this source (e.g., "elefant", "drupal").
	Name() string

	// DisplayName returns the human-readable name for the UI.
	DisplayName() string

	// Description returns a brief description of what this source imports.
	Description() string

	// ConfigFields returns the configuration fields needed for this source.
	ConfigFields() []ConfigField

	// TestConnection tests the connection using the provided configuration.
	TestConnection(cfg map[string]string) error

	// Import performs the actual import using the provided configuration and options.
	// The tracker can be used to record imported items for later deletion.
	//
	// Implementations must not wrap the whole import in a single transaction:
	// oCMS runs SQLite with a 5s busy_timeout, so holding the write lock for
	// the duration of an import starves every other writer. Write per entity
	// and let the tracking table provide the undo path instead.
	Import(ctx context.Context, db *sql.DB, cfg map[string]string, opts ImportOptions, tracker ImportTracker) (*ImportResult, error)
}

// ContextConnectionTester is an optional capability of a Source. The legacy
// TestConnection method remains on Source for compatibility; the HTTP handler
// prefers this method so request cancellation and its deadline reach the
// database driver.
type ContextConnectionTester interface {
	TestConnectionContext(ctx context.Context, cfg map[string]string) error
}

// OptionSupporter is an optional capability of a Source: it declares which
// import options the source actually acts on, as form keys.
//
// Like ProgressReporter above, it is a separate interface rather than a method
// on Source so that adding it does not break every implementer, including test
// doubles and any out-of-tree module. A source that does not implement it is
// treated as supporting everything.
//
// It exists because the import form rendered all eight checkboxes for every
// source, with menus and categories checked by default, while the Elefant
// importer reads neither — so an Elefant run promised menus and categories,
// imported neither, and still reported "Completed".
type OptionSupporter interface {
	SupportedImportOptions() []string
}

// SupportsImportOption reports whether a source acts on an import option.
func SupportsImportOption(src Source, formKey string) bool {
	supporter, ok := src.(OptionSupporter)
	if !ok {
		return true
	}
	return slices.Contains(supporter.SupportedImportOptions(), formKey)
}

// SupportedImportOptionSet returns the form keys a source acts on, keyed for
// template lookup.
func SupportedImportOptionSet(src Source) map[string]bool {
	set := make(map[string]bool, len(ImportOptionKeys()))
	for _, key := range ImportOptionKeys() {
		set[key] = SupportsImportOption(src, key)
	}
	return set
}

// MaskUnsupportedImportOptions clears every option the source does not act on.
//
// Omitting a checkbox already stops the browser submitting it, so this only
// matters for a hand-crafted POST. It is still worth doing: the options blob is
// persisted on the job row and read back by the admin UI, and ImportMenus also
// drives cache invalidation — recording an option the source ignored would
// misreport what the run was asked to do.
func MaskUnsupportedImportOptions(src Source, opts ImportOptions) ImportOptions {
	if _, ok := src.(OptionSupporter); !ok {
		return opts
	}
	value := reflect.ValueOf(&opts).Elem()
	optionsType := value.Type()
	for i := 0; i < optionsType.NumField(); i++ {
		if !SupportsImportOption(src, ImportOptionFormKey(optionsType.Field(i).Name)) {
			value.Field(i).SetBool(false)
		}
	}
	return opts
}

// ImportOptionKeys returns the form key of every ImportOptions field, in
// declaration order.
func ImportOptionKeys() []string {
	optionsType := reflect.TypeOf(ImportOptions{})
	keys := make([]string, 0, optionsType.NumField())
	for i := 0; i < optionsType.NumField(); i++ {
		keys = append(keys, ImportOptionFormKey(optionsType.Field(i).Name))
	}
	return keys
}

// ImportOptionFormKey turns an ImportOptions field name such as
// "ImportCategories" into its form key "import_categories".
//
// This is the one place the convention lives. parseImportOptions writes the
// same mapping out longhand for readability, and
// TestImportOptionsHaveFormCheckboxes proves the two agree.
func ImportOptionFormKey(fieldName string) string {
	var out strings.Builder
	for i, r := range fieldName {
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

// ConfigField represents a configuration field for a migration source.
type ConfigField struct {
	Name        string // Field name (form key)
	Label       string // Display label
	Type        string // Field type: "text", "password", "number", "path"
	Required    bool   // Whether the field is required
	Default     string // Default value
	Placeholder string // Placeholder text
}

// ImportOptions contains options for the import operation.
//
// Every Import* field must have a matching checkbox in the migrator view and a
// matching read in parseImportOptions; TestImportOptionsHaveFormCheckboxes
// enforces both mechanically.
//
// URL aliases intentionally have no option: an alias without its page is
// meaningless, so they always follow pages and posts.
type ImportOptions struct {
	ImportTags       bool
	ImportCategories bool
	ImportMedia      bool
	ImportPosts      bool
	ImportPages      bool
	ImportMenus      bool
	ImportUsers      bool
	SkipExisting     bool
}

// ImportResult contains the results of an import operation.
//
// Counters and SkippedCounters are the single place that maps these fields onto
// entity types — the totals, the persisted job blob, the admin stats panel and
// the event metadata all derive from them, so adding a counter is a one-site
// edit. TestImportResultCountersCoverAllImportedFields fails if a new field is
// added without extending the map.
type ImportResult struct {
	TagsImported       int `json:"tags_imported"`
	CategoriesImported int `json:"categories_imported"`
	MediaImported      int `json:"media_imported"`
	PostsImported      int `json:"posts_imported"`
	PagesImported      int `json:"pages_imported"`
	MenusImported      int `json:"menus_imported"`
	MenuItemsImported  int `json:"menu_items_imported"`
	RedirectsImported  int `json:"redirects_imported"`
	AliasesImported    int `json:"aliases_imported"`
	UsersImported      int `json:"users_imported"`

	TagsSkipped       int `json:"tags_skipped"`
	CategoriesSkipped int `json:"categories_skipped"`
	MediaSkipped      int `json:"media_skipped"`
	PostsSkipped      int `json:"posts_skipped"`
	PagesSkipped      int `json:"pages_skipped"`
	MenusSkipped      int `json:"menus_skipped"`
	// MenuItemsSkipped counts links already present in a reused menu. Menu
	// items have no uniqueness constraint, so a re-run would otherwise append
	// a second copy of every link.
	MenuItemsSkipped int `json:"menu_items_skipped"`
	RedirectsSkipped int `json:"redirects_skipped"`
	UsersSkipped     int `json:"users_skipped"`

	// Errors records things that failed but should have worked: a row that
	// could not be created, a table that exists but could not be read.
	Errors []string `json:"errors,omitempty"`

	// Notices records expected, informational outcomes: an optional source
	// table the site does not have, content deliberately out of scope, a link
	// form with no oCMS equivalent.
	//
	// These are kept apart from Errors because reporting them as errors makes a
	// perfectly healthy import look broken — a stock Drupal site with no body
	// field and some translations would report "3 errors" having done exactly
	// what it was asked to.
	Notices []string `json:"notices,omitempty"`

	// Summaries records end-of-stage aggregates, uncapped. See AddSummary for
	// why these must not share the per-item budget.
	Summaries []string `json:"summaries,omitempty"`

	// ErrorsOmitted and NoticesOmitted count messages dropped past the cap.
	// Without them 101 failures and 100,000 failures both rendered as "100
	// errors", so a total failure read as a 1% failure.
	ErrorsOmitted  int `json:"errors_omitted,omitempty"`
	NoticesOmitted int `json:"notices_omitted,omitempty"`
}

// MaxTrackedMessages caps the number of retained per-item errors and notices so
// a badly broken source database cannot grow the result — and the persisted job
// row — without bound.
const MaxTrackedMessages = 100

// AddError appends a per-item error, capping the retained list. Once the cap is
// reached the final entry becomes a truncation marker, so "no more errors" is
// distinguishable from "we stopped recording them".
func (r *ImportResult) AddError(format string, args ...any) {
	r.Errors = appendCapped(r.Errors, &r.ErrorsOmitted, format, args...)
}

// AddNotice appends an informational message about expected, non-fatal
// behaviour. Use this rather than AddError for anything the operator does not
// need to act on.
func (r *ImportResult) AddNotice(format string, args ...any) {
	r.Notices = appendCapped(r.Notices, &r.NoticesOmitted, format, args...)
}

// AddSummary records an end-of-stage aggregate — "N files were skipped", "N
// embeds were removed from page bodies".
//
// Summaries are kept outside the capped list on purpose. They are emitted after
// the per-item loops, so when a systematic failure produced a per-item message
// for every file the cap was already exhausted and precisely the messages that
// explained the failure were the ones dropped. One of them reports that
// unresolvable media embeds were deleted from page bodies, which is the exact
// silent content loss the importer sets out to make visible.
func (r *ImportResult) AddSummary(format string, args ...any) {
	r.Summaries = append(r.Summaries, fmt.Sprintf(format, args...))
}

// appendCapped appends a formatted message, counting anything dropped past the
// cap so "no more errors" stays distinguishable from "we stopped recording".
func appendCapped(messages []string, omitted *int, format string, args ...any) []string {
	if len(messages) >= MaxTrackedMessages {
		*omitted++
		return messages
	}
	return append(messages, fmt.Sprintf(format, args...))
}

// Counters returns imported counts keyed by entity type.
func (r *ImportResult) Counters() map[EntityType]int {
	return map[EntityType]int{
		EntityMenuItem: r.MenuItemsImported,
		EntityMenu:     r.MenusImported,
		EntityRedirect: r.RedirectsImported,
		EntityAlias:    r.AliasesImported,
		EntityPost:     r.PostsImported,
		EntityPage:     r.PagesImported,
		EntityTag:      r.TagsImported,
		EntityCategory: r.CategoriesImported,
		EntityMedia:    r.MediaImported,
		EntityUser:     r.UsersImported,
	}
}

// SkippedCounters returns skipped counts keyed by entity type. Aliases are
// never skipped independently of their parent, so they are absent.
func (r *ImportResult) SkippedCounters() map[EntityType]int {
	return map[EntityType]int{
		EntityMenuItem: r.MenuItemsSkipped,
		EntityMenu:     r.MenusSkipped,
		EntityRedirect: r.RedirectsSkipped,
		EntityPost:     r.PostsSkipped,
		EntityPage:     r.PagesSkipped,
		EntityTag:      r.TagsSkipped,
		EntityCategory: r.CategoriesSkipped,
		EntityMedia:    r.MediaSkipped,
		EntityUser:     r.UsersSkipped,
	}
}

// TotalImported returns the total number of items imported.
func (r *ImportResult) TotalImported() int {
	return sumCounters(r.Counters())
}

// TotalSkipped returns the total number of items skipped.
func (r *ImportResult) TotalSkipped() int {
	return sumCounters(r.SkippedCounters())
}

// HasErrors returns true if anything failed during the import.
func (r *ImportResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasNotices returns true if the import recorded informational messages.
//
// Summaries count: they are notices that were moved off the capped list so a
// systematic per-item failure could not bury them, not a different kind of
// message. finishJob folds them back in ahead of the per-item notices.
func (r *ImportResult) HasNotices() bool {
	return len(r.Notices) > 0 || len(r.Summaries) > 0
}

// sumCounters totals a counter map.
func sumCounters(counters map[EntityType]int) int {
	total := 0
	for _, n := range counters {
		total += n
	}
	return total
}
