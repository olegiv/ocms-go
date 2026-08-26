// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

const (
	// maxTaxonomySlugSuffix bounds the probe for a free taxonomy slug.
	maxTaxonomySlugSuffix = 100

	// oCMS discriminates posts from pages with pages.page_type.
	pageTypePost = "post"
	pageTypePage = "page"
)

// slugFor builds a unique slug from a title, falling back to a stable
// identifier when the title transliterates to nothing usable.
//
// Every page write in this file goes through here. Slug allocation is kept
// beside the writes it protects so the route guard below cannot be bypassed by
// a new stage that reaches CreatePage directly.
func (s *Source) slugFor(ctx context.Context, queries *store.Queries, title, fallback string) string {
	base := util.Slugify(title)
	if base == "" {
		base = fallback
	}
	return s.makeUniquePageSlug(ctx, queries, base)
}

// makeUniquePageSlug allocates a slug no other page, alias, redirect, module
// route, core route, or active language prefix already owns.
//
// shared.MakeUniqueSlugWithGuard supplies the language-prefix half: the
// language middleware strips a leading segment matching an active language
// code before the frontend router runs, so a page slugged "ru" is answered by
// the Russian homepage and is unreachable forever.
func (s *Source) makeUniquePageSlug(ctx context.Context, queries *store.Queries, baseSlug string) string {
	return shared.MakeUniqueSlugWithGuard(ctx, queries, baseSlug, func(slug string) bool {
		if corePathReserved(slug) {
			return false
		}
		if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath("/"+slug) {
			return false
		}
		occupied, err := shared.RedirectPathOccupied(ctx, queries, "/"+slug)
		return err == nil && !occupied
	})
}

// corePathReserved reports whether a path belongs to a core oCMS route that an
// imported page must not shadow.
func corePathReserved(p string) bool {
	p = strings.Trim(p, "/")
	if p == "" {
		return true
	}
	first, _, _ := strings.Cut(p, "/")
	if util.IsReservedLanguageCode(strings.ToLower(first)) {
		return true
	}
	switch p {
	case "sitemap.xml", "robots.txt", "favicon.ico":
		return true
	}
	return p == ".well-known" || strings.HasPrefix(p, ".well-known/")
}

// importUsers imports the accounts credited on a story.
//
// Imported accounts are deliberately inert: role "public" grants no admin
// access, and the password hash is a random secret nobody holds, so the
// account cannot be signed into until its owner completes a password reset.
func (s *Source) importUsers(ctx context.Context, queries *store.Queries, reader sourceReader,
	userMap map[string]int64, opts types.ImportOptions, result *types.ImportResult,
	tracker types.ImportTracker) error {
	users, err := reader.GetStoryAuthors(ctx)
	if err != nil {
		return fmt.Errorf("failed to get story authors: %w", err)
	}
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityUser, Total: len(users),
	})

	// One hash for every user in this run — hashing per user is needlessly
	// expensive when nobody can use the credential anyway.
	passwordHash, err := shared.UnguessablePlaceholderHash()
	if err != nil {
		return err
	}
	now := time.Now()

	for i := range users {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		user := &users[i]
		email := strings.TrimSpace(user.Email)
		if email == "" {
			result.AddNotice("User %q has no email address and was not imported", user.Username)
			continue
		}

		// The lookup runs regardless of SkipExisting because users.email is
		// UNIQUE: without it a second run would fail the insert instead of
		// reusing the account, and every story by that author would then fall
		// back to the default author.
		existing, lookupErr := queries.GetUserByEmail(ctx, email)
		switch {
		case lookupErr == nil:
			userMap[user.Username] = existing.ID
			result.UsersSkipped++
			continue
		case errors.Is(lookupErr, sql.ErrNoRows):
			// Not present: create it below.
		default:
			result.AddError("Failed to check for existing user %q: %v", email, lookupErr)
			continue
		}

		created, err := queries.CreateUser(ctx, store.CreateUserParams{
			Email:        email,
			PasswordHash: passwordHash,
			Role:         model.RolePublic,
			Name:         user.DisplayName(),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create user %q: %v", email, err)
			continue
		}
		if !s.track(ctx, tracker, result, types.EntityUser, created.ID, func(rollbackCtx context.Context) error {
			return queries.DeleteUser(rollbackCtx, created.ID)
		}) {
			continue
		}

		userMap[user.Username] = created.ID
		result.UsersImported++
	}
	return nil
}

