// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
)

// Demo mode credentials
const (
	DemoAdminEmail    = "demo@example.com"
	DemoAdminPassword = "demo1234demo"
	DemoAdminName     = "Demo Admin"

	DemoEditorEmail    = "editor@example.com"
	DemoEditorPassword = "demo1234demo"
	DemoEditorName     = "Demo Editor"
)

// SeedDemo creates demo content for showcasing oCMS functionality.
// This is called after the regular Seed() when OCMS_DEMO_MODE=true.
func SeedDemo(ctx context.Context, db *sql.DB) error {
	if os.Getenv("OCMS_DEMO_MODE") != "true" {
		return nil
	}

	slog.Info("seeding demo content")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin demo seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := New(db).WithTx(tx)

	// Get default language
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return fmt.Errorf("getting default language: %w", err)
	}

	// Create demo users
	adminID, err := seedDemoUsers(ctx, queries)
	if err != nil {
		return fmt.Errorf("seeding demo users: %w", err)
	}

	// Create demo categories
	categoryIDs, err := seedDemoCategories(ctx, queries, defaultLang.Code)
	if err != nil {
		return fmt.Errorf("seeding demo categories: %w", err)
	}

	// Create demo tags
	tagIDs, err := seedDemoTags(ctx, queries, defaultLang.Code)
	if err != nil {
		return fmt.Errorf("seeding demo tags: %w", err)
	}

	// Create demo media
	uploadsDir := os.Getenv("OCMS_UPLOADS_DIR")
	if uploadsDir == "" {
		uploadsDir = "./uploads"
	}
	mediaIDs, err := seedDemoMedia(ctx, queries, adminID, defaultLang.Code, uploadsDir)
	if err != nil {
		return fmt.Errorf("seeding demo media: %w", err)
	}

	// Create demo pages
	if err := seedDemoPages(ctx, tx, adminID, defaultLang.Code, categoryIDs, tagIDs, mediaIDs); err != nil {
		return fmt.Errorf("seeding demo pages: %w", err)
	}

	if err := seedDemoContactForm(ctx, queries, defaultLang.Code); err != nil {
		return fmt.Errorf("seeding demo contact form: %w", err)
	}

	// Create demo menu items
	if err := seedDemoMenuItems(ctx, queries); err != nil {
		return fmt.Errorf("seeding demo menu items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit demo seed: %w", err)
	}
	slog.Info("demo content seeded successfully")
	return nil
}

// SeedDemoInformerSettings enables the informer bar with demo credentials.
// Must be called after module initialization (informer module creates the table).
//
// The password is NOT baked into the banner HTML. The banner's JS fetches the
// current password from /api/informer/demo-credentials at page-view time so it
// always matches the live DB hash, even after cold-start rotations.
func SeedDemoInformerSettings(db *sql.DB) error {
	if os.Getenv("OCMS_DEMO_MODE") != "true" {
		return nil
	}

	// Check if the informer module table exists (created by module migration, not core migrations)
	var tableExists int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='informer_settings'`).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		slog.Info("informer_settings table not found, skipping demo informer setup")
		return nil
	}

	demoText := fmt.Sprintf(
		`This is a demo instance. Admin panel: <a href="/admin/" style="color:#fff;text-decoration:underline">/admin/</a> &mdash; Login: <strong>%s</strong> / <strong class="ocms-demo-pw">&hellip;</strong>`,
		DemoAdminEmail,
	)

	_, err = db.Exec(`
		UPDATE informer_settings SET
			enabled = 1,
			text = ?,
			bg_color = '#1e40af',
			text_color = '#ffffff',
			version = version + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, demoText)
	if err != nil {
		return fmt.Errorf("updating informer settings: %w", err)
	}

	slog.Info("demo informer bar enabled")
	return nil
}

// RotateDemoAdminPassword rotates the demo admin password and returns the new password.
// Rotation only occurs when OCMS_DEMO_MODE=true and the demo admin account exists.
func RotateDemoAdminPassword(ctx context.Context, db *sql.DB) (string, error) {
	if os.Getenv("OCMS_DEMO_MODE") != "true" {
		return "", nil
	}

	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating demo admin password: %w", err)
	}
	password := "demo-" + base64.RawURLEncoding.EncodeToString(raw)

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return "", fmt.Errorf("hashing rotated demo admin password: %w", err)
	}
	updated, err := New(db).RotatePublicDemoAdminPassword(ctx, RotatePublicDemoAdminPasswordParams{
		PasswordHash: passwordHash,
		UpdatedAt:    time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("updating demo admin password: %w", err)
	}
	if updated == 0 {
		return "", nil
	}

	return password, nil
}

