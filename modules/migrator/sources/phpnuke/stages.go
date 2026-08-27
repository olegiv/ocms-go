// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

	// maxInertEmailSlugLen keeps the readable half of a substitute address
	// short enough that the whole local part stays inside RFC 5321's 64 octets.
	maxInertEmailSlugLen = 40

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
func (s *Source) slugFor(ctx context.Context, queries *store.Queries, title, fallback,
	langCode string) string {
	return s.makeUniquePageSlug(ctx, queries, baseSlug(title, fallback), langCode)
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

// storyIdentity, staticPageIdentity and encyclopediaIdentity return the display
// title of a source row and the slug it falls back to when that title yields
// nothing sluggable.
//
// They exist so the skip probe and the slug allocator cannot drift apart: those
// two now run in different functions, and a probe that derived either half
// differently from the allocator would look for a slug the import never claims.
func storyIdentity(s *Story) (title, fallback string) {
	return rowIdentity(shared.NullString(s.Title), "Story", s.ID)
}

func staticPageIdentity(p *StaticPage) (title, fallback string) {
	return rowIdentity(shared.NullString(p.Title), "Page", p.ID)
}

func encyclopediaIdentity(e *EncyclopediaEntry) (title, fallback string) {
	return rowIdentity(e.Name(), "Encyclopedia", e.ID)
}

// rowIdentity names a source row: the title a reader sees, and the slug to fall
// back on when that title yields nothing sluggable — routine on a
// mis-transcoded archive, where a title can be entirely punctuation.
func rowIdentity(sourceTitle, kind string, id int64) (title, fallback string) {
	title = strings.TrimSpace(sourceTitle)
	if title == "" {
		title = fmt.Sprintf("%s %d", kind, id)
	}
	return title, fmt.Sprintf("%s-%d", strings.ToLower(kind), id)
}

// makeUniquePageSlug allocates a slug no other page, alias, redirect, module
// route, core route, or active language prefix already owns.
//
// shared.MakeUniqueSlugWithGuard supplies the language-prefix half: the
// language middleware strips a leading segment matching an active language
// code before the frontend router runs, so a page slugged "ru" is answered by
// the Russian homepage and is unreachable forever.
func (s *Source) makeUniquePageSlug(ctx context.Context, queries *store.Queries,
	baseSlug, langCode string) string {
	prefix := importedPagePathPrefix(ctx, queries, langCode)
	return shared.MakeUniqueSlugWithGuard(ctx, queries, baseSlug, func(slug string) bool {
		if corePathReserved(slug) {
			return false
		}
		if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath("/"+slug) {
			return false
		}
		// Both paths, because two different middlewares answer them. The
		// redirects middleware is mounted on the root router, ahead of the
		// language-aware frontend router, so it sees "/ru/news" whole: an
		// enabled redirect there owns the imported page's only public URL, and
		// checking the bare "/news" never notices. The unprefixed check still
		// matters, because page slugs are unique across languages.
		for _, candidate := range redirectGuardPaths(slug, prefix) {
			occupied, err := shared.RedirectPathOccupied(ctx, queries, candidate)
			if err != nil || occupied {
				return false
			}
		}
		return true
	})
}

// redirectGuardPaths lists the public URLs a page at this slug would answer.
func redirectGuardPaths(slug, langPrefix string) []string {
	paths := []string{"/" + slug}
	if langPrefix != "" {
		paths = append(paths, langPrefix+"/"+slug)
	}
	return paths
}

// importedPagePathPrefix returns the path segment imported pages are served
// beneath: empty for the default language, "/ru" for any other.
func importedPagePathPrefix(ctx context.Context, queries *store.Queries, langCode string) string {
	code := strings.ToLower(strings.TrimSpace(langCode))
	if code == "" {
		return ""
	}
	defaultLanguage, err := shared.RoutableDefaultLanguage(ctx, queries)
	if err != nil {
		// Guard the prefixed path when the default cannot be read. Over-strict
		// costs a slug suffix; under-strict imports a page nobody can reach.
		return "/" + code
	}
	if strings.EqualFold(defaultLanguage.Code, code) {
		return ""
	}
	return "/" + code
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

// authorKey folds a PHP-Nuke username for lookup.
//
// The source query matches stories.informant and stories.aid against
// users.username under the MySQL column collation, which on a PHP-Nuke
// database of this vintage is case-insensitive. A Go map is not, so a story
// crediting "Olegiv" against a user row stored as "olegiv" imported the user
// successfully and then attributed every one of their stories to the fallback
// account. Both sides of the map fold the same way.
func authorKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// phaseProgress publishes how far one import phase has got.
//
// A stage that announces only its total leaves the admin job view reading
// "0 / 669" from the first row to the last, which is indistinguishable from a
// stalled import — and on a detached job with no console output that view is
// all an operator has. Pages are written by two stages sharing one total, so
// the counter is a value a caller can own and hand to both.
type phaseProgress struct {
	source    string
	phase     types.EntityType
	total     int
	processed int
}

func newPhaseProgress(ctx context.Context, tracker types.ImportTracker, source string,
	phase types.EntityType, total int) *phaseProgress {
	p := &phaseProgress{source: source, phase: phase, total: total}
	p.publish(ctx, tracker)
	return p
}

// step records one attempted row. Skipped and failed rows count too: the
// denominator is rows read, so anything else leaves the bar short of its total
// on a run that skipped anything.
func (p *phaseProgress) step(ctx context.Context, tracker types.ImportTracker) {
	if p == nil {
		return
	}
	p.processed++
	p.publish(ctx, tracker)
}

func (p *phaseProgress) publish(ctx context.Context, tracker types.ImportTracker) {
	types.Report(ctx, tracker, types.Progress{
		Source: p.source, Phase: p.phase, Processed: p.processed, Total: p.total,
	})
}

// optionalTableMissing reports whether a read failed only because the table does
// not exist.
//
// On a twenty-year-old install that is routine — a module was never enabled, or
// the database was stripped to a content archive. Everything else must stay an
// error: a dropped connection, a missing SELECT grant or a canceled context all
// produce an empty read that looks exactly like a site which never had the data,
// so downgrading them to a notice loses content without saying so.
func optionalTableMissing(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrNoSuchTable
}

// creditedAuthors merges the two tables that credit a story into one list.
//
// A `users` row wins over an `authors` row for the same name: it carries the
// profile the site actually displays, and `authors.email` is frequently the
// shared webmaster address, which would collapse several people into a single
// oCMS account.
func (s *Source) creditedAuthors(ctx context.Context, reader sourceReader,
	result *types.ImportResult) ([]User, error) {
	registered, err := reader.GetStoryAuthors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get story authors: %w", err)
	}

	admins, err := reader.GetPublishingAdmins(ctx)
	if err != nil {
		if optionalTableMissing(err) {
			result.AddNotice("The %sauthors table does not exist; publishing bylines were "+
				"taken from %susers alone, and stories credited only to an administrator "+
				"account fall back to the default author.", reader.Prefix(), reader.Prefix())
		} else {
			result.AddError("Failed to read publishing admins: %v; stories credited only "+
				"to an administrator account were attributed to the default author", err)
		}
		admins = nil
	}

	seen := make(map[string]bool, len(registered))
	for i := range registered {
		seen[authorKey(registered[i].Login())] = true
	}
	for i := range admins {
		if !seen[authorKey(admins[i].Login())] {
			registered = append(registered, admins[i])
		}
	}
	return registered, nil
}

// distinctInertEmail builds a stable, undeliverable address for a source
// account whose own address is already spoken for.
//
// oCMS enforces one account per email, but `authors.email` on a PHP-Nuke site
// is very often the shared webmaster address. Honouring the source address for
// everyone therefore merged several administrators into a single oCMS account
// and handed all of their bylines to whichever was read first — and a merge is
// not something the operator can undo afterwards, while two accounts they can.
//
// The domain is suffixed with ".invalid", which RFC 2606 reserves precisely so
// that it can never resolve, and the local part is derived from the source
// username so a re-run finds this account again instead of creating another.
//
// Every address carries a hash of the username, not only the ones whose slug
// comes out empty. Slugify is lossy — "john.doe" and "john_doe" both give
// "johndoe" — so building the local part from the slug alone let two
// administrators land on one substitute address and collapse back into the
// single shared account this function exists to prevent. The slug stays in
// front of the hash only so the address remains readable.
func distinctInertEmail(username, sourceEmail string) string {
	sum := sha256.Sum256([]byte(authorKey(username)))
	local := hex.EncodeToString(sum[:4])
	if slug := util.Slugify(username); slug != "" {
		// Bounded so the local part cannot outgrow the 64 octets RFC 5321
		// allows, however long the source username is.
		if len(slug) > maxInertEmailSlugLen {
			slug = strings.Trim(slug[:maxInertEmailSlugLen], "-")
		}
		if slug != "" {
			local = slug + "-" + local
		}
	}
	domain := "phpnuke"
	if _, host, found := strings.Cut(sourceEmail, "@"); found {
		if cleaned := sanitizeEmailDomain(host); cleaned != "" {
			domain = cleaned
		}
	}
	return local + "@" + domain + ".invalid"
}

// sanitizeEmailDomain rebuilds a source address's domain label by label,
// keeping only what a hostname may contain, so a malformed source address
// cannot shape the address this importer writes.
//
// Filtering characters in one pass is not enough: dropping the illegal ones out
// of "ev'il/../site" leaves "evil..site", an empty label that no longer names a
// host. Discarding empty labels is what keeps the result well-formed.
func sanitizeEmailDomain(host string) string {
	var labels []string
	for _, label := range strings.Split(strings.ToLower(strings.TrimSpace(host)), ".") {
		var b strings.Builder
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				b.WriteRune(r)
			}
		}
		if cleaned := strings.Trim(b.String(), "-"); cleaned != "" {
			labels = append(labels, cleaned)
		}
	}
	return strings.Join(labels, ".")
}

