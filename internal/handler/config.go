package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/cache"
	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// ConfigHandler handles configuration management routes.
type ConfigHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	cacheManager   *cache.Manager
}

// NewConfigHandler creates a new ConfigHandler.
func NewConfigHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, cm *cache.Manager) *ConfigHandler {
	return &ConfigHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		cacheManager:   cm,
	}
}

// ConfigItem represents a config item with display metadata.
type ConfigItem struct {
	Key         string
	Value       string
	Type        string
	Description string
	Label       string
}

// ConfigFormData holds data for the config form template.
type ConfigFormData struct {
	Items  []ConfigItem
	Errors map[string]string
}

// List handles GET /admin/config - displays configuration settings.
func (h *ConfigHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get all config items
	configs, err := h.queries.ListConfig(r.Context())
	if err != nil {
		slog.Error("failed to list config", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := ConfigFormData{
		Items:  toConfigItems(configs, lang, nil),
		Errors: make(map[string]string),
	}

	h.renderConfigPage(w, r, user, lang, data)
}

// Update handles PUT /admin/config - updates configuration values.
func (h *ConfigHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, i18n.T(lang, "error.invalid_form"), "error")
		http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
		return
	}

	// Get all config items to know their types
	configs, err := h.queries.ListConfig(r.Context())
	if err != nil {
		slog.Error("failed to list config", "error", err)
		h.renderer.SetFlash(r, i18n.T(lang, "error.loading_config"), "error")
		http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
		return
	}

	validationErrors := make(map[string]string)
	now := time.Now()
	userID := middleware.GetUserID(r)
	updatedBy := sql.NullInt64{Int64: userID, Valid: userID > 0}

	// Update each config item
	for _, cfg := range configs {
		// Skip active_theme - managed in Themes settings
		if cfg.Key == "active_theme" {
			continue
		}

		value := r.FormValue(cfg.Key)

		// Validate based on type
		if cfg.Type == model.ConfigTypeInt {
			if value != "" {
				if _, err := strconv.Atoi(value); err != nil {
					validationErrors[cfg.Key] = i18n.T(lang, "error.invalid_number")
					continue
				}
			}
		} else if cfg.Type == model.ConfigTypeBool {
			// Checkbox: if not present, it's false
			if value == "" || value == "false" {
				value = "false"
			} else {
				value = "true"
			}
		}

		// Update the config value
		_, err := h.queries.UpdateConfigValue(r.Context(), store.UpdateConfigValueParams{
			Key:       cfg.Key,
			Value:     value,
			UpdatedAt: now,
			UpdatedBy: updatedBy,
		})
		if err != nil {
			slog.Error("failed to update config", "key", cfg.Key, "error", err)
			validationErrors[cfg.Key] = i18n.T(lang, "error.saving_value")
		}
	}

	if len(validationErrors) > 0 {
		// Build form values map for re-rendering
		formValues := make(map[string]string)
		for _, cfg := range configs {
			formValues[cfg.Key] = r.FormValue(cfg.Key)
		}

		data := ConfigFormData{
			Items:  toConfigItems(configs, lang, formValues),
			Errors: validationErrors,
		}

		h.renderConfigPage(w, r, user, lang, data)
		return
	}

	// Invalidate config cache
	if h.cacheManager != nil {
		h.cacheManager.InvalidateConfig()
	}

	slog.Info("config updated", "updated_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, i18n.T(lang, "msg.config_saved"), "success")
	http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
}

// configKeyToLabel converts a config key to a translated label.
// If no translation exists, generates a readable label from the key.
func configKeyToLabel(key string, lang string) string {
	// Try to get translation for this config key
	translationKey := "config." + key
	translated := i18n.T(lang, translationKey)

	// If translation exists (different from key), return it
	if translated != translationKey {
		return translated
	}

	// Fallback: replace underscores with spaces and capitalize each word
	words := strings.Split(key, "_")
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(string(word[0])) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// configKeyToDescription returns a translated description for a config key.
// Falls back to the database description if no translation exists.
func configKeyToDescription(key string, dbDescription string, lang string) string {
	// Try to get translation for this config key's hint
	translationKey := "config." + key + "_hint"
	translated := i18n.T(lang, translationKey)

	// If translation exists (different from key), return it
	if translated != translationKey {
		return translated
	}

	// Fallback to database description
	return dbDescription
}

// configBreadcrumbs returns the standard breadcrumbs for config pages.
func configBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "config.title"), URL: "/admin/config", Active: true},
	}
}

// renderConfigPage renders the config page with standard template data.
func (h *ConfigHandler) renderConfigPage(w http.ResponseWriter, r *http.Request, user any, lang string, data ConfigFormData) {
	h.renderer.RenderPage(w, r, "admin/config", render.TemplateData{
		Title:       i18n.T(lang, "config.title"),
		User:        user,
		Data:        data,
		Breadcrumbs: configBreadcrumbs(lang),
	})
}

// toConfigItems converts store.Config slice to ConfigItem slice.
// It skips keys managed elsewhere (like active_theme).
// If formValues is provided, it uses those values instead of database values.
func toConfigItems(configs []store.Config, lang string, formValues map[string]string) []ConfigItem {
	items := make([]ConfigItem, 0, len(configs))
	for _, cfg := range configs {
		// Skip active_theme - managed in Themes settings
		if cfg.Key == "active_theme" {
			continue
		}
		value := cfg.Value
		if formValues != nil {
			if v, ok := formValues[cfg.Key]; ok {
				value = v
			} else if cfg.Type == model.ConfigTypeBool {
				value = "false"
			}
		}
		items = append(items, ConfigItem{
			Key:         cfg.Key,
			Value:       value,
			Type:        cfg.Type,
			Description: configKeyToDescription(cfg.Key, cfg.Description, lang),
			Label:       configKeyToLabel(cfg.Key, lang),
		})
	}
	return items
}