// importCategories imports topics and static page categories as oCMS
// categories, filling the supplied source-id to category-id maps.
//
// PHP-Nuke has no category nesting: topics and page categories are both flat
// lists, so every imported category is a root.
func (s *Source) importCategories(ctx context.Context, queries *store.Queries, reader sourceReader,
	langCode string, topicMap, pageCategoryMap map[int64]int64, opts types.ImportOptions,
	result *types.ImportResult, tracker types.ImportTracker) error {
	topics, err := reader.GetTopics(ctx)
	if err != nil {
		return fmt.Errorf("failed to get topics: %w", err)
	}
	pageCategories, err := reader.GetPageCategories(ctx)
	if err != nil {
		// A site that never enabled the static pages module has no such table.
		result.AddNotice("Static page categories were not imported: %v", err)
		pageCategories = nil
	}
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityCategory, Total: len(topics) + len(pageCategories),
	})

	for i := range topics {
		topic := &topics[i]
		label := topic.Label()
		if label == "" {
			result.AddNotice("Topic %d has no name and was not imported", topic.ID)
			continue
		}
		if id, ok := s.createCategory(ctx, queries, label, fmt.Sprintf("topic-%d", topic.ID),
			langCode, int64(i), opts, result, tracker); ok {
			topicMap[topic.ID] = id
		}
	}
	for i := range pageCategories {
		category := &pageCategories[i]
		title := strings.TrimSpace(category.Title)
		if title == "" {
			result.AddNotice("Page category %d has no title and was not imported", category.ID)
			continue
		}
		if id, ok := s.createCategory(ctx, queries, title, fmt.Sprintf("section-%d", category.ID),
			langCode, int64(len(topics)+i), opts, result, tracker); ok {
			pageCategoryMap[category.ID] = id
		}
	}
	return nil
}

// createCategory creates one category, reusing an existing row when the slug
// is already taken. It reports the resulting category ID.
func (s *Source) createCategory(ctx context.Context, queries *store.Queries, name, fallbackSlug,
	langCode string, position int64, opts types.ImportOptions, result *types.ImportResult,
	tracker types.ImportTracker) (int64, bool) {
	base := util.Slugify(name)
	if base == "" {
		base = fallbackSlug
	}

	if existing, err := queries.GetCategoryBySlug(ctx, base); err == nil {
		// Reuse rather than duplicate: a category named "Hotels" already in
		// oCMS is the one the operator means, and re-running an import must
		// not create "hotels-2".
		result.CategoriesSkipped++
		return existing.ID, true
	} else if !errors.Is(err, sql.ErrNoRows) {
		result.AddError("Failed to check for existing category %q: %v", name, err)
		return 0, false
	}

	slug := uniqueTaxonomySlug(ctx, base, func(candidate string) (bool, error) {
		_, err := queries.GetCategoryBySlug(ctx, candidate)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, sql.ErrNoRows):
			return false, nil
		default:
			return false, err
		}
	})
	if slug == "" {
		result.AddError("Failed to allocate a free slug for category %q", name)
		return 0, false
	}

	now := time.Now()
	category, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:         name,
		Slug:         slug,
		Description:  sql.NullString{String: "", Valid: true},
		ParentID:     sql.NullInt64{Valid: false},
		Position:     position,
		LanguageCode: langCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		result.AddError("Failed to create category %q: %v", name, err)
		return 0, false
	}
	if !s.track(ctx, tracker, result, types.EntityCategory, category.ID, func(rollbackCtx context.Context) error {
		return queries.DeleteCategory(rollbackCtx, category.ID)
	}) {
		return 0, false
	}
	result.CategoriesImported++
	return category.ID, true
}

