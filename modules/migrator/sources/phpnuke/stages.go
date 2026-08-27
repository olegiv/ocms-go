// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
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

	// mysqlErrNoSuchTable is MySQL's ER_NO_SUCH_TABLE, the one read failure
	// that is genuinely routine against an old PHP-Nuke install.
	mysqlErrNoSuchTable = 1146

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
	return s.makeUniquePageSlug(ctx, queries, baseSlug(title, fallback))
}

// baseSlug is the slug a title would claim before collision suffixing.
//
// The SkipExisting probe and the allocator must derive it the same way. They
// did not: the probe used util.Slugify(title) directly, which is "" for a title
// made only of punctuation or of characters that transliterate to nothing —
// routine on a mis-transcoded twenty-year-old database. pageExists reports a
// blank slug as free, so those rows were never skipped and every re-run created
// another copy.
func baseSlug(title, fallback string) string {
	if base := util.Slugify(title); base != "" {
		return base
	}
	return fallback
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
		email := strings.TrimSpace(user.Address())
		if email == "" {
			result.AddNotice("User %q has no email address and was not imported", user.Login())
			continue
		}

		// The lookup runs regardless of SkipExisting because users.email is
		// UNIQUE: without it a second run would fail the insert instead of
		// reusing the account, and every story by that author would then fall
		// back to the default author.
		existing, lookupErr := queries.GetUserByEmail(ctx, email)
		switch {
		case lookupErr == nil:
			userMap[user.Login()] = existing.ID
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

		userMap[user.Login()] = created.ID
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
		// A site that never enabled the static pages module genuinely has no
		// such table. Anything else — a dropped connection, a missing SELECT
		// grant, a corrupt row — silently imports every static page with no
		// category, so it must not be downgraded to a notice.
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrNoSuchTable {
			result.AddNotice("Static page categories were not imported: the %spages_categories "+
				"table does not exist; this site never enabled the static pages module.", reader.Prefix())
		} else {
			result.AddError("Failed to read static page categories: %v; "+
				"imported pages will have no category assigned", err)
		}
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
		title := strings.TrimSpace(category.Name())
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

	slug, err := uniqueTaxonomySlug(ctx, base, func(candidate string) (bool, error) {
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
	if err != nil {
		result.AddError("Failed to allocate a free slug for category %q: %v", name, err)
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
		name := strings.TrimSpace(category.Name())
		if name == "" {
			result.AddNotice("Story category %d has no title and was not imported; "+
				"posts in that category were left untagged", category.ID)
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

		slug, slugErr := uniqueTaxonomySlug(ctx, base, func(candidate string) (bool, error) {
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
		if slugErr != nil {
			result.AddError("Failed to allocate a free slug for tag %q: %v", name, slugErr)
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

// uniqueTaxonomySlug returns a slug the taken predicate reports as free,
// appending -2, -3, … and finally a timestamp suffix.
//
// A probe error aborts the search rather than being read as either "free" or
// "taken". Reading it as free collides with a row that already exists; reading
// it as taken issues a hundred more doomed probes per row and reports a
// database outage to the operator as a slug collision.
func uniqueTaxonomySlug(ctx context.Context, base string, taken func(string) (bool, error)) (string, error) {
	// A probe error still means "not free" — reading a transient failure as
	// "nothing there" is how an import collides with a row that already exists.
	// But it also aborts the search: continuing would issue a hundred more
	// doomed probes per row and report the database outage as a slug collision.
	isFree := func(candidate string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		exists, err := taken(candidate)
		if err != nil {
			return false, err
		}
		return !exists, nil
	}
	free, err := isFree(base)
	if err != nil {
		return "", err
	}
	if free {
		return base, nil
	}
	for i := 2; i <= maxTaxonomySlugSuffix; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		free, err := isFree(candidate)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	fallback := base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	free, err = isFree(fallback)
	if err != nil {
		return "", err
	}
	if free {
		return fallback, nil
	}
	return "", fmt.Errorf("no free slug after %d attempts", maxTaxonomySlugSuffix)
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

	var absent, failed int
	for _, relPath := range paths {
		select {
		case <-ctx.Done():
			return mediaMap, ctx.Err()
		default:
		}

		newURL, outcome := s.importOneFile(ctx, queries, mediaRoot, processor, canonicalUploadRoot,
			relPath, userID, langCode, result, tracker)
		if outcome == mediaImported && newURL == "" {
			// Guards the one pairing the enum cannot express. Rewriting a body
			// against an empty URL would blank every matching src attribute.
			result.AddError("Internal error: %q reported as imported with no URL", relPath)
			outcome = mediaFailed
		}
		if outcome != mediaImported {
			// Counted separately because the two mean very different things to
			// an operator: one sends them to the old server, the other to their
			// own uploads directory.
			if outcome == mediaAbsent {
				absent++
			} else {
				failed++
			}
			result.MediaSkipped++
			continue
		}
		for _, raw := range byPath[relPath] {
			mediaMap[raw] = newURL
		}
		result.MediaImported++
	}
	if absent > 0 {
		result.AddSummary("%d of %d referenced files were missing from the source tree; "+
			"those image references were left unchanged in the imported bodies.", absent, len(paths))
	}
	if failed > 0 {
		result.AddSummary("%d of %d referenced files exist in the source tree but could not be "+
			"imported; see the errors above. Those image references were left unchanged.",
			failed, len(paths))
	}
	return mediaMap, nil
}

// mediaOutcome distinguishes the reasons importOneFile can decline a file.
//
// A single boolean conflated "the old site had already deleted this" with
// "importing it failed", and the run summary then told the operator that a
// disk-full or permissions problem was missing source data.
type mediaOutcome int

const (
	// mediaFailed is first so it is the zero value: an unclassified path must
	// never read as success. A future naked return of the zero value would
	// otherwise map every reference to an empty URL and rewrite the archive to
	// src="", counted as imported.
	mediaFailed   mediaOutcome = iota // the file is there, but importing it failed
	mediaAbsent                       // the file is genuinely not in the source tree
	mediaImported                     // the file is now in the media library
)

// String names the outcome so it is legible in a log line.
func (o mediaOutcome) String() string {
	switch o {
	case mediaImported:
		return "imported"
	case mediaAbsent:
		return "absent"
	default:
		return "failed"
	}
}

// importOneFile ingests a single referenced file, returning its new oCMS URL
// and an outcome distinguishing a file the source no longer has from one that
// is there but could not be imported.
func (s *Source) importOneFile(ctx context.Context, queries *store.Queries, mediaRoot *shared.MediaRoot,
	processor *imaging.Processor, canonicalUploadRoot, relPath string, userID int64, langCode string,
	result *types.ImportResult, tracker types.ImportTracker) (string, mediaOutcome) {
	srcFile, err := mediaRoot.Open(relPath)
	if err != nil {
		// Only a genuinely absent file is routine on a decade-old site. Every
		// other cause — unreadable permissions, a stale mount, an I/O error, a
		// symlink escaping the trusted root — is a real failure, and reporting
		// it as a notice let a run that imported no media at all still finish
		// as "Completed".
		if errors.Is(err, fs.ErrNotExist) {
			result.AddNotice("Referenced file %q was not found under the source files path", relPath)
			return "", mediaAbsent
		}
		result.AddError("Failed to open referenced file %q: %v", relPath, err)
		return "", mediaFailed
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
			return "", mediaFailed
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
			return "", mediaFailed
		}
		if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
			if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
				return err
			}
			return s.cleanupMediaFiles(rollbackCtx, tracker, writtenRoot, fileUUID)
		}) {
			return "", mediaFailed
		}
		return model.MediaURL(model.VariantOriginal, fileUUID, filename), mediaImported
	}

	// Migration files come from a trusted local directory, so an oversized
	// photo is downscaled rather than dropped.
	processResult, processErr := processor.ProcessImageWithOptions(srcFile, fileUUID, filename,
		imaging.ProcessOptions{DownscaleOversized: true})
	closeErr := srcFile.Close()
	if err := errors.Join(processErr, closeErr); err != nil {
		cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
		result.AddError("Failed to process %q: %v", relPath, errors.Join(err, cleanupErr))
		return "", mediaFailed
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
		return "", mediaFailed
	}
	if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
		if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
			return err
		}
		return s.cleanupMediaFiles(rollbackCtx, tracker, canonicalUploadRoot, fileUUID)
	}) {
		return "", mediaFailed
	}

	// CreateAllVariants errors only when every variant failed — the signal
	// that the whole library will have no thumbnails.
	variants, varErr := processor.CreateAllVariants(processResult.FilePath, fileUUID, filename)
	if varErr != nil {
		result.AddError("%s: no resized variants could be created: %v", filename, varErr)
	}
	now := time.Now()
	var unrecorded []string
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
			unrecorded = append(unrecorded, v.Type)
		}
	}
	// One message per file rather than per variant: a systemic failure would
	// otherwise exhaust the capped message budget and hide its own cause.
	if len(unrecorded) > 0 {
		result.AddError("%s: variant records %v could not be written; "+
			"that image will be served at full size", filename, unrecorded)
	}
	return model.MediaURL(model.VariantOriginal, fileUUID, filename), mediaImported
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
	opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker,
	bodiesAltered *int) {
	types.Report(ctx, tracker, types.Progress{
		Source: s.Name(), Phase: types.EntityPost, Total: len(stories),
	})
	now := time.Now()
	// Tallied rather than reported per story: 669 individual notices would
	// exhaust the capped budget and bury everything else.
	unattributed := 0

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

		if opts.SkipExisting && s.pageExists(ctx, queries, baseSlug(title, fmt.Sprintf("story-%d", story.ID)), title, result) {
			result.PostsSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fmt.Sprintf("story-%d", story.ID))
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for story %q", title)
			continue
		}

		publishedAt := storyTimestamp(story, now)
		// Whether the lookup succeeded, not whether the id happens to equal the
		// fallback: a source author mapped onto an existing oCMS account is
		// very often that same oldest-admin row, and comparing ids reported
		// every such story as having lost its author.
		authorID, resolved := resolveAuthorID(story, userMap, fallbackAuthorID)
		if !resolved && namesAnAuthor(story) {
			unattributed++
		}
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:        title,
			Slug:         slug,
			Body:         s.prepareBody(assembleStoryBody(story), mediaMap, bodiesAltered),
			Summary:      deriveSummary(shared.NullString(story.HomeText)),
			Status:       model.PageStatusPublished,
			AuthorID:     authorID,
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

		if categoryID, ok := topicMap[story.Topic()]; ok {
			if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID: page.ID, CategoryID: categoryID,
			}); err != nil {
				result.AddError("Failed to add category to post %q: %v", title, err)
			}
		}
		if tagID, ok := storyCategoryMap[story.Category()]; ok {
			if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: page.ID, TagID: tagID,
			}); err != nil {
				result.AddError("Failed to add tag to post %q: %v", title, err)
			}
		}
		result.PostsImported++
	}
	if unattributed > 0 {
		result.AddSummary("%d of %d posts named a source author that was not imported and were "+
			"attributed to the fallback account instead.", unattributed, len(stories))
	}
}

