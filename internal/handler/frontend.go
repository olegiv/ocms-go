// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package handler provides HTTP handlers for the application.
package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
)

// PageView represents a page with computed fields for template rendering.
type PageView struct {
	ID                   int64
	Title                string
	Slug                 string
	Body                 template.HTML
	Excerpt              string
	URL                  string
	Status               string
	Type                 string // "page", "post", etc.
	PublishedAt          *time.Time
	PublishedAtFormatted string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	FeaturedImage        string
	FeaturedImageMedium  string // Medium variant for mobile grid views
	FeaturedImageLarge   string // Large variant for single page views
	FeaturedImageID      int64  // Media ID for translation lookup
	FeaturedImageAlt     string // Alt text (default language)
	HideFeaturedImage    bool   // Show image below title instead of hero banner
	ReadingTime          int    // Estimated reading time in minutes
	Highlight            string // Search result highlight
	Author               *AuthorView
	Category             *CategoryView
	Categories           []CategoryView
	Tags                 []TagView
	// SEO fields
	MetaTitle       string
	MetaDescription string
	MetaKeywords    string
	OGImage         string
	NoIndex         bool
	NoFollow        bool
	CanonicalURL    string
}

// AuthorView represents an author for template rendering.
type AuthorView struct {
	ID     int64
	Name   string
	Email  string
	Avatar string
	Bio    string
}

// CategoryView represents a category for template rendering.
type CategoryView struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	URL         string
	PageCount   int64
}

// TagView represents a tag for template rendering.
type TagView struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	URL         string
	PageCount   int64
}

// Pagination holds pagination data for templates.
type Pagination struct {
	CurrentPage int
	TotalPages  int
	TotalItems  int64
	PerPage     int
	HasPrev     bool
	HasNext     bool
	HasFirst    bool
	HasLast     bool
	PrevURL     string
	NextURL     string
	FirstURL    string
	LastURL     string
	Pages       []PaginationPage
}

// PaginationPage represents a single page link in pagination.
type PaginationPage struct {
	Number     int
	URL        string
	IsCurrent  bool
	IsEllipsis bool
}

// SiteData holds site-wide data for templates.
type SiteData struct {
	SiteName    string
	Description string
	URL         string
	Theme       *theme.Config
	Settings    map[string]string
	CurrentYear int
}

// BaseTemplateData contains common fields expected by all frontend templates.
type BaseTemplateData struct {
	// SEO Meta
	Title           string
	MetaDescription string
	MetaKeywords    string
	Canonical       string
	FeaturedImage   string
	Robots          string      // Robots directive (index,follow / noindex,nofollow)
	OGImage         string      // Open Graph image (absolute URL)
	OGType          string      // Open Graph type (website, article)
	JSONLD          template.JS // JSON-LD structured data

	// Site info
	SiteName    string
	SiteURL     string
	SiteLogo    string
	SiteTagline string
	RequestURI  string
	CurrentPath string
	Year        int

	// Layout options
	BodyClass   string
	ShowSidebar bool

	// Page - set when rendering a single page (for Open Graph article type)
	Page *PageView

	// Site data
	Site SiteData

	// Theme settings - key-value pairs from theme configuration
	ThemeSettings map[string]string
	CustomCSS     string

	// Menus - MainMenu/FooterMenu for code, Navigation/FooterNav for templates
	MainMenu   []MenuItem
	FooterMenu []MenuItem
	Navigation []MenuItem
	FooterNav  []MenuItem

	// Footer
	FooterText    string
	CopyrightText string
	FooterWidgets []FooterWidget
	SocialLinks   []SocialLink

	// Search
	ShowSearch  bool
	SearchQuery string

	// Widgets - map of widget area ID to widgets
	Widgets map[string][]service.WidgetView

	// Language support
	CurrentLanguage    *LanguageView     // Current language for the request
	Languages          []LanguageView    // All active languages
	Translations       []TranslationLink // Available translations for current page
	HrefLangs          []HrefLangLink    // hreflang links for SEO
	LangCode           string            // Current language code (shortcut)
	LangDirection      string            // Current language direction (ltr/rtl)
	LangPrefix         string            // URL prefix for current language (e.g., "/ru" or "" for default)
	ShowLanguagePicker bool              // Whether to show language picker
}

// FooterWidget represents a widget in the footer area.
type FooterWidget struct {
	Title   string
	Content template.HTML
}

// SocialLink represents a social media link.
type SocialLink struct {
	Name string
	URL  string
	Icon template.HTML
}

// LanguageView represents a language for template rendering.
type LanguageView struct {
	ID         int64
	Code       string
	Name       string
	NativeName string
	Direction  string
	IsDefault  bool
	IsCurrent  bool
}

// TranslationLink represents a translation for the language switcher.
type TranslationLink struct {
	Language  LanguageView
	URL       string // Full URL to the translated page
	PageTitle string // Title of the translated page
	HasPage   bool   // Whether a translation exists for this language
}

// HrefLangLink represents an hreflang link for SEO.
type HrefLangLink struct {
	Lang string // Language code (e.g., "en", "ru", "x-default")
	Href string // Full URL
}

// MenuItem represents a menu item for templates.
type MenuItem struct {
	Title    string
	URL      string
	Target   string
	Children []MenuItem
	IsActive bool
}

// RecentPost holds minimal data for sidebar recent posts widget.
type RecentPost struct {
	URL   string
	Title string
	Date  string
}

// HomeData holds data for the homepage template.
type HomeData struct {
	BaseTemplateData
	Page             *PageView
	FeaturedPages    []PageView
	RecentPages      []PageView
	Categories       []CategoryView
	Tags             []TagView
	RecentPosts      []RecentPost // For sidebar widget
	HeroEnabled      bool
	HeroTitle        string
	HeroSubtitle     string
	HeroCTA          string
	HeroCTAURL       string
	HeroImage        string
	ShowAllPostsLink bool
}

// PageData holds data for single page templates.
type PageData struct {
	BaseTemplateData
	Page          *PageView
	RelatedPages  []PageView
	ShowAuthorBox bool
	// Sidebar data for themes that show sidebar on single pages
	Categories  []CategoryView
	Tags        []TagView
	RecentPages []PageView
}

// ListData holds data for list templates (blog, archives).
type ListData struct {
	BaseTemplateData
	Pages       []PageView
	Pagination  Pagination
	Description string // Optional description for list pages (blog, archives, etc.)
	// Sidebar data for themes that show sidebar on list pages
	Categories  []CategoryView
	Tags        []TagView
	RecentPages []PageView
}

// SubcategoryView represents a subcategory with page count for template rendering.
type SubcategoryView struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	URL         string
	Count       int64
}

// CategoryPageData holds data for category archive templates.
type CategoryPageData struct {
	BaseTemplateData
	Category      CategoryView
	Pages         []PageView
	Pagination    Pagination
	PageCount     int
	Subcategories []SubcategoryView
	// Sidebar data for themes that show sidebar on category pages
	Categories  []CategoryView
	Tags        []TagView
	RecentPages []PageView
}

// TagPageData holds data for tag archive templates.
type TagPageData struct {
	BaseTemplateData
	Tag         TagView
	Pages       []PageView
	Pagination  Pagination
	PageCount   int
	RelatedTags []TagView
	// Sidebar data for themes that show sidebar on tag pages
	Categories  []CategoryView
	Tags        []TagView
	RecentPages []PageView
}

// SearchData holds data for search results templates.
type SearchData struct {
	BaseTemplateData
	Query           string
	Pages           []PageView
	Pagination      Pagination
	ResultCount     int
	PopularSearches []string
	// Sidebar data for themes that show sidebar on search page
	Categories  []CategoryView
	Tags        []TagView
	RecentPages []PageView
}

