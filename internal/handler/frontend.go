// Package handler provides HTTP handlers for the application.
package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/cache"
	"ocms-go/internal/middleware"
	"ocms-go/internal/seo"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
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
	FeaturedImageLarge   string // Large variant for single page views
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
	Theme       *theme.ThemeConfig
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

// HomeData holds data for the homepage template.
type HomeData struct {
	BaseTemplateData
	Page             *PageView
	FeaturedPages    []PageView
	RecentPages      []PageView
	Categories       []CategoryView
	Tags             []TagView
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
}

// ListData holds data for list templates (blog, archives).
type ListData struct {
	BaseTemplateData
	Pages       []PageView
	Pagination  Pagination
	Description string // Optional description for list pages (blog, archives, etc.)
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
}

// TagPageData holds data for tag archive templates.
type TagPageData struct {
	BaseTemplateData
	Tag         TagView
	Pages       []PageView
	Pagination  Pagination
	PageCount   int
	RelatedTags []TagView
}

// SearchData holds data for search results templates.
type SearchData struct {
	BaseTemplateData
	Query           string
	Pages           []PageView
	Pagination      Pagination
	ResultCount     int
	PopularSearches []string
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

	// Get base template data
	base := h.getBaseTemplateData(r, "", "")
	base.MetaDescription = base.Site.Description
	base.BodyClass = "home"

	// Get recent published pages
	recentPages, err := h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
		Limit:  10,
		Offset: 0,
	})
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

	// Get categories with usage counts
	categoriesWithCount, err := h.queries.GetCategoryUsageCounts(ctx)
	if err != nil {
		h.logger.Error("failed to get categories", "error", err)
	}
	categoryViews := make([]CategoryView, 0, len(categoriesWithCount))
	for _, c := range categoriesWithCount {
		categoryViews = append(categoryViews, CategoryView{
			ID:          c.ID,
			Name:        c.Name,
			Slug:        c.Slug,
			Description: c.Description.String,
			URL:         "/category/" + c.Slug,
			PageCount:   c.UsageCount,
		})
	}

	// Get tags with usage counts
	tagsWithCount, err := h.queries.GetTagUsageCounts(ctx, store.GetTagUsageCountsParams{
		Limit:  20,
		Offset: 0,
	})
	if err != nil {
		h.logger.Error("failed to get tags", "error", err)
	}
	tagViews := make([]TagView, 0, len(tagsWithCount))
	for _, t := range tagsWithCount {
		tagViews = append(tagViews, TagView{
			ID:        t.ID,
			Name:      t.Name,
			Slug:      t.Slug,
			URL:       "/tag/" + t.Slug,
			PageCount: t.UsageCount,
		})
	}

	// Split into featured (first 3) and recent (rest)
	var featuredPages, restPages []PageView
	if len(recentPageViews) > 3 {
		featuredPages = recentPageViews[:3]
		restPages = recentPageViews[3:]
	} else {
		featuredPages = recentPageViews
	}

	// Set language cookie if detected from URL
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		middleware.SetLanguageCookie(w, langInfo.Code)
	}

	// Build homepage translations for language switcher
	if base.ShowLanguagePicker {
		base.Translations = h.getHomepageTranslations(base.LangCode, base.Languages)
	}

	data := HomeData{
		BaseTemplateData: base,
		FeaturedPages:    featuredPages,
		RecentPages:      restPages,
		Categories:       categoryViews,
		Tags:             tagViews,
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
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get page", "slug", slug, "error", err)
		h.renderInternalError(w)
		return
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

	data := PageData{
		BaseTemplateData: base,
		Page:             &pageView,
		RelatedPages:     relatedPages,
		ShowAuthorBox:    true,
	}

	h.render(w, "page", data)
}