// importStoryCategoryTags imports the PHP-Nuke story category table as tags.
//
// A story carries both a topic and a category. Topics are the meaningful
// editorial taxonomy and become categories; the far smaller category table
// becomes tags, so both survive without one shadowing the other.
func (s *Source) importStoryCategoryTags(ctx context.Context, queries *store.Queries, reader sourceReader,
	langCode string, storyCategoryMap map[int64]int64, opts types.ImportOptions,
	result *types.ImportResult, tracker types.ImportTracker) error {
	categories, err := reader.GetStoryCategories(ctx)
	if err != nil {
		return fmt.Errorf("failed to get story categories: %w", err)
	}
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityTag, Total: len(categories),
	})

	now := time.Now()
	for i := range categories {
		category := &categories[i]
		name := strings.TrimSpace(category.Title)
		if name == "" {
			continue
		}
		base := util.Slugify(name)
		if base == "" {
			base = fmt.Sprintf("category-%d", category.ID)
		}

		if existing, err := queries.GetTagBySlug(ctx, base); err == nil {
			storyCategoryMap[category.ID] = existing.ID
			result.TagsSkipped++
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			result.AddError("Failed to check for existing tag %q: %v", name, err)
			continue
		}

		slug := uniqueTaxonomySlug(ctx, base, func(candidate string) (bool, error) {
			_, err := queries.GetTagBySlug(ctx, candidate)
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, sql.ErrNoRows):
				return false, nil
			default:
				return false, err
			}
		})
		if slug == "" {
			result.AddError("Failed to allocate a free slug for tag %q", name)
			continue
		}

		tag, err := queries.CreateTag(ctx, store.CreateTagParams{
			Name:         name,
			Slug:         slug,
			LanguageCode: langCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create tag %q: %v", name, err)
			continue
		}
		if !s.track(ctx, tracker, result, types.EntityTag, tag.ID, func(rollbackCtx context.Context) error {
			return queries.DeleteTag(rollbackCtx, tag.ID)
		}) {
			continue
		}
		storyCategoryMap[category.ID] = tag.ID
		result.TagsImported++
	}
	return nil
}

// uniqueTaxonomySlug probes for a slug the taken predicate rejects, appending
// -2, -3, … and finally a timestamp suffix. It returns "" when no candidate
// could be confirmed free, which the caller must treat as a failure.
//
// A predicate error counts as taken, never free: reading a transient database
// error as "nothing there" is how an import silently collides with a row that
// already exists.
func uniqueTaxonomySlug(ctx context.Context, base string, taken func(string) (bool, error)) string {
	isFree := func(candidate string) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		exists, err := taken(candidate)
		return err == nil && !exists
	}
	if isFree(base) {
		return base
	}
	for i := 2; i <= maxTaxonomySlugSuffix; i++ {
		if candidate := base + "-" + strconv.Itoa(i); isFree(candidate) {
			return candidate
		}
	}
	if fallback := base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36); isFree(fallback) {
		return fallback
	}
	return ""
}

