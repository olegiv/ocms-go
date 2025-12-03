package developer

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"ocms-go/internal/model"
	"ocms-go/internal/store"
	"ocms-go/internal/util"
)

// Word lists for generating random content
var (
	adjectives = []string{
		"Amazing", "Beautiful", "Creative", "Dynamic", "Elegant",
		"Fantastic", "Global", "Helpful", "Innovative", "Joyful",
		"Kind", "Lovely", "Modern", "Natural", "Outstanding",
		"Perfect", "Quality", "Reliable", "Smart", "Trendy",
		"Unique", "Vibrant", "Wonderful", "Excellent", "Zesty",
	}

	nouns = []string{
		"Technology", "Science", "Art", "Design", "Business",
		"Health", "Education", "Travel", "Food", "Music",
		"Sports", "Nature", "Culture", "Fashion", "Finance",
		"Entertainment", "Lifestyle", "Photography", "Architecture", "Innovation",
		"Marketing", "Development", "Research", "Solutions", "Services",
	}

	categoryDescriptions = []string{
		"Explore the latest trends and insights",
		"Discover comprehensive resources and guides",
		"Find expert advice and recommendations",
		"Learn from industry professionals",
		"Stay updated with current developments",
	}

	loremParagraphs = []string{
		"Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.",
		"Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.",
		"Sed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium, totam rem aperiam, eaque ipsa quae ab illo inventore veritatis et quasi architecto beatae vitae dicta sunt explicabo.",
		"Nemo enim ipsam voluptatem quia voluptas sit aspernatur aut odit aut fugit, sed quia consequuntur magni dolores eos qui ratione voluptatem sequi nesciunt. Neque porro quisquam est, qui dolorem ipsum quia dolor sit amet.",
		"At vero eos et accusamus et iusto odio dignissimos ducimus qui blanditiis praesentium voluptatum deleniti atque corrupti quos dolores et quas molestias excepturi sint occaecati cupiditate non provident.",
	}

	// Placeholder colors for generated images (RGB values)
	placeholderColors = []struct{ R, G, B uint8 }{
		{66, 133, 244}, // Blue
		{219, 68, 55},  // Red
		{244, 180, 0},  // Yellow
		{15, 157, 88},  // Green
		{171, 71, 188}, // Purple
		{0, 172, 193},  // Cyan
		{255, 112, 67}, // Orange
		{124, 179, 66}, // Light Green
		{63, 81, 181},  // Indigo
		{233, 30, 99},  // Pink
	}
)

// GeneratedCounts holds the counts of generated items
type GeneratedCounts struct {
	Tags       int
	Categories int
	Media      int
	Pages      int
}

// GenerateResult contains the result of the generation operation
type GenerateResult struct {
	Counts   GeneratedCounts
	TagIDs   []int64
	CatIDs   []int64
	MediaIDs []int64
	PageIDs  []int64
}

// generateRandomCount returns a random number between 5 and 20
func generateRandomCount() int {
	return rand.Intn(16) + 5 // 5-20
}

// generateMenuItemCount returns a random number between 5 and 10 for menu items
func generateMenuItemCount() int {
	return rand.Intn(6) + 5 // 5-10
}

// randomElement returns a random element from a string slice
func randomElement(slice []string) string {
	return slice[rand.Intn(len(slice))]
}

// generateLoremIpsum generates 3-5 paragraphs of Lorem Ipsum
func generateLoremIpsum() string {
	numParagraphs := rand.Intn(3) + 3 // 3-5 paragraphs
	var paragraphs []string
	for i := 0; i < numParagraphs; i++ {
		paragraphs = append(paragraphs, loremParagraphs[rand.Intn(len(loremParagraphs))])
	}
	return strings.Join(paragraphs, "\n\n")
}