// NotFoundData holds data for 404 templates.
type NotFoundData struct {
	BaseTemplateData
	SuggestedPages []PageView
}

// FrontendHandler handles public frontend routes.
type FrontendHandler struct {
	db            *sql.DB
	queries       *store.Queries
	themeManager  *theme.Manager
	menuService   *service.MenuService
	widgetService *service.WidgetService
	searchService *service.SearchService
	cacheManager  *cache.Manager
	logger        *slog.Logger
}

// NewFrontendHandler creates a new FrontendHandler.
// If menuService is nil, a new one will be created. Pass a shared menuService for cache consistency.
func NewFrontendHandler(db *sql.DB, themeManager *theme.Manager, cacheManager *cache.Manager, logger *slog.Logger, menuService *service.MenuService) *FrontendHandler {
	if menuService == nil {
		menuService = service.NewMenuService(db)
	}
	return &FrontendHandler{
		db:            db,
		queries:       store.New(db),
		themeManager:  themeManager,
		menuService:   menuService,
		widgetService: service.NewWidgetService(db),
		searchService: service.NewSearchService(db),
		cacheManager:  cacheManager,
		logger:        logger,
	}
}

// Home handles the homepage.
func (h *FrontendHandler) Home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get current language for filtering
	var languageCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		languageCode = langInfo.Code
	}

	// Get base template data
	base := h.getBaseTemplateData(r, "", "")
	base.MetaDescription = base.Site.Description
	base.BodyClass = "home"

	// Get recent published pages filtered by language
	var recentPages []store.Page
	var err error
	if languageCode != "" {
		recentPages, err = h.queries.ListPublishedPagesByLanguage(ctx, store.ListPublishedPagesByLanguageParams{
			LanguageCode: languageCode,
			Limit:        10,
			Offset:       0,
		})
	} else {
		recentPages, err = h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
			Limit:  10,
			Offset: 0,
		})
	}
	if err != nil {
		h.logger.Error("failed to get recent pages", "error", err)
		h.renderInternalError(w)
		return
	}

	// Convert to PageViews
	recentPageViews := make([]PageView, 0, len(recentPages))
	for _, p := range recentPages {
		pv := h.pageToView(ctx, p)
		recentPageViews = append(recentPageViews, pv)
	}

	// Get categories and tags filtered by language
	var categoryViews []CategoryView
	var tagViews []TagView

	if languageCode != "" {
		// Get categories with usage counts filtered by language
		categoriesWithCount, err := h.queries.GetCategoryUsageCountsByLanguage(ctx, languageCode)
		if err != nil {
			h.logger.Error("failed to get categories", "error", err)
		}
		categoryViews = make([]CategoryView, 0, len(categoriesWithCount))
		for _, c := range categoriesWithCount {
			categoryViews = append(categoryViews, CategoryView{
				ID:          c.ID,
				Name:        c.Name,
				Slug:        c.Slug,
				Description: c.Description.String,
				URL:         redirectCategory + c.Slug,
				PageCount:   c.UsageCount,
			})
		}

		// Get tags with usage counts filtered by language
		tagsWithCount, err := h.queries.GetTagUsageCountsByLanguage(ctx, store.GetTagUsageCountsByLanguageParams{
			LanguageCode: languageCode,
			Limit:        20,
			Offset:       0,
		})
		if err != nil {
			h.logger.Error("failed to get tags", "error", err)
		}
		tagViews = make([]TagView, 0, len(tagsWithCount))
		for _, t := range tagsWithCount {
			tagViews = append(tagViews, TagView{
				ID:        t.ID,
				Name:      t.Name,
				Slug:      t.Slug,
				URL:       redirectTag + t.Slug,
				PageCount: t.UsageCount,
			})
		}
	} else {
		// Fallback to all languages
		categoriesWithCount, err := h.queries.GetCategoryUsageCounts(ctx)
		if err != nil {
			h.logger.Error("failed to get categories", "error", err)
		}
		categoryViews = make([]CategoryView, 0, len(categoriesWithCount))
		for _, c := range categoriesWithCount {
			categoryViews = append(categoryViews, CategoryView{
				ID:          c.ID,
				Name:        c.Name,
				Slug:        c.Slug,
				Description: c.Description.String,
				URL:         redirectCategory + c.Slug,
				PageCount:   c.UsageCount,
			})
		}

		tagsWithCount, err := h.queries.GetTagUsageCounts(ctx, store.GetTagUsageCountsParams{
			Limit:  20,
			Offset: 0,
		})
		if err != nil {
			h.logger.Error("failed to get tags", "error", err)
		}
		tagViews = make([]TagView, 0, len(tagsWithCount))
		for _, t := range tagsWithCount {
			tagViews = append(tagViews, TagView{
				ID:        t.ID,
				Name:      t.Name,
				Slug:      t.Slug,
				URL:       redirectTag + t.Slug,
				PageCount: t.UsageCount,
			})
		}
	}

	// Build RecentPosts for sidebar (first 5 posts)
	recentPosts := make([]RecentPost, 0, 5)
	for i, pv := range recentPageViews {
		if i >= 5 {
			break
		}
		date := ""
		if pv.PublishedAt != nil {
			date = pv.PublishedAt.Format("Jan 2, 2006")
		}
		recentPosts = append(recentPosts, RecentPost{
			URL:   pv.URL,
			Title: pv.Title,
			Date:  date,
		})
	}

	// Set language cookie if detected from URL
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		middleware.SetLanguageCookie(w, langInfo.Code)
	}

	// Build homepage translations for language switcher
	if base.ShowLanguagePicker {
		base.Translations = h.getHomepageTranslations(base.LangCode, base.Languages)
	}

	// Enable sidebar for homepage
	base.ShowSidebar = true

	data := HomeData{
		BaseTemplateData: base,
		RecentPages:      recentPageViews,
		Categories:       categoryViews,
		Tags:             tagViews,
		RecentPosts:      recentPosts,
		HeroEnabled:      true,
		HeroTitle:        base.SiteName,
		HeroSubtitle:     base.Site.Description,
		ShowAllPostsLink: len(recentPageViews) > 6,
	}

	h.render(w, "home", data)
}