// importMedia copies every file referenced by an imported body into the oCMS
// media library and returns a map from the original attribute text to the new
// oCMS URL.
//
// Only referenced files are imported. A PHP-Nuke document root also holds
// theme furniture, banner creatives, smilies, and years of unreferenced
// uploads; walking it wholesale would fill the media library with thousands of
// files the content never mentions.
func (s *Source) importMedia(ctx context.Context, queries *store.Queries, content *importContent,
	filesPath, uploadDir string, userID int64, langCode string, result *types.ImportResult,
	tracker types.ImportTracker) (map[string]string, error) {
	byPath := make(map[string][]string)
	for _, body := range content.bodies() {
		for _, ref := range extractAssetRefs(body) {
			byPath[ref.Path] = append(byPath[ref.Path], ref.Raw)
		}
	}
	mediaMap := make(map[string]string)
	if len(byPath) == 0 {
		return mediaMap, nil
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	mediaRoot, err := shared.OpenMediaRoot(filesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open media root: %w", err)
	}
	defer func() {
		if err := mediaRoot.Close(); err != nil {
			slog.Error("failed to close phpnuke media root", "error", err)
		}
	}()

	canonicalUploadRoot, err := imaging.CanonicalUploadRoot(uploadDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open uploads root: %w", err)
	}
	processor := imaging.NewProcessor(canonicalUploadRoot)

	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityMedia, Total: len(paths),
	})

	missing := 0
	for _, relPath := range paths {
		select {
		case <-ctx.Done():
			return mediaMap, ctx.Err()
		default:
		}

		newURL, ok := s.importOneFile(ctx, queries, mediaRoot, processor, canonicalUploadRoot,
			relPath, userID, langCode, result, tracker)
		if !ok {
			missing++
			continue
		}
		for _, raw := range byPath[relPath] {
			mediaMap[raw] = newURL
		}
		result.MediaImported++
	}
	if missing > 0 {
		result.AddSummary("%d of %d referenced files were missing from the source tree; "+
			"those image references were left unchanged in the imported bodies.", missing, len(paths))
	}
	return mediaMap, nil
}

// importOneFile ingests a single referenced file and returns its new oCMS URL.
func (s *Source) importOneFile(ctx context.Context, queries *store.Queries, mediaRoot *shared.MediaRoot,
	processor *imaging.Processor, canonicalUploadRoot, relPath string, userID int64, langCode string,
	result *types.ImportResult, tracker types.ImportTracker) (string, bool) {
	srcFile, err := mediaRoot.Open(relPath)
	if err != nil {
		// A decade-old site routinely references files that were deleted long
		// ago. That is expected, not a failure of the import.
		result.AddNotice("Referenced file %q was not found under the source files path", relPath)
		return "", false
	}

	filename := path.Base(relPath)
	fileUUID := uuid.New().String()
	mimeType := shared.MimeTypeFromExt(relPath)

	// Read the size before the file is handed to a writer that seeks it.
	var size int64
	if info, statErr := srcFile.Stat(); statErr == nil {
		size = info.Size()
	}

	if !processor.IsImage(mimeType) {
		writtenRoot, saveErr := shared.SaveNonImageFileWithCanonicalRoot(srcFile, canonicalUploadRoot, fileUUID, filename)
		closeErr := srcFile.Close()
		if err := errors.Join(saveErr, closeErr); err != nil {
			var cleanupErr error
			if writtenRoot != "" {
				cleanupErr = s.cleanupMediaFiles(ctx, tracker, writtenRoot, fileUUID)
			}
			result.AddError("Failed to save %q: %v", relPath, errors.Join(err, cleanupErr))
			return "", false
		}
		media, err := s.createMediaRow(ctx, queries, store.CreateMediaParams{
			Uuid:         fileUUID,
			Filename:     filename,
			MimeType:     mimeType,
			Size:         size,
			Width:        sql.NullInt64{Valid: false},
			Height:       sql.NullInt64{Valid: false},
			UploadedBy:   userID,
			LanguageCode: langCode,
		})
		if err != nil {
			cleanupErr := s.cleanupMediaFiles(ctx, tracker, writtenRoot, fileUUID)
			result.AddError("Failed to create media record for %q: %v", relPath, errors.Join(err, cleanupErr))
			return "", false
		}
		if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
			if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
				return err
			}
			return s.cleanupMediaFiles(rollbackCtx, tracker, writtenRoot, fileUUID)
		}) {
			return "", false
		}
		return model.MediaURL(model.VariantOriginal, fileUUID, filename), true
	}

	// Migration files come from a trusted local directory, so an oversized
	// photo is downscaled rather than dropped.
	processResult, processErr := processor.ProcessImageWithOptions(srcFile, fileUUID, filename,
		imaging.ProcessOptions{DownscaleOversized: true})
	closeErr := srcFile.Close()
	if err := errors.Join(processErr, closeErr); err != nil {
		cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
		result.AddError("Failed to process %q: %v", relPath, errors.Join(err, cleanupErr))
		return "", false
	}

	media, err := s.createMediaRow(ctx, queries, store.CreateMediaParams{
		Uuid:         fileUUID,
		Filename:     filename,
		MimeType:     processResult.MimeType,
		Size:         processResult.Size,
		Width:        sql.NullInt64{Int64: int64(processResult.Width), Valid: true},
		Height:       sql.NullInt64{Int64: int64(processResult.Height), Valid: true},
		UploadedBy:   userID,
		LanguageCode: langCode,
	})
	if err != nil {
		cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
		result.AddError("Failed to create media record for %q: %v", relPath, errors.Join(err, cleanupErr))
		return "", false
	}
	if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
		if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
			return err
		}
		return s.cleanupMediaFiles(rollbackCtx, tracker, canonicalUploadRoot, fileUUID)
	}) {
		return "", false
	}

	// CreateAllVariants errors only when every variant failed — the signal
	// that the whole library will have no thumbnails.
	variants, varErr := processor.CreateAllVariants(processResult.FilePath, fileUUID, filename)
	if varErr != nil {
		result.AddError("%s: no resized variants could be created: %v", filename, varErr)
	}
	now := time.Now()
	for _, v := range variants {
		if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
			MediaID:   media.ID,
			Type:      v.Type,
			Width:     int64(v.Width),
			Height:    int64(v.Height),
			Size:      v.Size,
			CreatedAt: now,
		}); err != nil {
			slog.Warn("failed to record media variant",
				"media_id", media.ID, "variant", v.Type, "error", err)
		}
	}
	return model.MediaURL(model.VariantOriginal, fileUUID, filename), true
}

