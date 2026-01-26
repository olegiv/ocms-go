// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// ListSourcesData contains data for the source list template.
type ListSourcesData struct {
	Sources []Source
}

// SourceFormData contains data for the source form template.
type SourceFormData struct {
	Source         Source
	ConfigFields   []ConfigField
	Config         map[string]string
	ImportedCounts map[string]int // Counts of imported items by entity type
}

// handleListSources handles GET /admin/migrator - displays available sources.
func (m *Module) handleListSources(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	data := ListSourcesData{
		Sources: ListSources(),
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_migrator_list", render.TemplateData{
		Title: i18n.T(lang, "migrator.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "migrator.title"), URL: "/admin/migrator", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// sessionKeyMigratorConfig is the session key for storing migrator config between requests.
const sessionKeyMigratorConfig = "migrator_config"

// handleSourceForm handles GET /admin/migrator/{source} - displays source import form.
func (m *Module) handleSourceForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return
	}

	// Get imported item counts for this source
	importedCounts, _ := m.getImportedCounts(r.Context(), sourceName)

	data := SourceFormData{
		Source:         source,
		ConfigFields:   source.ConfigFields(),
		Config:         make(map[string]string),
		ImportedCounts: importedCounts,
	}

	// Try to restore config from session (after failed test/import)
	if savedConfig := m.ctx.Render.PopSessionData(r, sessionKeyMigratorConfig); savedConfig != nil {
		data.Config = savedConfig
	} else {
		// Set default values only if no saved config
		for _, field := range data.ConfigFields {
			if field.Default != "" {
				data.Config[field.Name] = field.Default
			}
		}
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_migrator_source", render.TemplateData{
		Title: i18n.T(lang, "migrator.import_from", source.DisplayName()),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "migrator.title"), URL: "/admin/migrator"},
			{Label: source.DisplayName(), URL: "/admin/migrator/" + sourceName, Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleTestConnection handles POST /admin/migrator/{source}/test - tests connection.
func (m *Module) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
		return
	}

	// Collect config from form
	cfg := make(map[string]string)
	for _, field := range source.ConfigFields() {
		cfg[field.Name] = r.FormValue(field.Name)
	}

	// Test connection
	if err := source.TestConnection(cfg); err != nil {
		m.ctx.Logger.Error("connection test failed", "source", sourceName, "error", err)
		// Save config to session so form values are preserved
		m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, cfg)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_connection")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
		return
	}

	// Save config on success too, so user can proceed with import
	m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, cfg)
	m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.success_connection"), "success")
	http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
}

// handleImport handles POST /admin/migrator/{source}/import - runs import.
func (m *Module) handleImport(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
		return
	}

	// Collect config from form
	cfg := make(map[string]string)
	for _, field := range source.ConfigFields() {
		cfg[field.Name] = r.FormValue(field.Name)
	}

	// Build import options
	opts := ImportOptions{
		ImportTags:   r.FormValue("import_tags") == "on",
		ImportMedia:  r.FormValue("import_media") == "on",
		ImportPosts:  r.FormValue("import_posts") == "on",
		ImportUsers:  r.FormValue("import_users") == "on",
		SkipExisting: r.FormValue("skip_existing") == "on",
	}

	m.ctx.Logger.Info("starting import",
		"source", sourceName,
		"user", user.Email,
		"import_tags", opts.ImportTags,
		"import_media", opts.ImportMedia,
		"import_posts", opts.ImportPosts,
		"import_users", opts.ImportUsers,
		"skip_existing", opts.SkipExisting,
	)

	// Run import (pass module as tracker for recording imported items)
	result, err := source.Import(r.Context(), m.ctx.DB, cfg, opts, m)
	if err != nil {
		m.ctx.Logger.Error("import failed", "source", sourceName, "error", err)
		// Save config to session so form values are preserved
		m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, cfg)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_import")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
		return
	}

	// Log results
	m.ctx.Logger.Info("import completed",
		"source", sourceName,
		"tags_imported", result.TagsImported,
		"media_imported", result.MediaImported,
		"posts_imported", result.PostsImported,
		"users_imported", result.UsersImported,
		"tags_skipped", result.TagsSkipped,
		"media_skipped", result.MediaSkipped,
		"posts_skipped", result.PostsSkipped,
		"users_skipped", result.UsersSkipped,
		"errors", len(result.Errors),
	)

	// Log each error for debugging
	for i, errMsg := range result.Errors {
		m.ctx.Logger.Error("import error", "source", sourceName, "index", i, "message", errMsg)
	}

	// Build success message
	msg := i18n.T(lang, "migrator.success_import",
		result.PostsImported, result.TagsImported, result.MediaImported)

	if result.TotalSkipped() > 0 {
		msg += " " + i18n.T(lang, "migrator.skipped_count", result.TotalSkipped())
	}

	if result.HasErrors() {
		msg += " " + i18n.T(lang, "migrator.errors_count", len(result.Errors))
	}

	m.ctx.Render.SetFlash(r, msg, "success")
	http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
}

