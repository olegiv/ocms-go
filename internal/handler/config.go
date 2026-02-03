// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// configKeyOrder defines the display order for config keys.
// Keys not in this list will appear after these, sorted alphabetically.
var configKeyOrder = map[string]int{
	model.ConfigKeySiteName:        1,
	model.ConfigKeySiteDescription: 2,
	model.ConfigKeySiteURL:         3,
	model.ConfigKeyDefaultOGImage:  4,
	model.ConfigKeyCopyright:       5,
	model.ConfigKeyPoweredBy:       6,
	model.ConfigKeyPostsPerPage:    7,
	model.ConfigKeyAdminEmail:      8,
}

// sortConfigs sorts config items according to configKeyOrder.
func sortConfigs(configs []store.Config) {
	sort.Slice(configs, func(i, j int) bool {
		orderI, hasI := configKeyOrder[configs[i].Key]
		orderJ, hasJ := configKeyOrder[configs[j].Key]

		// Both have defined order: sort by order
		if hasI && hasJ {
			return orderI < orderJ
		}
		// Only i has order: i comes first
		if hasI {
			return true
		}
		// Only j has order: j comes first
		if hasJ {
			return false
		}
		// Neither has order: sort alphabetically
		return configs[i].Key < configs[j].Key
	})
}

// mergeWithStandardFields ensures all standard config fields are present,
// using DB values when available and defaults otherwise.
func mergeWithStandardFields(dbConfigs []store.Config) []store.Config {
	// Create a map of existing DB configs
	existing := make(map[string]store.Config)
	for _, cfg := range dbConfigs {
		existing[cfg.Key] = cfg
	}

	// Build result with all standard fields
	var result []store.Config
	seen := make(map[string]bool)

	for _, def := range model.StandardConfigFields {
		if cfg, ok := existing[def.Key]; ok {
			// Use existing DB value
			result = append(result, cfg)
		} else {
			// Create placeholder from standard definition
			result = append(result, store.Config{
				Key:         def.Key,
				Value:       def.DefaultValue,
				Type:        def.Type,
				Description: def.Description,
			})
		}
		seen[def.Key] = true
	}

	// Add any additional DB configs not in standard fields (e.g., theme settings)
	for _, cfg := range dbConfigs {
		if !seen[cfg.Key] {
			result = append(result, cfg)
		}
	}

	return result
}

// ConfigHandler handles configuration management routes.
type ConfigHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	cacheManager   *cache.Manager
	eventService   *service.EventService
}

// NewConfigHandler creates a new ConfigHandler.
func NewConfigHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, cm *cache.Manager) *ConfigHandler {
	return &ConfigHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		cacheManager:   cm,
		eventService:   service.NewEventService(db),
	}
}

// ConfigItem represents a config item with display metadata.
type ConfigItem struct {
	Key          string
	Value        string
	Type         string
	Description  string
	Label        string
	Translatable bool
}

// ConfigTranslationValue holds a translation value for a specific language.
type ConfigTranslationValue struct {
	LanguageID   int64
	LanguageCode string
	LanguageName string
	Value        string
}

// TranslatableConfigItem holds a translatable config item with its translations per language.
type TranslatableConfigItem struct {
	Key          string
	Label        string
	Description  string
	Type         string
	Translations []ConfigTranslationValue // Values per language (includes default lang)
}

// ConfigLanguage represents a language option for the config form.
type ConfigLanguage struct {
	ID        int64
	Code      string
	Name      string
	IsDefault bool
}

// ConfigFormData holds data for the config form template.
type ConfigFormData struct {
	Items                []ConfigItem             // Non-translatable items
	TranslatableItems    []TranslatableConfigItem // Translatable items with language tabs
	Languages            []ConfigLanguage         // Available languages for translation
	Errors               map[string]string
	HasMultipleLanguages bool
}