// Page handles single page display.
func (h *FrontendHandler) Page(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get published page by slug
	page, err := h.queries.GetPublishedPageBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Slug not found - check if it's an alias
			aliasPage, aliasErr := h.queries.GetPublishedPageByAlias(ctx, slug)
			if aliasErr == nil {
				// Alias found - redirect to canonical URL (HTTP 301 Moved Permanently)
				http.Redirect(w, r, "/"+aliasPage.Slug, http.StatusMovedPermanently)
				return
			}
			// Not a slug, not an alias - render 404
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get page", "slug", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Update language context based on page's language (fixes translated pages like /slug-ru)
	// This ensures that when visiting a translated page directly, the UI language matches the content
	if page.LanguageCode != "" {
		if pageLang, err := h.queries.GetLanguageByCode(ctx, page.LanguageCode); err == nil {
			// Update the request context with the page's language
			langInfo := middleware.LanguageInfo{
				ID:         pageLang.ID,
				Code:       pageLang.Code,
				Name:       pageLang.Name,
				NativeName: pageLang.NativeName,
				Direction:  pageLang.Direction,
				IsDefault:  pageLang.IsDefault,
			}
			ctx = context.WithValue(ctx, middleware.ContextKeyLanguage, langInfo)
			ctx = context.WithValue(ctx, middleware.ContextKeyLanguageCode, pageLang.Code)
			r = r.WithContext(ctx)

			// Set language cookie to remember the preference
			middleware.SetLanguageCookie(w, pageLang.Code)
		}
	}

	// Convert to PageView
	pageView := h.pageToView(ctx, page)

	// Use large variant for single page featured image
	if pageView.FeaturedImageLarge != "" {
		pageView.FeaturedImage = pageView.FeaturedImageLarge
	}

	// Get related pages (same category)
	var relatedPages []PageView
	if pageView.Category != nil {
		related, err := h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
			CategoryID: pageView.Category.ID,
			Limit:      4,
			Offset:     0,
		})
		if err == nil {
			for _, p := range related {
				if p.ID != page.ID {
					relatedPages = append(relatedPages, h.pageToView(ctx, p))
				}
			}
			// Limit to 3 related pages
			if len(relatedPages) > 3 {
				relatedPages = relatedPages[:3]
			}
		}
	}

	// Get base template data first (for site info)
	base := h.getBaseTemplateData(r, pageView.Title, pageView.Excerpt)

	// Build SEO meta with fallbacks
	siteConfig := &seo.SiteConfig{
		SiteName:        base.SiteName,
		SiteURL:         base.SiteURL,
		SiteDescription: base.Site.Description,
	}

	var authorName string
	if pageView.Author != nil {
		authorName = pageView.Author.Name
	}

	pageData := &seo.PageData{
		Title:           pageView.Title,
		Body:            string(pageView.Body),
		Slug:            pageView.Slug,
		MetaTitle:       pageView.MetaTitle,
		MetaDescription: pageView.MetaDescription,
		MetaKeywords:    pageView.MetaKeywords,
		OGImageURL:      pageView.OGImage,
		FeaturedImage:   pageView.FeaturedImage,
		NoIndex:         pageView.NoIndex,
		NoFollow:        pageView.NoFollow,
		CanonicalURL:    pageView.CanonicalURL,
		PublishedAt:     pageView.PublishedAt,
		AuthorName:      authorName,
	}

	meta := seo.BuildMeta(pageData, siteConfig)

	// Apply SEO meta with fallbacks to base template data
	base.Title = meta.Title
	base.MetaDescription = meta.Description
	base.MetaKeywords = meta.Keywords
	base.Canonical = meta.Canonical
	base.FeaturedImage = pageView.FeaturedImage
	base.Robots = meta.Robots
	base.OGImage = meta.OGImage
	base.OGType = meta.OGType
	base.BodyClass = "single-page"

	// Build JSON-LD structured data
	base.JSONLD = seo.BuildArticleSchema(pageData, siteConfig, page.UpdatedAt)

	// Get translations for language switcher and hreflang
	if base.ShowLanguagePicker {
		translations, hrefLangs := h.getPageTranslations(ctx, page.ID, base.LangCode, base.SiteURL)
		base.Translations = translations
		base.HrefLangs = hrefLangs
	}

	// Set language cookie if detected from URL
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		middleware.SetLanguageCookie(w, langInfo.Code)
	}

	// Fetch sidebar data for themes that show sidebar on single pages
	languageCode := page.LanguageCode
	sidebarCategories, sidebarTags, sidebarRecent := h.getSidebarData(ctx, languageCode)

	data := PageData{
		BaseTemplateData: base,
		Page:             &pageView,
		RelatedPages:     relatedPages,
		ShowAuthorBox:    true,
		Categories:       sidebarCategories,
		Tags:             sidebarTags,
		RecentPages:      sidebarRecent,
	}

	h.render(w, "page", data)
}

// PageByID handles /page/{id} - redirects to the canonical slug URL.
// This provides a permanent, stable URL that won't break if the page slug changes.
func (h *FrontendHandler) PageByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	// Parse the page ID
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderNotFound(w, r)
		return
	}

	// Get published page by ID
	page, err := h.queries.GetPublishedPageByID(ctx, id)
	if err != nil {
		h.renderNotFound(w, r)
		return
	}

	// Build the redirect URL
	// Check if language prefix is needed (non-default language)
	redirectURL := "/" + page.Slug
	if page.LanguageCode != "" {
		// Check if this is the default language
		if defaultLang, err := h.queries.GetDefaultLanguage(ctx); err == nil {
			if page.LanguageCode != defaultLang.Code {
				redirectURL = "/" + page.LanguageCode + "/" + page.Slug
			}
		}
	}

	// HTTP 301 Moved Permanently - signals that this URL permanently redirects to the canonical
	http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
}

