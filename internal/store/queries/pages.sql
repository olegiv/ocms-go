-- name: CreatePage :one
INSERT INTO pages (title, slug, body, status, author_id, featured_image_id, meta_title, meta_description, meta_keywords, og_image_id, no_index, no_follow, canonical_url, scheduled_at, language_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPageByID :one
SELECT * FROM pages WHERE id = ?;

-- name: GetPageBySlug :one
SELECT * FROM pages WHERE slug = ?;

-- name: ListPages :many
SELECT * FROM pages ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListPagesByStatus :many
SELECT * FROM pages WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: UpdatePage :one
UPDATE pages
SET title = ?, slug = ?, body = ?, status = ?, featured_image_id = ?, meta_title = ?, meta_description = ?, meta_keywords = ?, og_image_id = ?, no_index = ?, no_follow = ?, canonical_url = ?, scheduled_at = ?, language_id = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: PublishPage :one
UPDATE pages
SET status = 'published', published_at = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: UnpublishPage :one
UPDATE pages
SET status = 'draft', published_at = NULL, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeletePage :exec
DELETE FROM pages WHERE id = ?;

-- name: CountPages :one
SELECT COUNT(*) FROM pages;

-- name: CountPagesByStatus :one
SELECT COUNT(*) FROM pages WHERE status = ?;

-- name: SlugExists :one
SELECT EXISTS(SELECT 1 FROM pages WHERE slug = ?);

-- name: SlugExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM pages WHERE slug = ? AND id != ?);

-- Page Version queries

