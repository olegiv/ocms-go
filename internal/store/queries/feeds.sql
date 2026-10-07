-- name: ListFeedPosts :many
-- Apply all visibility and taxonomy filters before the fixed feed limit.
SELECT p.id, p.title, p.slug, p.body, p.summary, p.published_at,
       p.created_at, p.updated_at, u.name AS author_name
FROM pages p
INNER JOIN users u ON u.id = p.author_id
WHERE p.status = 'published' AND p.page_type = 'post'
  AND p.exclude_from_lists = 0 AND p.language_code = sqlc.arg(language_code)
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR EXISTS (
      SELECT 1 FROM page_categories pc
      WHERE pc.page_id = p.id AND pc.category_id = sqlc.arg(category_id)
  ))
  AND (CAST(sqlc.arg(tag_id) AS INTEGER) = 0 OR EXISTS (
      SELECT 1 FROM page_tags pt
      WHERE pt.page_id = p.id AND pt.tag_id = sqlc.arg(tag_id)
  ))
ORDER BY p.published_at DESC, p.id DESC
LIMIT 20;