// namesAnAuthor reports whether the source row credited anyone at all, so a
// story that never had an author is not counted as having lost one.
func namesAnAuthor(story *Story) bool {
	return strings.TrimSpace(shared.NullString(story.Informant)) != "" ||
		strings.TrimSpace(shared.NullString(story.AuthorID)) != ""
}

// importStaticPages imports the PHP-Nuke static pages module.
func (s *Source) importStaticPages(ctx context.Context, queries *store.Queries, pages []StaticPage,
	authorID int64, langCode string, pageCategoryMap map[int64]int64, mediaMap map[string]string,
	opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker,
	bodiesAltered *int) {
	now := time.Now()

	for i := range pages {
		select {
		case <-ctx.Done():
			return
		default:
		}

		source := &pages[i]
		title := strings.TrimSpace(shared.NullString(source.Title))
		if title == "" {
			title = fmt.Sprintf("Page %d", source.ID)
		}
		if opts.SkipExisting && s.pageExists(ctx, queries, baseSlug(title, fmt.Sprintf("page-%d", source.ID)), title, result) {
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
			Body:         s.prepareBody(assembleStaticPageBody(source), mediaMap, bodiesAltered),
			Status:       status,
			AuthorID:     authorID,
			LanguageCode: langCode,
			PageType:     pageTypePage,
			MetaTitle:    title,
			// The subtitle is source-controlled HTML, and a meta description is
			// plain text by definition — strip markup rather than storing it raw.
			MetaDescription: plainText(shared.NullString(source.Subtitle)),
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
		if categoryID, ok := pageCategoryMap[source.Category()]; ok {
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
	result *types.ImportResult, tracker types.ImportTracker, bodiesAltered *int) {
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
		title := strings.TrimSpace(entry.Name())
		if title == "" {
			title = fmt.Sprintf("Encyclopedia %d", entry.ID)
		}
		if opts.SkipExisting && s.pageExists(ctx, queries, baseSlug(title, fmt.Sprintf("encyclopedia-%d", entry.ID)), title, result) {
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
			Body:         s.prepareBody(buildEncyclopediaBody(entry, terms), mediaMap, bodiesAltered),
			Summary:      deriveSummary(entry.Body()),
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

// prepareBody rewrites media references, applies the admin HTML policy, and
// tallies into altered every body that lost markup.
//
// Sanitizing is unconditional here. Every other page-write path gates on
// OCMS_SANITIZE_PAGE_HTML, so an install running with it off would store
// PHP-Nuke's decade of hand-written markup verbatim.
func (s *Source) prepareBody(body string, mediaMap map[string]string, altered *int) string {
	if mediaMap != nil {
		body = shared.ReplaceURLs(body, mediaMap)
	}
	clean := security.SanitizePageHTML(body)
	// A decade of hand-written markup meets a modern UGC policy, so <font>,
	// inline styles and presentational table attributes are dropped from a lot
	// of bodies. Sanitizing is right; doing it without telling anyone is not.
	if altered != nil && markupRemoved(body, clean) {
		*altered++
	}
	return clean
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

// resolveAuthorID maps a story onto an imported oCMS account, reporting whether
// the lookup succeeded. On failure it returns fallbackAuthorID.
//
// PHP-Nuke records two names per story: `informant` is whoever submitted it and
// `aid` is the administrator who published it. The submitter is the better
// attribution, so it wins when both are present.
func resolveAuthorID(story *Story, userMap map[string]int64, fallbackAuthorID int64) (int64, bool) {
	for _, username := range []string{
		strings.TrimSpace(shared.NullString(story.Informant)),
		strings.TrimSpace(shared.NullString(story.AuthorID)),
	} {
		if username == "" {
			continue
		}
		if id, ok := userMap[username]; ok {
			return id, true
		}
	}
	return fallbackAuthorID, false
}