// Category handles category archive display.
func (h *FrontendHandler) Category(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get current language for filtering
	var languageCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		languageCode = langInfo.Code
	}

	// Get category
	category, err := h.queries.GetCategoryBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get category", "slug", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage
	limit := int64(defaultPerPage)

	// Fetch pages for this category (with optional language filter)
	var pages []store.Page
	var total int64
	catID := category.ID
	if languageCode != "" {
		pages, err = h.queries.ListPublishedPagesByCategoryAndLanguage(ctx, store.ListPublishedPagesByCategoryAndLanguageParams{
			CategoryID: catID, LanguageCode: languageCode, Limit: limit, Offset: int64(offset)})
		total, _ = h.queries.CountPublishedPagesByCategoryAndLanguage(ctx, store.CountPublishedPagesByCategoryAndLanguageParams{
			CategoryID: catID, LanguageCode: languageCode})
	} else {
		pages, err = h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
			CategoryID: catID, Limit: limit, Offset: int64(offset)})
		total, _ = h.queries.CountPublishedPagesByCategory(ctx, catID)
	}
	if err != nil {
		h.logger.Error("failed to get pages for category", "category", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Convert to PageViews
	pageViews := make([]PageView, 0, len(pages))
	for _, p := range pages {
		pageViews = append(pageViews, h.pageToView(ctx, p))
	}

	categoryView := CategoryView{
		ID:          category.ID,
		Name:        category.Name,
		Slug:        category.Slug,
		Description: category.Description.String,
		URL:         redirectCategory + category.Slug,
	}

	// Get base template data first to access LangPrefix
	title := "Category: " + category.Name
	base := h.getBaseTemplateData(r, title, category.Description.String)
	base.BodyClass = "archive category"

	// Build pagination with language prefix
	pagination := h.buildPagination(page, int(total), fmt.Sprintf("%s/category/%s", base.LangPrefix, slug))

	// Fetch sidebar data for themes that show sidebar on category pages
	sidebarCategories, sidebarTags, sidebarRecent := h.getSidebarData(ctx, languageCode)

	data := CategoryPageData{
		BaseTemplateData: base,
		Category:         categoryView,
		Pages:            pageViews,
		Pagination:       pagination,
		PageCount:        int(total),
		Categories:       sidebarCategories,
		Tags:             sidebarTags,
		RecentPages:      sidebarRecent,
	}

	h.render(w, "category", data)
}

// Tag handles tag archive display.
func (h *FrontendHandler) Tag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get current language for filtering
	var languageCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		languageCode = langInfo.Code
	}

	// Get tag
	tag, err := h.queries.GetTagBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get tag", "slug", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage
	limit := int64(defaultPerPage)

	// Fetch pages for this tag (with optional language filter)
	var pages []store.Page
	var total int64
	tID := tag.ID
	if languageCode != "" {
		pages, err = h.queries.ListPublishedPagesForTagAndLanguage(ctx, store.ListPublishedPagesForTagAndLanguageParams{
			TagID: tID, LanguageCode: languageCode, Limit: limit, Offset: int64(offset)})
		total, _ = h.queries.CountPublishedPagesForTagAndLanguage(ctx, store.CountPublishedPagesForTagAndLanguageParams{
			TagID: tID, LanguageCode: languageCode})
	} else {
		pages, err = h.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{
			TagID: tID, Limit: limit, Offset: int64(offset)})
		total, _ = h.queries.CountPublishedPagesForTag(ctx, tID)
	}
	if err != nil {
		h.logger.Error("failed to get pages for tag", "tag", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Convert to PageViews
	pageViews := make([]PageView, 0, len(pages))
	for _, p := range pages {
		pageViews = append(pageViews, h.pageToView(ctx, p))
	}

	tagView := TagView{
		ID:   tag.ID,
		Name: tag.Name,
		Slug: tag.Slug,
		URL:  redirectTag + tag.Slug,
	}

	// Get base template data first to access LangPrefix
	title := "Tag: " + tag.Name
	base := h.getBaseTemplateData(r, title, "")
	base.BodyClass = "archive tag"

	// Build pagination with language prefix
	pagination := h.buildPagination(page, int(total), fmt.Sprintf("%s/tag/%s", base.LangPrefix, slug))

	// Fetch sidebar data for themes that show sidebar on tag pages
	sidebarCategories, sidebarTags, sidebarRecent := h.getSidebarData(ctx, languageCode)

	data := TagPageData{
		BaseTemplateData: base,
		Tag:              tagView,
		Pages:            pageViews,
		Pagination:       pagination,
		PageCount:        int(total),
		Categories:       sidebarCategories,
		Tags:             sidebarTags,
		RecentPages:      sidebarRecent,
	}

	h.render(w, "tag", data)
}

// Blog handles the blog listing page displaying all published posts.
func (h *FrontendHandler) Blog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get current language for filtering
	var languageCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		languageCode = langInfo.Code
	}

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage

	// Get published pages filtered by language
	var pages []store.Page
	var total int64
	var err error

	if languageCode != "" {
		pages, err = h.queries.ListPublishedPagesByLanguage(ctx, store.ListPublishedPagesByLanguageParams{
			LanguageCode: languageCode,
			Limit:        int64(defaultPerPage),
			Offset:       int64(offset),
		})
		if err != nil {
			h.logger.Error("failed to get blog pages", "error", err)
			h.renderInternalError(w)
			return
		}

		total, err = h.queries.CountPublishedPagesByLanguage(ctx, languageCode)
		if err != nil {
			h.logger.Error("failed to count blog pages", "error", err)
			total = 0
		}
	} else {
		pages, err = h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
			Limit:  int64(defaultPerPage),
			Offset: int64(offset),
		})
		if err != nil {
			h.logger.Error("failed to get blog pages", "error", err)
			h.renderInternalError(w)
			return
		}

		total, err = h.queries.CountPublishedPages(ctx)
		if err != nil {
			h.logger.Error("failed to count blog pages", "error", err)
			total = 0
		}
	}

	// Convert to PageViews
	pageViews := make([]PageView, 0, len(pages))
	for _, p := range pages {
		pageViews = append(pageViews, h.pageToView(ctx, p))
	}

	// Get base template data first to access LangPrefix
	base := h.getBaseTemplateData(r, "Blog", "")
	base.BodyClass = "archive blog"

	// Build pagination with language prefix
	pagination := h.buildPagination(page, int(total), base.LangPrefix+"/blog")

	// Fetch sidebar data for themes that show sidebar on list pages
	sidebarCategories, sidebarTags, sidebarRecent := h.getSidebarData(ctx, languageCode)

	data := ListData{
		BaseTemplateData: base,
		Pages:            pageViews,
		Pagination:       pagination,
		Categories:       sidebarCategories,
		Tags:             sidebarTags,
		RecentPages:      sidebarRecent,
	}

	h.render(w, "list", data)
}

// Search handles search results display using FTS5 full-text search.
func (h *FrontendHandler) Search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")

	// Get current language for filtering
	var languageCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		languageCode = langInfo.Code
	}

	// Fetch sidebar data (categories, tags, recent pages) filtered by language
	sidebarCategories, sidebarTags, sidebarRecent := h.getSidebarData(ctx, languageCode)

	// If no query, show empty search page
	if query == "" {
		base := h.getBaseTemplateData(r, "Search", "")
		base.BodyClass = "search"
		data := SearchData{
			BaseTemplateData: base,
			Query:            "",
			Pages:            []PageView{},
			Categories:       sidebarCategories,
			Tags:             sidebarTags,
			RecentPages:      sidebarRecent,
		}
		h.render(w, "search", data)
		return
	}

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage

	// Use FTS5 search service with language filtering
	searchResults, total, err := h.searchService.SearchPublishedPages(ctx, service.SearchParams{
		Query:        query,
		Limit:        defaultPerPage,
		Offset:       offset,
		LanguageCode: languageCode,
	})
	if err != nil {
		h.logger.Error("failed to search pages", "query", query, "error", err)
		h.renderInternalError(w)
		return
	}

	// Convert search results to PageViews
	pageViews := make([]PageView, 0, len(searchResults))
	for _, sr := range searchResults {
		// Clean highlight by stripping HTML but preserving <mark> tags
		cleanHighlight := h.stripHTMLPreserveMark(sr.Highlight)

		pv := PageView{
			ID:        sr.ID,
			Title:     sr.Title,
			Slug:      sr.Slug,
			Body:      template.HTML(sr.Body),
			Excerpt:   sr.Excerpt,
			Highlight: cleanHighlight,
			URL:       "/" + sr.Slug,
			Status:    sr.Status,
			Type:      "page",
			CreatedAt: sr.CreatedAt,
			UpdatedAt: sr.UpdatedAt,
		}

		// Set published date
		if sr.PublishedAt.Valid {
			t := sr.PublishedAt.Time
			pv.PublishedAt = &t
			pv.PublishedAtFormatted = t.Format("Jan 2, 2006")
		}

		// Get featured image
		if sr.FeaturedImageID.Valid {
			media, err := h.queries.GetMediaByID(ctx, sr.FeaturedImageID.Int64)
			if err == nil {
				pv.FeaturedImage = fmt.Sprintf("/uploads/thumbnail/%s/%s", media.Uuid, media.Filename)
				pv.FeaturedImageID = media.ID
				pv.FeaturedImageAlt = media.Alt.String
			}
		}

		pageViews = append(pageViews, pv)
	}

	// Get base template data first to access LangPrefix
	title := fmt.Sprintf("Search: %s", query)
	base := h.getBaseTemplateData(r, title, "")
	base.BodyClass = "search"

	// Build pagination with language prefix
	pagination := h.buildPagination(page, int(total), fmt.Sprintf("%s/search?q=%s", base.LangPrefix, query))

	data := SearchData{
		BaseTemplateData: base,
		Query:            query,
		Pages:            pageViews,
		Pagination:       pagination,
		ResultCount:      int(total),
		Categories:       sidebarCategories,
		Tags:             sidebarTags,
		RecentPages:      sidebarRecent,
	}

	h.render(w, "search", data)
}

// NotFound renders the 404 page.
func (h *FrontendHandler) NotFound(w http.ResponseWriter, r *http.Request) {
	h.renderNotFound(w, r)
}

// Sitemap generates and serves the sitemap.xml file.
func (h *FrontendHandler) Sitemap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteURL := h.getSiteURL(ctx, r)

	// Get sitemap from cache (or generate it)
	var xmlContent []byte
	var err error

	if h.cacheManager != nil {
		xmlContent, err = h.cacheManager.GetSitemap(ctx, siteURL)
	} else {
		// Fallback: generate sitemap directly (no caching)
		xmlContent, err = h.generateSitemap(ctx, siteURL)
	}

	if err != nil {
		logAndHTTPError(w, "Failed to generate sitemap", http.StatusInternalServerError, "failed to generate sitemap", "error", err)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Browser cache for 1 hour
	_, _ = w.Write(xmlContent)
}