// handleDeleteImported handles POST /admin/migrator/{source}/delete - deletes all imported content.
func (m *Module) handleDeleteImported(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("deleting imported content", "source", sourceName, "user", user.Email)

	// Delete all imported items for this source
	deleted, err := m.deleteImportedItems(r.Context(), sourceName)
	if err != nil {
		m.ctx.Logger.Error("delete failed", "source", sourceName, "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_delete")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("deleted imported content",
		"source", sourceName,
		"pages", deleted["page"],
		"tags", deleted["tag"],
		"users", deleted["user"],
	)

	msg := i18n.T(lang, "migrator.success_delete", deleted["page"], deleted["tag"], deleted["user"])
	m.ctx.Render.SetFlash(r, msg, "success")
	http.Redirect(w, r, "/admin/migrator/"+sourceName, http.StatusSeeOther)

	_ = source // used for validation
}

// TrackImportedItem records an imported item for later deletion.
func (m *Module) TrackImportedItem(ctx context.Context, source, entityType string, entityID int64) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO migrator_imported_items (source, entity_type, entity_id, created_at)
		VALUES (?, ?, ?, ?)
	`, source, entityType, entityID, time.Now())
	return err
}

// getImportedCounts returns counts of imported items by entity type for a source.
func (m *Module) getImportedCounts(ctx context.Context, source string) (map[string]int, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_type, COUNT(*) as cnt
		FROM migrator_imported_items
		WHERE source = ?
		GROUP BY entity_type
	`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var entityType string
		var count int
		if err := rows.Scan(&entityType, &count); err != nil {
			return nil, err
		}
		counts[entityType] = count
	}
	return counts, rows.Err()
}

// getImportedItems returns all imported item IDs of a given type for a source.
func (m *Module) getImportedItems(ctx context.Context, source, entityType string) ([]int64, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_id FROM migrator_imported_items
		WHERE source = ? AND entity_type = ?
	`, source, entityType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// deleteImportedItems deletes all content imported from a source.
func (m *Module) deleteImportedItems(ctx context.Context, source string) (map[string]int, error) {
	queries := store.New(m.ctx.DB)
	deleted := make(map[string]int)

	// Delete pages first (they reference tags)
	pageIDs, err := m.getImportedItems(ctx, source, "page")
	if err != nil {
		return nil, err
	}
	for _, id := range pageIDs {
		// Clear page associations
		_ = queries.ClearPageTags(ctx, id)
		_ = queries.ClearPageCategories(ctx, id)
		if err := queries.DeletePage(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete page", "id", id, "error", err)
		} else {
			deleted["page"]++
		}
	}

	// Delete tags
	tagIDs, err := m.getImportedItems(ctx, source, "tag")
	if err != nil {
		return nil, err
	}
	for _, id := range tagIDs {
		if err := queries.DeleteTag(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete tag", "id", id, "error", err)
		} else {
			deleted["tag"]++
		}
	}

	// Delete users
	userIDs, err := m.getImportedItems(ctx, source, "user")
	if err != nil {
		return nil, err
	}
	for _, id := range userIDs {
		if err := queries.DeleteUser(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete user", "id", id, "error", err)
		} else {
			deleted["user"]++
		}
	}

	// Clear tracking table for this source
	_, err = m.ctx.DB.ExecContext(ctx, `DELETE FROM migrator_imported_items WHERE source = ?`, source)
	if err != nil {
		return deleted, err
	}

	return deleted, nil
}
