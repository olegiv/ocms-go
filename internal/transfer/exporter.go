package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"

	"ocms-go/internal/store"
)

// Exporter handles exporting CMS content to JSON format.
type Exporter struct {
	store  *store.Queries
	logger *slog.Logger
}

// NewExporter creates a new Exporter instance.
func NewExporter(queries *store.Queries, logger *slog.Logger) *Exporter {
	return &Exporter{
		store:  queries,
		logger: logger,
	}
}

// Export generates an ExportData structure based on the provided options.
func (e *Exporter) Export(ctx context.Context, opts ExportOptions) (*ExportData, error) {
	data := &ExportData{
		Version:    ExportVersion,
		ExportedAt: time.Now().UTC(),
		Site:       ExportSite{},
	}

	// Export site configuration
	if opts.IncludeConfig {
		if err := e.exportConfig(ctx, data); err != nil {
			e.logger.Warn("failed to export config", "error", err)
		}
	}

	// Export languages
	if opts.IncludeLanguages {
		if err := e.exportLanguages(ctx, data); err != nil {
			e.logger.Warn("failed to export languages", "error", err)
		}
	}

	// Build lookup maps for reference resolution
	userMap, err := e.buildUserMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build user map", "error", err)
	}

	categoryMap, err := e.buildCategoryMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build category map", "error", err)
	}

	tagMap, err := e.buildTagMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build tag map", "error", err)
	}

	mediaMap, err := e.buildMediaMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build media map", "error", err)
	}

	languageMap, err := e.buildLanguageMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build language map", "error", err)
	}

	pageMap, err := e.buildPageSlugMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build page slug map", "error", err)
	}

	// Export users
	if opts.IncludeUsers {
		if err := e.exportUsers(ctx, data, userMap); err != nil {
			e.logger.Warn("failed to export users", "error", err)
		}
	}

	// Export categories
	if opts.IncludeCategories {
		if err := e.exportCategories(ctx, data, categoryMap, languageMap); err != nil {
			e.logger.Warn("failed to export categories", "error", err)
		}
	}

	// Export tags
	if opts.IncludeTags {
		if err := e.exportTags(ctx, data, languageMap); err != nil {
			e.logger.Warn("failed to export tags", "error", err)
		}
	}

	// Export media
	if opts.IncludeMedia {
		if err := e.exportMedia(ctx, data, userMap); err != nil {
			e.logger.Warn("failed to export media", "error", err)
		}
	}

	// Export pages
	if opts.IncludePages {
		if err := e.exportPages(ctx, data, opts, userMap, categoryMap, tagMap, mediaMap, languageMap); err != nil {
			e.logger.Warn("failed to export pages", "error", err)
		}
	}

	// Export menus
	if opts.IncludeMenus {
		if err := e.exportMenus(ctx, data, pageMap, languageMap); err != nil {
			e.logger.Warn("failed to export menus", "error", err)
		}
	}

	// Export forms
	if opts.IncludeForms {
		if err := e.exportForms(ctx, data, opts.IncludeSubmissions); err != nil {
			e.logger.Warn("failed to export forms", "error", err)
		}
	}

	return data, nil
}