// RotateDefaultAdminPassword rotates the seeded default admin password and returns the new password.
// Rotation only occurs when OCMS_DEMO_MODE=true, the default admin account exists, and its stored
// hash still authenticates DefaultAdminPassword — so an operator who has rotated the account to a
// real credential is never silently overwritten on restart.
//
// The production startup audit refuses to boot when default admin credentials are still active;
// demo deploys seed those credentials on every boot, so this rotation runs ahead of the audit
// to clear them in place. The returned password is intentionally not logged — the demo's public
// credentials are DemoAdminEmail / DemoEditorEmail.
func RotateDefaultAdminPassword(ctx context.Context, db *sql.DB) (string, error) {
	if os.Getenv("OCMS_DEMO_MODE") != "true" {
		return "", nil
	}

	queries := New(db)
	admin, err := queries.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("loading default admin account: %w", err)
	}
	if admin.Role != "admin" {
		return "", nil
	}

	stillDefault, err := auth.CheckPassword(DefaultAdminPassword, admin.PasswordHash)
	if err != nil {
		return "", fmt.Errorf("verifying default admin credentials: %w", err)
	}
	if !stillDefault {
		return "", nil
	}

	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating default admin password: %w", err)
	}
	password := "demo-" + base64.RawURLEncoding.EncodeToString(raw)

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return "", fmt.Errorf("hashing rotated default admin password: %w", err)
	}
	if err := queries.UpdateUserPassword(ctx, UpdateUserPasswordParams{
		PasswordHash: passwordHash,
		UpdatedAt:    time.Now(),
		ID:           admin.ID,
	}); err != nil {
		return "", fmt.Errorf("updating default admin password: %w", err)
	}

	return password, nil
}