// importUsers imports the accounts credited on a story.
//
// Imported accounts are deliberately inert: role "public" grants no admin
// access, and the password hash is a random secret nobody holds, so the
// account cannot be signed into until its owner completes a password reset.
func (s *Source) importUsers(ctx context.Context, queries *store.Queries, reader sourceReader,
	userMap map[string]int64, opts types.ImportOptions, result *types.ImportResult,
	tracker types.ImportTracker) error {
	users, err := s.creditedAuthors(ctx, reader, result)
	if err != nil {
		return err
	}
	progress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityUser, len(users))

	// One hash for every user in this run — hashing per user is needlessly
	// expensive when nobody can use the credential anyway.
	passwordHash, err := shared.UnguessablePlaceholderHash()
	if err != nil {
		return err
	}
	now := time.Now()

	// Which source account claimed which destination row in this run, keyed by
	// destination id and holding the username as the source spells it — the
	// folded form is for comparison, but an operator reading the notice below
	// needs to recognise the name. Two source accounts sharing one address must
	// not collapse into one byline.
	claimedBy := make(map[int64]string, len(users))

	for i := range users {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		progress.step(ctx, tracker)

		user := &users[i]
		email := strings.TrimSpace(user.Address())
		if email == "" {
			result.AddNotice("User %q has no email address and was not imported", user.Login())
			continue
		}
		key := authorKey(user.Login())

		// The lookup runs regardless of SkipExisting because users.email is
		// UNIQUE: without it a second run would fail the insert instead of
		// reusing the account, and every story by that author would then fall
		// back to the default author.
		existing, lookupErr := queries.GetUserByEmail(ctx, email)
		switch {
		case lookupErr == nil:
			owner, taken := claimedBy[existing.ID]
			if !taken || authorKey(owner) == key {
				claimedBy[existing.ID] = user.Login()
				userMap[key] = existing.ID
				result.UsersSkipped++
				continue
			}
			// Another source account in this run already holds that row. Give
			// this one its own inert address rather than merge the two.
			email = distinctInertEmail(user.Login(), email)
			result.AddNotice("%q and %q are both credited under %s, which oCMS can grant to "+
				"only one account; %q was imported as %s so their bylines stay apart. "+
				"Merge the two accounts if they are the same person.",
				owner, user.Login(), strings.TrimSpace(user.Address()), user.Login(), email)

			retry, retryErr := queries.GetUserByEmail(ctx, email)
			switch {
			case retryErr == nil:
				claimedBy[retry.ID] = user.Login()
				userMap[key] = retry.ID
				result.UsersSkipped++
				continue
			case errors.Is(retryErr, sql.ErrNoRows):
				// Not present: create it below, under the inert address.
			default:
				result.AddError("Failed to check for existing user %q: %v", email, retryErr)
				continue
			}
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

		claimedBy[created.ID] = user.Login()
		userMap[key] = created.ID
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
		if optionalTableMissing(err) {
			result.AddNotice("Static page categories were not imported: the %spages_categories "+
				"table does not exist; this site never enabled the static pages module.", reader.Prefix())
		} else {
			result.AddError("Failed to read static page categories: %v; "+
				"imported pages will have no category assigned", err)
		}
		pageCategories = nil
	}
	progress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityCategory,
		len(topics)+len(pageCategories))

	for i := range topics {
		progress.step(ctx, tracker)
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
		progress.step(ctx, tracker)
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

	// Reuse rather than duplicate: a category named "Hotels" already in oCMS is
	// the one the operator means, and re-running an import must not create
	// "hotels-2". Reuse now turns on the name rather than the slug — see
	// resolveTaxonomySlug.
	existingID, slug, err := resolveTaxonomySlug(ctx, base, name, langCode,
		categoryTermLookup(ctx, queries))
	if err != nil {
		result.AddError("Failed to allocate a free slug for category %q: %v", name, err)
		return 0, false
	}
	if existingID != 0 {
		result.CategoriesSkipped++
		return existingID, true
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
	progress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityTag, len(categories))

	now := time.Now()
	for i := range categories {
		progress.step(ctx, tracker)
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

		existingID, slug, slugErr := resolveTaxonomySlug(ctx, base, name, langCode,
			tagTermLookup(ctx, queries))
		if slugErr != nil {
			result.AddError("Failed to allocate a free slug for tag %q: %v", name, slugErr)
			continue
		}
		if existingID != 0 {
			storyCategoryMap[category.ID] = existingID
			result.TagsSkipped++
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
// taxonomyTerm identifies an existing taxonomy row: enough to tell "this is the
// term being imported" from "this is a different term that happens to slugify
// the same way".
type taxonomyTerm struct {
	ID       int64
	Name     string
	Language string
}

// matches reports whether this row is the term a source label names.
func (t taxonomyTerm) matches(name, langCode string) bool {
	return t.Language == langCode &&
		strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name))
}

// resolveTaxonomySlug walks the suffix family for a base slug and returns
// either the row that already holds this term or a free slug to create it under.
//
// Stopping at the base slug was wrong in both directions. Slugification is
// lossy — "Hello World" and "Hello, World!" both give "hello-world" — so a
// matching slug does not mean a matching term, and reusing on the slug alone
// silently merged the two, refiling every post from the second under the first.
// Walking the family instead of giving up after the base is what keeps a re-run
// idempotent: the second term, stored as "hello-world-2" on the first run, is
// recovered rather than duplicated as "hello-world-3".
//
// Reuse is still the default the importer wants — an operator's own "Hotels"
// category is the one they mean, and re-running must not create "hotels-2".
// Only the test for "same term" got stricter.
func resolveTaxonomySlug(ctx context.Context, base, name, langCode string,
	lookup func(string) (taxonomyTerm, bool, error)) (existingID int64, slug string, err error) {
	// A probe error still means "not free" — reading a transient failure as
	// "nothing there" is how an import collides with a row that already exists.
	// But it also aborts the search: continuing would issue a hundred more
	// doomed probes per row and report the database outage as a slug collision.
	probe := func(candidate string) (int64, bool, error) {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		term, found, err := lookup(candidate)
		switch {
		case err != nil:
			return 0, false, err
		case !found:
			return 0, true, nil
		case term.matches(name, langCode):
			return term.ID, false, nil
		default:
			return 0, false, nil
		}
	}

	candidates := make([]string, 0, maxTaxonomySlugSuffix)
	candidates = append(candidates, base)
	for i := 2; i <= maxTaxonomySlugSuffix; i++ {
		candidates = append(candidates, base+"-"+strconv.Itoa(i))
	}
	// Derived from the term, not from the clock. A timestamped overflow slug
	// cannot be found again: the next run probes the same hundred candidates,
	// misses, mints a different timestamp and duplicates the term. Hashing the
	// identity the matches() test already uses makes the overflow slug the same
	// on every run, so the walk recovers the row it created last time.
	candidates = append(candidates, base+"-"+taxonomyOverflowSuffix(name, langCode))

	for _, candidate := range candidates {
		id, free, err := probe(candidate)
		switch {
		case err != nil:
			return 0, "", err
		case id != 0:
			return id, "", nil
		case free:
			return 0, candidate, nil
		}
	}
	return 0, "", fmt.Errorf("no free slug after %d attempts", maxTaxonomySlugSuffix)
}

// taxonomyOverflowSuffix is the stable last resort when a base slug's whole
// suffix family is occupied, hashing exactly what taxonomyTerm.matches compares
// so the suffix and the reuse test agree on what "the same term" means.
func taxonomyOverflowSuffix(name, langCode string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name)) + "\x00" + langCode))
	return hex.EncodeToString(sum[:4])
}