// List handles GET /admin/config - displays configuration settings.
func (h *ConfigHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get all config items from DB
	dbConfigs, err := h.queries.ListConfig(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to list config", "error", err)
		return
	}

	// Merge with standard fields to show all expected fields even on empty site
	configs := mergeWithStandardFields(dbConfigs)
	sortConfigs(configs)

	// Get active languages
	languages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		languages = nil // Continue without translations
	}

	// Get all config translations
	allTranslations, err := h.queries.ListAllConfigTranslations(r.Context())
	if err != nil {
		slog.Error("failed to list config translations", "error", err)
		allTranslations = nil
	}

	// Build translations map: key -> langID -> value
	translationsMap := make(map[string]map[int64]string)
	for _, t := range allTranslations {
		if translationsMap[t.ConfigKey] == nil {
			translationsMap[t.ConfigKey] = make(map[int64]string)
		}
		translationsMap[t.ConfigKey][t.LanguageID] = t.Value
	}

	data := h.buildConfigFormData(configs, languages, translationsMap, lang, nil)

	h.renderConfigPage(w, r, user, lang, data)
}

// Update handles PUT /admin/config - updates configuration values.
func (h *ConfigHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminConfig) {
		return
	}

	// Get all config items from DB
	dbConfigs, err := h.queries.ListConfig(r.Context())
	if err != nil {
		slog.Error("failed to list config", "error", err)
		flashError(w, r, h.renderer, redirectAdminConfig, i18n.T(lang, "error.loading_config"))
		return
	}

	// Merge with standard fields to handle fields that don't exist in DB yet
	configs := mergeWithStandardFields(dbConfigs)
	sortConfigs(configs)

	// Get active languages for translation handling
	languages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		languages = nil
	}

	// Get default language code for new config entries
	defaultLangCode := "en"
	for _, lang := range languages {
		if lang.IsDefault {
			defaultLangCode = lang.Code
			break
		}
	}

	validationErrors := make(map[string]string)
	now := time.Now()
	userID := middleware.GetUserID(r)
	updatedBy := sql.NullInt64{Int64: userID, Valid: userID > 0}

	// Update each config item
	for _, cfg := range configs {
		// Skip theme-related config - managed in Themes settings
		if cfg.Key == "active_theme" || strings.HasPrefix(cfg.Key, "theme_settings_") {
			continue
		}

		// Handle translatable config items
		if model.IsTranslatableConfigKey(cfg.Key) && len(languages) > 0 {
			// Ensure the config key exists in the config table first (for FK constraint)
			_, err := h.queries.UpsertConfig(r.Context(), store.UpsertConfigParams{
				Key:          cfg.Key,
				Value:        cfg.Value,
				Type:         cfg.Type,
				Description:  cfg.Description,
				LanguageCode: defaultLangCode,
				UpdatedAt:    now,
				UpdatedBy:    updatedBy,
			})
			if err != nil {
				slog.Error("failed to upsert config for translation", "key", cfg.Key, "error", err)
			}

			// Save translations for each language
			for _, language := range languages {
				// Form field name: key_langcode (e.g., site_name_en, site_name_ru)
				fieldName := cfg.Key + "_" + language.Code
				value := r.FormValue(fieldName)

				// Save translation
				_, err := h.queries.UpsertConfigTranslation(r.Context(), store.UpsertConfigTranslationParams{
					ConfigKey:  cfg.Key,
					LanguageID: language.ID,
					Value:      value,
					UpdatedAt:  now,
					UpdatedBy:  updatedBy,
				})
				if err != nil {
					slog.Error("failed to save config translation", "key", cfg.Key, "lang", language.Code, "error", err)
					validationErrors[fieldName] = i18n.T(lang, "error.saving_value")
				}
			}
			continue
		}

		// Handle non-translatable config items
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

		// Upsert the config value (creates if not exists, updates if exists)
		_, err := h.queries.UpsertConfig(r.Context(), store.UpsertConfigParams{
			Key:          cfg.Key,
			Value:        value,
			Type:         cfg.Type,
			Description:  cfg.Description,
			LanguageCode: defaultLangCode,
			UpdatedAt:    now,
			UpdatedBy:    updatedBy,
		})
		if err != nil {
			slog.Error("failed to upsert config", "key", cfg.Key, "error", err)
			validationErrors[cfg.Key] = i18n.T(lang, "error.saving_value")
		}
	}

	if len(validationErrors) > 0 {
		// Get all config translations for re-rendering
		allTranslations, _ := h.queries.ListAllConfigTranslations(r.Context())
		translationsMap := make(map[string]map[int64]string)
		for _, t := range allTranslations {
			if translationsMap[t.ConfigKey] == nil {
				translationsMap[t.ConfigKey] = make(map[int64]string)
			}
			translationsMap[t.ConfigKey][t.LanguageID] = t.Value
		}

		// Build form values map for re-rendering (including translation values from form)
		formValues := make(map[string]string)
		for _, cfg := range configs {
			if model.IsTranslatableConfigKey(cfg.Key) {
				for _, language := range languages {
					fieldName := cfg.Key + "_" + language.Code
					formValues[fieldName] = r.FormValue(fieldName)
				}
			} else {
				formValues[cfg.Key] = r.FormValue(cfg.Key)
			}
		}

		data := h.buildConfigFormData(configs, languages, translationsMap, lang, formValues)
		data.Errors = validationErrors

		h.renderConfigPage(w, r, user, lang, data)
		return
	}

	// Invalidate config cache
	if h.cacheManager != nil {
		h.cacheManager.InvalidateConfig()
	}

	slog.Info("config updated", "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogConfigEvent(r.Context(), model.EventLevelInfo, "Configuration updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), nil)
	flashSuccess(w, r, h.renderer, redirectAdminConfig, i18n.T(lang, "msg.config_saved"))
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
		if word != "" {
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
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "config.title"), URL: redirectAdminConfig, Active: true},
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