func seedDemoUsers(ctx context.Context, queries *Queries) (int64, error) {
	// Check if demo admin already exists
	existingUser, err := queries.GetUserByEmail(ctx, DemoAdminEmail)
	if err == nil {
		slog.Info("demo users already exist, skipping")
		return existingUser.ID, nil
	}

	now := time.Now()

	// Create demo admin
	adminHash, err := auth.HashPassword(DemoAdminPassword)
	if err != nil {
		return 0, fmt.Errorf("hashing admin password: %w", err)
	}

	admin, err := queries.CreateUser(ctx, CreateUserParams{
		Email:        DemoAdminEmail,
		PasswordHash: adminHash,
		Role:         "admin",
		Name:         DemoAdminName,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return 0, fmt.Errorf("creating demo admin: %w", err)
	}

	// Create demo editor
	editorHash, err := auth.HashPassword(DemoEditorPassword)
	if err != nil {
		return 0, fmt.Errorf("hashing editor password: %w", err)
	}

	_, err = queries.CreateUser(ctx, CreateUserParams{
		Email:        DemoEditorEmail,
		PasswordHash: editorHash,
		Role:         "editor",
		Name:         DemoEditorName,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return 0, fmt.Errorf("creating demo editor: %w", err)
	}

	slog.Info("created demo users",
		"admin_email", DemoAdminEmail,
		"editor_email", DemoEditorEmail,
		"password", DemoAdminPassword,
	)

	return admin.ID, nil
}

func seedDemoCategories(ctx context.Context, queries *Queries, langCode string) (map[string]int64, error) {
	// Check if categories already exist
	count, err := queries.CountCategories(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		slog.Info("categories already exist, skipping demo categories")
		return make(map[string]int64), nil
	}

	now := time.Now()
	categories := []struct {
		Name        string
		Slug        string
		Description string
		Position    int64
	}{
		{Name: "Blog", Slug: "blog", Description: "Latest news, tutorials, and updates", Position: 1},
		{Name: "Portfolio", Slug: "portfolio", Description: "Showcase of our work and projects", Position: 2},
		{Name: "Services", Slug: "services", Description: "Our professional services", Position: 3},
		{Name: "Resources", Slug: "resources", Description: "Helpful guides and documentation", Position: 4},
	}

	ids := make(map[string]int64)
	for _, cat := range categories {
		created, err := queries.CreateCategory(ctx, CreateCategoryParams{
			Name:         cat.Name,
			Slug:         cat.Slug,
			Description:  sql.NullString{String: cat.Description, Valid: true},
			ParentID:     sql.NullInt64{Valid: false},
			Position:     cat.Position,
			LanguageCode: langCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			return nil, fmt.Errorf("creating category %s: %w", cat.Slug, err)
		}
		ids[cat.Slug] = created.ID
	}

	slog.Info("seeded demo categories", "count", len(categories))
	return ids, nil
}

func seedDemoTags(ctx context.Context, queries *Queries, langCode string) (map[string]int64, error) {
	// Check if tags already exist
	count, err := queries.CountTags(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		slog.Info("tags already exist, skipping demo tags")
		return make(map[string]int64), nil
	}

	now := time.Now()
	tags := []struct {
		Name string
		Slug string
	}{
		{Name: "Tutorial", Slug: "tutorial"},
		{Name: "News", Slug: "news"},
		{Name: "Featured", Slug: "featured"},
		{Name: "Go", Slug: "go"},
		{Name: "Web Development", Slug: "web-development"},
		{Name: "Design", Slug: "design"},
		{Name: "Open Source", Slug: "open-source"},
	}

	ids := make(map[string]int64)
	for _, tag := range tags {
		created, err := queries.CreateTag(ctx, CreateTagParams{
			Name:         tag.Name,
			Slug:         tag.Slug,
			LanguageCode: langCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			return nil, fmt.Errorf("creating tag %s: %w", tag.Slug, err)
		}
		ids[tag.Slug] = created.ID
	}

	slog.Info("seeded demo tags", "count", len(tags))
	return ids, nil
}

// demoPage represents a demo page with its metadata.
type demoPage struct {
	Title           string
	Slug            string
	Body            string
	Status          string
	PageType        string
	MetaTitle       string
	MetaDescription string
	CategorySlugs   []string
	TagSlugs        []string
	FeaturedImage   string // Filename from demo media to use as featured image
}

func seedDemoPages(ctx context.Context, tx *sql.Tx, authorID int64, langCode string, categoryIDs, tagIDs, mediaIDs map[string]int64) error {
	queries := New(tx)
	count, err := queries.CountPages(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	pages := getDemoPages()

	recordVersion := func(page Page) error {
		snapshot, err := queries.GetPageSnapshot(ctx, page.ID)
		if err != nil {
			return err
		}
		_, err = queries.CreateCompletePageVersion(ctx, CreateCompletePageVersionParams{
			PageID: page.ID, Title: page.Title, Body: page.Body, ChangedBy: authorID,
			CreatedAt: now, Revision: sql.NullInt64{Int64: page.Revision, Valid: true},
			SnapshotJson: sql.NullString{String: snapshot, Valid: true},
		})
		if err != nil {
			return err
		}
		return queries.CreateContentProvenance(ctx, CreateContentProvenanceParams{
			PageID: page.ID, Revision: page.Revision, Source: "system", Transport: "demo_seed",
			ValidatedJson: "{}", ReportedJson: "{}", CreatedAt: now,
		})
	}

	for i, page := range pages {
		if count > 0 {
			if page.Status != "draft" {
				// Older demo databases already contain the published pages.
				// Refresh only the original, unedited demo instructions.
				switch page.Slug {
				case "home", "contact", DemoArticleSlug:
					updated, err := queries.RefreshInitialDemoPageBody(ctx, RefreshInitialDemoPageBodyParams{
						Body: page.Body, UpdatedAt: now, Slug: page.Slug, LanguageCode: langCode, AuthorID: authorID, Title: page.Title,
					})
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					if err != nil {
						return err
					}
					if err := recordVersion(updated); err != nil {
						return err
					}
				}
				continue
			}
			exists, err := queries.SlugOrAliasExists(ctx, SlugOrAliasExistsParams{Slug: page.Slug, Alias: page.Slug})
			if err != nil {
				return err
			}
			if exists != 0 {
				continue
			}
		}
		publishedAt := sql.NullTime{Valid: false}
		if page.Status == "published" {
			publishedAt = sql.NullTime{Time: now.Add(-time.Duration(len(pages)-i) * 24 * time.Hour), Valid: true}
		}

		// Set featured image if specified and exists in mediaIDs
		featuredImageID := sql.NullInt64{Valid: false}
		if page.FeaturedImage != "" {
			if mediaID, ok := mediaIDs[page.FeaturedImage]; ok {
				featuredImageID = sql.NullInt64{Int64: mediaID, Valid: true}
			}
		}

		created, err := queries.CreatePage(ctx, CreatePageParams{
			Title:             page.Title,
			Slug:              page.Slug,
			Body:              page.Body,
			Status:            page.Status,
			AuthorID:          authorID,
			FeaturedImageID:   featuredImageID,
			MetaTitle:         page.MetaTitle,
			MetaDescription:   page.MetaDescription,
			MetaKeywords:      "",
			OgImageID:         sql.NullInt64{Valid: false},
			NoIndex:           0,
			NoFollow:          0,
			CanonicalUrl:      "",
			ScheduledAt:       sql.NullTime{Valid: false},
			LanguageCode:      langCode,
			HideFeaturedImage: 0,
			PageType:          page.PageType,
			ExcludeFromLists:  0,
			PublishedAt:       publishedAt,
			CreatedAt:         now,
			UpdatedAt:         now,
		})
		if err != nil {
			return fmt.Errorf("creating page %s: %w", page.Slug, err)
		}

		// Add categories
		for _, catSlug := range page.CategorySlugs {
			if catID, ok := categoryIDs[catSlug]; ok {
				if err := queries.AddCategoryToPage(ctx, AddCategoryToPageParams{
					PageID:     created.ID,
					CategoryID: catID,
				}); err != nil {
					return fmt.Errorf("seed page category: %w", err)
				}
			}
		}

		// Add tags
		for _, tagSlug := range page.TagSlugs {
			if tagID, ok := tagIDs[tagSlug]; ok {
				if err := queries.AddTagToPage(ctx, AddTagToPageParams{
					PageID: created.ID,
					TagID:  tagID,
				}); err != nil {
					return fmt.Errorf("seed page tag: %w", err)
				}
			}
		}
		if err := recordVersion(created); err != nil {
			return err
		}

	}

	slog.Info("seeded demo pages", "count", len(pages))
	return nil
}

func getDemoPages() []demoPage {
	return []demoPage{
		// Static pages
		{
			Title:           "Welcome to oCMS",
			Slug:            "home",
			Body:            getHomePageBody(),
			Status:          "published",
			PageType:        "page",
			MetaTitle:       "oCMS - Modern Content Management System",
			MetaDescription: "oCMS is a lightweight, fast, and secure content management system built with Go.",
			CategorySlugs:   []string{},
			TagSlugs:        []string{},
			FeaturedImage:   "studio-table.jpg",
		},
		{
			Title:           "About Us",
			Slug:            "about",
			Body:            getAboutPageBody(),
			Status:          "published",
			PageType:        "page",
			MetaTitle:       "About oCMS",
			MetaDescription: "Learn about oCMS, our mission, and the technology behind this modern CMS.",
			CategorySlugs:   []string{},
			TagSlugs:        []string{},
			FeaturedImage:   "reader-research.jpg",
		},
		{
			Title:           "Contact",
			Slug:            "contact",
			Body:            getContactPageBody(),
			Status:          "published",
			PageType:        "page",
			MetaTitle:       "Contact Us",
			MetaDescription: "Get in touch with the oCMS team.",
			CategorySlugs:   []string{},
			TagSlugs:        []string{},
			FeaturedImage:   "mobile-wireframe.jpg",
		},
		{
			Title:           "Cookie Policy",
			Slug:            "cookie-policy",
			Body:            getCookiePolicyBody(),
			Status:          "published",
			PageType:        "page",
			MetaTitle:       "Cookie Policy",
			MetaDescription: "Learn about the cookies used on this website, their purpose, and how to manage your preferences.",
			CategorySlugs:   []string{},
			TagSlugs:        []string{},
		},
		{
			Title: "Planning the next issue",
			Slug:  "planning-the-next-issue",
			Body: `<p>Our next studio journal will follow a story from the first interview to the published page.</p>
<h2>Outline for the editorial meeting</h2>
<ul><li>Choose the opening photograph and write its caption.</li><li>Review the interview notes and confirm the pull quote.</li><li>Check the article on a phone before sending it for review.</li></ul>
<p><em>Editor’s note: the interview and opening photograph are still under review.</em></p>`,
			Status:        "draft",
			PageType:      "post",
			FeaturedImage: "studio-table.jpg",
		},
		// Blog posts
		{
			Title:           "Getting Started with oCMS",
			Slug:            DemoArticleSlug,
			Body:            getGettingStartedBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "Getting Started with oCMS",
			MetaDescription: "Run a prebuilt oCMS release locally, explore the shared demo, and find current instructions for themes, deployment, and building from source.",
			CategorySlugs:   []string{"blog", "resources"},
			TagSlugs:        []string{"tutorial", "featured"},
			FeaturedImage:   "planning-workshop.jpg",
		},
		{
			Title:           "Building Modern Websites with Go",
			Slug:            "building-modern-websites-with-go",
			Body:            getGoWebsitesBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "Building Modern Websites with Go",
			MetaDescription: "Discover why Go is an excellent choice for building fast, secure web applications.",
			CategorySlugs:   []string{"blog"},
			TagSlugs:        []string{"go", "web-development", "tutorial"},
			FeaturedImage:   "service-architecture.jpg",
		},
		{
			Title:           "oCMS Theme Development",
			Slug:            "ocms-theme-development",
			Body:            getThemeDevelopmentBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "Creating Custom Themes for oCMS",
			MetaDescription: "A comprehensive guide to creating beautiful custom themes for oCMS.",
			CategorySlugs:   []string{"blog", "resources"},
			TagSlugs:        []string{"tutorial", "design", "web-development"},
			FeaturedImage:   "geometric-facade.jpg",
		},
		// Portfolio items
		{
			Title:           "E-Commerce Platform",
			Slug:            "portfolio-ecommerce-platform",
			Body:            getPortfolioEcommerceBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "E-Commerce Platform - Portfolio",
			MetaDescription: "A modern e-commerce platform built with oCMS and custom integrations.",
			CategorySlugs:   []string{"portfolio"},
			TagSlugs:        []string{"featured", "web-development"},
			FeaturedImage:   "book-catalog.jpg",
		},
		{
			Title:           "Corporate Website Redesign",
			Slug:            "portfolio-corporate-redesign",
			Body:            getPortfolioCorporateBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "Corporate Website Redesign - Portfolio",
			MetaDescription: "Complete redesign of a Fortune 500 company website using oCMS.",
			CategorySlugs:   []string{"portfolio"},
			TagSlugs:        []string{"design", "featured"},
			FeaturedImage:   "gallery-staircase.jpg",
		},
		// Services
		{
			Title:           "Web Development Services",
			Slug:            "web-development-services",
			Body:            getServicesWebDevBody(),
			Status:          "published",
			PageType:        "page",
			MetaTitle:       "Professional Web Development Services",
			MetaDescription: "Custom web development services using modern technologies and best practices.",
			CategorySlugs:   []string{"services"},
			TagSlugs:        []string{"web-development"},
			FeaturedImage:   "studio-interior.jpg",
		},
	}
}

func seedDemoMenuItems(ctx context.Context, queries *Queries) error {
	// Get main menu
	mainMenu, err := queries.GetMenuBySlug(ctx, "main")
	if err != nil {
		slog.Warn("main menu not found, skipping menu items", "error", err)
		return nil
	}

	// Check if menu already has items
	items, err := queries.ListMenuItems(ctx, mainMenu.ID)
	if err != nil {
		return fmt.Errorf("listing menu items: %w", err)
	}
	if len(items) > 0 {
		slog.Info("menu items already exist, skipping")
		return nil
	}

	now := time.Now()
	menuItems := []struct {
		Title    string
		URL      string
		Position int64
	}{
		{Title: "Home", URL: "/", Position: 1},
		{Title: "Blog", URL: "/category/blog", Position: 2},
		{Title: "Portfolio", URL: "/category/portfolio", Position: 3},
		{Title: "Services", URL: "/web-development-services", Position: 4},
		{Title: "About", URL: "/about", Position: 5},
		{Title: "Contact", URL: "/contact", Position: 6},
		{Title: "Cookie Policy", URL: "/cookie-policy", Position: 7},
	}

	for _, item := range menuItems {
		_, err := queries.CreateMenuItem(ctx, CreateMenuItemParams{
			MenuID:    mainMenu.ID,
			Title:     item.Title,
			Url:       sql.NullString{String: item.URL, Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			ParentID:  sql.NullInt64{Valid: false},
			Position:  item.Position,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating menu item %s: %w", item.Title, err)
		}
	}

	slog.Info("seeded demo menu items", "count", len(menuItems))
	return nil
}

// Page content helpers

func getHomePageBody() string {
	return `<div class="hero">
<h1>Welcome to oCMS Demo</h1>
<p class="lead">A modern, lightweight content management system built with Go. Fast, secure, and easy to use.</p>
</div>

<h2>Why Choose oCMS?</h2>

<div class="features">
<h3>Lightning Fast</h3>
<p>Built with Go, oCMS delivers exceptional performance. Pages load instantly with SQLite's efficient storage and Go's compiled speed.</p>

<h3>Secure by Design</h3>
<p>Security is not an afterthought. oCMS includes CSRF protection, secure session management, rate limiting, and follows security best practices.</p>

<h3>Developer Friendly</h3>
<p>Clean architecture, well-documented APIs, and a powerful theme system make customization straightforward.</p>

<h3>Self-Contained</h3>
<p>Single binary deployment with embedded assets. No external dependencies required - just run and go!</p>
</div>

<h2>Explore the Demo</h2>
<p>This demo showcases various oCMS features:</p>
<ul>
<li><strong>Blog</strong> - View our sample blog posts with categories and tags</li>
<li><strong>Portfolio</strong> - See how to showcase projects and work samples</li>
<li><strong>Services</strong> - Example of a services/corporate page layout</li>
<li><strong>Admin Panel</strong> - Login to explore the full admin interface</li>
</ul>

<p><a href="/admin/">Open the admin panel</a> and sign in using the demo sign-in option to explore the editor.</p>`
}

func getAboutPageBody() string {
	return `<h2>About oCMS</h2>

<p>oCMS (Opossum CMS) is a modern content management system designed for developers who value simplicity, performance, and security.</p>

<h3>Our Philosophy</h3>
<p>We believe that content management should be straightforward. No bloated features you'll never use, no complex configuration required. Just a clean, fast, and reliable system that gets out of your way.</p>

<h3>Technology Stack</h3>
<ul>
<li><strong>Go</strong> - High-performance compiled language</li>
<li><strong>SQLite</strong> - Lightweight, self-contained database</li>
<li><strong>HTMX</strong> - Modern, lightweight interactivity</li>
<li><strong>Alpine.js</strong> - Minimal JavaScript framework</li>
</ul>

<h3>Key Features</h3>
<ul>
<li>Multi-language content support</li>
<li>Flexible theme system with template inheritance</li>
<li>RESTful API with authentication</li>
<li>Media library with image optimization</li>
<li>Form builder with submission tracking</li>
<li>SEO tools (sitemaps, meta tags, robots.txt)</li>
<li>Webhook integrations</li>
<li>Scheduled publishing</li>
</ul>

<h3>Open Source</h3>
<p>oCMS is open source software released under the GPL-3.0 license. Contributions are welcome!</p>`
}

func getContactPageBody() string {
	return `<h2>Get in Touch</h2>

<p>We'd love to hear from you! Whether you have questions about oCMS, need support, or want to contribute to the project.</p>

<h3>Contact Information</h3>
<ul>
<li><strong>GitHub:</strong> <a href="https://github.com/olegiv/ocms-go">github.com/olegiv/ocms-go</a></li>
<li><strong>Issues:</strong> <a href="https://github.com/olegiv/ocms-go/issues">Report bugs or request features</a></li>
</ul>

<h3>Demo Note</h3>
<p><a href="/forms/contact">Preview the sample contact form</a> to see its fields and validation. The shared demo does not accept submissions for this example. In the admin panel, open <a href="/admin/forms">Forms</a> and choose “Sample contact form” to inspect its settings.</p>

<p>The form builder supports:</p>
<ul>
<li>Custom field types (text, email, textarea, select, checkbox)</li>
<li>Required field validation</li>
<li>Email notifications</li>
<li>Submission management in admin panel</li>
<li>Spam protection with hCaptcha integration</li>
</ul>`
}

func getGettingStartedBody() string {
	return `<p class="lead">Run oCMS locally from a prebuilt release, then use the admin panel to publish and customize your own site. This shared demo lets you explore the interface before installing it.</p>

<h2>Run a prebuilt release</h2>
<p>You do not need Go or Node.js installed to run a prebuilt oCMS binary. Release archives include the application and its admin assets.</p>
<ol>
<li>Open the <a href="https://ocms.tech/download">Download page</a> and choose Linux AMD64, Linux ARM64, or macOS Apple Silicon to match your machine.</li>
<li>Follow that platform's quick start in a new directory. It downloads the release, verifies its SHA-256 checksum, extracts the binary, and runs <code>ocms init my-site</code>.</li>
<li>The commands start the local server from the generated site directory. Open <a href="http://localhost:8080/admin/">localhost:8080/admin/</a> and follow the release README's first-run instructions.</li>
</ol>
<p>Change the bootstrap administrator email and password, then set <code>OCMS_DO_SEED=false</code> in the generated <code>my-site/.env</code>. Keep the generated <code>OCMS_SESSION_SECRET</code> and <code>OCMS_MFA_KEY</code> values; back them up with the site rather than replacing them during routine restarts.</p>

<h2>Publish your first page</h2>
<p>On your own installation, set a site name in Settings. Create an item in Pages, choose the Page type, give it a title and slug, add your content, and publish it. Open its public URL to check the result. Choose the Post type for entries that belong in the blog listing.</p>
<p>The <a href="https://ocms.tech/build-your-first-ocms-site">Build your first oCMS site walkthrough</a> continues through a first page, custom theme changes, and deployment preparation.</p>

<h2>Explore this shared demo</h2>
<p>Open the <a href="/admin/">admin panel</a> and use the demo sign-in option. Browse the media library, inspect the sample contact form, or open Pages and filter by Drafts to find “Planning the next issue.”</p>
<p>Try creating and editing an unpublished draft. Publishing is disabled in the shared demo, and the example data resets daily. Use a local installation to keep and publish your work.</p>

<h2>Build from source when you need it</h2>
<p>A source build requires Go and the frontend build tools. Follow the <a href="https://github.com/olegiv/ocms-go#build-from-source">repository's build instructions</a> for their current version requirements and commands.</p>
<p>Custom HTML themes can live alongside a prebuilt binary. Custom Go modules must be compiled into the application. See the <a href="https://github.com/olegiv/ocms-go/wiki/Theme-System">theme guide</a> and <a href="https://github.com/olegiv/ocms-go/wiki/Module-System">module guide</a> for the two workflows.</p>

<h2>Prepare your own site for deployment</h2>
<p>Before making a local site public, review the release's upgrade requirements and the <a href="https://github.com/olegiv/ocms-go/wiki/Deployment">deployment guide</a>. Configure the production site URL, required security settings, process manager, and HTTPS reverse proxy.</p>
<p>Back up a consistent SQLite database, uploads, custom themes, and the configuration containing the site's session and MFA keys. Use the Linux release matching the server's architecture when deploying from a Mac.</p>`
}

func getGoWebsitesBody() string {
	return `<p class="lead">Go has emerged as an excellent choice for building modern web applications. Here's why we chose it for oCMS.</p>

<h2>Performance</h2>
<p>Go compiles to native machine code, delivering exceptional performance. There's no interpreter or virtual machine overhead. This means:</p>
<ul>
<li>Sub-millisecond response times</li>
<li>Low memory footprint</li>
<li>Efficient CPU utilization</li>
</ul>

<h2>Simplicity</h2>
<p>Go's simplicity is a feature, not a limitation. The language has a small, consistent syntax that's easy to learn and read. Code reviews are faster, onboarding is smoother, and maintenance is simpler.</p>

<h2>Concurrency</h2>
<p>Go's goroutines make concurrent programming straightforward. Handle thousands of simultaneous connections without complex threading code:</p>
<pre><code>go handleRequest(conn)  // That's it!</code></pre>

<h2>Standard Library</h2>
<p>Go's standard library includes everything needed for web development:</p>
<ul>
<li>HTTP server and client</li>
<li>JSON encoding/decoding</li>
<li>Template engine</li>
<li>Cryptography</li>
<li>Testing framework</li>
</ul>

<h2>Single Binary Deployment</h2>
<p>Go compiles to a single, statically-linked binary. No runtime dependencies, no version conflicts, no "works on my machine" issues. Just copy the binary and run.</p>

<h2>Growing Ecosystem</h2>
<p>The Go ecosystem continues to mature with excellent libraries for:</p>
<ul>
<li>Web frameworks (Chi, Gin, Echo)</li>
<li>Database access (sqlc, GORM)</li>
<li>Testing and mocking</li>
<li>Observability and monitoring</li>
</ul>`
}

func getThemeDevelopmentBody() string {
	return `<p class="lead">oCMS features a powerful and flexible theme system. This guide covers everything you need to create custom themes.</p>

<h2>Theme Structure</h2>
<p>A theme consists of templates, static assets, and optional translations:</p>
<pre><code>mytheme/
├── theme.json           # Theme metadata
├── templates/
│   ├── layouts/
│   │   └── base.html    # Main layout
│   ├── pages/
│   │   ├── home.html
│   │   ├── page.html
│   │   └── post.html
│   └── partials/
│       ├── header.html
│       └── footer.html
├── static/
│   ├── css/
│   └── js/
└── locales/             # Optional translations
</code></pre>

<h2>Template Inheritance</h2>
<p>oCMS uses Go's html/template with a block-based inheritance system:</p>
<pre><code>&#123;&#123;/* base.html */&#125;&#125;
&lt;html&gt;
&lt;head&gt;
  &#123;&#123;block "head" .&#125;&#125;&#123;&#123;end&#125;&#125;
&lt;/head&gt;
&lt;body&gt;
  &#123;&#123;block "content" .&#125;&#125;&#123;&#123;end&#125;&#125;
&lt;/body&gt;
&lt;/html&gt;</code></pre>

<h2>Template Functions</h2>
<p>oCMS provides many helper functions:</p>
<ul>
<li><code>T</code> - Translation function</li>
<li><code>TTheme</code> - Theme-specific translations</li>
<li><code>FormatDate</code> - Date formatting</li>
<li><code>SafeHTML</code> - Render trusted HTML</li>
<li><code>Truncate</code> - Text truncation</li>
</ul>

<h2>Static Assets</h2>
<p>Place CSS, JavaScript, and images in the <code>static/</code> directory. Reference them in templates using the asset path.</p>

<h2>Best Practices</h2>
<ul>
<li>Use semantic HTML for accessibility</li>
<li>Optimize images before including them</li>
<li>Minimize CSS and JavaScript</li>
<li>Test responsive layouts thoroughly</li>
<li>Support both light and dark modes</li>
</ul>`
}

func getPortfolioEcommerceBody() string {
	return `<p class="lead">A feature-rich e-commerce platform demonstrating oCMS's extensibility and performance.</p>

<h2>Project Overview</h2>
<p>This project showcases how oCMS can be extended to power an e-commerce website with product catalog, shopping cart, and checkout functionality.</p>

<h2>Key Features</h2>
<ul>
<li>Product catalog with categories and filtering</li>
<li>Shopping cart with persistent storage</li>
<li>Secure checkout process</li>
<li>Order management system</li>
<li>Customer accounts and order history</li>
<li>Inventory tracking</li>
</ul>

<h2>Technical Implementation</h2>
<ul>
<li>Custom module for product management</li>
<li>REST API for cart operations</li>
<li>Payment gateway integration</li>
<li>Real-time inventory updates</li>
</ul>

<h2>Results</h2>
<ul>
<li>Page load times under 200ms</li>
<li>99.9% uptime</li>
<li>Handles 10,000+ products</li>
<li>Mobile-first responsive design</li>
</ul>`
}

func getPortfolioCorporateBody() string {
	return `<p class="lead">Complete website redesign for a major corporation, delivered on time and under budget.</p>

<h2>The Challenge</h2>
<p>The client needed a modern, fast, and secure website to replace their aging WordPress installation. Key requirements included:</p>
<ul>
<li>Improved performance and page load times</li>
<li>Enhanced security posture</li>
<li>Multi-language support for global audience</li>
<li>Easy content management for non-technical staff</li>
</ul>

<h2>Our Solution</h2>
<p>We migrated the entire website to oCMS, implementing:</p>
<ul>
<li>Custom corporate theme matching brand guidelines</li>
<li>Content migration from WordPress</li>
<li>Multi-language content in 5 languages</li>
<li>Integration with existing CRM</li>
</ul>

<h2>Results</h2>
<ul>
<li>85% improvement in page load times</li>
<li>Zero security incidents since launch</li>
<li>50% reduction in hosting costs</li>
<li>Positive feedback from content team</li>
</ul>`
}

func getServicesWebDevBody() string {
	return `<p class="lead">We build fast, secure, and maintainable web applications using modern technologies.</p>

<h2>What We Offer</h2>

<h3>Custom Web Development</h3>
<p>From simple websites to complex web applications, we deliver solutions tailored to your needs. Our expertise includes:</p>
<ul>
<li>Content management systems</li>
<li>E-commerce platforms</li>
<li>API development</li>
<li>Database design</li>
</ul>

<h3>oCMS Implementation</h3>
<p>As the creators of oCMS, we're uniquely qualified to help you:</p>
<ul>
<li>Deploy and configure oCMS</li>
<li>Develop custom themes</li>
<li>Build custom modules</li>
<li>Integrate with existing systems</li>
</ul>

<h3>Migration Services</h3>
<p>Moving from another CMS? We can help migrate your:</p>
<ul>
<li>Content and media</li>
<li>User accounts</li>
<li>URL structure (with redirects)</li>
<li>SEO settings</li>
</ul>

<h2>Our Process</h2>
<ol>
<li><strong>Discovery</strong> - Understanding your requirements</li>
<li><strong>Planning</strong> - Architecture and timeline</li>
<li><strong>Development</strong> - Iterative development with regular updates</li>
<li><strong>Testing</strong> - Comprehensive QA and security testing</li>
<li><strong>Deployment</strong> - Smooth launch with monitoring</li>
<li><strong>Support</strong> - Ongoing maintenance and updates</li>
</ol>

<h2>Get Started</h2>
<p>Contact us to discuss your project requirements. We'll provide a free consultation and estimate.</p>`
}

func getCookiePolicyBody() string {
	return `<p>This page explains what cookies we use, why we use them, and how you can manage your preferences.</p>

<h2>What Are Cookies?</h2>
<p>Cookies are small text files stored on your device by your web browser. They help websites remember information about your visit, making your next visit easier and the site more useful to you.</p>

<h2>Essential Cookies</h2>
<p>These cookies are required for the website to function and cannot be disabled.</p>

<table>
<thead>
<tr><th>Cookie</th><th>Purpose</th><th>Duration</th></tr>
</thead>
<tbody>
<tr>
<td><code>session</code> / <code>__Host-session</code></td>
<td>Maintains your login session and authentication state. The <code>__Host-</code> prefix is used in production for additional security.</td>
<td>24 hours</td>
</tr>
<tr>
<td><code>klaro</code></td>
<td>Stores your cookie consent preferences so you are not asked again on every visit.</td>
<td>1 year</td>
</tr>
</tbody>
</table>

<h2>Functional Cookies</h2>
<p>These cookies provide additional functionality and a better experience. They are classified as essential and enabled by default.</p>

<table>
<thead>
<tr><th>Cookie</th><th>Purpose</th><th>Duration</th></tr>
</thead>
<tbody>
<tr>
<td><code>ocms_lang</code></td>
<td>Remembers your preferred language so the site displays content in the correct language on return visits.</td>
<td>1 year</td>
</tr>
<tr>
<td><code>ocms_informer_dismissed</code></td>
<td>Remembers that you dismissed the notification bar so it does not reappear on every page.</td>
<td>1 year</td>
</tr>
</tbody>
</table>

<h2>Third-Party Cookies</h2>
<p>Depending on the services enabled by the site administrator, third-party cookies may be set for analytics or marketing purposes. These are only activated after you give consent through the cookie banner. Examples include:</p>
<ul>
<li><strong>Google Analytics</strong> (<code>_ga</code>, <code>_gid</code>) &mdash; website traffic analysis</li>
<li><strong>Google Ads</strong> (<code>_gcl</code>) &mdash; conversion tracking and advertising</li>
<li><strong>Matomo</strong> (<code>_pk_</code>) &mdash; privacy-focused analytics</li>
</ul>

<h2>Managing Your Preferences</h2>
<p>You can change your cookie preferences at any time by clicking the &ldquo;Cookie Settings&rdquo; link in the page footer. You can also configure your browser to block or delete cookies, though this may affect site functionality.</p>`
}