// trackItem adds an item to the tracking table
func (m *Module) trackItem(ctx context.Context, entityType string, entityID int64) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO developer_generated_items (entity_type, entity_id, created_at)
		VALUES (?, ?, ?)
	`, entityType, entityID, time.Now())
	return err
}

// getTrackedItems returns all tracked items of a given type
func (m *Module) getTrackedItems(ctx context.Context, entityType string) ([]int64, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_id FROM developer_generated_items WHERE entity_type = ?
	`, entityType)
	if err != nil {
		return nil, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Println("Failed to close rows:", err)
		}
	}(rows)

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// getTrackedCounts returns counts of tracked items by type
func (m *Module) getTrackedCounts(ctx context.Context) (map[string]int, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_type, COUNT(*) as cnt FROM developer_generated_items GROUP BY entity_type
	`)
	if err != nil {
		return nil, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Println("Failed to close rows:", err)
		}
	}(rows)

	counts := make(map[string]int)
	for rows.Next() {
		var entityType string
		var cnt int
		if err := rows.Scan(&entityType, &cnt); err != nil {
			return nil, err
		}
		counts[entityType] = cnt
	}
	return counts, rows.Err()
}

// clearTrackedItems removes all tracking records
func (m *Module) clearTrackedItems(ctx context.Context) error {
	_, err := m.ctx.DB.ExecContext(ctx, `DELETE FROM developer_generated_items`)
	return err
}

// generateTags creates random tags with translations
func (m *Module) generateTags(ctx context.Context, languages []store.Language) ([]int64, error) {
	count := generateRandomCount()
	var tagIDs []int64
	queries := store.New(m.ctx.DB)

	// Find default language
	var defaultLangID int64
	for _, lang := range languages {
		if lang.IsDefault {
			defaultLangID = lang.ID
			break
		}
	}

	usedNames := make(map[string]bool)

	for i := 0; i < count; i++ {
		// Generate unique name
		var name string
		for {
			name = randomElement(nouns)
			if !usedNames[name] {
				usedNames[name] = true
				break
			}
			name = randomElement(adjectives) + " " + randomElement(nouns)
			if !usedNames[name] {
				usedNames[name] = true
				break
			}
		}

		tagSlug := util.Slugify(name)
		now := time.Now()

		// Create tag in default language
		tag, err := queries.CreateTag(ctx, store.CreateTagParams{
			Name:       name,
			Slug:       tagSlug,
			LanguageID: sql.NullInt64{Int64: defaultLangID, Valid: true},
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create tag: %w", err)
		}

		if err := m.trackItem(ctx, "tag", tag.ID); err != nil {
			return nil, fmt.Errorf("failed to track tag: %w", err)
		}
		tagIDs = append(tagIDs, tag.ID)

		// Create translations for other languages
		for _, lang := range languages {
			if lang.ID == defaultLangID {
				continue
			}

			translatedName := fmt.Sprintf("%s (%s)", name, lang.Code)
			translatedSlug := fmt.Sprintf("%s-%s", tagSlug, lang.Code)

			transTag, err := queries.CreateTag(ctx, store.CreateTagParams{
				Name:       translatedName,
				Slug:       translatedSlug,
				LanguageID: sql.NullInt64{Int64: lang.ID, Valid: true},
				CreatedAt:  now,
				UpdatedAt:  now,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create tag translation: %w", err)
			}

			if err := m.trackItem(ctx, "tag", transTag.ID); err != nil {
				return nil, fmt.Errorf("failed to track tag translation: %w", err)
			}

			// Create translation record
			trans, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
				EntityType:    "tag",
				EntityID:      tag.ID,
				LanguageID:    lang.ID,
				TranslationID: transTag.ID,
				CreatedAt:     now,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create translation record: %w", err)
			}

			if err := m.trackItem(ctx, "translation", trans.ID); err != nil {
				return nil, fmt.Errorf("failed to track translation: %w", err)
			}
		}
	}

	return tagIDs, nil
}

// generateCategories creates random categories with nested structure and translations
func (m *Module) generateCategories(ctx context.Context, languages []store.Language) ([]int64, error) {
	count := generateRandomCount()
	var catIDs []int64
	queries := store.New(m.ctx.DB)

	// Find default language
	var defaultLangID int64
	for _, lang := range languages {
		if lang.IsDefault {
			defaultLangID = lang.ID
			break
		}
	}

	usedNames := make(map[string]bool)
	var rootCats []int64

	// Calculate distribution: 40% root, 40% children, 20% grandchildren
	numRoot := int(float64(count) * 0.4)
	if numRoot < 1 {
		numRoot = 1
	}
	numChildren := int(float64(count) * 0.4)
	numGrandchildren := count - numRoot - numChildren

	position := int64(0)

	// Create root categories
	for i := 0; i < numRoot; i++ {
		name := randomElement(nouns)
		for usedNames[name] {
			name = randomElement(adjectives) + " " + randomElement(nouns)
		}
		usedNames[name] = true

		catSlug := util.Slugify(name)
		desc := randomElement(categoryDescriptions)
		now := time.Now()
		position++

		cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:        name,
			Slug:        catSlug,
			Description: sql.NullString{String: desc, Valid: true},
			ParentID:    sql.NullInt64{Valid: false},
			Position:    position,
			LanguageID:  sql.NullInt64{Int64: defaultLangID, Valid: true},
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create category: %w", err)
		}

		if err := m.trackItem(ctx, "category", cat.ID); err != nil {
			return nil, fmt.Errorf("failed to track category: %w", err)
		}
		catIDs = append(catIDs, cat.ID)
		rootCats = append(rootCats, cat.ID)

		// Create translations
		if err := m.createCategoryTranslations(ctx, queries, cat, name, catSlug, desc, languages, defaultLangID); err != nil {
			return nil, err
		}
	}

	var childCats []int64

	// Create child categories
	for i := 0; i < numChildren && len(rootCats) > 0; i++ {
		parentID := rootCats[rand.Intn(len(rootCats))]

		name := randomElement(adjectives) + " " + randomElement(nouns)
		for usedNames[name] {
			name = randomElement(adjectives) + " " + randomElement(nouns)
		}
		usedNames[name] = true

		catSlug := util.Slugify(name)
		desc := randomElement(categoryDescriptions)
		now := time.Now()
		position++

		cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:        name,
			Slug:        catSlug,
			Description: sql.NullString{String: desc, Valid: true},
			ParentID:    sql.NullInt64{Int64: parentID, Valid: true},
			Position:    position,
			LanguageID:  sql.NullInt64{Int64: defaultLangID, Valid: true},
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create category: %w", err)
		}

		if err := m.trackItem(ctx, "category", cat.ID); err != nil {
			return nil, fmt.Errorf("failed to track category: %w", err)
		}
		catIDs = append(catIDs, cat.ID)
		childCats = append(childCats, cat.ID)

		if err := m.createCategoryTranslations(ctx, queries, cat, name, catSlug, desc, languages, defaultLangID); err != nil {
			return nil, err
		}
	}

	// Create grandchild categories
	for i := 0; i < numGrandchildren && len(childCats) > 0; i++ {
		parentID := childCats[rand.Intn(len(childCats))]

		name := randomElement(adjectives) + " " + randomElement(nouns)
		for usedNames[name] {
			name = randomElement(adjectives) + " " + randomElement(nouns)
		}
		usedNames[name] = true

		catSlug := util.Slugify(name)
		desc := randomElement(categoryDescriptions)
		now := time.Now()
		position++

		cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:        name,
			Slug:        catSlug,
			Description: sql.NullString{String: desc, Valid: true},
			ParentID:    sql.NullInt64{Int64: parentID, Valid: true},
			Position:    position,
			LanguageID:  sql.NullInt64{Int64: defaultLangID, Valid: true},
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create category: %w", err)
		}

		if err := m.trackItem(ctx, "category", cat.ID); err != nil {
			return nil, fmt.Errorf("failed to track category: %w", err)
		}
		catIDs = append(catIDs, cat.ID)

		if err := m.createCategoryTranslations(ctx, queries, cat, name, catSlug, desc, languages, defaultLangID); err != nil {
			return nil, err
		}
	}

	return catIDs, nil
}

// createCategoryTranslations creates translations for a category
func (m *Module) createCategoryTranslations(ctx context.Context, queries *store.Queries, cat store.Category, name, catSlug, desc string, languages []store.Language, defaultLangID int64) error {
	now := time.Now()

	for _, lang := range languages {
		if lang.ID == defaultLangID {
			continue
		}

		translatedName := fmt.Sprintf("%s (%s)", name, lang.Code)
		translatedSlug := fmt.Sprintf("%s-%s", catSlug, lang.Code)
		translatedDesc := fmt.Sprintf("%s [%s]", desc, lang.Code)

		transCat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:        translatedName,
			Slug:        translatedSlug,
			Description: sql.NullString{String: translatedDesc, Valid: true},
			ParentID:    cat.ParentID,
			Position:    cat.Position,
			LanguageID:  sql.NullInt64{Int64: lang.ID, Valid: true},
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			return fmt.Errorf("failed to create category translation: %w", err)
		}

		if err := m.trackItem(ctx, "category", transCat.ID); err != nil {
			return fmt.Errorf("failed to track category translation: %w", err)
		}

		trans, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
			EntityType:    "category",
			EntityID:      cat.ID,
			LanguageID:    lang.ID,
			TranslationID: transCat.ID,
			CreatedAt:     now,
		})
		if err != nil {
			return fmt.Errorf("failed to create translation record: %w", err)
		}

		if err := m.trackItem(ctx, "translation", trans.ID); err != nil {
			return fmt.Errorf("failed to track translation: %w", err)
		}
	}

	return nil
}

// generateMedia creates random placeholder images
func (m *Module) generateMedia(ctx context.Context, languages []store.Language, uploaderID int64) ([]int64, error) {
	count := generateRandomCount()
	var mediaIDs []int64
	queries := store.New(m.ctx.DB)

	uploadDir := "./uploads"

	for i := 0; i < count; i++ {
		mediaUUID := uuid.New().String()
		filename := fmt.Sprintf("placeholder-%d.jpg", i+1)

		// Pick a random color
		placeholderColor := placeholderColors[rand.Intn(len(placeholderColors))]

		// Create a placeholder image (800x600 colored rectangle)
		imgData, err := createPlaceholderImage(800, 600, placeholderColor.R, placeholderColor.G, placeholderColor.B)
		if err != nil {
			return nil, fmt.Errorf("failed to create placeholder image: %w", err)
		}

		// Save original
		originalsDir := filepath.Join(uploadDir, "originals", mediaUUID)
		if err := os.MkdirAll(originalsDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create originals directory: %w", err)
		}
		originalPath := filepath.Join(originalsDir, filename)
		if err := os.WriteFile(originalPath, imgData, 0644); err != nil {
			return nil, fmt.Errorf("failed to save original image: %w", err)
		}

		// Create variants
		variants := []struct {
			name   string
			width  int
			height int
			crop   bool
		}{
			{"thumbnail", 150, 150, true},
			{"medium", 800, 600, false},
			{"large", 1920, 1080, false},
		}

		for _, v := range variants {
			variantData, err := createPlaceholderImage(v.width, v.height, placeholderColor.R, placeholderColor.G, placeholderColor.B)
			if err != nil {
				continue // Skip variant on error
			}

			variantDir := filepath.Join(uploadDir, v.name, mediaUUID)
			if err := os.MkdirAll(variantDir, 0755); err != nil {
				continue
			}
			variantPath := filepath.Join(variantDir, filename)
			if err := os.WriteFile(variantPath, variantData, 0644); err != nil {
				continue
			}
		}

		// Generate alt and caption
		alt := fmt.Sprintf("Placeholder image %d", i+1)
		caption := fmt.Sprintf("Generated placeholder image with %s color", getColorName(placeholderColor.R, placeholderColor.G, placeholderColor.B))
		now := time.Now()

		// Create media record
		media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid:       mediaUUID,
			Filename:   filename,
			MimeType:   model.MimeTypeJPEG,
			Size:       int64(len(imgData)),
			Width:      sql.NullInt64{Int64: 800, Valid: true},
			Height:     sql.NullInt64{Int64: 600, Valid: true},
			Alt:        sql.NullString{String: alt, Valid: true},
			Caption:    sql.NullString{String: caption, Valid: true},
			FolderID:   sql.NullInt64{Valid: false},
			UploadedBy: uploaderID,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create media record: %w", err)
		}

		if err := m.trackItem(ctx, "media", media.ID); err != nil {
			return nil, fmt.Errorf("failed to track media: %w", err)
		}
		mediaIDs = append(mediaIDs, media.ID)

		// Create variant records
		for _, v := range variants {
			_, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
				MediaID:   media.ID,
				Type:      v.name,
				Width:     int64(v.width),
				Height:    int64(v.height),
				Size:      int64(len(imgData) / 2), // Approximate
				CreatedAt: now,
			})
			if err != nil {
				m.ctx.Logger.Warn("failed to create media variant", "error", err)
			}
		}
	}

	return mediaIDs, nil
}

// createPlaceholderImage creates a solid color JPEG image using Go's standard library
func createPlaceholderImage(width, height int, r, g, b uint8) ([]byte, error) {
	// Create a new RGBA image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Fill with the specified color
	fillColor := color.RGBA{R: r, G: g, B: b, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, fillColor)
		}
	}

	// Encode as JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}

	return buf.Bytes(), nil
}

// getColorName returns a descriptive name for a color
func getColorName(r, g, b uint8) string {
	colors := map[string]struct{ R, G, B uint8 }{
		"blue":        {66, 133, 244},
		"red":         {219, 68, 55},
		"yellow":      {244, 180, 0},
		"green":       {15, 157, 88},
		"purple":      {171, 71, 188},
		"cyan":        {0, 172, 193},
		"orange":      {255, 112, 67},
		"light green": {124, 179, 66},
		"indigo":      {63, 81, 181},
		"pink":        {233, 30, 99},
	}

	for name, c := range colors {
		if c.R == r && c.G == g && c.B == b {
			return name
		}
	}
	return "custom"
}

// generatePages creates random published pages with tags, categories, and images
func (m *Module) generatePages(ctx context.Context, languages []store.Language, tagIDs, catIDs, mediaIDs []int64, authorID int64) ([]int64, error) {
	count := generateRandomCount()
	var pageIDs []int64
	queries := store.New(m.ctx.DB)

	// Find default language
	var defaultLangID int64
	for _, lang := range languages {
		if lang.IsDefault {
			defaultLangID = lang.ID
			break
		}
	}

	usedTitles := make(map[string]bool)

	for i := 0; i < count; i++ {
		// Generate unique title
		var title string
		for {
			title = randomElement(adjectives) + " " + randomElement(nouns) + " Guide"
			if !usedTitles[title] {
				usedTitles[title] = true
				break
			}
		}

		pageSlug := util.Slugify(title)
		body := generateLoremIpsum()
		now := time.Now()

		// Select random featured image
		var featuredImageID sql.NullInt64
		if len(mediaIDs) > 0 {
			featuredImageID = sql.NullInt64{Int64: mediaIDs[rand.Intn(len(mediaIDs))], Valid: true}
		}

		// Create page
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:           title,
			Slug:            pageSlug,
			Body:            body,
			Status:          "published",
			AuthorID:        authorID,
			FeaturedImageID: featuredImageID,
			MetaTitle:       title,
			MetaDescription: body[:min(160, len(body))],
			MetaKeywords:    strings.ToLower(randomElement(nouns) + ", " + randomElement(nouns)),
			OgImageID:       featuredImageID,
			NoIndex:         0,
			NoFollow:        0,
			CanonicalUrl:    "",
			ScheduledAt:     sql.NullTime{Valid: false},
			LanguageID:      sql.NullInt64{Int64: defaultLangID, Valid: true},
			CreatedAt:       now,
			UpdatedAt:       now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create page: %w", err)
		}

		// Publish the page
		_, err = queries.PublishPage(ctx, store.PublishPageParams{
			ID:          page.ID,
			PublishedAt: sql.NullTime{Time: now, Valid: true},
			UpdatedAt:   now,
		})
		if err != nil {
			m.ctx.Logger.Warn("failed to publish page", "error", err)
		}

		if err := m.trackItem(ctx, "page", page.ID); err != nil {
			return nil, fmt.Errorf("failed to track page: %w", err)
		}
		pageIDs = append(pageIDs, page.ID)

		// Assign 1-3 random tags
		if len(tagIDs) > 0 {
			numTags := rand.Intn(3) + 1
			usedTagIDs := make(map[int64]bool)
			for j := 0; j < numTags && j < len(tagIDs); j++ {
				tagID := tagIDs[rand.Intn(len(tagIDs))]
				if usedTagIDs[tagID] {
					continue
				}
				usedTagIDs[tagID] = true
				if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
					PageID: page.ID,
					TagID:  tagID,
				}); err != nil {
					m.ctx.Logger.Warn("failed to add tag to page", "error", err)
				}
			}
		}

		// Assign 1-2 random categories
		if len(catIDs) > 0 {
			numCats := rand.Intn(2) + 1
			usedCatIDs := make(map[int64]bool)
			for j := 0; j < numCats && j < len(catIDs); j++ {
				catID := catIDs[rand.Intn(len(catIDs))]
				if usedCatIDs[catID] {
					continue
				}
				usedCatIDs[catID] = true
				if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
					PageID:     page.ID,
					CategoryID: catID,
				}); err != nil {
					m.ctx.Logger.Warn("failed to add category to page", "error", err)
				}
			}
		}

		// Create translations for other languages
		for _, lang := range languages {
			if lang.ID == defaultLangID {
				continue
			}

			translatedTitle := fmt.Sprintf("%s (%s)", title, lang.Code)
			translatedSlug := fmt.Sprintf("%s-%s", pageSlug, lang.Code)
			translatedBody := fmt.Sprintf("[%s]\n\n%s", lang.Code, body)

			transPage, err := queries.CreatePage(ctx, store.CreatePageParams{
				Title:           translatedTitle,
				Slug:            translatedSlug,
				Body:            translatedBody,
				Status:          "published",
				AuthorID:        authorID,
				FeaturedImageID: featuredImageID,
				MetaTitle:       translatedTitle,
				MetaDescription: translatedBody[:min(160, len(translatedBody))],
				MetaKeywords:    strings.ToLower(randomElement(nouns) + ", " + randomElement(nouns)),
				OgImageID:       featuredImageID,
				NoIndex:         0,
				NoFollow:        0,
				CanonicalUrl:    "",
				ScheduledAt:     sql.NullTime{Valid: false},
				LanguageID:      sql.NullInt64{Int64: lang.ID, Valid: true},
				CreatedAt:       now,
				UpdatedAt:       now,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create page translation: %w", err)
			}

			// Publish the translated page
			_, err = queries.PublishPage(ctx, store.PublishPageParams{
				ID:          transPage.ID,
				PublishedAt: sql.NullTime{Time: now, Valid: true},
				UpdatedAt:   now,
			})
			if err != nil {
				m.ctx.Logger.Warn("failed to publish translated page", "error", err)
			}

			if err := m.trackItem(ctx, "page", transPage.ID); err != nil {
				return nil, fmt.Errorf("failed to track page translation: %w", err)
			}

			// Create translation record
			trans, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
				EntityType:    "page",
				EntityID:      page.ID,
				LanguageID:    lang.ID,
				TranslationID: transPage.ID,
				CreatedAt:     now,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create translation record: %w", err)
			}

			if err := m.trackItem(ctx, "translation", trans.ID); err != nil {
				return nil, fmt.Errorf("failed to track translation: %w", err)
			}
		}
	}

	return pageIDs, nil
}

// generateMenuItems creates random menu items in the Main Menu (ID=1) with nested structure pointing to pages
func (m *Module) generateMenuItems(ctx context.Context, pageIDs []int64) ([]int64, error) {
	count := generateMenuItemCount() // 5-10 menu items
	var menuItemIDs []int64
	queries := store.New(m.ctx.DB)

	const mainMenuID int64 = 1 // Main Menu ID
	now := time.Now()

	// Get the current max position in the menu
	maxPos, err := queries.GetMaxMenuItemPosition(ctx, store.GetMaxMenuItemPositionParams{
		MenuID:   mainMenuID,
		ParentID: sql.NullInt64{Valid: false},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get max position: %w", err)
	}
	startPosition := int64(0)
	if maxPos != nil {
		if pos, ok := maxPos.(int64); ok {
			startPosition = pos + 1
		}
	}

	var rootItemIDs []int64

	for i := 0; i < count; i++ {
		// Determine if this item should have a parent (40% chance if we have root items)
		var parentID sql.NullInt64
		if len(rootItemIDs) > 0 && rand.Float32() < 0.4 {
			parentID = sql.NullInt64{Int64: rootItemIDs[rand.Intn(len(rootItemIDs))], Valid: true}
		}

		// All items link to pages (since we have generated pages)
		var pageID sql.NullInt64
		var url sql.NullString
		title := randomElement(adjectives) + " " + randomElement(nouns)

		if len(pageIDs) > 0 {
			pageID = sql.NullInt64{Int64: pageIDs[rand.Intn(len(pageIDs))], Valid: true}
		} else {
			// Fallback to URL if no pages
			url = sql.NullString{String: "#", Valid: true}
		}

		menuItem, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
			MenuID:    mainMenuID,
			ParentID:  parentID,
			Title:     title,
			Url:       url,
			Target:    sql.NullString{Valid: false},
			PageID:    pageID,
			Position:  startPosition + int64(i),
			CssClass:  sql.NullString{Valid: false},
			IsActive:  true,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create menu item: %w", err)
		}

		if err := m.trackItem(ctx, "menu_item", menuItem.ID); err != nil {
			return nil, fmt.Errorf("failed to track menu item: %w", err)
		}
		menuItemIDs = append(menuItemIDs, menuItem.ID)

		// Add to root items if no parent (for nesting)
		if !parentID.Valid {
			rootItemIDs = append(rootItemIDs, menuItem.ID)
		}
	}

	return menuItemIDs, nil
}

// deleteAllGeneratedItems deletes all items created by this module
func (m *Module) deleteAllGeneratedItems(ctx context.Context) error {
	queries := store.New(m.ctx.DB)
	uploadDir := "./uploads"

	// Delete in order: menu_items, translations, pages, media, categories, tags

	// Delete menu items first
	menuItemIDs, err := m.getTrackedItems(ctx, "menu_item")
	if err != nil {
		return fmt.Errorf("failed to get tracked menu items: %w", err)
	}
	for _, id := range menuItemIDs {
		if err := queries.DeleteMenuItem(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete menu item", "id", id, "error", err)
		}
	}

	// Delete translations
	transIDs, err := m.getTrackedItems(ctx, "translation")
	if err != nil {
		return fmt.Errorf("failed to get tracked translations: %w", err)
	}
	for _, id := range transIDs {
		if err := queries.DeleteTranslation(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete translation", "id", id, "error", err)
		}
	}

	// Delete pages
	pageIDs, err := m.getTrackedItems(ctx, "page")
	if err != nil {
		return fmt.Errorf("failed to get tracked pages: %w", err)
	}
	for _, id := range pageIDs {
		// Clear page associations first
		if err := queries.ClearPageTags(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to clear page tags", "id", id, "error", err)
		}
		if err := queries.ClearPageCategories(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to clear page categories", "id", id, "error", err)
		}
		if err := queries.DeletePage(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete page", "id", id, "error", err)
		}
	}

	// Delete media (including files)
	mediaIDs, err := m.getTrackedItems(ctx, "media")
	if err != nil {
		return fmt.Errorf("failed to get tracked media: %w", err)
	}
	for _, id := range mediaIDs {
		// Get media to find UUID for file deletion
		media, err := queries.GetMediaByID(ctx, id)
		if err != nil {
			m.ctx.Logger.Warn("failed to get media for deletion", "id", id, "error", err)
			continue
		}

		// Delete files
		deleteMediaFiles(uploadDir, media.Uuid)

		// Delete variants and media record
		if err := queries.DeleteMediaVariants(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete media variants", "id", id, "error", err)
		}
		if err := queries.DeleteMedia(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete media", "id", id, "error", err)
		}
	}

	// Delete categories
	catIDs, err := m.getTrackedItems(ctx, "category")
	if err != nil {
		return fmt.Errorf("failed to get tracked categories: %w", err)
	}
	for _, id := range catIDs {
		if err := queries.DeleteCategory(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete category", "id", id, "error", err)
		}
	}

	// Delete tags
	tagIDs, err := m.getTrackedItems(ctx, "tag")
	if err != nil {
		return fmt.Errorf("failed to get tracked tags: %w", err)
	}
	for _, id := range tagIDs {
		if err := queries.DeleteTag(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete tag", "id", id, "error", err)
		}
	}

	// Clear tracking table
	return m.clearTrackedItems(ctx)
}

// deleteMediaFiles removes all files associated with a media item
func deleteMediaFiles(uploadDir, mediaUUID string) {
	// Delete original
	originalsDir := filepath.Join(uploadDir, "originals", mediaUUID)
	_ = os.RemoveAll(originalsDir)

	// Delete variants
	for _, variant := range []string{"thumbnail", "medium", "large"} {
		variantDir := filepath.Join(uploadDir, variant, mediaUUID)
		_ = os.RemoveAll(variantDir)
	}
}