// generateSitemap generates sitemap XML without caching.
func (h *FrontendHandler) generateSitemap(ctx context.Context, siteURL string) ([]byte, error) {
	builder := seo.NewSitemapBuilder(siteURL)
	builder.AddHomepage()

	// Add published pages (excluding noindex pages)
	pages, err := h.queries.ListPublishedPagesForSitemap(ctx)
	if err != nil {
		h.logger.Error("failed to get pages for sitemap", "error", err)
	} else {
		for _, p := range pages {
			builder.AddPage(seo.SitemapPage{
				Slug:      p.Slug,
				UpdatedAt: p.UpdatedAt,
			})
		}
	}

	// Add categories
	categories, err := h.queries.ListCategoriesForSitemap(ctx)
	if err != nil {
		h.logger.Error("failed to get categories for sitemap", "error", err)
	} else {
		for _, c := range categories {
			builder.AddCategory(seo.SitemapCategory{
				Slug:      c.Slug,
				UpdatedAt: c.UpdatedAt,
			})
		}
	}

	// Add tags
	tags, err := h.queries.ListTagsForSitemap(ctx)
	if err != nil {
		h.logger.Error("failed to get tags for sitemap", "error", err)
	} else {
		for _, t := range tags {
			builder.AddTag(seo.SitemapTag{
				Slug:      t.Slug,
				UpdatedAt: t.UpdatedAt,
			})
		}
	}

	return builder.Build()
}

// Robots generates and serves the robots.txt file.
func (h *FrontendHandler) Robots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteURL := h.getSiteURL(ctx, r)

	// Check for robots_disallow_all config (for staging sites)
	disallowAll := false
	var disallowStr string
	if h.cacheManager != nil {
		disallowStr, _ = h.cacheManager.GetConfig(ctx, "robots_disallow_all")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "robots_disallow_all"); err == nil {
		disallowStr = cfg.Value
	}
	disallowAll = disallowStr == "1" || disallowStr == "true"

	// Get extra robots.txt rules from config
	extraRules := ""
	if h.cacheManager != nil {
		extraRules, _ = h.cacheManager.GetConfig(ctx, "robots_txt_extra")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "robots_txt_extra"); err == nil {
		extraRules = cfg.Value
	}

	// Build robots.txt
	robotsContent := seo.GenerateRobots(siteURL, disallowAll, extraRules)

	// Send response
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
	_, _ = w.Write([]byte(robotsContent))
}

// Security generates and serves the security.txt file (RFC 9116).
func (h *FrontendHandler) Security(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteURL := h.getSiteURL(ctx, r)

	// Get security contact from config
	var contact string
	if h.cacheManager != nil {
		contact, _ = h.cacheManager.GetConfig(ctx, "security_contact")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "security_contact"); err == nil {
		contact = cfg.Value
	}

	// If no contact configured, return 404
	if contact == "" {
		http.NotFound(w, r)
		return
	}

	// Get optional security policy URL
	var policy string
	if h.cacheManager != nil {
		policy, _ = h.cacheManager.GetConfig(ctx, "security_policy")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "security_policy"); err == nil {
		policy = cfg.Value
	}

	// Build security.txt with 1 year expiry
	config := seo.SecurityTxtConfig{
		Contact:            []string{contact},
		PreferredLanguages: "en",
	}

	if policy != "" {
		config.Policy = policy
	}

	// Set canonical URL
	if siteURL != "" {
		config.Canonical = siteURL + "/.well-known/security.txt"
	}

	builder := seo.NewSecurityTxtBuilder(config)
	securityContent := builder.Build()

	// Send response
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
	_, _ = w.Write([]byte(securityContent))
}

// pageToView converts a store.Page to a PageView with computed fields.
func (h *FrontendHandler) pageToView(ctx context.Context, p store.Page) PageView {
	pv := PageView{
		ID:        p.ID,
		Title:     p.Title,
		Slug:      p.Slug,
		Body:      template.HTML(p.Body),
		URL:       "/" + p.Slug,
		Status:    p.Status,
		Type:      "page",
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}

	// Set published date
	if p.PublishedAt.Valid {
		t := p.PublishedAt.Time
		pv.PublishedAt = &t
		pv.PublishedAtFormatted = t.Format("Jan 2, 2006")
	}

	// Generate excerpt from body (first 200 chars, strip HTML)
	pv.Excerpt = h.generateExcerpt(p.Body, 200)

	// Calculate reading time (approximately 200 words per minute)
	wordCount := len(strings.Fields(h.generateExcerpt(p.Body, len(p.Body))))
	pv.ReadingTime = (wordCount + 199) / 200 // Round up
	if pv.ReadingTime < 1 {
		pv.ReadingTime = 1
	}

	// Get featured image (thumbnail for listings - single page handlers can override to large)
	if p.FeaturedImageID.Valid {
		media, err := h.queries.GetMediaByID(ctx, p.FeaturedImageID.Int64)
		if err == nil {
			pv.FeaturedImage = fmt.Sprintf("/uploads/thumbnail/%s/%s", media.Uuid, media.Filename)
			pv.FeaturedImageMedium = fmt.Sprintf("/uploads/medium/%s/%s", media.Uuid, media.Filename)
			pv.FeaturedImageLarge = fmt.Sprintf("/uploads/large/%s/%s", media.Uuid, media.Filename)
			pv.FeaturedImageID = media.ID
			pv.FeaturedImageAlt = media.Alt.String
		}
	}

	// Set hide featured image option
	pv.HideFeaturedImage = p.HideFeaturedImage == 1

	// Get author
	author, err := h.queries.GetPageAuthor(ctx, p.ID)
	if err == nil {
		pv.Author = &AuthorView{
			ID:    author.ID,
			Name:  author.Name,
			Email: author.Email,
		}
	}

	// Get categories
	categories, err := h.queries.GetCategoriesForPage(ctx, p.ID)
	if err == nil && len(categories) > 0 {
		pv.Categories = make([]CategoryView, len(categories))
		for i, c := range categories {
			pv.Categories[i] = CategoryView{
				ID:          c.ID,
				Name:        c.Name,
				Slug:        c.Slug,
				Description: c.Description.String,
				URL:         redirectCategory + c.Slug,
			}
		}
		// Set primary category (first one)
		pv.Category = &pv.Categories[0]
	}

	// Get tags
	tags, err := h.queries.GetTagsForPage(ctx, p.ID)
	if err == nil {
		pv.Tags = make([]TagView, len(tags))
		for i, t := range tags {
			pv.Tags[i] = TagView{
				ID:   t.ID,
				Name: t.Name,
				Slug: t.Slug,
				URL:  redirectTag + t.Slug,
			}
		}
	}

	// Populate SEO fields
	pv.MetaTitle = p.MetaTitle
	pv.MetaDescription = p.MetaDescription
	pv.MetaKeywords = p.MetaKeywords
	pv.NoIndex = p.NoIndex != 0
	pv.NoFollow = p.NoFollow != 0
	pv.CanonicalURL = p.CanonicalUrl

	// Get OG image (from og_image_id or fall back to featured_image large variant)
	if p.OgImageID.Valid {
		ogMedia, err := h.queries.GetMediaByID(ctx, p.OgImageID.Int64)
		if err == nil {
			pv.OGImage = fmt.Sprintf("/uploads/large/%s/%s", ogMedia.Uuid, ogMedia.Filename)
		}
	} else if pv.FeaturedImageLarge != "" {
		pv.OGImage = pv.FeaturedImageLarge
	}

	return pv
}

