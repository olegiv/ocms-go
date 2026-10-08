-- name: ListFeedPosts :many
-- Apply all visibility and taxonomy filters before the fixed feed limit.
WITH eligible_posts AS (
    SELECT p.id, p.title, p.slug, p.body, p.summary, p.published_at,
           p.created_at, p.updated_at, u.name AS author_name,
           COALESCE(p.published_at, p.created_at) AS feed_date
    FROM pages p
    INNER JOIN users u ON u.id = p.author_id
    WHERE p.status = 'published' AND p.page_type = 'post'
      AND p.exclude_from_lists = 0 AND p.language_code = sqlc.arg(language_code)
      AND p.slug <> '' AND p.slug NOT GLOB '*[^a-z0-9-]*'
      AND p.slug NOT GLOB '-*' AND p.slug NOT GLOB '*-' AND p.slug NOT GLOB '*--*'
      AND instr(p.slug, char(0)) = 0
      AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR EXISTS (
          SELECT 1 FROM page_categories pc
          WHERE pc.page_id = p.id AND pc.category_id = sqlc.arg(category_id)
      ))
      AND (CAST(sqlc.arg(tag_id) AS INTEGER) = 0 OR EXISTS (
          SELECT 1 FROM page_tags pt
          WHERE pt.page_id = p.id AND pt.tag_id = sqlc.arg(tag_id)
      ))
), feed_offsets AS (
    SELECT *, instr(feed_date, ' +') + instr(feed_date, ' -') AS offset_start
    FROM eligible_posts
), feed_dates AS (
    -- Convert modernc's Go timestamp strings to SQLite's offset notation.
    SELECT *, CASE WHEN offset_start > 0 THEN
        substr(feed_date, 1, offset_start - 1) ||
        substr(feed_date, offset_start + 1, 3) || ':' || substr(feed_date, offset_start + 4, 2)
        ELSE feed_date END AS normalized_date
    FROM feed_offsets
)
SELECT id, title, slug, body, summary, published_at, created_at, updated_at, author_name
FROM feed_dates
-- Compare seconds and fractions separately; SQLite dates round to milliseconds.
ORDER BY unixepoch(CASE WHEN substr(normalized_date, 20, 1) = '.' THEN
    substr(normalized_date, 1, 19) || CASE WHEN substr(normalized_date, -6, 1) IN ('+', '-')
        THEN substr(normalized_date, -6) ELSE '' END
    ELSE normalized_date END) DESC,
    CASE WHEN substr(normalized_date, 20, 1) = '.'
        THEN CAST('0' || substr(normalized_date, 20) AS REAL) ELSE 0 END DESC,
    id DESC
LIMIT 20;