// createMediaRow fills in the fields every imported media row shares and
// creates it.
func (s *Source) createMediaRow(ctx context.Context, queries *store.Queries,
	params store.CreateMediaParams) (store.Medium, error) {
	now := time.Now()
	params.Alt = sql.NullString{String: "", Valid: true}
	params.Caption = sql.NullString{String: "", Valid: true}
	params.FolderID = sql.NullInt64{Valid: false}
	params.CreatedAt = now
	params.UpdatedAt = now
	return queries.CreateMedia(ctx, params)
}

// cleanupMediaFiles removes the files written for a media UUID, falling back to
// the module's durable retry queue when they cannot be removed now.
func (s *Source) cleanupMediaFiles(ctx context.Context, tracker types.ImportTracker,
	canonicalUploadRoot, mediaUUID string) error {
	err := imaging.DeleteMediaFilesFromCanonicalRoot(canonicalUploadRoot, mediaUUID)
	if err == nil {
		return nil
	}
	queuer, ok := tracker.(types.MediaCleanupQueuer)
	if !ok {
		return err
	}
	queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackingRollbackTimeout)
	queueErr := queuer.QueueMediaCleanup(queueCtx, s.Name(), canonicalUploadRoot, mediaUUID)
	cancel()
	if queueErr != nil {
		return errors.Join(err, fmt.Errorf("queue media cleanup: %w", queueErr))
	}
	return fmt.Errorf("%w (durable cleanup retry queued)", err)
}