// generateExcerpt creates a text excerpt from HTML content.
func (h *FrontendHandler) generateExcerpt(htmlContent string, maxLen int) string {
	// Simple HTML stripping (for a more robust solution, use a proper HTML parser)
	var result strings.Builder
	inTag := false
	for _, r := range htmlContent {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}

	text := strings.TrimSpace(result.String())
	// Unescape HTML entities (e.g., &nbsp; -> actual space)
	text = html.UnescapeString(text)
	// Collapse whitespace
	text = strings.Join(strings.Fields(text), " ")

	if len(text) <= maxLen {
		return text
	}

	// Truncate at word boundary
	truncated := text[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// getSidebarData fetches categories, tags, and recent pages for sidebar display.
// languageCode: if non-empty, filters by language; if empty, shows all languages.
func (h *FrontendHandler) getSidebarData(ctx context.Context, languageCode string) ([]CategoryView, []TagView, []PageView) {
	var categoryViews []CategoryView
	var tagViews []TagView
	var recentPageViews []PageView

	if languageCode != "" {
		// Get categories with usage counts filtered by language
		categoriesWithCount, err := h.queries.GetCategoryUsageCountsByLanguage(ctx, languageCode)
		if err != nil {
			h.logger.Error("failed to get sidebar categories", "error", err)
		}
		categoryViews = make([]CategoryView, 0, len(categoriesWithCount))
		for _, c := range categoriesWithCount {
			categoryViews = append(categoryViews, CategoryView{
				ID:          c.ID,
				Name:        c.Name,
				Slug:        c.Slug,
				Description: c.Description.String,
				URL:         redirectCategory + c.Slug,
				PageCount:   c.UsageCount,
			})
		}

		// Get tags with usage counts filtered by language
		tagsWithCount, err := h.queries.GetTagUsageCountsByLanguage(ctx, store.GetTagUsageCountsByLanguageParams{
			LanguageCode: languageCode,
			Limit:        20,
			Offset:       0,
		})
		if err != nil {
			h.logger.Error("failed to get sidebar tags", "error", err)
		}
		tagViews = make([]TagView, 0, len(tagsWithCount))
		for _, t := range tagsWithCount {
			tagViews = append(tagViews, TagView{
				ID:        t.ID,
				Name:      t.Name,
				Slug:      t.Slug,
				URL:       redirectTag + t.Slug,
				PageCount: t.UsageCount,
			})
		}

		// Get recent pages filtered by language
		recentPages, err := h.queries.ListPublishedPagesByLanguage(ctx, store.ListPublishedPagesByLanguageParams{
			LanguageCode: languageCode,
			Limit:        5,
			Offset:       0,
		})
		if err != nil {
			h.logger.Error("failed to get sidebar recent pages", "error", err)
		}
		recentPageViews = make([]PageView, 0, len(recentPages))
		for _, p := range recentPages {
			recentPageViews = append(recentPageViews, h.pageToView(ctx, p))
		}
	} else {
		// Fallback to all languages (for single-language sites)
		categoriesWithCount, err := h.queries.GetCategoryUsageCounts(ctx)
		if err != nil {
			h.logger.Error("failed to get sidebar categories", "error", err)
		}
		categoryViews = make([]CategoryView, 0, len(categoriesWithCount))
		for _, c := range categoriesWithCount {
			categoryViews = append(categoryViews, CategoryView{
				ID:          c.ID,
				Name:        c.Name,
				Slug:        c.Slug,
				Description: c.Description.String,
				URL:         redirectCategory + c.Slug,
				PageCount:   c.UsageCount,
			})
		}

		tagsWithCount, err := h.queries.GetTagUsageCounts(ctx, store.GetTagUsageCountsParams{
			Limit:  20,
			Offset: 0,
		})
		if err != nil {
			h.logger.Error("failed to get sidebar tags", "error", err)
		}
		tagViews = make([]TagView, 0, len(tagsWithCount))
		for _, t := range tagsWithCount {
			tagViews = append(tagViews, TagView{
				ID:        t.ID,
				Name:      t.Name,
				Slug:      t.Slug,
				URL:       redirectTag + t.Slug,
				PageCount: t.UsageCount,
			})
		}

		recentPages, err := h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
			Limit:  5,
			Offset: 0,
		})
		if err != nil {
			h.logger.Error("failed to get sidebar recent pages", "error", err)
		}
		recentPageViews = make([]PageView, 0, len(recentPages))
		for _, p := range recentPages {
			recentPageViews = append(recentPageViews, h.pageToView(ctx, p))
		}
	}

	return categoryViews, tagViews, recentPageViews
}

// stripHTMLPreserveMark strips HTML tags from a string but preserves <mark> and </mark> tags.
// Block-level elements (h1-h6, p, div, br, li) produce newlines when stripped.
// This is used for search result highlights where we want plain text with highlighted terms.
func (h *FrontendHandler) stripHTMLPreserveMark(s string) string {
	// Block-level tags that should produce newlines
	blockTags := []string{"</h1>", "</h2>", "</h3>", "</h4>", "</h5>", "</h6>", "</p>", "</div>", "</li>", "</tr>", "<br>", "<br/>", "<br />"}

	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			// Check if it's a <mark> or </mark> tag
			if i+6 <= len(s) && strings.ToLower(s[i:i+6]) == "<mark>" {
				result.WriteString("<mark>")
				i += 6
				continue
			}
			if i+7 <= len(s) && strings.ToLower(s[i:i+7]) == "</mark>" {
				result.WriteString("</mark>")
				i += 7
				continue
			}

			// Check if it's a block-level closing tag that should produce a newline
			foundBlock := false
			for _, blockTag := range blockTags {
				if i+len(blockTag) <= len(s) && strings.ToLower(s[i:i+len(blockTag)]) == blockTag {
					result.WriteString("\n")
					i += len(blockTag)
					foundBlock = true
					break
				}
			}
			if foundBlock {
				continue
			}

			// Skip other HTML tags
			for i < len(s) && s[i] != '>' {
				i++
			}
			if i < len(s) {
				i++ // skip the '>'
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}

	// Process the text: trim each line and remove empty lines
	text := result.String()
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	// Join with <br> for HTML rendering
	return strings.Join(cleanLines, "<br>")
}

// getSiteData returns site-wide data for templates.
func (h *FrontendHandler) getSiteData(ctx context.Context) SiteData {
	site := SiteData{
		SiteName:    "oCMS",
		Description: "A simple content management system",
		CurrentYear: time.Now().Year(),
		Settings:    make(map[string]string),
	}

	// Get site config values from cache (single DB query on cache miss)
	if h.cacheManager != nil {
		if name, err := h.cacheManager.GetConfig(ctx, "site_name"); err == nil && name != "" {
			site.SiteName = name
		}
		if desc, err := h.cacheManager.GetConfig(ctx, "site_description"); err == nil && desc != "" {
			site.Description = desc
		}
		if url, err := h.cacheManager.GetConfig(ctx, "site_url"); err == nil && url != "" {
			site.URL = url
		}
	} else {
		// Fallback to direct DB queries if cache not available
		if cfg, err := h.queries.GetConfigByKey(ctx, "site_name"); err == nil {
			site.SiteName = cfg.Value
		}
		if cfg, err := h.queries.GetConfigByKey(ctx, "site_description"); err == nil {
			site.Description = cfg.Value
		}
		if cfg, err := h.queries.GetConfigByKey(ctx, "site_url"); err == nil {
			site.URL = cfg.Value
		}
	}

	// Get active theme config and load theme settings
	if activeTheme := h.themeManager.GetActiveTheme(); activeTheme != nil {
		site.Theme = &activeTheme.Config

		// Load theme settings from cache
		configKey := "theme_settings_" + activeTheme.Name
		var settingsJSON string
		if h.cacheManager != nil {
			settingsJSON, _ = h.cacheManager.GetConfig(ctx, configKey)
		} else {
			if cfg, err := h.queries.GetConfigByKey(ctx, configKey); err == nil {
				settingsJSON = cfg.Value
			}
		}
		if settingsJSON != "" {
			var settings map[string]string
			if err := json.Unmarshal([]byte(settingsJSON), &settings); err == nil {
				site.Settings = settings
			}
		}
		// Note: defaults from theme.json are NOT merged here.
		// The CSS file contains defaults; ThemeSettings only holds user overrides.
	}

	return site
}

