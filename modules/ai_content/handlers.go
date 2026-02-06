// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const usagePerPage = 25

// handleDashboard handles GET /admin/ai-content - shows dashboard with usage stats.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	stats, err := loadUsageStats(m.ctx.DB)
	if err != nil {
		m.ctx.Logger.Error("failed to load AI usage stats", "error", err)
		stats = &UsageStats{ByProvider: make(map[string]*ProviderUsageStats)}
	}

	// Load recent records for dashboard
	recentRecords, _, err := loadUsageRecords(m.ctx.DB, 10, 0)
	if err != nil {
		m.ctx.Logger.Error("failed to load recent usage records", "error", err)
	}
	stats.RecentRecords = recentRecords

	// Check if any provider is configured
	enabledProvider, _ := loadEnabledProvider(m.ctx.DB)
	hasProvider := enabledProvider != nil

	// Get all provider settings for status display
	allSettings, _ := loadAllSettings(m.ctx.DB)
	settingsMap := make(map[string]*ProviderSettings)
	for _, s := range allSettings {
		settingsMap[s.Provider] = s
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_ai_content", render.TemplateData{
		Title: i18n.T(lang, "ai_content.title"),
		User:  user,
		Data: map[string]any{
			"Stats":       stats,
			"HasProvider": hasProvider,
			"Providers":   AllProviders(),
			"SettingsMap": settingsMap,
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "ai_content.title"), URL: "/admin/ai-content", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSettings handles GET /admin/ai-content/settings - shows provider settings.
func (m *Module) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	allSettings, err := loadAllSettings(m.ctx.DB)
	if err != nil {
		m.ctx.Logger.Error("failed to load AI settings", "error", err)
	}

	settingsMap := make(map[string]*ProviderSettings)
	for _, s := range allSettings {
		settingsMap[s.Provider] = s
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_ai_content_settings", render.TemplateData{
		Title: i18n.T(lang, "ai_content.settings_title"),
		User:  user,
		Data: map[string]any{
			"Providers":   AllProviders(),
			"SettingsMap": settingsMap,
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "ai_content.title"), URL: "/admin/ai-content"},
			{Label: i18n.T(lang, "ai_content.settings_title"), URL: "/admin/ai-content/settings", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSaveSettings handles POST /admin/ai-content/settings - saves provider settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	providerID := r.FormValue("provider")
	if providerID == "" {
		m.ctx.Render.SetFlash(r, "Provider is required", "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	ps := &ProviderSettings{
		Provider:     providerID,
		APIKey:       strings.TrimSpace(r.FormValue("api_key")),
		Model:        strings.TrimSpace(r.FormValue("model")),
		BaseURL:      strings.TrimSpace(r.FormValue("base_url")),
		IsEnabled:    r.FormValue("is_enabled") == "1",
		ImageEnabled: r.FormValue("image_enabled") == "1",
		ImageModel:   strings.TrimSpace(r.FormValue("image_model")),
	}

	// Validate
	pInfo, err := GetProviderInfo(providerID)
	if err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_unknown_provider"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	if ps.IsEnabled && pInfo.NeedsAPIKey && ps.APIKey == "" {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_api_key_required"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	if ps.IsEnabled && ps.Model == "" {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_model_required"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	if err := saveProviderSettings(m.ctx.DB, ps); err != nil {
		m.ctx.Logger.Error("failed to save AI settings", "error", err, "provider", providerID)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_save"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	m.ctx.Logger.Info("AI content settings updated", "user", user.Email, "provider", providerID, "enabled", ps.IsEnabled)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.success_save"), "success")
	http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
}

// handleTestConnection handles POST /admin/ai-content/settings/test - tests provider connection.
func (m *Module) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	providerID := r.FormValue("provider")
	ps, err := loadProviderSettings(m.ctx.DB, providerID)
	if err != nil || (ps.APIKey == "" && providerID != ProviderOllama) {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_configure_first"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	testReq := ChatRequest{
		Model: ps.Model,
		Messages: []ChatMessage{
			{Role: "user", Content: "Say hello in one word."},
		},
		MaxTokens: 10,
	}

	var testErr error
	client := getProviderClient(ps)
	if client == nil {
		testErr = fmt.Errorf("unsupported provider")
	} else {
		_, testErr = client.ChatCompletion(ctx, ps.APIKey, testReq)
	}

	if testErr != nil {
		m.ctx.Logger.Error("AI connection test failed", "error", testErr, "provider", providerID)
		m.ctx.Render.SetFlash(r, fmt.Sprintf("%s: %s", i18n.T(lang, "ai_content.test_failed"), testErr.Error()), "error")
	} else {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.test_success"), "success")
	}

	http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
}

