package developer

import (
	"net/http"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// DashboardData contains data for the developer dashboard template
type DashboardData struct {
	Counts map[string]int
	Total  int
}

// handleDashboard handles GET /admin/developer - shows the developer dashboard
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	counts, err := m.getTrackedCounts(r.Context())
	if err != nil {
		m.ctx.Logger.Error("failed to get tracked counts", "error", err)
		counts = make(map[string]int)
	}

	total := 0
	for _, c := range counts {
		total += c
	}

	data := DashboardData{
		Counts: counts,
		Total:  total,
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_developer", render.TemplateData{
		Title: i18n.T(lang, "developer.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "developer.title"), URL: "/admin/developer", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleGenerate handles POST /admin/developer/generate - generates test data
func (m *Module) handleGenerate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	lang := m.ctx.Render.GetAdminLang(r)

	// Get all active languages
	queries := store.New(m.ctx.DB)
	languages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		m.ctx.Logger.Error("failed to list languages", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_get_languages")+": "+err.Error())
		return
	}

	if len(languages) == 0 {
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_no_languages"))
		return
	}

	m.ctx.Logger.Info("starting test data generation",
		"user", user.Email,
		"languages", len(languages))

	// Generate tags
	tagIDs, err := m.generateTags(ctx, languages)
	if err != nil {
		m.ctx.Logger.Error("failed to generate tags", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_generate_tags")+": "+err.Error())
		return
	}
	m.ctx.Logger.Info("generated tags", "count", len(tagIDs))

	// Generate categories
	catIDs, err := m.generateCategories(ctx, languages)
	if err != nil {
		m.ctx.Logger.Error("failed to generate categories", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_generate_categories")+": "+err.Error())
		return
	}
	m.ctx.Logger.Info("generated categories", "count", len(catIDs))

	// Generate media
	mediaIDs, err := m.generateMedia(ctx, languages, user.ID)
	if err != nil {
		m.ctx.Logger.Error("failed to generate media", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_generate_media")+": "+err.Error())
		return
	}
	m.ctx.Logger.Info("generated media", "count", len(mediaIDs))

	// Generate pages
	pageIDs, err := m.generatePages(ctx, languages, tagIDs, catIDs, mediaIDs, user.ID)
	if err != nil {
		m.ctx.Logger.Error("failed to generate pages", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_generate_pages")+": "+err.Error())
		return
	}
	m.ctx.Logger.Info("generated pages", "count", len(pageIDs))

	// Generate menu items in Main Menu
	menuItemIDs, err := m.generateMenuItems(ctx, pageIDs)
	if err != nil {
		m.ctx.Logger.Error("failed to generate menu items", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_generate_menu")+": "+err.Error())
		return
	}
	m.ctx.Logger.Info("generated menu items", "count", len(menuItemIDs))

	// Invalidate menu cache after adding menu items
	m.ctx.Render.InvalidateMenuCache("")

	// Success message
	msg := i18n.T(lang, "developer.success_generate",
		len(tagIDs), len(catIDs), len(mediaIDs), len(pageIDs), len(menuItemIDs), len(languages))

	m.setFlashAndRedirect(w, r, "success", msg)
}

// handleDelete handles POST /admin/developer/delete - deletes all generated data
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	lang := m.ctx.Render.GetAdminLang(r)

	m.ctx.Logger.Info("deleting all generated test data", "user", user.Email)

	if err := m.deleteAllGeneratedItems(ctx); err != nil {
		m.ctx.Logger.Error("failed to delete generated items", "error", err)
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "developer.error_delete")+": "+err.Error())
		return
	}

	// Invalidate menu cache after deleting menu items
	m.ctx.Render.InvalidateMenuCache("")

	m.ctx.Logger.Info("deleted all generated test data")
	m.setFlashAndRedirect(w, r, "success", i18n.T(lang, "developer.success_delete"))
}

// setFlashAndRedirect sets a flash message and redirects to the dashboard
func (m *Module) setFlashAndRedirect(w http.ResponseWriter, r *http.Request, msgType, message string) {
	m.ctx.Render.SetFlash(r, message, msgType)
	http.Redirect(w, r, "/admin/developer", http.StatusSeeOther)
}