// categoryTermLookup and tagTermLookup adapt the two taxonomy tables to the
// single slug resolver, so the collision rules cannot drift apart.
func categoryTermLookup(ctx context.Context, queries *store.Queries) func(string) (taxonomyTerm, bool, error) {
	return func(slug string) (taxonomyTerm, bool, error) {
		category, err := queries.GetCategoryBySlug(ctx, slug)
		switch {
		case err == nil:
			return taxonomyTerm{ID: category.ID, Name: category.Name,
				Language: category.LanguageCode}, true, nil
		case errors.Is(err, sql.ErrNoRows):
			return taxonomyTerm{}, false, nil
		default:
			return taxonomyTerm{}, false, err
		}
	}
}

func tagTermLookup(ctx context.Context, queries *store.Queries) func(string) (taxonomyTerm, bool, error) {
	return func(slug string) (taxonomyTerm, bool, error) {
		tag, err := queries.GetTagBySlug(ctx, slug)
		switch {
		case err == nil:
			return taxonomyTerm{ID: tag.ID, Name: tag.Name, Language: tag.LanguageCode}, true, nil
		case errors.Is(err, sql.ErrNoRows):
			return taxonomyTerm{}, false, nil
		default:
			return taxonomyTerm{}, false, err
		}
	}
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
	skips *plannedSkips, filesPath, uploadDir string, userID int64, langCode string,
	result *types.ImportResult, tracker types.ImportTracker) (map[string]string, error) {
	byPath := make(map[string][]string)
	for _, body := range content.bodies(skips) {
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

	progress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityMedia, len(paths))

	var absent, failed int
	for _, relPath := range paths {
		select {
		case <-ctx.Done():
			return mediaMap, ctx.Err()
		default:
		}
		progress.step(ctx, tracker)

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
	skips *plannedSkips, opts types.ImportOptions, result *types.ImportResult,
	tracker types.ImportTracker, bodiesAltered *int) {
	progress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityPost, len(stories))
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
		progress.step(ctx, tracker)

		story := &stories[i]
		title, fallback := storyIdentity(story)
		if skips.skipped(types.EntityPost, story.ID) {
			result.PostsSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fallback, langCode)
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
	skips *plannedSkips, progress *phaseProgress, opts types.ImportOptions,
	result *types.ImportResult, tracker types.ImportTracker, bodiesAltered *int) {
	now := time.Now()

	for i := range pages {
		select {
		case <-ctx.Done():
			return
		default:
		}
		progress.step(ctx, tracker)

		source := &pages[i]
		title, fallback := staticPageIdentity(source)
		if skips.skipped(entityStaticPage, source.ID) {
			result.PagesSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fallback, langCode)
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
	authorID int64, langCode string, mediaMap map[string]string, skips *plannedSkips,
	progress *phaseProgress, opts types.ImportOptions, result *types.ImportResult,
	tracker types.ImportTracker, bodiesAltered *int) {
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
		progress.step(ctx, tracker)

		entry := &content.encEntries[i]
		title, fallback := encyclopediaIdentity(entry)
		if skips.skipped(entityEncyclopedia, entry.ID) {
			result.PagesSkipped++
			continue
		}
		slug := s.slugFor(ctx, queries, title, fallback, langCode)
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
	body = rewriteAssetRefs(body, mediaMap)
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
		authorKey(shared.NullString(story.Informant)),
		authorKey(shared.NullString(story.AuthorID)),
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