// importStories imports news stories as oCMS posts.
func (s *Source) importStories(ctx context.Context, queries *store.Queries, stories []Story,
	userMap map[string]int64, fallbackAuthorID int64, langCode string,
	topicMap, storyCategoryMap map[int64]int64, mediaMap map[string]string,
	opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) {
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityPost, Total: len(stories),
	})
	now := time.Now()

	for i := range stories {
		select {
		case <-ctx.Done():
			return
		default:
		}

		story := &stories[i]
		title := strings.TrimSpace(shared.NullString(story.Title))
		if title == "" {
			title = fmt.Sprintf("Story %d", story.ID)
		}

		if opts.SkipExisting && s.pageExists(ctx, queries, util.Slugify(title), title, result) {
			result.PostsSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fmt.Sprintf("story-%d", story.ID))
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for story %q", title)
			continue
		}

		publishedAt := storyTimestamp(story, now)
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:        title,
			Slug:         slug,
			Body:         s.prepareBody(assembleStoryBody(story), mediaMap),
			Summary:      deriveSummary(shared.NullString(story.HomeText)),
			Status:       model.PageStatusPublished,
			AuthorID:     resolveAuthorID(story, userMap, fallbackAuthorID),
			LanguageCode: langCode,
			PageType:     pageTypePost,
			MetaTitle:    title,
			PublishedAt:  sql.NullTime{Time: publishedAt, Valid: true},
			CreatedAt:    publishedAt,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create post %q: %v", title, err)
			continue
		}
		if !s.track(ctx, tracker, result, types.EntityPost, page.ID, func(rollbackCtx context.Context) error {
			return queries.DeletePage(rollbackCtx, page.ID)
		}) {
			continue
		}

		if categoryID, ok := topicMap[story.TopicID]; ok {
			if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID: page.ID, CategoryID: categoryID,
			}); err != nil {
				result.AddError("Failed to add category to post %q: %v", title, err)
			}
		}
		if tagID, ok := storyCategoryMap[story.CategoryID]; ok {
			if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: page.ID, TagID: tagID,
			}); err != nil {
				result.AddError("Failed to add tag to post %q: %v", title, err)
			}
		}
		result.PostsImported++
	}
}

// importStaticPages imports the PHP-Nuke static pages module.
func (s *Source) importStaticPages(ctx context.Context, queries *store.Queries, pages []StaticPage,
	authorID int64, langCode string, pageCategoryMap map[int64]int64, mediaMap map[string]string,
	opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) {
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityPage, Total: len(pages),
	})
	now := time.Now()

	for i := range pages {
		select {
		case <-ctx.Done():
			return
		default:
		}

		source := &pages[i]
		title := strings.TrimSpace(source.Title)
		if title == "" {
			title = fmt.Sprintf("Page %d", source.ID)
		}
		if opts.SkipExisting && s.pageExists(ctx, queries, util.Slugify(title), title, result) {
			result.PagesSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fmt.Sprintf("page-%d", source.ID))
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for page %q", title)
			continue
		}

		createdAt := now
		if source.Date.Valid && !source.Date.Time.IsZero() {
			createdAt = source.Date.Time
		}
		status := model.PageStatusDraft
		publishedAt := sql.NullTime{}
		if source.IsActive() {
			status = model.PageStatusPublished
			publishedAt = sql.NullTime{Time: createdAt, Valid: true}
		}

		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:        title,
			Slug:         slug,
			Body:         s.prepareBody(assembleStaticPageBody(source), mediaMap),
			Status:       status,
			AuthorID:     authorID,
			LanguageCode: langCode,
			PageType:     pageTypePage,
			MetaTitle:    title,
			// The subtitle is source-controlled HTML, and a meta description is
			// plain text by definition — strip markup rather than storing it raw.
			MetaDescription: plainText(source.Subtitle),
			PublishedAt:     publishedAt,
			CreatedAt:       createdAt,
			UpdatedAt:       now,
		})
		if err != nil {
			result.AddError("Failed to create page %q: %v", title, err)
			continue
		}
		if !s.track(ctx, tracker, result, types.EntityPage, page.ID, func(rollbackCtx context.Context) error {
			return queries.DeletePage(rollbackCtx, page.ID)
		}) {
			continue
		}
		if categoryID, ok := pageCategoryMap[source.CategoryID]; ok {
			if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID: page.ID, CategoryID: categoryID,
			}); err != nil {
				result.AddError("Failed to add category to page %q: %v", title, err)
			}
		}
		result.PagesImported++
	}
}