// Category handles category archive display.
func (h *FrontendHandler) Category(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

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

	// Get pages in category
	pages, err := h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
		CategoryID: category.ID,
		Limit:      int64(defaultPerPage),
		Offset:     int64(offset),
	})
	if err != nil {
		h.logger.Error("failed to get pages for category", "category", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Get total count
	total, err := h.queries.CountPublishedPagesByCategory(ctx, category.ID)
	if err != nil {
		h.logger.Error("failed to count pages for category", "error", err)
		total = 0
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
		URL:         "/category/" + category.Slug,
	}

	pagination := h.buildPagination(page, int(total), fmt.Sprintf("/category/%s", slug))

	// Get base template data
	title := "Category: " + category.Name
	base := h.getBaseTemplateData(r, title, category.Description.String)
	base.BodyClass = "archive category"

	data := CategoryPageData{
		BaseTemplateData: base,
		Category:         categoryView,
		Pages:            pageViews,
		Pagination:       pagination,
		PageCount:        int(total),
	}

	h.render(w, "category", data)
}

// Tag handles tag archive display.
func (h *FrontendHandler) Tag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

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

	// Get pages with tag
	pages, err := h.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{
		TagID:  tag.ID,
		Limit:  int64(defaultPerPage),
		Offset: int64(offset),
	})
	if err != nil {
		h.logger.Error("failed to get pages for tag", "tag", slug, "error", err)
		h.renderInternalError(w)
		return
	}

	// Get total count
	total, err := h.queries.CountPublishedPagesForTag(ctx, tag.ID)
	if err != nil {
		h.logger.Error("failed to count pages for tag", "error", err)
		total = 0
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
		URL:  "/tag/" + tag.Slug,
	}

	pagination := h.buildPagination(page, int(total), fmt.Sprintf("/tag/%s", slug))

	// Get base template data
	title := "Tag: " + tag.Name
	base := h.getBaseTemplateData(r, title, "")
	base.BodyClass = "archive tag"

	data := TagPageData{
		BaseTemplateData: base,
		Tag:              tagView,
		Pages:            pageViews,
		Pagination:       pagination,
		PageCount:        int(total),
	}

	h.render(w, "tag", data)
}

// Blog handles the blog listing page displaying all published posts.
func (h *FrontendHandler) Blog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage

	// Get published pages
	pages, err := h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
		Limit:  int64(defaultPerPage),
		Offset: int64(offset),
	})
	if err != nil {
		h.logger.Error("failed to get blog pages", "error", err)
		h.renderInternalError(w)
		return
	}

	// Get total count
	total, err := h.queries.CountPublishedPages(ctx)
	if err != nil {
		h.logger.Error("failed to count blog pages", "error", err)
		total = 0
	}

	// Convert to PageViews
	pageViews := make([]PageView, 0, len(pages))
	for _, p := range pages {
		pageViews = append(pageViews, h.pageToView(ctx, p))
	}

	pagination := h.buildPagination(page, int(total), "/blog")

	// Get base template data
	base := h.getBaseTemplateData(r, "Blog", "")
	base.BodyClass = "archive blog"

	data := ListData{
		BaseTemplateData: base,
		Pages:            pageViews,
		Pagination:       pagination,
	}

	h.render(w, "list", data)
}

