-- Page Alias queries

-- name: CreatePageAlias :one
INSERT INTO page_aliases (page_id, alias, created_at)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetAliasesForPage :many
SELECT * FROM page_aliases WHERE page_id = ? ORDER BY created_at;

-- name: ClearPageAliases :exec
DELETE FROM page_aliases WHERE page_id = ?;

-- name: GetPublishedPageByAlias :one
SELECT p.* FROM pages p
INNER JOIN page_aliases pa ON pa.page_id = p.id
WHERE pa.alias = ? AND p.status = 'published';

-- name: AliasExists :one
SELECT EXISTS(SELECT 1 FROM page_aliases WHERE alias = ?);

-- name: AliasExistsExcludingPage :one
SELECT EXISTS(SELECT 1 FROM page_aliases WHERE alias = ? AND page_id != ?);
