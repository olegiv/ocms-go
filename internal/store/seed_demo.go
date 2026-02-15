// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
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
	if err := seedDemoPages(ctx, queries, adminID, defaultLang.Code, categoryIDs, tagIDs, mediaIDs); err != nil {
		return fmt.Errorf("seeding demo pages: %w", err)
	}

	// Create demo menu items
	if err := seedDemoMenuItems(ctx, queries); err != nil {
		return fmt.Errorf("seeding demo menu items: %w", err)
	}

	slog.Info("demo content seeded successfully")
	return nil
}

// SeedDemoInformerSettings enables the informer bar with demo credentials.
// Must be called after module initialization (informer module creates the table).
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

	const demoText = `This is a demo instance. Admin panel: <a href="/admin/" style="color:#fff;text-decoration:underline">/admin/</a> &mdash; Login: <strong>demo@example.com</strong> / <strong>demo1234demo</strong>`

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

func seedDemoPages(ctx context.Context, queries *Queries, authorID int64, langCode string, categoryIDs, tagIDs, mediaIDs map[string]int64) error {
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
			FeaturedImage:   "hero-banner.png",
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
			FeaturedImage:   "about-image.png",
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
			FeaturedImage:   "team-photo.png",
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
			FeaturedImage:   "blog-post-1.png",
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
			FeaturedImage:   "blog-post-2.png",
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
			FeaturedImage:   "blog-post-3.png",
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
			FeaturedImage:   "portfolio-1.png",
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
			FeaturedImage:   "portfolio-2.png",
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
			FeaturedImage:   "services-web.png",
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

// demoImage defines a placeholder image to generate.
type demoImage struct {
	Filename string
	Alt      string
	Width    int
	Height   int
	Color    color.RGBA
}

func seedDemoMedia(ctx context.Context, queries *Queries, userID int64, langCode, uploadsDir string) (map[string]int64, error) {
	mediaIDs := make(map[string]int64)

	// Check if media already exists
	count, err := queries.CountMedia(ctx)
	if err != nil {
		return mediaIDs, err
	}
	if count > 0 {
		slog.Info("media already exists, skipping demo media")
		// Return existing media IDs by filename
		media, err := queries.ListMedia(ctx, ListMediaParams{Limit: 100, Offset: 0})
		if err == nil {
			for _, m := range media {
				mediaIDs[m.Filename] = m.ID
			}
		}
		return mediaIDs, nil
	}

	now := time.Now()

	// Define demo images with different colors for variety
	// All images are 2400x1600 to ensure all variants (including large 1920x1080) are created
	images := []demoImage{
		{Filename: "hero-banner.png", Alt: "Hero banner image", Width: 2400, Height: 1600, Color: color.RGBA{R: 59, G: 130, B: 246, A: 255}},    // Blue
		{Filename: "about-image.png", Alt: "About page image", Width: 2400, Height: 1600, Color: color.RGBA{R: 16, G: 185, B: 129, A: 255}},   // Green
		{Filename: "blog-post-1.png", Alt: "Blog post featured image", Width: 2400, Height: 1600, Color: color.RGBA{R: 245, G: 158, B: 11, A: 255}},  // Amber
		{Filename: "blog-post-2.png", Alt: "Blog tutorial image", Width: 2400, Height: 1600, Color: color.RGBA{R: 139, G: 92, B: 246, A: 255}},  // Purple
		{Filename: "blog-post-3.png", Alt: "Blog news image", Width: 2400, Height: 1600, Color: color.RGBA{R: 236, G: 72, B: 153, A: 255}},     // Pink
		{Filename: "portfolio-1.png", Alt: "E-commerce project screenshot", Width: 2400, Height: 1600, Color: color.RGBA{R: 20, G: 184, B: 166, A: 255}}, // Teal
		{Filename: "portfolio-2.png", Alt: "Corporate website screenshot", Width: 2400, Height: 1600, Color: color.RGBA{R: 99, G: 102, B: 241, A: 255}},  // Indigo
		{Filename: "services-web.png", Alt: "Web development services", Width: 2400, Height: 1600, Color: color.RGBA{R: 239, G: 68, B: 68, A: 255}},    // Red
		{Filename: "team-photo.png", Alt: "Team photo placeholder", Width: 2400, Height: 1600, Color: color.RGBA{R: 107, G: 114, B: 128, A: 255}},      // Gray
		{Filename: "logo-sample.png", Alt: "Sample logo image", Width: 2400, Height: 1600, Color: color.RGBA{R: 34, G: 197, B: 94, A: 255}},            // Green-500
	}

	for _, img := range images {
		mediaID, err := createDemoImage(ctx, queries, userID, langCode, uploadsDir, img, now)
		if err != nil {
			slog.Warn("failed to create demo image", "filename", img.Filename, "error", err)
			// Continue with other images
		} else if mediaID > 0 {
			mediaIDs[img.Filename] = mediaID
		}
	}

	slog.Info("seeded demo media", "count", len(images))
	return mediaIDs, nil
}

// imageVariant defines settings for a single image variant.
type imageVariant struct {
	name   string
	width  int
	height int
	crop   bool
}

// demoImageVariants matches the variants defined in internal/model/media.go
var demoImageVariants = []imageVariant{
	{name: "thumbnail", width: 150, height: 150, crop: true},
	{name: "small", width: 400, height: 300, crop: false},
	{name: "medium", width: 800, height: 600, crop: false},
	{name: "large", width: 1920, height: 1080, crop: false},
}

func createDemoImage(ctx context.Context, queries *Queries, userID int64, langCode, uploadsDir string, img demoImage, now time.Time) (int64, error) {
	// Generate UUID for the file
	fileUUID := uuid.New().String()

	// Create the placeholder image
	rect := image.Rect(0, 0, img.Width, img.Height)
	rgba := image.NewRGBA(rect)

	// Fill with the specified color
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: img.Color}, image.Point{}, draw.Src)

	// Add a lighter center rectangle to make it look like a placeholder
	centerRect := image.Rect(img.Width/4, img.Height/4, img.Width*3/4, img.Height*3/4)
	lighterColor := color.RGBA{
		R: min(img.Color.R+40, 255),
		G: min(img.Color.G+40, 255),
		B: min(img.Color.B+40, 255),
		A: 255,
	}
	draw.Draw(rgba, centerRect, &image.Uniform{C: lighterColor}, image.Point{}, draw.Src)

	// Create directory structure for original
	originalsDir := filepath.Join(uploadsDir, "originals", fileUUID)
	if err := os.MkdirAll(originalsDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create originals directory: %w", err)
	}

	// Save the original image
	originalPath := filepath.Join(originalsDir, img.Filename)
	originalData, err := encodePNG(rgba)
	if err != nil {
		return 0, fmt.Errorf("failed to encode original PNG: %w", err)
	}
	if err := os.WriteFile(originalPath, originalData, 0644); err != nil {
		return 0, fmt.Errorf("failed to write original file: %w", err)
	}

	// Create media record in database
	media, err := queries.CreateMedia(ctx, CreateMediaParams{
		Uuid:         fileUUID,
		Filename:     img.Filename,
		MimeType:     "image/png",
		Size:         int64(len(originalData)),
		Width:        sql.NullInt64{Int64: int64(img.Width), Valid: true},
		Height:       sql.NullInt64{Int64: int64(img.Height), Valid: true},
		Alt:          sql.NullString{String: img.Alt, Valid: true},
		Caption:      sql.NullString{String: "", Valid: true},
		FolderID:     sql.NullInt64{Valid: false},
		UploadedBy:   userID,
		LanguageCode: langCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		_ = os.Remove(originalPath)
		return 0, fmt.Errorf("failed to create media record: %w", err)
	}

	// Create image variants directly (following developer module pattern)
	for _, v := range demoImageVariants {
		// Skip if source is smaller than target (for non-crop variants)
		if !v.crop && img.Width <= v.width && img.Height <= v.height {
			continue
		}

		// Create variant directory
		variantDir := filepath.Join(uploadsDir, v.name, fileUUID)
		if err := os.MkdirAll(variantDir, 0755); err != nil {
			slog.Warn("failed to create variant directory", "variant", v.name, "error", err)
			continue
		}

		// Resize the image
		variantData, variantWidth, variantHeight, err := resizeImage(rgba, v.width, v.height, v.crop)
		if err != nil {
			slog.Warn("failed to resize image for variant", "variant", v.name, "error", err)
			continue
		}

		// Save variant file
		variantPath := filepath.Join(variantDir, img.Filename)
		if err := os.WriteFile(variantPath, variantData, 0644); err != nil {
			slog.Warn("failed to write variant file", "variant", v.name, "error", err)
			continue
		}

		// Create variant record in database
		_, err = queries.CreateMediaVariant(ctx, CreateMediaVariantParams{
			MediaID:   media.ID,
			Type:      v.name,
			Width:     int64(variantWidth),
			Height:    int64(variantHeight),
			Size:      int64(len(variantData)),
			CreatedAt: now,
		})
		if err != nil {
			slog.Warn("failed to store variant record", "variant", v.name, "error", err)
		}
	}

	return media.ID, nil
}

// encodePNG encodes an image as PNG and returns the bytes.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resizeImage resizes an image to the specified dimensions.
// If crop is true, it crops to exact size; otherwise it fits within bounds.
func resizeImage(src image.Image, width, height int, crop bool) ([]byte, int, int, error) {
	var resized image.Image
	if crop {
		resized = imaging.Fill(src, width, height, imaging.Center, imaging.Lanczos)
	} else {
		resized = imaging.Fit(src, width, height, imaging.Lanczos)
	}

	bounds := resized.Bounds()
	data, err := encodePNG(resized)
	if err != nil {
		return nil, 0, 0, err
	}

	return data, bounds.Dx(), bounds.Dy(), nil
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
