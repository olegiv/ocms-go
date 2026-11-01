// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package grounding_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/editorial"
	"github.com/olegiv/ocms-go/internal/store"
)

type fixture struct {
	db           *sql.DB
	q            *store.Queries
	ctx          context.Context
	owner, other int64
}

func seedFixture(t testing.TB) fixture {
	t.Helper()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "grounding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q := store.New(db)
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	users := []int64{}
	for i, role := range []string{"admin", "editor", "editor"} {
		u, err := q.CreateUser(ctx, store.CreateUserParams{Email: fmt.Sprintf("ground-%d@example.test", i), Name: role, Role: role, PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u.ID)
	}
	_, err = q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, q: q, ctx: ctx, owner: users[1], other: users[2]}
}
func (f fixture) page(t testing.TB, slug, language, status string, owner int64) store.Page {
	t.Helper()
	now := time.Unix(1700000000, 0)
	page, err := f.q.CreatePage(f.ctx, store.CreatePageParams{Title: slug, Slug: slug, Body: "<p>Reference text for " + slug + ".</p>", Summary: "Reference summary", Status: status, AuthorID: owner, LanguageCode: language, PageType: "post", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func (f fixture) resolver(t testing.TB, access grounding.Access) *grounding.Resolver {
	t.Helper()
	r, err := grounding.NewResolver(f.q, "https://example.test", access)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f fixture) allow(t testing.TB, id int64) {
	t.Helper()
	if err := f.q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: id, AiEditable: true, SetBy: f.owner, SetAt: time.Unix(1700000000, 0)}); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) redirect(t testing.TB, source, target string, wildcard bool) {
	t.Helper()
	_, err := f.q.CreateRedirect(f.ctx, store.CreateRedirectParams{SourcePath: source, TargetUrl: target, StatusCode: 301, IsWildcard: wildcard, TargetType: "_self", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolverVisibilityAndCanonicalLanguageMatrix(t *testing.T) {
	f := seedFixture(t)
	published := f.page(t, "published", "en", "published", f.other)
	ru := f.page(t, "russian", "ru", "published", f.owner)
	own := f.page(t, "own", "en", "draft", f.owner)
	f.page(t, "human", "en", "draft", f.owner)
	other := f.page(t, "other", "en", "draft", f.other)
	scheduled := f.page(t, "scheduled", "en", "draft", f.owner)
	f.allow(t, own.ID)
	f.allow(t, other.ID)
	f.allow(t, scheduled.ID)
	if _, err := f.db.Exec("UPDATE pages SET scheduled_at=CURRENT_TIMESTAMP WHERE id=?", scheduled.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, ref string
		access    grounding.Access
		want      int64
	}{
		{"published ID", strconv.FormatInt(published.ID, 10), grounding.Access{}, published.ID},
		{"published prefix", "/en/published", grounding.Access{}, published.ID},
		{"same-origin URL", "https://EXAMPLE.test:443/published?q=x#section", grounding.Access{}, published.ID},
		{"Russian prefix", "/ru/russian", grounding.Access{}, ru.ID},
		{"root language mismatch", "/russian", grounding.Access{}, 0},
		{"wrong explicit language", "/en/russian", grounding.Access{}, 0},
		{"uppercase prefix", "/RU/russian", grounding.Access{}, 0},
		{"hidden own draft", "own", grounding.Access{UserID: f.owner}, 0},
		{"own AI draft", "page:" + strconv.FormatInt(own.ID, 10), grounding.Access{UserID: f.owner, OwnDrafts: true}, own.ID},
		{"human draft without opt-in", "human", grounding.Access{UserID: f.owner, OwnDrafts: true}, 0},
		{"other user draft", "other", grounding.Access{UserID: f.owner, OwnDrafts: true}, 0},
		{"scheduled own draft", "scheduled", grounding.Access{UserID: f.owner, OwnDrafts: true}, 0},
		{"general draft policy", "other", grounding.Access{AllDrafts: true}, other.ID},
		{"foreign host", "https://outside.test/published", grounding.Access{}, 0},
		{"embedded credentials", "https://user:secret@example.test/published", grounding.Access{}, 0},
		{"private route", "/admin/published", grounding.Access{AllDrafts: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.resolver(t, tc.access).Resolve(f.ctx, tc.ref)
			if tc.want == 0 {
				if !errors.Is(err, grounding.ErrUnavailable) || page.ID != 0 {
					t.Fatalf("unavailable reference leaked %+v: %v", page, err)
				}
			} else if err != nil || page.ID != tc.want {
				t.Fatalf("page=%+v err=%v want=%d", page, err, tc.want)
			}
		})
	}
	url, err := f.resolver(t, grounding.Access{}).URL(f.ctx, ru)
	if err != nil || url != "https://example.test/ru/russian" {
		t.Fatalf("canonical %q: %v", url, err)
	}
}

func TestResolverAliasesRedirectOrderingAndHopLimit(t *testing.T) {
	f := seedFixture(t)
	published := f.page(t, "published", "en", "published", f.owner)
	ru := f.page(t, "russian", "ru", "published", f.owner)
	for alias, id := range map[string]int64{"old/about": published.ID, "old-ru": ru.ID} {
		if _, err := f.q.CreatePageAlias(f.ctx, store.CreatePageAliasParams{PageID: id, Alias: alias, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	f.redirect(t, "/a", "/b", false)
	f.redirect(t, "/b", "/c", false)
	f.redirect(t, "/c", "/published", false)
	f.redirect(t, "/too-many", "/a", false)
	f.redirect(t, "/loop", "/loop", false)
	f.redirect(t, "/query-loop", "/query-loop?again=1", false)
	f.redirect(t, "/r/**", "/published", true)
	f.redirect(t, "/r/exact", "/missing", false)
	f.redirect(t, "/external", "https://example.test/published", false)
	f.redirect(t, "/unsafe", "javascript:alert(1)", false)
	f.redirect(t, "/api/**", "/published", true)
	f.redirect(t, "/relative/start", "../published", false)
	r := f.resolver(t, grounding.Access{})
	for ref, want := range map[string]int64{"/old/about": published.ID, "/en/old/about": published.ID, "/old-ru": ru.ID, "/ru/old-ru": ru.ID, "/en/old-ru": 0, "/a?keep=1": published.ID, "/too-many": 0, "/loop": 0, "/query-loop": 0, "/r/exact": published.ID, "/external": 0, "/unsafe": 0, "/api/published": 0, "/relative/start": published.ID} {
		t.Run(ref, func(t *testing.T) {
			page, err := r.Resolve(f.ctx, ref)
			if want == 0 {
				if !errors.Is(err, grounding.ErrUnavailable) {
					t.Fatalf("unsafe reference passed: %+v %v", page, err)
				}
			} else if err != nil || page.ID != want {
				t.Fatalf("wrong redirect page %+v %v", page, err)
			}
		})
	}
	f.redirect(t, "/", "/missing", false)
	if exists, err := r.InternalLink(f.ctx, "/"); err != nil || exists {
		t.Fatalf("homepage ignored redirect: %v %v", exists, err)
	}
}

func TestContextRetainsEveryReferenceAndBoundsItsEnvelope(t *testing.T) {
	f := seedFixture(t)
	r := f.resolver(t, grounding.Access{})
	references := []draft.Reference{}
	for i := range grounding.MaxReferences {
		page := f.page(t, fmt.Sprintf("context-%d", i), "en", "published", f.owner)
		if _, err := f.db.Exec("UPDATE pages SET body=? WHERE id=?", "<p>"+strings.Repeat("&lt;", 20000)+"</p>", page.ID); err != nil {
			t.Fatal(err)
		}
		references = append(references, draft.Reference{Ref: strconv.FormatInt(page.ID, 10), Role: "source"})
	}
	if err := editorial.New(f.q).SaveGuide(f.ctx, 1, "", strings.Repeat("<", 8000)); err != nil {
		t.Fatal(err)
	}
	value, err := r.Build(f.ctx, grounding.Input{Brief: strings.Repeat("<", 3000), References: references})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := grounding.JSON(value)
	if err != nil || utf8.RuneCountInString(raw) > grounding.MaxContextRunes {
		t.Fatalf("envelope over limit: %d %v", len(raw), err)
	}
	if len(value.References) != grounding.MaxReferences || !value.Truncated {
		t.Fatalf("reference list lost: %+v", value)
	}
	for i, ref := range value.References {
		if ref.PageID == 0 || ref.Revision != 1 || ref.Requested != references[i].Ref || !ref.Truncated {
			t.Fatalf("reference identity/truncation lost: %+v", ref)
		}
	}
	if !strings.Contains(raw, `\u003c`) {
		t.Fatal("data delimiters were not JSON-escaped")
	}
	if !value.GuideTruncated {
		t.Fatal("truncated guidance section was not marked")
	}
}

func TestContextDuplicateSnapshotsAndInaccessibleReferences(t *testing.T) {
	f := seedFixture(t)
	page := f.page(t, "original", "en", "published", f.owner)
	hidden := f.page(t, "hidden", "en", "draft", f.other)
	r := f.resolver(t, grounding.Access{UserID: f.owner, OwnDrafts: true})
	value, err := r.Build(f.ctx, grounding.Input{References: []draft.Reference{{Ref: "original", Role: "example"}, {Ref: strconv.FormatInt(page.ID, 10), Role: "source"}, {Ref: "hidden", Role: "parent"}, {Ref: "no-such-page", Role: "source"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.References) != 4 || value.References[1].DuplicateOf == nil || *value.References[1].DuplicateOf != 0 || value.References[1].Markdown != "" {
		t.Fatalf("duplicates not retained: %+v", value.References)
	}
	if !value.HasReferenceErrors() {
		t.Fatal("inaccessible references were silently omitted")
	}
	for _, ref := range value.References[2:] {
		if ref.PageID != 0 || ref.Title != "" || len(ref.Issues) != 1 || ref.Issues[0].Code != "reference_unresolved" {
			t.Fatalf("visibility leak for hidden id %d: %+v", hidden.ID, ref)
		}
	}
	if _, err := f.db.Exec("UPDATE pages SET body='changed', revision=revision+1 WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	if value.References[0].Revision != 1 || strings.Contains(value.References[0].Markdown, "changed") {
		t.Fatal("context silently substituted a later revision")
	}
}

func TestExistingMediaSearchEscapesWildcardsAndOffersTranslations(t *testing.T) {
	f := seedFixture(t)
	now := time.Now()
	for i, caption := range []string{"literal%_caption", "literalZZcaption"} {
		image, err := f.q.CreateMedia(f.ctx, store.CreateMediaParams{Uuid: fmt.Sprintf("aaaaaaaa-bbbb-4ccc-8ddd-%012d", i+1), Filename: fmt.Sprintf("image-%d.jpg", i), MimeType: "image/jpeg", Size: 1, Width: sql.NullInt64{Int64: 1536, Valid: true}, Height: sql.NullInt64{Int64: 1024, Valid: true}, Caption: sql.NullString{String: caption, Valid: true}, UploadedBy: f.owner, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.q.CreateMediaVariant(f.ctx, store.CreateMediaVariantParams{MediaID: image.ID, Type: "medium", Width: 800, Height: 533, Size: 1, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		ru, err := f.q.GetLanguageByCode(f.ctx, "ru")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.q.UpsertMediaTranslation(f.ctx, store.UpsertMediaTranslationParams{MediaID: image.ID, LanguageID: ru.ID, Alt: "Перевод изображения", Caption: caption}); err != nil {
			t.Fatal(err)
		}
	}
	value, err := f.resolver(t, grounding.Access{}).Build(f.ctx, grounding.Input{LanguageCode: "ru", MediaSearch: "%_"})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Offered.Images.Items) != 1 {
		t.Fatalf("LIKE wildcard escaped incorrectly: %+v", value.Offered.Images)
	}
	image := value.Offered.Images.Items[0]
	if image.Alt != "Перевод изображения" || !image.FeaturedEligible || len(image.Variants) != 1 || image.MarkdownReference == "" {
		t.Fatalf("incomplete image offer %+v", image)
	}
}

func TestResolverOriginNormalizationAndOperationalErrors(t *testing.T) {
	f := seedFixture(t)
	page := f.page(t, "origin", "en", "published", f.owner)
	for _, tc := range []struct{ site, ref string }{{"https://bücher.example", "https://XN--BCHER-KVA.EXAMPLE:443/origin"}, {"https://[2001:db8::1]", "https://[2001:db8::1]:0443/origin"}} {
		r, err := grounding.NewResolver(f.q, tc.site, grounding.Access{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.Resolve(f.ctx, tc.ref)
		if err != nil || got.ID != page.ID {
			t.Fatalf("origin mismatch %+v: %v", tc, err)
		}
	}
	if _, err := f.db.Exec("DROP TABLE page_ai_access"); err != nil {
		t.Fatal(err)
	}
	hidden := f.page(t, "needs-access", "en", "draft", f.owner)
	if _, err := f.resolver(t, grounding.Access{UserID: f.owner, OwnDrafts: true}).Resolve(f.ctx, strconv.FormatInt(hidden.ID, 10)); err == nil || errors.Is(err, grounding.ErrUnavailable) {
		t.Fatalf("database failure hidden as unavailable: %v", err)
	}
}

func TestExampleOverlapIsAWarningOnlyForSubstantialReuse(t *testing.T) {
	words := []string{}
	for i := range 100 {
		words = append(words, fmt.Sprintf("word%d", i))
	}
	body := strings.Join(words, " ")
	refs := []grounding.Reference{{PageID: 1, Revision: 4, Role: "example", Markdown: body}, {PageID: 2, Revision: 2, Role: "source", Markdown: body}}
	hits, issues := grounding.CheckOverlap(body, refs, "ru")
	if len(hits) != 1 || hits[0].PageID != 1 || len(issues) != 1 || issues[0].Severity != draft.Warning {
		t.Fatalf("overlap not flagged: %+v %+v", hits, issues)
	}
	if hits, _ := grounding.CheckOverlap("a short quotation", refs, "en"); len(hits) != 0 {
		t.Fatal("short quotation flagged")
	}
}

func FuzzReferenceResolver(f *testing.F) {
	fixture := seedFixture(f)
	page := fixture.page(f, "fuzz-page", "en", "published", fixture.owner)
	resolver := fixture.resolver(f, grounding.Access{})
	for _, value := range []string{"fuzz-page", "/en/fuzz-page", "https://example.test/fuzz-page", "http://127.0.0.1/private", "javascript:alert(1)", "/api/secret", "//outside.test/x", "/ru/fuzz-page", "%zz"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		got, err := resolver.Resolve(context.Background(), ref)
		if err == nil && (got.ID != page.ID || got.Status != "published") {
			t.Fatalf("resolver exposed unexpected content: %+v", got)
		}
	})
}

func BenchmarkContextBuild(b *testing.B) {
	f := seedFixture(b)
	page := f.page(b, "benchmark-page", "en", "published", f.owner)
	r := f.resolver(b, grounding.Access{})
	input := grounding.Input{Brief: "A local benchmark", References: []draft.Reference{{Ref: strconv.FormatInt(page.ID, 10), Role: "source"}}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := r.Build(context.Background(), input); err != nil {
			b.Fatal(err)
		}
	}
}

func TestOfferedPagingIsDeterministicAndAdvancesPastUnsafeLegacyImages(t *testing.T) {
	f := seedFixture(t)
	now := time.Now()
	for i := range 23 {
		if _, err := f.q.CreateTag(f.ctx, store.CreateTagParams{Name: fmt.Sprintf("Tag %02d", i), Slug: fmt.Sprintf("tag-%02d", i), LanguageCode: "en", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.q.CreateCategory(f.ctx, store.CreateCategoryParams{Name: fmt.Sprintf("Category %02d", i), Slug: fmt.Sprintf("category-%02d", i), LanguageCode: "en", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 12 {
		f.page(t, fmt.Sprintf("link-%02d", i), "en", "published", f.owner)
	}
	for i := range 9 {
		uuid := fmt.Sprintf("invalid-%d", i)
		if i == 0 {
			uuid = "aaaaaaaa-bbbb-4ccc-8ddd-000000000001"
		}
		if _, err := f.q.CreateMedia(f.ctx, store.CreateMediaParams{Uuid: uuid, Filename: "legacy.jpg", MimeType: "image/jpeg", Size: 1, UploadedBy: f.owner, LanguageCode: "en", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	r := f.resolver(t, grounding.Access{})
	first, err := r.Build(f.ctx, grounding.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Offered.Tags.Items) != 20 || first.Offered.Tags.NextOffset == nil || *first.Offered.Tags.NextOffset != 20 || first.Offered.Categories.NextOffset == nil || first.Offered.Links.NextOffset == nil {
		t.Fatalf("missing bounded paging %+v", first.Offered)
	}
	if len(first.Offered.Images.Items) != 0 || first.Offered.Images.NextOffset == nil || *first.Offered.Images.NextOffset != 8 {
		t.Fatalf("unsafe historical rows lost follow-up position %+v", first.Offered.Images)
	}
	again, err := r.Build(f.ctx, grounding.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Offered.Tags.Items[0].ID != again.Offered.Tags.Items[0].ID || first.Offered.Links.Items[0].ID != again.Offered.Links.Items[0].ID {
		t.Fatal("offered ordering changed without a storage change")
	}
	next, err := r.Build(f.ctx, grounding.Input{TaxonomyOffset: 20, LinkOffset: 10, MediaOffset: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Offered.Tags.Items) != 3 || next.Offered.Tags.NextOffset != nil || len(next.Offered.Links.Items) != 2 || len(next.Offered.Images.Items) != 1 {
		t.Fatalf("follow-up page incomplete %+v", next.Offered)
	}
}

func TestGroundingMarkdownRetainsExistingMediaAndLeavesStoredHTMLUntouched(t *testing.T) {
	f := seedFixture(t)
	page := f.page(t, "media-reference", "en", "published", f.owner)
	now := time.Now()
	image, err := f.q.CreateMedia(f.ctx, store.CreateMediaParams{Uuid: "aaaaaaaa-bbbb-4ccc-8ddd-000000000001", Filename: "reference.jpg", MimeType: "image/jpeg", Size: 1, UploadedBy: f.owner, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`<figure><img src="/uploads/originals/%s/%s" data-media-id="%d" alt="Reference image"><figcaption>Reference caption</figcaption></figure>`, image.Uuid, image.Filename, image.ID)
	if _, err = f.db.Exec("UPDATE pages SET body=? WHERE id=?", body, page.ID); err != nil {
		t.Fatal(err)
	}
	value, err := f.resolver(t, grounding.Access{}).Build(f.ctx, grounding.Input{References: []draft.Reference{{Ref: fmt.Sprint(page.ID), Role: "source"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value.References[0].Markdown, fmt.Sprintf("media:%d", image.ID)) || !strings.Contains(value.References[0].Markdown, "Reference caption") {
		t.Fatalf("existing image identity/caption lost: %+v", value.References[0])
	}
	stored, err := f.q.GetPageByID(f.ctx, page.ID)
	if err != nil || stored.Body != body {
		t.Fatal("grounding rewrote authoritative HTML")
	}
}