// handleGenerateForm handles GET /admin/ai-content/generate - shows content generation form.
func (m *Module) handleGenerateForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Load languages for dropdown
	languages, err := m.ctx.Store.ListActiveLanguages(r.Context())
	if err != nil {
		m.ctx.Logger.Error("failed to load languages", "error", err)
	}

	// Load categories for dropdown
	categories, err := m.ctx.Store.ListCategories(r.Context())
	if err != nil {
		m.ctx.Logger.Error("failed to load categories", "error", err)
	}

	// Load tags for selection
	tags, err := m.ctx.Store.ListAllTags(r.Context())
	if err != nil {
		m.ctx.Logger.Error("failed to load tags", "error", err)
	}

	// Check if provider is configured
	enabledProvider, _ := loadEnabledProvider(m.ctx.DB)
	if enabledProvider == nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_no_provider"), "error")
		http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
		return
	}

	// Get all settings for provider selection
	allSettings, _ := loadAllSettings(m.ctx.DB)
	var enabledProviders []*ProviderSettings
	for _, s := range allSettings {
		if s.IsEnabled {
			enabledProviders = append(enabledProviders, s)
		}
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_ai_content_generate", render.TemplateData{
		Title: i18n.T(lang, "ai_content.generate_title"),
		User:  user,
		Data: map[string]any{
			"Languages":        languages,
			"Categories":       categories,
			"Tags":             tags,
			"EnabledProviders": enabledProviders,
			"AllProviders":     AllProviders(),
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "ai_content.title"), URL: "/admin/ai-content"},
			{Label: i18n.T(lang, "ai_content.generate_title"), URL: "/admin/ai-content/generate", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleGenerate handles POST /admin/ai-content/generate - generates content.
func (m *Module) handleGenerate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Parse form input
	input := &GenerateInput{
		Topic:          strings.TrimSpace(r.FormValue("topic")),
		TargetAudience: strings.TrimSpace(r.FormValue("target_audience")),
		Tone:           strings.TrimSpace(r.FormValue("tone")),
		KeyPoints:      strings.TrimSpace(r.FormValue("key_points")),
		LanguageCode:   strings.TrimSpace(r.FormValue("language_code")),
		ContentType:    strings.TrimSpace(r.FormValue("content_type")),
		AdditionalInfo: strings.TrimSpace(r.FormValue("additional_info")),
	}

	if input.Topic == "" {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_topic_required"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Resolve language name
	if input.LanguageCode != "" {
		langObj, err := m.ctx.Store.GetLanguageByCode(r.Context(), input.LanguageCode)
		if err == nil {
			input.LanguageName = langObj.Name
		}
	}
	if input.LanguageName == "" {
		input.LanguageName = "English"
	}

	// Get selected provider
	providerID := strings.TrimSpace(r.FormValue("provider"))
	if providerID == "" {
		enabledProvider, _ := loadEnabledProvider(m.ctx.DB)
		if enabledProvider == nil {
			m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_no_provider"), "error")
			http.Redirect(w, r, "/admin/ai-content/settings", http.StatusSeeOther)
			return
		}
		providerID = enabledProvider.Provider
	}

	settings, err := loadProviderSettings(m.ctx.DB, providerID)
	if err != nil || !settings.IsEnabled {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_provider_not_enabled"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Generate content
	ctx, cancel := context.WithTimeout(r.Context(), httpTimeout)
	defer cancel()

	generated, chatResp, err := m.generateContent(ctx, settings, input)
	if err != nil {
		m.ctx.Logger.Error("AI content generation failed", "error", err, "provider", providerID)
		errMsg := i18n.T(lang, "ai_content.error_generation_failed")
		if chatResp != nil {
			errMsg += fmt.Sprintf(" (tokens used: %d)", chatResp.TotalTokens)
		}
		m.ctx.Render.SetFlash(r, errMsg, "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Log usage
	costUSD := CalculateCost(providerID, settings.Model, chatResp.PromptTokens, chatResp.CompletionTokens)
	usageRecord := &UsageRecord{
		Provider:         providerID,
		Model:            settings.Model,
		Operation:        "text",
		PromptTokens:     int64(chatResp.PromptTokens),
		CompletionTokens: int64(chatResp.CompletionTokens),
		TotalTokens:      int64(chatResp.TotalTokens),
		CostUSD:          costUSD,
		LanguageCode:     input.LanguageCode,
		PageTitle:        generated.Title,
		CreatedBy:        user.ID,
		CreatedAt:        time.Now(),
	}
	if err := logUsage(m.ctx.DB, usageRecord); err != nil {
		m.ctx.Logger.Error("failed to log AI usage", "error", err)
	}

	// Load categories and tags for the preview form
	categories, _ := m.ctx.Store.ListCategories(r.Context())
	tags, _ := m.ctx.Store.ListAllTags(r.Context())
	languages, _ := m.ctx.Store.ListActiveLanguages(r.Context())

	// Get all settings for image generation availability check
	openaiSettings, _ := loadProviderSettings(m.ctx.DB, ProviderOpenAI)
	imageAvailable := openaiSettings != nil && openaiSettings.APIKey != "" && openaiSettings.ImageEnabled

	if err := m.ctx.Render.Render(w, r, "admin/module_ai_content_preview", render.TemplateData{
		Title: i18n.T(lang, "ai_content.preview_title"),
		User:  user,
		Data: map[string]any{
			"Generated":          generated,
			"Input":              input,
			"Provider":           providerID,
			"Model":              settings.Model,
			"TokensUsed":         int64(chatResp.TotalTokens),
			"CostUSD":            costUSD,
			"Categories":         categories,
			"Tags":               tags,
			"Languages":          languages,
			"ImageAvailable":     imageAvailable,
			"SelectedCategories": r.Form["categories"],
			"SelectedTags":       r.Form["tags"],
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "ai_content.title"), URL: "/admin/ai-content"},
			{Label: i18n.T(lang, "ai_content.generate_title"), URL: "/admin/ai-content/generate"},
			{Label: i18n.T(lang, "ai_content.preview_title"), URL: "", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleCreatePage handles POST /admin/ai-content/create-page - creates a draft page from generated content.
func (m *Module) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := r.FormValue("body")
	slug := strings.TrimSpace(r.FormValue("slug"))
	metaTitle := strings.TrimSpace(r.FormValue("meta_title"))
	metaDescription := strings.TrimSpace(r.FormValue("meta_description"))
	metaKeywords := strings.TrimSpace(r.FormValue("meta_keywords"))
	languageCode := strings.TrimSpace(r.FormValue("language_code"))
	contentType := strings.TrimSpace(r.FormValue("content_type"))
	imagePrompt := strings.TrimSpace(r.FormValue("image_prompt"))
	generateImage := r.FormValue("generate_image") == "1"

	if title == "" {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_title_required"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Generate slug if empty
	if slug == "" {
		slug = util.Slugify(title)
	}

	// Ensure slug is valid
	if !util.IsValidSlug(slug) {
		slug = util.Slugify(slug)
	}

	// Ensure slug uniqueness
	slug, err := m.ensureUniqueSlug(r.Context(), slug)
	if err != nil {
		m.ctx.Logger.Error("failed to ensure unique slug", "error", err)
		m.ctx.Render.SetFlash(r, "Failed to create unique slug", "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	if languageCode == "" {
		defaultLang, err := m.ctx.Store.GetDefaultLanguage(r.Context())
		if err == nil {
			languageCode = defaultLang.Code
		} else {
			languageCode = "en"
		}
	}

	if contentType == "" {
		contentType = "post"
	}

	now := time.Now()

	// Generate featured image if requested
	var featuredImageID sql.NullInt64
	if generateImage && imagePrompt != "" {
		imgCtx, imgCancel := context.WithTimeout(r.Context(), httpTimeout)
		defer imgCancel()

		imgResp, imgErr := m.generateFeaturedImage(imgCtx, imagePrompt)
		if imgErr != nil {
			m.ctx.Logger.Error("failed to generate featured image", "error", imgErr)
			// Continue without image - don't fail page creation
		} else {
			mediaID, saveErr := m.saveFeaturedImage(r.Context(), imgResp.ImageData, title, languageCode, user.ID)
			if saveErr != nil {
				m.ctx.Logger.Error("failed to save featured image", "error", saveErr)
			} else {
				featuredImageID = sql.NullInt64{Int64: mediaID, Valid: true}
				// Log image usage
				imageUsage := &UsageRecord{
					Provider:     ProviderOpenAI,
					Model:        imgResp.Model,
					Operation:    "image",
					CostUSD:      imgResp.CostUSD,
					LanguageCode: languageCode,
					PageTitle:    title,
					CreatedBy:    user.ID,
					CreatedAt:    now,
				}
				if err := logUsage(m.ctx.DB, imageUsage); err != nil {
					m.ctx.Logger.Error("failed to log image usage", "error", err)
				}
			}
		}
	}

	// Create the page as draft
	page, err := m.ctx.Store.CreatePage(r.Context(), store.CreatePageParams{
		Title:             title,
		Slug:              slug,
		Body:              body,
		Status:            "draft",
		AuthorID:          user.ID,
		FeaturedImageID:   featuredImageID,
		MetaTitle:         metaTitle,
		MetaDescription:   metaDescription,
		MetaKeywords:      metaKeywords,
		OgImageID:         sql.NullInt64{},
		NoIndex:           0,
		NoFollow:          0,
		CanonicalUrl:      "",
		ScheduledAt:       sql.NullTime{},
		LanguageCode:      languageCode,
		HideFeaturedImage: 0,
		PageType:          contentType,
		ExcludeFromLists:  0,
		PublishedAt:       sql.NullTime{},
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		m.ctx.Logger.Error("failed to create AI-generated page", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.error_create_page"), "error")
		http.Redirect(w, r, "/admin/ai-content/generate", http.StatusSeeOther)
		return
	}

	// Create initial page version
	_, err = m.ctx.Store.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    page.ID,
		Title:     title,
		Body:      body,
		ChangedBy: user.ID,
		CreatedAt: now,
	})
	if err != nil {
		m.ctx.Logger.Error("failed to create page version", "error", err)
	}

	// Link categories
	for _, catIDStr := range r.Form["categories"] {
		catID, err := strconv.ParseInt(catIDStr, 10, 64)
		if err != nil {
			continue
		}
		if err := m.ctx.Store.AddCategoryToPage(r.Context(), store.AddCategoryToPageParams{
			PageID:     page.ID,
			CategoryID: catID,
		}); err != nil {
			m.ctx.Logger.Error("failed to add category to page", "error", err, "category_id", catID)
		}
	}

	// Link tags
	for _, tagIDStr := range r.Form["tags"] {
		tagID, err := strconv.ParseInt(tagIDStr, 10, 64)
		if err != nil {
			continue
		}
		if err := m.ctx.Store.AddTagToPage(r.Context(), store.AddTagToPageParams{
			PageID: page.ID,
			TagID:  tagID,
		}); err != nil {
			m.ctx.Logger.Error("failed to add tag to page", "error", err, "tag_id", tagID)
		}
	}

	// Update usage records with page ID
	_, err = m.ctx.DB.Exec(
		`UPDATE ai_content_usage SET page_id = ? WHERE page_title = ? AND page_id IS NULL AND created_by = ? ORDER BY created_at DESC LIMIT 5`,
		page.ID, title, user.ID,
	)
	if err != nil {
		m.ctx.Logger.Error("failed to update usage with page ID", "error", err)
	}

	m.ctx.Logger.Info("AI-generated page created",
		"page_id", page.ID,
		"slug", slug,
		"user", user.Email,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "ai_content.success_page_created"), "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/edit", page.ID), http.StatusSeeOther)
}

// handleUsageLog handles GET /admin/ai-content/usage - shows full usage log.
func (m *Module) handleUsageLog(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	pageNum := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			pageNum = n
		}
	}

	offset := (pageNum - 1) * usagePerPage
	records, total, err := loadUsageRecords(m.ctx.DB, usagePerPage, offset)
	if err != nil {
		m.ctx.Logger.Error("failed to load usage records", "error", err)
	}

	stats, err := loadUsageStats(m.ctx.DB)
	if err != nil {
		m.ctx.Logger.Error("failed to load usage stats", "error", err)
		stats = &UsageStats{ByProvider: make(map[string]*ProviderUsageStats)}
	}

	totalPages := (total + usagePerPage - 1) / usagePerPage

	if err := m.ctx.Render.Render(w, r, "admin/module_ai_content_usage", render.TemplateData{
		Title: i18n.T(lang, "ai_content.usage_title"),
		User:  user,
		Data: map[string]any{
			"Records":    records,
			"Stats":      stats,
			"Total":      total,
			"Page":       pageNum,
			"TotalPages": totalPages,
			"PerPage":    usagePerPage,
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "ai_content.title"), URL: "/admin/ai-content"},
			{Label: i18n.T(lang, "ai_content.usage_title"), URL: "/admin/ai-content/usage", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// ensureUniqueSlug appends a suffix if the slug already exists.
func (m *Module) ensureUniqueSlug(ctx context.Context, slug string) (string, error) {
	baseSlug := slug
	for i := 0; i < 100; i++ {
		candidate := baseSlug
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", baseSlug, i)
		}
		exists, err := m.ctx.Store.SlugOrAliasExists(ctx, store.SlugOrAliasExistsParams{
			Slug:  candidate,
			Alias: candidate,
		})
		if err != nil {
			return "", fmt.Errorf("checking slug uniqueness: %w", err)
		}
		if exists == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not generate unique slug for %q", baseSlug)
}

// saveFeaturedImage saves the generated image to disk and creates a media record.
func (m *Module) saveFeaturedImage(ctx context.Context, imgData []byte, title, languageCode string, userID int64) (int64, error) {
	fileUUID := uuid.New().String()
	filename := util.Slugify(title) + ".png"
	if filename == ".png" {
		filename = "ai-generated-" + fileUUID[:8] + ".png"
	}

	// Decode image to get dimensions
	imgReader := bytes.NewReader(imgData)
	imgConfig, _, err := image.DecodeConfig(imgReader)
	if err != nil {
		return 0, fmt.Errorf("decoding image config: %w", err)
	}

	// Save original file
	originalsDir := filepath.Join(m.uploadsDir, "originals", fileUUID)
	if err := os.MkdirAll(originalsDir, 0755); err != nil {
		return 0, fmt.Errorf("creating originals directory: %w", err)
	}

	originalPath := filepath.Join(originalsDir, filename)
	if err := os.WriteFile(originalPath, imgData, 0644); err != nil {
		return 0, fmt.Errorf("writing original file: %w", err)
	}

	now := time.Now()

	// Create media record
	media, err := m.ctx.Store.CreateMedia(ctx, store.CreateMediaParams{
		Uuid:         fileUUID,
		Filename:     filename,
		MimeType:     "image/png",
		Size:         int64(len(imgData)),
		Width:        sql.NullInt64{Int64: int64(imgConfig.Width), Valid: true},
		Height:       sql.NullInt64{Int64: int64(imgConfig.Height), Valid: true},
		Alt:          sql.NullString{String: title, Valid: true},
		Caption:      sql.NullString{String: "AI-generated image", Valid: true},
		FolderID:     sql.NullInt64{Valid: false},
		UploadedBy:   userID,
		LanguageCode: languageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		_ = os.Remove(originalPath)
		return 0, fmt.Errorf("creating media record: %w", err)
	}

	// Create image variants
	m.createImageVariants(ctx, imgData, fileUUID, filename, media.ID, imgConfig.Width, imgConfig.Height)

	return media.ID, nil
}

// createImageVariants creates thumbnail, small, medium, and large variants.
func (m *Module) createImageVariants(ctx context.Context, imgData []byte, fileUUID, filename string, mediaID int64, origWidth, origHeight int) {
	// Decode full image
	imgReader := bytes.NewReader(imgData)
	srcImg, err := png.Decode(imgReader)
	if err != nil {
		slog.Warn("failed to decode image for variants", "error", err)
		return
	}

	variants := []struct {
		name   string
		width  int
		height int
		crop   bool
	}{
		{"thumbnail", 150, 150, true},
		{"small", 400, 300, false},
		{"medium", 800, 600, false},
		{"large", 1920, 1080, false},
	}

	for _, v := range variants {
		// Skip if source is smaller than target for non-crop variants
		if !v.crop && origWidth <= v.width && origHeight <= v.height {
			continue
		}

		variantDir := filepath.Join(m.uploadsDir, v.name, fileUUID)
		if err := os.MkdirAll(variantDir, 0755); err != nil {
			slog.Warn("failed to create variant directory", "variant", v.name, "error", err)
			continue
		}

		// Simple resize using nearest-neighbor for now
		variantImg := resizeSimple(srcImg, v.width, v.height, v.crop)
		var buf bytes.Buffer
		if err := png.Encode(&buf, variantImg); err != nil {
			slog.Warn("failed to encode variant", "variant", v.name, "error", err)
			continue
		}

		variantPath := filepath.Join(variantDir, filename)
		if err := os.WriteFile(variantPath, buf.Bytes(), 0644); err != nil {
			slog.Warn("failed to write variant file", "variant", v.name, "error", err)
			continue
		}

		_, err := m.ctx.Store.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
			MediaID:   mediaID,
			Type:      v.name,
			Width:     int64(variantImg.Bounds().Dx()),
			Height:    int64(variantImg.Bounds().Dy()),
			Size:      int64(buf.Len()),
			CreatedAt: time.Now(),
		})
		if err != nil {
			slog.Warn("failed to create variant record", "variant", v.name, "error", err)
		}
	}
}

// resizeSimple performs a simple image resize. For crop=true, crops to center then resizes.
func resizeSimple(src image.Image, targetW, targetH int, crop bool) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if crop {
		// Center crop to target aspect ratio, then resize
		targetRatio := float64(targetW) / float64(targetH)
		srcRatio := float64(srcW) / float64(srcH)

		var cropW, cropH int
		if srcRatio > targetRatio {
			cropH = srcH
			cropW = int(float64(srcH) * targetRatio)
		} else {
			cropW = srcW
			cropH = int(float64(srcW) / targetRatio)
		}

		offsetX := (srcW - cropW) / 2
		offsetY := (srcH - cropH) / 2

		cropped := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
		for y := 0; y < cropH; y++ {
			for x := 0; x < cropW; x++ {
				cropped.Set(x, y, src.At(offsetX+x, offsetY+y))
			}
		}
		src = cropped
		srcW = cropW
		srcH = cropH
	}

	// Calculate output size maintaining aspect ratio (for non-crop)
	outW, outH := targetW, targetH
	if !crop {
		ratio := float64(srcW) / float64(srcH)
		if float64(targetW)/float64(targetH) > ratio {
			outW = int(float64(targetH) * ratio)
			outH = targetH
		} else {
			outW = targetW
			outH = int(float64(targetW) / ratio)
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			srcX := x * srcW / outW
			srcY := y * srcH / outH
			dst.Set(x, y, src.At(bounds.Min.X+srcX, bounds.Min.Y+srcY))
		}
	}

	return dst
}
