// Package handler provides HTTP handlers for the application.
package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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
	Highlight            string // Search result highlight
	Author               *AuthorView
	Category             *CategoryView
	Categories           []CategoryView
	Tags                 []TagView
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
	PrevURL     string
	NextURL     string
	Pages       []PaginationPage
}

// PaginationPage represents a single page link in pagination.
type PaginationPage struct {
	Number    int
	URL       string
	IsCurrent bool
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
	Pages      []PageView
	Pagination Pagination
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
	db           *sql.DB
	queries      *store.Queries
	themeManager *theme.Manager
	menuService  *service.MenuService
	logger       *slog.Logger
}

// NewFrontendHandler creates a new FrontendHandler.
func NewFrontendHandler(db *sql.DB, themeManager *theme.Manager, logger *slog.Logger) *FrontendHandler {
	return &FrontendHandler{
		db:           db,
		queries:      store.New(db),
		themeManager: themeManager,
		menuService:  service.NewMenuService(db),
		logger:       logger,
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
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
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

	h.render(w, r, "home", data)
}

// Page handles single page display.
func (h *FrontendHandler) Page(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get published page by slug
	page, err := h.queries.GetPublishedPageBySlug(ctx, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get page", "slug", slug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Convert to PageView
	pageView := h.pageToView(ctx, page)

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

	// Get base template data
	base := h.getBaseTemplateData(r, pageView.Title, pageView.Excerpt)
	base.FeaturedImage = pageView.FeaturedImage
	base.Canonical = base.SiteURL + "/" + slug
	base.BodyClass = "single-page"

	data := PageData{
		BaseTemplateData: base,
		Page:             &pageView,
		RelatedPages:     relatedPages,
		ShowAuthorBox:    true,
	}

	h.render(w, r, "page", data)
}

// Category handles category archive display.
func (h *FrontendHandler) Category(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get category
	category, err := h.queries.GetCategoryBySlug(ctx, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get category", "slug", slug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Pagination
	page := h.getPageNum(r)
	perPage := 10
	offset := (page - 1) * perPage

	// Get pages in category
	pages, err := h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
		CategoryID: category.ID,
		Limit:      int64(perPage),
		Offset:     int64(offset),
	})
	if err != nil {
		h.logger.Error("failed to get pages for category", "category", slug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
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

	pagination := h.buildPagination(page, int(total), perPage, fmt.Sprintf("/category/%s", slug))

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

	h.render(w, r, "category", data)
}

// Tag handles tag archive display.
func (h *FrontendHandler) Tag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")

	// Get tag
	tag, err := h.queries.GetTagBySlug(ctx, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderNotFound(w, r)
			return
		}
		h.logger.Error("failed to get tag", "slug", slug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Pagination
	page := h.getPageNum(r)
	perPage := 10
	offset := (page - 1) * perPage

	// Get pages with tag
	pages, err := h.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{
		TagID:  tag.ID,
		Limit:  int64(perPage),
		Offset: int64(offset),
	})
	if err != nil {
		h.logger.Error("failed to get pages for tag", "tag", slug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
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

	pagination := h.buildPagination(page, int(total), perPage, fmt.Sprintf("/tag/%s", slug))

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

	h.render(w, r, "tag", data)
}

// Search handles search results display.
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
		h.render(w, r, "search", data)
		return
	}

	// Pagination
	page := h.getPageNum(r)
	perPage := 10
	offset := (page - 1) * perPage

	// Simple search - search in title and body
	// For now, use LIKE queries until FTS5 is set up in a later iteration
	searchPattern := "%" + query + "%"

	// Get all published pages and filter (simple approach for now)
	allPages, err := h.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
		Limit:  1000, // Get all for searching
		Offset: 0,
	})
	if err != nil {
		h.logger.Error("failed to search pages", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Filter pages that match the query
	var matchedPages []store.Page
	lowerQuery := strings.ToLower(query)
	for _, p := range allPages {
		if strings.Contains(strings.ToLower(p.Title), lowerQuery) ||
			strings.Contains(strings.ToLower(p.Body), lowerQuery) {
			matchedPages = append(matchedPages, p)
		}
	}

	// Apply pagination to matched results
	total := len(matchedPages)
	start := offset
	end := offset + perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginatedPages := matchedPages[start:end]

	// Convert to PageViews
	pageViews := make([]PageView, 0, len(paginatedPages))
	for _, p := range paginatedPages {
		pageViews = append(pageViews, h.pageToView(ctx, p))
	}

	pagination := h.buildPagination(page, total, perPage, fmt.Sprintf("/search?q=%s", query))

	_ = searchPattern // Will be used when FTS5 is implemented

	// Get base template data
	title := fmt.Sprintf("Search: %s", query)
	base := h.getBaseTemplateData(r, title, "")
	base.BodyClass = "search"

	data := SearchData{
		BaseTemplateData: base,
		Query:            query,
		Pages:            pageViews,
		Pagination:       pagination,
		ResultCount:      total,
	}

	h.render(w, r, "search", data)
}

// NotFound renders the 404 page.
func (h *FrontendHandler) NotFound(w http.ResponseWriter, r *http.Request) {
	h.renderNotFound(w, r)
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

	// Get featured image
	if p.FeaturedImageID.Valid {
		media, err := h.queries.GetMediaByID(ctx, p.FeaturedImageID.Int64)
		if err == nil {
			pv.FeaturedImage = fmt.Sprintf("/uploads/%s/%s", media.Uuid, media.Filename)
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

// getSiteData returns site-wide data for templates.
func (h *FrontendHandler) getSiteData(ctx context.Context) SiteData {
	site := SiteData{
		SiteName:    "oCMS",
		Description: "A simple content management system",
		CurrentYear: time.Now().Year(),
		Settings:    make(map[string]string),
	}

	// Get site name from config
	if cfg, err := h.queries.GetConfigByKey(ctx, "site_name"); err == nil {
		site.SiteName = cfg.Value
	}

	// Get site description from config
	if cfg, err := h.queries.GetConfigByKey(ctx, "site_description"); err == nil {
		site.Description = cfg.Value
	}

	// Get site URL from config
	if cfg, err := h.queries.GetConfigByKey(ctx, "site_url"); err == nil {
		site.URL = cfg.Value
	}

	// Get active theme config and load theme settings
	if activeTheme := h.themeManager.GetActiveTheme(); activeTheme != nil {
		site.Theme = &activeTheme.Config

		// Load theme settings from config table
		configKey := "theme_settings_" + activeTheme.Name
		if cfg, err := h.queries.GetConfigByKey(ctx, configKey); err == nil {
			var settings map[string]string
			if err := json.Unmarshal([]byte(cfg.Value), &settings); err == nil {
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

	// Get site logo from config
	if cfg, err := h.queries.GetConfigByKey(ctx, "site_logo"); err == nil && cfg.Value != "" {
		data.SiteLogo = cfg.Value
	}

	// Load custom CSS from config
	if cfg, err := h.queries.GetConfigByKey(ctx, "custom_css"); err == nil && cfg.Value != "" {
		data.CustomCSS = cfg.Value
	}

	// Load menus by slug
	data.MainMenu = h.loadMenu("main", r.URL.Path)
	data.FooterMenu = h.loadMenu("footer", r.URL.Path)
	// Navigation/FooterNav are aliases for MainMenu/FooterMenu (for template compatibility)
	data.Navigation = data.MainMenu
	data.FooterNav = data.FooterMenu

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

// getPageNum extracts page number from request query params.
func (h *FrontendHandler) getPageNum(r *http.Request) int {
	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// buildPagination creates pagination data for templates.
func (h *FrontendHandler) buildPagination(currentPage, totalItems, perPage int, baseURL string) Pagination {
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

	// Build page links (show max 5 pages around current)
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

	for i := start; i <= end; i++ {
		pagination.Pages = append(pagination.Pages, PaginationPage{
			Number:    i,
			URL:       buildURL(i),
			IsCurrent: i == currentPage,
		})
	}

	return pagination
}

// render renders a template using the active theme.
func (h *FrontendHandler) render(w http.ResponseWriter, r *http.Request, templateName string, data any) {
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
	buf.WriteTo(w)
}

// renderNotFound renders the 404 page.
func (h *FrontendHandler) renderNotFound(w http.ResponseWriter, r *http.Request) {
	base := h.getBaseTemplateData(r, "Page Not Found", "")
	base.BodyClass = "error-404"
	data := NotFoundData{
		BaseTemplateData: base,
	}
	w.WriteHeader(http.StatusNotFound)
	h.render(w, r, "404", data)
}

// renderError renders an error page.
func (h *FrontendHandler) renderError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Error</title></head>
<body>
<h1>%d - %s</h1>
<p>An error occurred while processing your request.</p>
</body>
</html>`, statusCode, message)
}
