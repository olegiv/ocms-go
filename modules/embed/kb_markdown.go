// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"context"
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const kbMaxPages = 10000

// GenerateSiteContentMarkdown builds a markdown document from all published
// pages, posts, tags, and categories.
func GenerateSiteContentMarkdown(ctx context.Context, q *store.Queries) (string, error) {
	siteURL, siteName, siteDesc := loadSiteInfo(ctx, q)

	// Fetch all published content
	allPages, err := q.ListPublishedPages(ctx, store.ListPublishedPagesParams{
		Limit:  kbMaxPages,
		Offset: 0,
	})
	if err != nil {
		return "", fmt.Errorf("listing published pages: %w", err)
	}

	// Split into pages and posts
	var pages, posts []store.Page
	for _, p := range allPages {
		if p.PageType == "post" {
			posts = append(posts, p)
		} else {
			pages = append(pages, p)
		}
	}

	// Build author cache
	authorCache := make(map[int64]string)
	for _, p := range allPages {
		if _, ok := authorCache[p.AuthorID]; !ok {
			user, err := q.GetUserByID(ctx, p.AuthorID)
			if err == nil {
				// Only use display name — never derive author label from email,
				// as the domain is easily guessable from the site URL in the same file.
				authorCache[p.AuthorID] = user.Name
			}
		}
	}

	// Pre-fetch all category and tag names per published page (avoids N+1).
	pageCats := make(map[int64][]string)
	if catRows, err := q.GetCategoryNamesForPublishedPages(ctx); err == nil {
		for _, r := range catRows {
			pageCats[r.PageID] = append(pageCats[r.PageID], r.Name)
		}
	}
	pageTags := make(map[int64][]string)
	if tagRows, err := q.GetTagNamesForPublishedPages(ctx); err == nil {
		for _, r := range tagRows {
			pageTags[r.PageID] = append(pageTags[r.PageID], r.Name)
		}
	}

	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(siteName)
	b.WriteString(" — Content Library\n\n")
	if siteDesc != "" {
		b.WriteString(siteDesc)
		b.WriteString("\n\n")
	}
	if siteURL != "" {
		b.WriteString("Site URL: ")
		b.WriteString(siteURL)
		b.WriteString("\n\n")
	}

	// Pages section
	if len(pages) > 0 {
		b.WriteString("## Pages\n\n")
		for _, p := range pages {
			writePageEntry(&b, p, siteURL, authorCache, pageCats, pageTags)
		}
	}

	// Posts section
	if len(posts) > 0 {
		b.WriteString("## Blog Posts\n\n")
		for _, p := range posts {
			writePageEntry(&b, p, siteURL, authorCache, pageCats, pageTags)
		}
	}

	// Categories section (published pages only)
	categories, err := q.GetPublishedCategoryUsageCounts(ctx)
	if err == nil && len(categories) > 0 {
		b.WriteString("## Categories\n\n")
		catNameByID := make(map[int64]string, len(categories))
		for _, c := range categories {
			catNameByID[c.ID] = c.Name
		}
		for _, c := range categories {
			b.WriteString("### ")
			b.WriteString(c.Name)
			b.WriteString("\n")
			b.WriteString("- URL: ")
			b.WriteString(siteURL)
			b.WriteString("/category/")
			b.WriteString(c.Slug)
			b.WriteString("\n")
			if c.Description.Valid && c.Description.String != "" {
				b.WriteString("- Description: ")
				b.WriteString(c.Description.String)
				b.WriteString("\n")
			}
			if c.ParentID.Valid {
				if name, ok := catNameByID[c.ParentID.Int64]; ok {
					b.WriteString("- Parent: ")
					b.WriteString(name)
					b.WriteString("\n")
				}
			}
			b.WriteString("- Pages: ")
			b.WriteString(fmt.Sprintf("%d", c.UsageCount))
			b.WriteString("\n\n")
		}
	}

	// Tags section (published pages only)
	tags, err := q.GetPublishedTagUsageCounts(ctx, store.GetPublishedTagUsageCountsParams{
		Limit:  kbMaxPages,
		Offset: 0,
	})
	if err == nil && len(tags) > 0 {
		b.WriteString("## Tags\n\n")
		for _, t := range tags {
			b.WriteString("- ")
			b.WriteString(t.Name)
			b.WriteString(" (")
			b.WriteString(fmt.Sprintf("%d", t.UsageCount))
			b.WriteString(" pages) — URL: ")
			b.WriteString(siteURL)
			b.WriteString("/tag/")
			b.WriteString(t.Slug)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// GenerateUserGuideMarkdown builds a markdown user guide based on actual
// site features detected from config, menus, categories, tags, and forms.
func GenerateUserGuideMarkdown(ctx context.Context, q *store.Queries) (string, error) {
	siteURL, siteName, _ := loadSiteInfo(ctx, q)

	// Do not include admin_email in the generated guide — it would be indexed
	// by the third-party AI service (Dify) and returned in chatbot responses.
	adminEmail := configValue(ctx, q, "admin_email")
	postsPerPage := configValue(ctx, q, "posts_per_page")
	if postsPerPage == "" {
		postsPerPage = "10"
	}
	hcaptchaKey := configValue(ctx, q, "hcaptcha_site_key")

	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(siteName)
	b.WriteString(" — User Guide\n\n")

	// Navigation section with menus
	b.WriteString("## Navigating the Site\n\n")
	b.WriteString("The website is organized using navigation menus. ")
	b.WriteString("Use the main menu to access different sections of the site.\n\n")

	writeMenuSection(&b, ctx, q, siteURL)

	// Categories (published pages only)
	categories, err := q.GetPublishedCategoryUsageCounts(ctx)
	if err == nil && len(categories) > 0 {
		b.WriteString("### Browsing by Category\n\n")
		b.WriteString("Categories organize content by topic. Available categories:\n\n")
		for _, c := range categories {
			b.WriteString("- **")
			b.WriteString(c.Name)
			b.WriteString("**")
			if c.Description.Valid && c.Description.String != "" {
				b.WriteString(": ")
				b.WriteString(c.Description.String)
			}
			b.WriteString(" — ")
			b.WriteString(siteURL)
			b.WriteString("/category/")
			b.WriteString(c.Slug)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Tags (published pages only)
	tags, err := q.GetPublishedTagUsageCounts(ctx, store.GetPublishedTagUsageCountsParams{
		Limit:  kbMaxPages,
		Offset: 0,
	})
	if err == nil && len(tags) > 0 {
		b.WriteString("### Browsing by Tag\n\n")
		b.WriteString("Tags provide additional content classification:\n\n")
		for _, t := range tags {
			b.WriteString("- **")
			b.WriteString(t.Name)
			b.WriteString("** (")
			b.WriteString(fmt.Sprintf("%d", t.UsageCount))
			b.WriteString(" pages) — ")
			b.WriteString(siteURL)
			b.WriteString("/tag/")
			b.WriteString(t.Slug)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Search
	b.WriteString("### Search\n\n")
	b.WriteString("Use the search page at ")
	b.WriteString(siteURL)
	b.WriteString("/search to find content. Enter your search query and browse paginated results.\n\n")

	// Content types
	b.WriteString("## Accessing Content\n\n")
	b.WriteString("### Pages\n\n")
	b.WriteString("Static pages contain reference information. Visit them directly via their URL or find them in the navigation menu.\n\n")
	b.WriteString("### Blog Posts\n\n")
	b.WriteString("Blog posts are listed at ")
	b.WriteString(siteURL)
	b.WriteString("/blog with the most recent first. Posts show author, publication date, estimated reading time, categories, and tags.\n\n")
	b.WriteString("### Pagination\n\n")
	b.WriteString("Content listings show ")
	b.WriteString(postsPerPage)
	b.WriteString(" items per page. Use page navigation at the bottom to browse more content.\n\n")

	// Language support
	writeLanguageSection(&b, ctx, q, siteURL)

	// User accounts
	b.WriteString("## User Accounts\n\n")
	b.WriteString("User accounts are managed by site administrators. There is no public registration.")
	if adminEmail != "" {
		b.WriteString(" If you need an account, contact the site administrator at ")
		b.WriteString(siteURL)
		b.WriteString("/forms/contact-us.")
	}
	b.WriteString("\n\n")

	b.WriteString("### Logging In\n\n")
	b.WriteString("Visit ")
	b.WriteString(siteURL)
	b.WriteString("/login to sign in with your email and password.")
	if hcaptchaKey != "" {
		b.WriteString(" Captcha verification may be required.")
	}
	b.WriteString("\n\n")
	b.WriteString("### Logging Out\n\n")
	b.WriteString("Click the logout button or navigate to ")
	b.WriteString(siteURL)
	b.WriteString("/logout.\n\n")

	// Forms
	writeFormsSection(&b, ctx, q, siteURL, adminEmail)

	// API
	b.WriteString("## REST API\n\n")
	b.WriteString("The site provides a public API at ")
	b.WriteString(siteURL)
	b.WriteString("/api/v2:\n\n")
	b.WriteString("- `GET /api/v2/pages` — Browse published pages\n")
	b.WriteString("- `GET /api/v2/tags` — List tags\n")
	b.WriteString("- `GET /api/v2/categories` — List categories\n")
	b.WriteString("- `GET /api/v2/media` — Browse media files\n")

	return b.String(), nil
}

func writePageEntry(b *strings.Builder, p store.Page, siteURL string, authorCache map[int64]string, pageCats, pageTagNames map[int64][]string) {
	b.WriteString("### ")
	b.WriteString(p.Title)
	b.WriteString("\n\n")
	b.WriteString("- URL: ")
	b.WriteString(siteURL)
	b.WriteString("/")
	b.WriteString(p.Slug)
	b.WriteString("\n")

	if p.PublishedAt.Valid {
		b.WriteString("- Published: ")
		b.WriteString(p.PublishedAt.Time.Format("2006-01-02"))
		b.WriteString("\n")
	}

	if author, ok := authorCache[p.AuthorID]; ok && author != "" {
		b.WriteString("- Author: ")
		b.WriteString(author)
		b.WriteString("\n")
	}

	if cats := pageCats[p.ID]; len(cats) > 0 {
		b.WriteString("- Categories: ")
		b.WriteString(strings.Join(cats, ", "))
		b.WriteString("\n")
	}

	if tags := pageTagNames[p.ID]; len(tags) > 0 {
		b.WriteString("- Tags: ")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString("\n")
	}

	// Summary or meta description
	summary := p.Summary
	if summary == "" {
		summary = p.MetaDescription
	}
	if summary != "" {
		b.WriteString("- Summary: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	// Body text
	bodyText := strings.TrimSpace(htmlToText(p.Body))
	if bodyText != "" {
		b.WriteString("\n")
		b.WriteString(bodyText)
		b.WriteString("\n")
	}

	b.WriteString("\n---\n\n")
}

func writeMenuSection(b *strings.Builder, ctx context.Context, q *store.Queries, siteURL string) {
	// Find default language
	defaultLang := ""
	langs, err := q.ListLanguages(ctx)
	if err == nil {
		for _, l := range langs {
			if l.IsDefault {
				defaultLang = l.Code
				break
			}
		}
	}
	if defaultLang == "" {
		defaultLang = "en"
	}

	menus, err := q.ListMenusByLanguage(ctx, defaultLang)
	if err != nil || len(menus) == 0 {
		return
	}

	for _, menu := range menus {
		items, err := q.ListMenuItemsWithPublishedPage(ctx, menu.ID)
		if err != nil || len(items) == 0 {
			continue
		}

		b.WriteString("**")
		b.WriteString(menu.Name)
		b.WriteString(" menu:**\n\n")
		for _, item := range items {
			if !item.IsActive {
				continue
			}
			// Skip menu items that reference an unpublished page.
			// The LEFT JOIN nullifies page_slug for drafts, so if
			// page_id is set but page_slug is empty the page is not
			// published and the item should be hidden from the KB.
			if item.PageID.Valid && (!item.PageSlug.Valid || item.PageSlug.String == "") {
				continue
			}
			b.WriteString("- ")
			b.WriteString(item.Title)
			itemURL := ""
			if item.PageSlug.Valid && item.PageSlug.String != "" {
				itemURL = siteURL + "/" + item.PageSlug.String
			} else if item.Url.Valid && item.Url.String != "" {
				itemURL = item.Url.String
			}
			if itemURL != "" {
				b.WriteString(" — ")
				b.WriteString(itemURL)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

func writeLanguageSection(b *strings.Builder, ctx context.Context, q *store.Queries, siteURL string) {
	langs, err := q.ListLanguages(ctx)
	if err != nil {
		return
	}

	var activeLangs []store.Language
	for _, l := range langs {
		if l.IsActive {
			activeLangs = append(activeLangs, l)
		}
	}

	b.WriteString("## Language Support\n\n")
	if len(activeLangs) > 1 {
		b.WriteString("The site supports multiple languages: ")
		for i, l := range activeLangs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(l.NativeName)
			b.WriteString(" (")
			b.WriteString(l.Code)
			b.WriteString(")")
		}
		b.WriteString(". Use the language switcher in the navigation to change language. URLs include a language prefix (e.g., ")
		b.WriteString(siteURL)
		b.WriteString("/")
		for _, l := range activeLangs {
			if !l.IsDefault {
				b.WriteString(l.Code)
				break
			}
		}
		b.WriteString("/).\n\n")
	} else if len(activeLangs) == 1 {
		b.WriteString("The site is available in ")
		b.WriteString(activeLangs[0].NativeName)
		b.WriteString(".\n\n")
	}
}

func writeFormsSection(b *strings.Builder, ctx context.Context, q *store.Queries, siteURL, adminEmail string) {
	forms, err := q.ListForms(ctx, store.ListFormsParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil || len(forms) == 0 {
		if adminEmail != "" {
			b.WriteString("## Contact\n\n")
			b.WriteString("Contact the site administrator at ")
			b.WriteString(siteURL)
			b.WriteString("/forms/contact-us.\n\n")
		}
		return
	}

	var activeForms []store.Form
	for _, f := range forms {
		if f.IsActive {
			activeForms = append(activeForms, f)
		}
	}

	if len(activeForms) == 0 {
		if adminEmail != "" {
			b.WriteString("## Contact\n\n")
			b.WriteString("Contact the site administrator at ")
			b.WriteString(siteURL)
			b.WriteString("/forms/contact-us.\n\n")
		}
		return
	}

	b.WriteString("## Forms\n\n")
	b.WriteString("The following forms are available:\n\n")
	for _, f := range activeForms {
		b.WriteString("- **")
		b.WriteString(f.Title)
		b.WriteString("**")
		if f.Description.Valid && f.Description.String != "" {
			b.WriteString(": ")
			b.WriteString(f.Description.String)
		}
		b.WriteString(" — ")
		b.WriteString(siteURL)
		b.WriteString("/forms/")
		b.WriteString(f.Slug)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func loadSiteInfo(ctx context.Context, q *store.Queries) (siteURL, siteName, siteDesc string) {
	siteName = configValue(ctx, q, "site_name")
	if siteName == "" {
		siteName = "Site"
	}
	siteDesc = configValue(ctx, q, "site_description")
	siteURL = strings.TrimRight(configValue(ctx, q, "site_url"), "/")
	return siteURL, siteName, siteDesc
}

func configValue(ctx context.Context, q *store.Queries, key string) string {
	cfg, err := q.GetConfigByKey(ctx, key)
	if err != nil {
		return ""
	}
	return cfg.Value
}

// htmlToText strips HTML tags and returns readable plain text.
// Preserves paragraph breaks and list structure.
func htmlToText(rawHTML string) string {
	if rawHTML == "" {
		return ""
	}

	tokenizer := html.NewTokenizer(strings.NewReader(rawHTML))
	var b strings.Builder
	skipContent := false
	var skipAtom atom.Atom

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return collapseWhitespace(b.String())

		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			a := atom.Lookup(tn)

			if a == atom.Script || a == atom.Style {
				skipContent = true
				skipAtom = a
				continue
			}

			switch {
			case isBlockElement(a), a == atom.Br:
				b.WriteString("\n")
			case a == atom.Li:
				b.WriteString("\n- ")
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			a := atom.Lookup(tn)

			if skipContent && a == skipAtom {
				skipContent = false
				skipAtom = 0
				continue
			}

			if isBlockElement(a) {
				b.WriteString("\n")
			}

		case html.TextToken:
			if skipContent {
				continue
			}
			text := string(tokenizer.Text())
			b.WriteString(text)
		}
	}
}

func isBlockElement(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Blockquote, atom.Pre, atom.Ul, atom.Ol, atom.Table, atom.Tr,
		atom.Section, atom.Article, atom.Header, atom.Footer, atom.Nav, atom.Hr:
		return true
	}
	return false
}

func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	prevEmpty := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevEmpty {
				result = append(result, "")
				prevEmpty = true
			}
			continue
		}
		result = append(result, trimmed)
		prevEmpty = false
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