// importEncyclopedia imports each encyclopedia as a single page holding all of
// its terms.
func (s *Source) importEncyclopedia(ctx context.Context, queries *store.Queries, content *importContent,
	authorID int64, langCode string, mediaMap map[string]string, opts types.ImportOptions,
	result *types.ImportResult, tracker types.ImportTracker) {
	if len(content.encEntries) == 0 {
		return
	}
	now := time.Now()

	for i := range content.encEntries {
		select {
		case <-ctx.Done():
			return
		default:
		}

		entry := &content.encEntries[i]
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = fmt.Sprintf("Encyclopedia %d", entry.ID)
		}
		if opts.SkipExisting && s.pageExists(ctx, queries, util.Slugify(title), title, result) {
			result.PagesSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fmt.Sprintf("encyclopedia-%d", entry.ID))
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for encyclopedia %q", title)
			continue
		}

		terms := content.encTerms[entry.ID]
		status := model.PageStatusDraft
		publishedAt := sql.NullTime{}
		if entry.IsActive() {
			status = model.PageStatusPublished
			publishedAt = sql.NullTime{Time: now, Valid: true}
		}

		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:        title,
			Slug:         slug,
			Body:         s.prepareBody(buildEncyclopediaBody(entry, terms), mediaMap),
			Summary:      deriveSummary(entry.Description),
			Status:       status,
			AuthorID:     authorID,
			LanguageCode: langCode,
			PageType:     pageTypePage,
			MetaTitle:    title,
			PublishedAt:  publishedAt,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create encyclopedia page %q: %v", title, err)
			continue
		}
		if !s.track(ctx, tracker, result, types.EntityPage, page.ID, func(rollbackCtx context.Context) error {
			return queries.DeletePage(rollbackCtx, page.ID)
		}) {
			continue
		}
		result.PagesImported++
		if len(terms) > 0 {
			result.AddSummary("Encyclopedia %q was imported as one page holding %d terms.", title, len(terms))
		}
	}
}

// prepareBody rewrites media references and applies the admin HTML policy.
//
// Sanitizing is unconditional. OCMS_SANITIZE_PAGE_HTML governs render-time
// sanitizing only, and PHP-Nuke bodies are a decade of hand-written markup
// that predates any such policy.
func (s *Source) prepareBody(body string, mediaMap map[string]string) string {
	if mediaMap != nil {
		body = shared.ReplaceURLs(body, mediaMap)
	}
	return security.SanitizePageHTML(body)
}

// pageExists reports whether a page already occupies a slug, for SkipExisting.
func (s *Source) pageExists(ctx context.Context, queries *store.Queries, slug, title string,
	result *types.ImportResult) bool {
	if slug == "" {
		return false
	}
	_, err := queries.GetPageBySlug(ctx, slug)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		return false
	default:
		result.AddError("Failed to check for existing page %q: %v", title, err)
		// Treat an unreadable destination as occupied: creating a second copy
		// is worse than skipping one the operator can import on a re-run.
		return true
	}
}

// resolveAuthorID maps a story onto an imported oCMS account.
//
// PHP-Nuke records two names per story: `informant` is whoever submitted it and
// `aid` is the administrator who published it. The submitter is the better
// attribution, so it wins when both are present.
func resolveAuthorID(story *Story, userMap map[string]int64, fallbackAuthorID int64) int64 {
	for _, username := range []string{strings.TrimSpace(story.Informant), strings.TrimSpace(story.AuthorID)} {
		if username == "" {
			continue
		}
		if id, ok := userMap[username]; ok {
			return id
		}
	}
	return fallbackAuthorID
}