-- name: CreatePageVersion :one
INSERT INTO page_versions (page_id, title, body, changed_by, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPageVersion :one
SELECT * FROM page_versions WHERE id = ?;

-- name: ListPageVersions :many
SELECT * FROM page_versions WHERE page_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CountPageVersions :one
SELECT COUNT(*) FROM page_versions WHERE page_id = ?;

-- name: GetLatestPageVersion :one
SELECT * FROM page_versions WHERE page_id = ? ORDER BY created_at DESC LIMIT 1;

-- name: DeletePageVersions :exec
DELETE FROM page_versions WHERE page_id = ?;

-- name: ListPageVersionsWithUser :many
SELECT
    pv.id,
    pv.page_id,
    pv.title,
    pv.body,
    pv.changed_by,
    pv.created_at,
    u.name as changed_by_name,
    u.email as changed_by_email
FROM page_versions pv
JOIN users u ON pv.changed_by = u.id
WHERE pv.page_id = ?
ORDER BY pv.created_at DESC
LIMIT ? OFFSET ?;

-- name: GetPageVersionWithUser :one
SELECT
    pv.id,
    pv.page_id,
    pv.title,
    pv.body,
    pv.changed_by,
    pv.created_at,
    u.name as changed_by_name,
    u.email as changed_by_email
FROM page_versions pv
JOIN users u ON pv.changed_by = u.id
WHERE pv.id = ?;

-- Featured image queries

-- name: GetFeaturedImageForPage :one
SELECT m.* FROM media m
INNER JOIN pages p ON p.featured_image_id = m.id
WHERE p.id = ?;

-- name: UpdatePageFeaturedImage :exec
UPDATE pages SET featured_image_id = ?, updated_at = ? WHERE id = ?;

-- name: ClearPageFeaturedImage :exec
UPDATE pages SET featured_image_id = NULL, updated_at = ? WHERE id = ?;

-- Frontend queries for public pages

-- name: ListPublishedPages :many
SELECT * FROM pages WHERE status = 'published' ORDER BY published_at DESC LIMIT ? OFFSET ?;

-- name: CountPublishedPages :one
SELECT COUNT(*) FROM pages WHERE status = 'published';

-- name: GetPublishedPageBySlug :one
SELECT * FROM pages WHERE slug = ? AND status = 'published';

-- name: ListPublishedPagesByCategory :many
SELECT DISTINCT p.* FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesByCategory :one
SELECT COUNT(DISTINCT p.id) FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published';

-- name: ListPublishedPagesForTag :many
SELECT p.* FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesForTag :one
SELECT COUNT(*) FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published';

-- name: GetPageAuthor :one
SELECT u.id, u.name, u.email FROM users u
INNER JOIN pages p ON p.author_id = u.id
WHERE p.id = ?;

-- SEO OG image query

-- name: GetOGImageForPage :one
SELECT m.* FROM media m
INNER JOIN pages p ON p.og_image_id = m.id
WHERE p.id = ?;

-- name: ListPublishedPagesForSitemap :many
SELECT id, slug, updated_at, no_index FROM pages
WHERE status = 'published' AND no_index = 0
ORDER BY updated_at DESC;

-- Scheduled publishing queries

-- name: GetScheduledPagesForPublishing :many
SELECT * FROM pages
WHERE scheduled_at IS NOT NULL AND scheduled_at <= ? AND status = 'draft'
ORDER BY scheduled_at;

-- name: PublishScheduledPage :one
UPDATE pages
SET status = 'published', published_at = ?, scheduled_at = NULL, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: ClearPageScheduledAt :exec
UPDATE pages SET scheduled_at = NULL, updated_at = ? WHERE id = ?;

-- name: CountScheduledPages :one
SELECT COUNT(*) FROM pages WHERE scheduled_at IS NOT NULL AND status = 'draft';

-- name: ListScheduledPages :many
SELECT * FROM pages WHERE scheduled_at IS NOT NULL AND status = 'draft' ORDER BY scheduled_at LIMIT ? OFFSET ?;

-- Admin search queries (using LIKE for searching all pages regardless of status)

-- name: SearchPages :many
SELECT * FROM pages
WHERE title LIKE ? OR body LIKE ? OR slug LIKE ?
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: CountSearchPages :one
SELECT COUNT(*) FROM pages
WHERE title LIKE ? OR body LIKE ? OR slug LIKE ?;

-- name: SearchPagesByStatus :many
SELECT * FROM pages
WHERE status = ? AND (title LIKE ? OR body LIKE ? OR slug LIKE ?)
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: CountSearchPagesByStatus :one
SELECT COUNT(*) FROM pages
WHERE status = ? AND (title LIKE ? OR body LIKE ? OR slug LIKE ?);

-- Language-related page queries

-- name: ListPagesWithLanguage :many
SELECT
    p.*,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name
FROM pages p
LEFT JOIN languages l ON l.id = p.language_id
ORDER BY p.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListPagesByStatusWithLanguage :many
SELECT
    p.*,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name
FROM pages p
LEFT JOIN languages l ON l.id = p.language_id
WHERE p.status = ?
ORDER BY p.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListPagesByLanguageAndStatus :many
SELECT * FROM pages
WHERE language_id = ? AND status = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountPagesByLanguageAndStatus :one
SELECT COUNT(*) FROM pages WHERE language_id = ? AND status = ?;

-- name: SearchPagesWithLanguage :many
SELECT
    p.*,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name
FROM pages p
LEFT JOIN languages l ON l.id = p.language_id
WHERE p.title LIKE ? OR p.body LIKE ? OR p.slug LIKE ?
ORDER BY p.updated_at DESC
LIMIT ? OFFSET ?;

-- name: SearchPagesByLanguage :many
SELECT * FROM pages
WHERE language_id = ? AND (title LIKE ? OR body LIKE ? OR slug LIKE ?)
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: CountSearchPagesByLanguage :one
SELECT COUNT(*) FROM pages
WHERE language_id = ? AND (title LIKE ? OR body LIKE ? OR slug LIKE ?);

-- name: GetPublishedPageBySlugAndLanguage :one
SELECT * FROM pages
WHERE slug = ? AND language_id = ? AND status = 'published';

-- Frontend queries filtered by language (for showing pages in current language only)
-- Note: ListPublishedPagesByLanguage and CountPublishedPagesByLanguage are in translations.sql

-- name: ListPublishedPagesByCategoryAndLanguage :many
-- Include pages with matching language_id OR NULL language_id (universal pages)
SELECT DISTINCT p.* FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published' AND (p.language_id = ? OR p.language_id IS NULL)
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesByCategoryAndLanguage :one
-- Include pages with matching language_id OR NULL language_id (universal pages)
SELECT COUNT(DISTINCT p.id) FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published' AND (p.language_id = ? OR p.language_id IS NULL);

-- name: ListPublishedPagesForTagAndLanguage :many
-- Include pages with matching language_id OR NULL language_id (universal pages)
SELECT p.* FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published' AND (p.language_id = ? OR p.language_id IS NULL)
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesForTagAndLanguage :one
-- Include pages with matching language_id OR NULL language_id (universal pages)
SELECT COUNT(*) FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published' AND (p.language_id = ? OR p.language_id IS NULL);