// ExportToWriter writes the export as JSON to the provided writer.
func (e *Exporter) ExportToWriter(ctx context.Context, opts ExportOptions, w io.Writer) error {
	data, err := e.Export(ctx, opts)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// ExportToFile writes the export as JSON to a file.
func (e *Exporter) ExportToFile(ctx context.Context, opts ExportOptions, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return e.ExportToWriter(ctx, opts, f)
}

// exportConfig exports site configuration.
func (e *Exporter) exportConfig(ctx context.Context, data *ExportData) error {
	configs, err := e.store.ListConfig(ctx)
	if err != nil {
		return err
	}

	data.Config = make(map[string]string)
	for _, cfg := range configs {
		data.Config[cfg.Key] = cfg.Value

		// Populate site info
		switch cfg.Key {
		case "site_name":
			data.Site.Name = cfg.Value
		case "site_description":
			data.Site.Description = cfg.Value
		case "site_url":
			data.Site.URL = cfg.Value
		}
	}

	return nil
}

// exportLanguages exports all languages.
func (e *Exporter) exportLanguages(ctx context.Context, data *ExportData) error {
	languages, err := e.store.ListLanguages(ctx)
	if err != nil {
		return err
	}

	data.Languages = make([]ExportLanguage, 0, len(languages))
	for _, lang := range languages {
		data.Languages = append(data.Languages, ExportLanguage{
			Code:       lang.Code,
			Name:       lang.Name,
			NativeName: lang.NativeName,
			IsDefault:  lang.IsDefault,
			IsActive:   lang.IsActive,
			Direction:  lang.Direction,
			Position:   lang.Position,
		})
	}

	return nil
}

// exportUsers exports all users (without passwords).
func (e *Exporter) exportUsers(ctx context.Context, data *ExportData, userMap map[int64]string) error {
	// Use a reasonable limit for users
	users, err := e.store.ListUsers(ctx, store.ListUsersParams{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		return err
	}

	data.Users = make([]ExportUser, 0, len(users))
	for _, user := range users {
		data.Users = append(data.Users, ExportUser{
			Email:     user.Email,
			Name:      user.Name,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
		})
	}

	return nil
}

// exportCategories exports all categories with hierarchy.
func (e *Exporter) exportCategories(ctx context.Context, data *ExportData, categoryMap map[int64]string, languageMap map[int64]string) error {
	categories, err := e.store.ListCategories(ctx)
	if err != nil {
		return err
	}

	data.Categories = make([]ExportCategory, 0, len(categories))
	for _, cat := range categories {
		exportCat := ExportCategory{
			ID:          cat.ID,
			Name:        cat.Name,
			Slug:        cat.Slug,
			Description: nullStringToString(cat.Description),
			Position:    cat.Position,
			CreatedAt:   cat.CreatedAt,
			UpdatedAt:   cat.UpdatedAt,
		}

		// Resolve parent category slug
		if cat.ParentID.Valid {
			if parentSlug, ok := categoryMap[cat.ParentID.Int64]; ok {
				exportCat.ParentSlug = parentSlug
			}
		}

		// Get translations for this category
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "category",
			EntityID:   cat.ID,
		})
		if err == nil && len(translations) > 0 {
			exportCat.Translations = make(map[string]int64)
			for _, t := range translations {
				exportCat.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Categories = append(data.Categories, exportCat)
	}

	return nil
}

// exportTags exports all tags.
func (e *Exporter) exportTags(ctx context.Context, data *ExportData, languageMap map[int64]string) error {
	tags, err := e.store.ListAllTags(ctx)
	if err != nil {
		return err
	}

	data.Tags = make([]ExportTag, 0, len(tags))
	for _, tag := range tags {
		exportTag := ExportTag{
			ID:        tag.ID,
			Name:      tag.Name,
			Slug:      tag.Slug,
			CreatedAt: tag.CreatedAt,
			UpdatedAt: tag.UpdatedAt,
		}

		// Get translations for this tag
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "tag",
			EntityID:   tag.ID,
		})
		if err == nil && len(translations) > 0 {
			exportTag.Translations = make(map[string]int64)
			for _, t := range translations {
				exportTag.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Tags = append(data.Tags, exportTag)
	}

	return nil
}

// exportMedia exports all media metadata.
func (e *Exporter) exportMedia(ctx context.Context, data *ExportData, userMap map[int64]string) error {
	// Build folder path map
	folderPaths, err := e.buildFolderPathMap(ctx)
	if err != nil {
		e.logger.Warn("failed to build folder path map", "error", err)
		folderPaths = make(map[int64]string)
	}

	// Export all media items (using large limit)
	media, err := e.store.ListMedia(ctx, store.ListMediaParams{
		Limit:  100000,
		Offset: 0,
	})
	if err != nil {
		return err
	}

	data.Media = make([]ExportMedia, 0, len(media))
	for _, m := range media {
		exportMedia := ExportMedia{
			UUID:       m.Uuid,
			Filename:   m.Filename,
			MimeType:   m.MimeType,
			Size:       m.Size,
			Alt:        nullStringToString(m.Alt),
			Caption:    nullStringToString(m.Caption),
			UploadedBy: userMap[m.UploadedBy],
			CreatedAt:  m.CreatedAt,
		}

		if m.Width.Valid {
			w := m.Width.Int64
			exportMedia.Width = &w
		}
		if m.Height.Valid {
			h := m.Height.Int64
			exportMedia.Height = &h
		}

		if m.FolderID.Valid {
			exportMedia.FolderPath = folderPaths[m.FolderID.Int64]
		}

		// Get variants
		variants, err := e.store.GetMediaVariants(ctx, m.ID)
		if err == nil && len(variants) > 0 {
			exportMedia.Variants = make([]ExportVariant, 0, len(variants))
			for _, v := range variants {
				exportMedia.Variants = append(exportMedia.Variants, ExportVariant{
					Type:   v.Type,
					Width:  v.Width,
					Height: v.Height,
					Size:   v.Size,
				})
			}
		}

		data.Media = append(data.Media, exportMedia)
	}

	return nil
}

// exportPages exports pages based on options.
func (e *Exporter) exportPages(
	ctx context.Context,
	data *ExportData,
	opts ExportOptions,
	userMap map[int64]string,
	categoryMap map[int64]string,
	tagMap map[int64]string,
	mediaMap map[int64]ExportMediaRef,
	languageMap map[int64]string,
) error {
	var pages []store.Page
	var err error

	// Fetch pages based on status filter
	switch opts.PageStatus {
	case "published":
		pages, err = e.store.ListPublishedPages(ctx, store.ListPublishedPagesParams{
			Limit:  100000,
			Offset: 0,
		})
	case "draft":
		pages, err = e.store.ListPagesByStatus(ctx, store.ListPagesByStatusParams{
			Status: "draft",
			Limit:  100000,
			Offset: 0,
		})
	default: // "all"
		pages, err = e.store.ListPages(ctx, store.ListPagesParams{
			Limit:  100000,
			Offset: 0,
		})
	}
	if err != nil {
		return err
	}

	data.Pages = make([]ExportPage, 0, len(pages))
	for _, page := range pages {
		exportPage := ExportPage{
			ID:          page.ID,
			Title:       page.Title,
			Slug:        page.Slug,
			Body:        page.Body,
			Status:      page.Status,
			AuthorEmail: userMap[page.AuthorID],
			CreatedAt:   page.CreatedAt,
			UpdatedAt:   page.UpdatedAt,
		}

		// Handle published_at
		if page.PublishedAt.Valid {
			t := page.PublishedAt.Time
			exportPage.PublishedAt = &t
		}

		// Handle scheduled_at
		if page.ScheduledAt.Valid {
			t := page.ScheduledAt.Time
			exportPage.ScheduledAt = &t
		}

		// Handle language
		if page.LanguageID.Valid {
			exportPage.LanguageCode = languageMap[page.LanguageID.Int64]
		}

		// Get categories for page
		categories, err := e.store.GetCategoriesForPage(ctx, page.ID)
		if err == nil && len(categories) > 0 {
			exportPage.Categories = make([]string, 0, len(categories))
			for _, cat := range categories {
				exportPage.Categories = append(exportPage.Categories, cat.Slug)
			}
		}

		// Get tags for page
		tags, err := e.store.GetTagsForPage(ctx, page.ID)
		if err == nil && len(tags) > 0 {
			exportPage.Tags = make([]string, 0, len(tags))
			for _, tag := range tags {
				exportPage.Tags = append(exportPage.Tags, tag.Slug)
			}
		}

		// Handle SEO metadata
		if page.MetaTitle != "" || page.MetaDescription != "" || page.MetaKeywords != "" ||
			page.NoIndex != 0 || page.NoFollow != 0 || page.CanonicalUrl != "" || page.OgImageID.Valid {
			exportPage.SEO = &ExportPageSEO{
				MetaTitle:       page.MetaTitle,
				MetaDescription: page.MetaDescription,
				MetaKeywords:    page.MetaKeywords,
				NoIndex:         page.NoIndex != 0,
				NoFollow:        page.NoFollow != 0,
				CanonicalURL:    page.CanonicalUrl,
			}

			// Handle OG image
			if page.OgImageID.Valid {
				if ref, ok := mediaMap[page.OgImageID.Int64]; ok {
					exportPage.SEO.OgImage = &ref
				}
			}
		}

		// Handle featured image
		if page.FeaturedImageID.Valid {
			if ref, ok := mediaMap[page.FeaturedImageID.Int64]; ok {
				exportPage.FeaturedImage = &ref
			}
		}

		// Get translations for this page
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "page",
			EntityID:   page.ID,
		})
		if err == nil && len(translations) > 0 {
			exportPage.Translations = make(map[string]int64)
			for _, t := range translations {
				exportPage.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Pages = append(data.Pages, exportPage)
	}

	return nil
}

// exportMenus exports all menus with their items.
func (e *Exporter) exportMenus(ctx context.Context, data *ExportData, pageMap map[int64]string, languageMap map[int64]string) error {
	menus, err := e.store.ListMenus(ctx)
	if err != nil {
		return err
	}

	data.Menus = make([]ExportMenu, 0, len(menus))
	for _, menu := range menus {
		exportMenu := ExportMenu{
			ID:        menu.ID,
			Name:      menu.Name,
			Slug:      menu.Slug,
			CreatedAt: menu.CreatedAt,
			UpdatedAt: menu.UpdatedAt,
		}

		// Get all menu items
		items, err := e.store.ListTopLevelMenuItems(ctx, menu.ID)
		if err == nil && len(items) > 0 {
			exportMenu.Items = make([]ExportMenuItem, 0, len(items))
			for _, item := range items {
				exportItem := e.exportMenuItem(ctx, item, pageMap)
				exportMenu.Items = append(exportMenu.Items, exportItem)
			}
		}

		data.Menus = append(data.Menus, exportMenu)
	}

	return nil
}

// exportMenuItem exports a menu item recursively (for nested menus).
func (e *Exporter) exportMenuItem(ctx context.Context, item store.MenuItem, pageMap map[int64]string) ExportMenuItem {
	exportItem := ExportMenuItem{
		ID:       item.ID,
		Title:    item.Title,
		URL:      nullStringToString(item.Url),
		Target:   nullStringToString(item.Target),
		CssClass: nullStringToString(item.CssClass),
		IsActive: item.IsActive,
		Position: item.Position,
	}

	// Resolve page slug
	if item.PageID.Valid {
		exportItem.PageSlug = pageMap[item.PageID.Int64]
	}

	// Get children
	children, err := e.store.ListChildMenuItems(ctx, sql.NullInt64{Int64: item.ID, Valid: true})
	if err == nil && len(children) > 0 {
		exportItem.Children = make([]ExportMenuItem, 0, len(children))
		for _, child := range children {
			exportItem.Children = append(exportItem.Children, e.exportMenuItem(ctx, child, pageMap))
		}
	}

	return exportItem
}

// exportForms exports all forms.
func (e *Exporter) exportForms(ctx context.Context, data *ExportData, includeSubmissions bool) error {
	forms, err := e.store.ListForms(ctx, store.ListFormsParams{
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		return err
	}

	data.Forms = make([]ExportForm, 0, len(forms))
	for _, form := range forms {
		exportForm := ExportForm{
			ID:             form.ID,
			Name:           form.Name,
			Slug:           form.Slug,
			Title:          form.Title,
			Description:    nullStringToString(form.Description),
			SuccessMessage: nullStringToString(form.SuccessMessage),
			EmailTo:        nullStringToString(form.EmailTo),
			IsActive:       form.IsActive,
			CreatedAt:      form.CreatedAt,
			UpdatedAt:      form.UpdatedAt,
		}

		// Get form fields
		fields, err := e.store.GetFormFields(ctx, form.ID)
		if err == nil && len(fields) > 0 {
			exportForm.Fields = make([]ExportFormField, 0, len(fields))
			for _, field := range fields {
				exportForm.Fields = append(exportForm.Fields, ExportFormField{
					Type:        field.Type,
					Name:        field.Name,
					Label:       field.Label,
					Placeholder: nullStringToString(field.Placeholder),
					HelpText:    nullStringToString(field.HelpText),
					Options:     nullStringToString(field.Options),
					Validation:  nullStringToString(field.Validation),
					IsRequired:  field.IsRequired,
					Position:    field.Position,
				})
			}
		}

		// Get submissions if requested
		if includeSubmissions {
			submissions, err := e.store.GetFormSubmissions(ctx, store.GetFormSubmissionsParams{
				FormID: form.ID,
				Limit:  100000,
				Offset: 0,
			})
			if err == nil && len(submissions) > 0 {
				exportForm.Submissions = make([]ExportFormSubmission, 0, len(submissions))
				for _, sub := range submissions {
					exportForm.Submissions = append(exportForm.Submissions, ExportFormSubmission{
						Data:      sub.Data,
						IPAddress: nullStringToString(sub.IpAddress),
						UserAgent: nullStringToString(sub.UserAgent),
						IsRead:    sub.IsRead,
						CreatedAt: sub.CreatedAt,
					})
				}
			}
		}

		data.Forms = append(data.Forms, exportForm)
	}

	return nil
}

// Helper methods for building lookup maps

// buildUserMap creates a map of user ID to email.
func (e *Exporter) buildUserMap(ctx context.Context) (map[int64]string, error) {
	users, err := e.store.ListUsers(ctx, store.ListUsersParams{
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	userMap := make(map[int64]string, len(users))
	for _, user := range users {
		userMap[user.ID] = user.Email
	}
	return userMap, nil
}

// buildCategoryMap creates a map of category ID to slug.
func (e *Exporter) buildCategoryMap(ctx context.Context) (map[int64]string, error) {
	categories, err := e.store.ListCategories(ctx)
	if err != nil {
		return nil, err
	}

	categoryMap := make(map[int64]string, len(categories))
	for _, cat := range categories {
		categoryMap[cat.ID] = cat.Slug
	}
	return categoryMap, nil
}

// buildTagMap creates a map of tag ID to slug.
func (e *Exporter) buildTagMap(ctx context.Context) (map[int64]string, error) {
	tags, err := e.store.ListAllTags(ctx)
	if err != nil {
		return nil, err
	}

	tagMap := make(map[int64]string, len(tags))
	for _, tag := range tags {
		tagMap[tag.ID] = tag.Slug
	}
	return tagMap, nil
}

// buildMediaMap creates a map of media ID to media reference.
func (e *Exporter) buildMediaMap(ctx context.Context) (map[int64]ExportMediaRef, error) {
	media, err := e.store.ListMedia(ctx, store.ListMediaParams{
		Limit:  100000,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	mediaMap := make(map[int64]ExportMediaRef, len(media))
	for _, m := range media {
		mediaMap[m.ID] = ExportMediaRef{
			UUID:     m.Uuid,
			Filename: m.Filename,
		}
	}
	return mediaMap, nil
}

// buildLanguageMap creates a map of language ID to code.
func (e *Exporter) buildLanguageMap(ctx context.Context) (map[int64]string, error) {
	languages, err := e.store.ListLanguages(ctx)
	if err != nil {
		return nil, err
	}

	languageMap := make(map[int64]string, len(languages))
	for _, lang := range languages {
		languageMap[lang.ID] = lang.Code
	}
	return languageMap, nil
}

// buildPageSlugMap creates a map of page ID to slug.
func (e *Exporter) buildPageSlugMap(ctx context.Context) (map[int64]string, error) {
	pages, err := e.store.ListPages(ctx, store.ListPagesParams{
		Limit:  100000,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	pageMap := make(map[int64]string, len(pages))
	for _, page := range pages {
		pageMap[page.ID] = page.Slug
	}
	return pageMap, nil
}

// buildFolderPathMap creates a map of folder ID to full path.
func (e *Exporter) buildFolderPathMap(ctx context.Context) (map[int64]string, error) {
	folders, err := e.store.ListMediaFolders(ctx)
	if err != nil {
		return nil, err
	}

	// Build folder name map first
	folderNames := make(map[int64]string, len(folders))
	parentIDs := make(map[int64]int64, len(folders))
	for _, folder := range folders {
		folderNames[folder.ID] = folder.Name
		if folder.ParentID.Valid {
			parentIDs[folder.ID] = folder.ParentID.Int64
		}
	}

	// Build full paths
	folderPaths := make(map[int64]string, len(folders))
	for _, folder := range folders {
		path := buildFolderPath(folder.ID, folderNames, parentIDs)
		folderPaths[folder.ID] = path
	}

	return folderPaths, nil
}

// buildFolderPath recursively builds the full folder path.
func buildFolderPath(id int64, names map[int64]string, parents map[int64]int64) string {
	name := names[id]
	if parentID, ok := parents[id]; ok {
		return buildFolderPath(parentID, names, parents) + "/" + name
	}
	return name
}

// nullStringToString converts sql.NullString to string.
func nullStringToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