// buildConfigFormData builds the form data for the config page, separating translatable
// and non-translatable items, and including translation values per language.
func (h *ConfigHandler) buildConfigFormData(
	configs []store.Config,
	languages []store.Language,
	translationsMap map[string]map[int64]string,
	adminLang string,
	formValues map[string]string,
) ConfigFormData {
	data := ConfigFormData{
		Items:                make([]ConfigItem, 0),
		TranslatableItems:    make([]TranslatableConfigItem, 0),
		Languages:            make([]ConfigLanguage, 0, len(languages)),
		Errors:               make(map[string]string),
		HasMultipleLanguages: len(languages) > 1,
	}

	// Build language list
	for _, lang := range languages {
		data.Languages = append(data.Languages, ConfigLanguage{
			ID:        lang.ID,
			Code:      lang.Code,
			Name:      lang.Name,
			IsDefault: lang.IsDefault,
		})
	}

	// Process config items
	for _, cfg := range configs {
		// Skip theme-related config - managed in Themes settings
		if cfg.Key == "active_theme" || strings.HasPrefix(cfg.Key, "theme_settings_") {
			continue
		}

		// Handle translatable items
		if model.IsTranslatableConfigKey(cfg.Key) && len(languages) > 0 {
			item := TranslatableConfigItem{
				Key:          cfg.Key,
				Label:        configKeyToLabel(cfg.Key, adminLang),
				Description:  configKeyToDescription(cfg.Key, cfg.Description, adminLang),
				Type:         cfg.Type,
				Translations: make([]ConfigTranslationValue, 0, len(languages)),
			}

			// Add translation value for each language
			for _, lang := range languages {
				var value string
				fieldName := cfg.Key + "_" + lang.Code

				// Use form value if provided (for re-rendering after validation errors)
				if formValues != nil {
					if v, ok := formValues[fieldName]; ok {
						value = v
					}
				} else {
					// Use saved translation value or default config value
					if trans, ok := translationsMap[cfg.Key]; ok {
						if v, ok := trans[lang.ID]; ok {
							value = v
						}
					}
					// If no translation, use the base config value for default language only
					if value == "" && lang.IsDefault {
						value = cfg.Value
					}
				}

				item.Translations = append(item.Translations, ConfigTranslationValue{
					LanguageID:   lang.ID,
					LanguageCode: lang.Code,
					LanguageName: lang.Name,
					Value:        value,
				})
			}

			data.TranslatableItems = append(data.TranslatableItems, item)
			continue
		}

		// Handle non-translatable items
		value := cfg.Value
		if formValues != nil {
			if v, ok := formValues[cfg.Key]; ok {
				value = v
			} else if cfg.Type == model.ConfigTypeBool {
				value = "false"
			}
		}
		data.Items = append(data.Items, ConfigItem{
			Key:          cfg.Key,
			Value:        value,
			Type:         cfg.Type,
			Description:  configKeyToDescription(cfg.Key, cfg.Description, adminLang),
			Label:        configKeyToLabel(cfg.Key, adminLang),
			Translatable: false,
		})
	}

	return data
}