// getBaseTemplateData returns the base template data with common fields populated.
func (h *FrontendHandler) getBaseTemplateData(r *http.Request, title, metaDesc string) BaseTemplateData {
	ctx := r.Context()
	site := h.getSiteData(ctx)

	data := BaseTemplateData{
		Title:           title,
		MetaDescription: metaDesc,
		SiteName:        site.SiteName,
		SiteTagline:     site.Description,
		SiteURL:         site.URL,
		RequestURI:      r.URL.RequestURI(),
		CurrentPath:     r.URL.Path,
		Year:            site.CurrentYear,
		Site:            site,
		ThemeSettings:   site.Settings,
		ShowSearch:      true,
		SearchQuery:     r.URL.Query().Get("q"),
	}

	// Get site logo and custom CSS from cache
	if h.cacheManager != nil {
		if logo, err := h.cacheManager.GetConfig(ctx, "site_logo"); err == nil && logo != "" {
			data.SiteLogo = logo
		}
		if css, err := h.cacheManager.GetConfig(ctx, "custom_css"); err == nil && css != "" {
			data.CustomCSS = css
		}
	} else {
		// Fallback to direct DB queries if cache not available
		if cfg, err := h.queries.GetConfigByKey(ctx, "site_logo"); err == nil && cfg.Value != "" {
			data.SiteLogo = cfg.Value
		}
		if cfg, err := h.queries.GetConfigByKey(ctx, "custom_css"); err == nil && cfg.Value != "" {
			data.CustomCSS = cfg.Value
		}
	}

	// Load language info from middleware context FIRST (needed for menu loading)
	var langCode string
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		data.CurrentLanguage = &LanguageView{
			ID:         langInfo.ID,
			Code:       langInfo.Code,
			Name:       langInfo.Name,
			NativeName: langInfo.NativeName,
			Direction:  langInfo.Direction,
			IsDefault:  langInfo.IsDefault,
			IsCurrent:  true,
		}
		data.LangCode = langInfo.Code
		langCode = langInfo.Code
		data.LangDirection = langInfo.Direction
		if data.LangDirection == "" {
			data.LangDirection = "ltr"
		}
		// Set language prefix for URLs (empty for default language)
		if !langInfo.IsDefault {
			data.LangPrefix = "/" + langInfo.Code
		}

		// Apply translated config values for current language
		if translatedName, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "site_name",
			Code:      langInfo.Code,
		}); err == nil && translatedName.Value != "" {
			data.SiteName = translatedName.Value
			data.Site.SiteName = translatedName.Value
		}
		if translatedDesc, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "site_description",
			Code:      langInfo.Code,
		}); err == nil && translatedDesc.Value != "" {
			data.SiteTagline = translatedDesc.Value
			data.Site.Description = translatedDesc.Value
		}
		// Get powered_by translation for footer
		if translatedPoweredBy, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "powered_by",
			Code:      langInfo.Code,
		}); err == nil && translatedPoweredBy.Value != "" {
			data.FooterText = translatedPoweredBy.Value
		}
		// Get copyright translation for footer
		if translatedCopyright, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "copyright",
			Code:      langInfo.Code,
		}); err == nil && translatedCopyright.Value != "" {
			data.CopyrightText = translatedCopyright.Value
		}
	}

	// Load menus by slug and language
	data.MainMenu = h.loadMenu("main", r.URL.Path, langCode)
	data.FooterMenu = h.loadMenu("footer", r.URL.Path, langCode)
	// Navigation/FooterNav are aliases for MainMenu/FooterMenu (for template compatibility)
	data.Navigation = data.MainMenu
	data.FooterNav = data.FooterMenu

	// Load widgets for the active theme
	if activeTheme := h.themeManager.GetActiveTheme(); activeTheme != nil {
		data.Widgets = h.widgetService.GetAllWidgetsForTheme(ctx, activeTheme.Name)
	}

	// Load all active languages for language picker
	activeLanguages, err := h.queries.ListActiveLanguages(ctx)
	if err == nil && len(activeLanguages) > 1 {
		data.ShowLanguagePicker = true
		data.Languages = make([]LanguageView, 0, len(activeLanguages))
		for _, lang := range activeLanguages {
			lv := LanguageView{
				ID:         lang.ID,
				Code:       lang.Code,
				Name:       lang.Name,
				NativeName: lang.NativeName,
				Direction:  lang.Direction,
				IsDefault:  lang.IsDefault,
				IsCurrent:  data.LangCode == lang.Code,
			}
			data.Languages = append(data.Languages, lv)
		}
	}

	// Load footer text values (fallback to database if cache unavailable)
	if data.FooterText == "" {
		data.FooterText = h.getConfigValue(ctx, "powered_by")
	}
	if data.CopyrightText == "" {
		data.CopyrightText = h.getConfigValue(ctx, "copyright")
	}

	return data
}

// loadMenu loads a menu by slug and language, and marks active items.
func (h *FrontendHandler) loadMenu(slug, currentPath, langCode string) []MenuItem {
	var items []service.MenuItem
	if langCode != "" {
		items = h.menuService.GetMenuForLanguage(slug, langCode)
	} else {
		items = h.menuService.GetMenu(slug)
	}
	if items == nil {
		return nil
	}

	return h.menuItemsToView(items, currentPath)
}

// menuItemsToView converts service menu items to view items with active state.
func (h *FrontendHandler) menuItemsToView(items []service.MenuItem, currentPath string) []MenuItem {
	result := make([]MenuItem, 0, len(items))
	for _, item := range items {
		mi := MenuItem{
			Title:    item.Title,
			URL:      item.URL,
			Target:   item.Target,
			IsActive: item.URL == currentPath,
			Children: h.menuItemsToView(item.Children, currentPath),
		}
		result = append(result, mi)
	}
	return result
}

// getConfigValue retrieves a config value, trying cache first then database.
func (h *FrontendHandler) getConfigValue(ctx context.Context, key string) string {
	if h.cacheManager != nil {
		if value, err := h.cacheManager.GetConfig(ctx, key); err == nil && value != "" {
			return value
		}
	}
	if cfg, err := h.queries.GetConfigByKey(ctx, key); err == nil && cfg.Value != "" {
		return cfg.Value
	}
	return ""
}