// Search handles search results display using FTS5 full-text search.
func (h *FrontendHandler) Search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")

	// If no query, show empty search page
	if query == "" {
		base := h.getBaseTemplateData(r, "Search", "")
		base.BodyClass = "search"
		data := SearchData{
			BaseTemplateData: base,
			Query:            "",
			Pages:            []PageView{},
		}
		h.render(w, "search", data)
		return
	}

	// Pagination
	page := h.getPageNum(r)
	offset := (page - 1) * defaultPerPage

	// Use FTS5 search service
	searchResults, total, err := h.searchService.SearchPublishedPages(ctx, service.SearchParams{
		Query:  query,
		Limit:  defaultPerPage,
		Offset: offset,
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
			}
		}

		pageViews = append(pageViews, pv)
	}

	pagination := h.buildPagination(page, int(total), fmt.Sprintf("/search?q=%s", query))

	// Get base template data
	title := fmt.Sprintf("Search: %s", query)
	base := h.getBaseTemplateData(r, title, "")
	base.BodyClass = "search"

	data := SearchData{
		BaseTemplateData: base,
		Query:            query,
		Pages:            pageViews,
		Pagination:       pagination,
		ResultCount:      int(total),
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

	// Get site URL from config (use cache if available)
	siteURL := ""
	if h.cacheManager != nil {
		siteURL, _ = h.cacheManager.GetConfig(ctx, "site_url")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "site_url"); err == nil {
		siteURL = cfg.Value
	}
	if siteURL == "" {
		// Fallback to request host
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		siteURL = scheme + "://" + r.Host
	}

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
		h.logger.Error("failed to generate sitemap", "error", err)
		http.Error(w, "Failed to generate sitemap", http.StatusInternalServerError)
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

	// Get site URL from config (use cache if available)
	siteURL := ""
	if h.cacheManager != nil {
		siteURL, _ = h.cacheManager.GetConfig(ctx, "site_url")
	} else if cfg, err := h.queries.GetConfigByKey(ctx, "site_url"); err == nil {
		siteURL = cfg.Value
	}
	if siteURL == "" {
		// Fallback to request host
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		siteURL = scheme + "://" + r.Host
	}

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

	// Get featured image (medium for listings - single page handlers can override to large)
	if p.FeaturedImageID.Valid {
		media, err := h.queries.GetMediaByID(ctx, p.FeaturedImageID.Int64)
		if err == nil {
			pv.FeaturedImage = fmt.Sprintf("/uploads/medium/%s/%s", media.Uuid, media.Filename)
			pv.FeaturedImageLarge = fmt.Sprintf("/uploads/large/%s/%s", media.Uuid, media.Filename)
		}
	}

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
				URL:         "/category/" + c.Slug,
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
				URL:  "/tag/" + t.Slug,
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
func (h *FrontendHandler) generateExcerpt(html string, maxLen int) string {
	// Simple HTML stripping (for a more robust solution, use a proper HTML parser)
	var result strings.Builder
	inTag := false
	for _, r := range html {
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

		// Fill in defaults for any missing settings
		for _, setting := range activeTheme.Config.Settings {
			if _, ok := site.Settings[setting.Key]; !ok {
				site.Settings[setting.Key] = setting.Default
			}
		}
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

	// Load menus by slug
	data.MainMenu = h.loadMenu("main", r.URL.Path)
	data.FooterMenu = h.loadMenu("footer", r.URL.Path)
	// Navigation/FooterNav are aliases for MainMenu/FooterMenu (for template compatibility)
	data.Navigation = data.MainMenu
	data.FooterNav = data.FooterMenu

	// Load widgets for the active theme
	if activeTheme := h.themeManager.GetActiveTheme(); activeTheme != nil {
		data.Widgets = h.widgetService.GetAllWidgetsForTheme(ctx, activeTheme.Name)
	}

	// Load language info from middleware context
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
		data.LangDirection = langInfo.Direction
		if data.LangDirection == "" {
			data.LangDirection = "ltr"
		}
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

	return data
}

// loadMenu loads a menu by slug and marks active items.
func (h *FrontendHandler) loadMenu(slug, currentPath string) []MenuItem {
	items := h.menuService.GetMenu(slug)
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
	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// defaultPerPage is the default number of items per page for pagination.
const defaultPerPage = 10

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

	// Build page links (show max 5 pages around current with ellipsis)
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
		pagination.Pages = append(pagination.Pages, PaginationPage{
			Number:    1,
			URL:       buildURL(1),
			IsCurrent: false,
		})
		if start > 2 {
			pagination.Pages = append(pagination.Pages, PaginationPage{
				IsEllipsis: true,
			})
		}
	}

	// Add page numbers
	for i := start; i <= end; i++ {
		pagination.Pages = append(pagination.Pages, PaginationPage{
			Number:    i,
			URL:       buildURL(i),
			IsCurrent: i == currentPage,
		})
	}

	// Add ellipsis and last page if needed
	if end < totalPages {
		if end < totalPages-1 {
			pagination.Pages = append(pagination.Pages, PaginationPage{
				IsEllipsis: true,
			})
		}
		pagination.Pages = append(pagination.Pages, PaginationPage{
			Number:    totalPages,
			URL:       buildURL(totalPages),
			IsCurrent: false,
		})
	}

	return pagination
}

// render renders a template using the active theme.
func (h *FrontendHandler) render(w http.ResponseWriter, templateName string, data any) {
	activeTheme := h.themeManager.GetActiveTheme()
	if activeTheme == nil {
		h.logger.Error("no active theme")
		http.Error(w, "No active theme", http.StatusInternalServerError)
		return
	}

	// Render to buffer first to catch errors
	buf := new(bytes.Buffer)
	if err := activeTheme.RenderPage(buf, templateName, data); err != nil {
		h.logger.Error("failed to render template", "template", templateName, "error", err)
		http.Error(w, "Template rendering error", http.StatusInternalServerError)
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
