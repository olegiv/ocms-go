// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
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
	queries := New(db)

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

	// Create demo pages
	if err := seedDemoPages(ctx, queries, adminID, defaultLang.Code, categoryIDs, tagIDs); err != nil {
		return fmt.Errorf("seeding demo pages: %w", err)
	}

	// Create demo menu items
	if err := seedDemoMenuItems(ctx, queries); err != nil {
		return fmt.Errorf("seeding demo menu items: %w", err)
	}

	slog.Info("demo content seeded successfully")
	return nil
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
		{"Blog", "blog", "Latest news, tutorials, and updates", 1},
		{"Portfolio", "portfolio", "Showcase of our work and projects", 2},
		{"Services", "services", "Our professional services", 3},
		{"Resources", "resources", "Helpful guides and documentation", 4},
	}

	ids := make(map[string]int64)
	for _, cat := range categories {
		created, err := queries.CreateCategory(ctx, CreateCategoryParams{
			Name:         cat.Name,
			Slug:         cat.Slug,
			Description:  cat.Description,
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
		{"Tutorial", "tutorial"},
		{"News", "news"},
		{"Featured", "featured"},
		{"Go", "go"},
		{"Web Development", "web-development"},
		{"Design", "design"},
		{"Open Source", "open-source"},
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
}

func seedDemoPages(ctx context.Context, queries *Queries, authorID int64, langCode string, categoryIDs, tagIDs map[string]int64) error {
	// Check if pages already exist
	count, err := queries.CountPages(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		slog.Info("pages already exist, skipping demo pages")
		return nil
	}

	now := time.Now()
	pages := getDemoPages()

	for i, page := range pages {
		publishedAt := sql.NullTime{Valid: false}
		if page.Status == "published" {
			publishedAt = sql.NullTime{Time: now.Add(-time.Duration(len(pages)-i) * 24 * time.Hour), Valid: true}
		}

		created, err := queries.CreatePage(ctx, CreatePageParams{
			Title:             page.Title,
			Slug:              page.Slug,
			Body:              page.Body,
			Status:            page.Status,
			AuthorID:          authorID,
			FeaturedImageID:   sql.NullInt64{Valid: false},
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
					slog.Warn("failed to add category to page", "page", page.Slug, "category", catSlug, "error", err)
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
					slog.Warn("failed to add tag to page", "page", page.Slug, "tag", tagSlug, "error", err)
				}
			}
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
		},
		// Blog posts
		{
			Title:           "Getting Started with oCMS",
			Slug:            "getting-started-with-ocms",
			Body:            getGettingStartedBody(),
			Status:          "published",
			PageType:        "post",
			MetaTitle:       "Getting Started with oCMS - A Complete Guide",
			MetaDescription: "Learn how to set up and configure oCMS for your website in minutes.",
			CategorySlugs:   []string{"blog", "resources"},
			TagSlugs:        []string{"tutorial", "featured"},
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
		{"Home", "/", 1},
		{"Blog", "/category/blog", 2},
		{"Portfolio", "/category/portfolio", 3},
		{"Services", "/web-development-services", 4},
		{"About", "/about", 5},
		{"Contact", "/contact", 6},
	}

	for _, item := range menuItems {
		_, err := queries.CreateMenuItem(ctx, CreateMenuItemParams{
			MenuID:    mainMenu.ID,
			Title:     item.Title,
			Url:       item.URL,
			Target:    "_self",
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

<p><em>Demo credentials: <code>demo@example.com</code> / <code>demo1234demo</code></em></p>`
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
<p>This is a demo installation. The contact form is disabled, but in a production environment, you can create custom forms using the built-in form builder.</p>

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
	return `<p class="lead">This guide will help you get oCMS up and running in minutes.</p>

<h2>Prerequisites</h2>
<ul>
<li>Go 1.24 or later</li>
<li>Node.js 20+ (for asset compilation)</li>
<li>Make (optional, for convenience)</li>
</ul>

<h2>Quick Start</h2>

<h3>1. Clone the Repository</h3>
<pre><code>git clone https://github.com/olegiv/ocms-go.git
cd ocms-go</code></pre>

<h3>2. Build Assets</h3>
<pre><code>make assets</code></pre>

<h3>3. Run the Server</h3>
<pre><code>OCMS_SESSION_SECRET=your-secret-key-here make dev</code></pre>

<h3>4. Access the Admin</h3>
<p>Open <code>http://localhost:8080/admin</code> and login with the default credentials.</p>

<h2>Configuration</h2>
<p>oCMS is configured via environment variables:</p>
<ul>
<li><code>OCMS_SESSION_SECRET</code> - Required. Min 32 characters.</li>
<li><code>OCMS_DB_PATH</code> - Database location (default: ./data/ocms.db)</li>
<li><code>OCMS_SERVER_PORT</code> - HTTP port (default: 8080)</li>
<li><code>OCMS_ACTIVE_THEME</code> - Theme name (default: default)</li>
</ul>

<h2>Next Steps</h2>
<ul>
<li>Create your first page in the admin panel</li>
<li>Customize the theme to match your brand</li>
<li>Set up categories and tags for content organization</li>
<li>Configure menus for site navigation</li>
</ul>`
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