// getPageTranslations returns translation links for a page for the language switcher.
func (h *FrontendHandler) getPageTranslations(ctx context.Context, pageID int64, currentLangCode, siteURL string) ([]TranslationLink, []HrefLangLink) {
	translations, err := h.queries.GetPageAvailableTranslations(ctx, store.GetPageAvailableTranslationsParams{
		EntityID:      pageID,
		ID:            pageID,
		EntityID_2:    pageID,
		TranslationID: pageID,
	})
	if err != nil {
		h.logger.Error("failed to get page translations", "pageID", pageID, "error", err)
		return nil, nil
	}

	var links []TranslationLink
	var hrefLangs []HrefLangLink
	var defaultLangURL string

	for _, t := range translations {
		lv := LanguageView{
			ID:         t.LanguageID,
			Code:       t.LanguageCode,
			Name:       t.LanguageName,
			NativeName: t.LanguageNativeName,
			Direction:  t.LanguageDirection,
			IsDefault:  t.IsDefault,
			IsCurrent:  t.LanguageCode == currentLangCode,
		}

		hasPage := t.PageID != 0 && t.PageSlug != ""

		// Build URL
		var url string
		if hasPage {
			if lv.IsDefault {
				// Default language uses root URL
				url = "/" + t.PageSlug
			} else {
				// Non-default language uses language prefix
				url = "/" + t.LanguageCode + "/" + t.PageSlug
			}
		} else {
			// No translation - link to homepage in that language
			if lv.IsDefault {
				url = "/"
			} else {
				url = "/" + t.LanguageCode + "/"
			}
		}

		links = append(links, TranslationLink{
			Language:  lv,
			URL:       url,
			PageTitle: t.PageTitle,
			HasPage:   hasPage,
		})

		// Build hreflang link
		if hasPage && siteURL != "" {
			fullURL := strings.TrimRight(siteURL, "/") + url
			hrefLangs = append(hrefLangs, HrefLangLink{
				Lang: t.LanguageCode,
				Href: fullURL,
			})
			if lv.IsDefault {
				defaultLangURL = fullURL
			}
		}
	}

	// Add x-default hreflang (points to default language version)
	if defaultLangURL != "" {
		hrefLangs = append(hrefLangs, HrefLangLink{
			Lang: "x-default",
			Href: defaultLangURL,
		})
	}

	return links, hrefLangs
}

// getHomepageTranslations returns translation links for the homepage.
// All language links use ?lang=XX to explicitly set the language preference cookie.
func (h *FrontendHandler) getHomepageTranslations(currentLangCode string, languages []LanguageView) []TranslationLink {
	links := make([]TranslationLink, 0, len(languages))
	for _, lang := range languages {
		// Use ?lang= parameter for all languages to ensure cookie is set correctly
		// This fixes the issue where clicking a language link didn't update the cookie
		url := "/?lang=" + lang.Code

		links = append(links, TranslationLink{
			Language: LanguageView{
				ID:         lang.ID,
				Code:       lang.Code,
				Name:       lang.Name,
				NativeName: lang.NativeName,
				Direction:  lang.Direction,
				IsDefault:  lang.IsDefault,
				IsCurrent:  lang.Code == currentLangCode,
			},
			URL:       url,
			PageTitle: "",
			HasPage:   true,
		})
	}
	return links
}

// getPageNum extracts page number from request query params.
func (h *FrontendHandler) getPageNum(r *http.Request) int {
	return ParsePageParam(r)
}

// defaultPerPage is the default number of items per page for pagination.
const defaultPerPage = 10

// getSiteURL retrieves the site URL from config with cache fallback.
// Falls back to request host if no config is set.
func (h *FrontendHandler) getSiteURL(ctx context.Context, r *http.Request) string {
	var siteURL string
	if h.cacheManager != nil {
		siteURL, _ = h.cacheManager.GetConfig(ctx, "site_url")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "site_url"); err == nil {
		siteURL = cfg.Value
	}
	if siteURL == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		siteURL = scheme + "://" + r.Host
	}
	return siteURL
}

// buildPaginationPages builds the page links with ellipsis for pagination.
// This is extracted to avoid duplication between frontend and admin pagination.
func buildPaginationPages[T any](currentPage, totalPages int, buildURL func(int) string, makePage func(number int, url string, isCurrent, isEllipsis bool) T) []T {
	var pages []T

	start := currentPage - 2
	end := currentPage + 2
	if start < 1 {
		start = 1
		end = 5
	}
	if end > totalPages {
		end = totalPages
		start = end - 4
		if start < 1 {
			start = 1
		}
	}

	// Add first page and ellipsis if needed
	if start > 1 {
		pages = append(pages, makePage(1, buildURL(1), false, false))
		if start > 2 {
			pages = append(pages, makePage(0, "", false, true))
		}
	}

	// Add page numbers
	for i := start; i <= end; i++ {
		pages = append(pages, makePage(i, buildURL(i), i == currentPage, false))
	}

	// Add ellipsis and last page if needed
	if end < totalPages {
		if end < totalPages-1 {
			pages = append(pages, makePage(0, "", false, true))
		}
		pages = append(pages, makePage(totalPages, buildURL(totalPages), false, false))
	}

	return pages
}

// buildPagination creates pagination data for templates.
func (h *FrontendHandler) buildPagination(currentPage, totalItems int, baseURL string) Pagination {
	perPage := defaultPerPage
	totalPages := (totalItems + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	pagination := Pagination{
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		TotalItems:  int64(totalItems),
		PerPage:     perPage,
		HasPrev:     currentPage > 1,
		HasNext:     currentPage < totalPages,
		HasFirst:    currentPage > 1,
		HasLast:     currentPage < totalPages,
	}

	// Build URL helper
	buildURL := func(page int) string {
		if strings.Contains(baseURL, "?") {
			return fmt.Sprintf("%s&page=%d", baseURL, page)
		}
		return fmt.Sprintf("%s?page=%d", baseURL, page)
	}

	if pagination.HasPrev {
		pagination.PrevURL = buildURL(currentPage - 1)
	}
	if pagination.HasNext {
		pagination.NextURL = buildURL(currentPage + 1)
	}
	if pagination.HasFirst {
		pagination.FirstURL = buildURL(1)
	}
	if pagination.HasLast {
		pagination.LastURL = buildURL(totalPages)
	}

	// Build page links using shared helper
	pagination.Pages = buildPaginationPages(currentPage, totalPages, buildURL,
		func(number int, url string, isCurrent, isEllipsis bool) PaginationPage {
			return PaginationPage{Number: number, URL: url, IsCurrent: isCurrent, IsEllipsis: isEllipsis}
		})

	return pagination
}

// render renders a template using the active theme.
func (h *FrontendHandler) render(w http.ResponseWriter, templateName string, data any) {
	activeTheme := h.themeManager.GetActiveTheme()
	if activeTheme == nil {
		logAndHTTPError(w, "No active theme", http.StatusInternalServerError, "no active theme")
		return
	}

	// Render to buffer first to catch errors
	buf := new(bytes.Buffer)
	if err := activeTheme.RenderPage(buf, templateName, data); err != nil {
		logAndHTTPError(w, "Template rendering error", http.StatusInternalServerError, "failed to render template", "template", templateName, "error", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// renderNotFound renders the 404 page.
func (h *FrontendHandler) renderNotFound(w http.ResponseWriter, r *http.Request) {
	base := h.getBaseTemplateData(r, "Page Not Found", "")
	base.BodyClass = "error-404"

	// Get suggested pages (5 most recent published)
	var suggestedPages []PageView
	pages, err := h.queries.ListPublishedPages(r.Context(), store.ListPublishedPagesParams{
		Limit:  5,
		Offset: 0,
	})
	if err == nil {
		for _, p := range pages {
			suggestedPages = append(suggestedPages, h.pageToView(r.Context(), p))
		}
	}

	data := NotFoundData{
		BaseTemplateData: base,
		SuggestedPages:   suggestedPages,
	}
	w.WriteHeader(http.StatusNotFound)
	h.render(w, "404", data)
}

// renderInternalError renders a 500 Internal Server Error page.
func (h *FrontendHandler) renderInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Error 500</title>
<style>body{font-family:system-ui,sans-serif;max-width:600px;margin:100px auto;padding:20px;text-align:center}h1{color:#dc3545}</style>
</head>
<body>
<h1>500 - Internal Server Error</h1>
<p>An error occurred while processing your request.</p>
<p><a href="/">Return to homepage</a></p>
</body>
</html>`)
}
