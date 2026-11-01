-- name: ListGroundingPages :many
SELECT p.* FROM pages p JOIN languages l ON l.code=p.language_code AND l.is_active=1
WHERE (sqlc.arg(language_code) = '' OR p.language_code = sqlc.arg(language_code))
AND (p.status='published' OR sqlc.arg(all_drafts) OR
 (sqlc.arg(own_drafts) AND p.status='draft' AND p.scheduled_at IS NULL AND p.author_id=sqlc.arg(user_id)
 AND EXISTS (SELECT 1 FROM page_ai_access a WHERE a.page_id=p.id AND a.ai_editable=1)))
ORDER BY p.created_at DESC, p.id DESC LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: ListGroundingImages :many
SELECT m.* FROM media m WHERE m.mime_type IN ('image/jpeg','image/png','image/gif','image/webp')
AND (sqlc.arg(search) = '' OR (m.filename LIKE sqlc.arg(search_pattern) ESCAPE '!') OR (m.alt LIKE sqlc.arg(search_pattern) ESCAPE '!') OR (m.caption LIKE sqlc.arg(search_pattern) ESCAPE '!')
 OR EXISTS (SELECT 1 FROM media_translations mt WHERE mt.media_id=m.id AND ((mt.alt LIKE sqlc.arg(search_pattern) ESCAPE '!') OR (mt.caption LIKE sqlc.arg(search_pattern) ESCAPE '!'))))
ORDER BY m.created_at DESC, m.id DESC LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: ListGroundingTags :many
SELECT * FROM tags WHERE language_code = sqlc.arg(language_code) ORDER BY name,id LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: ListGroundingCategories :many
SELECT * FROM categories WHERE language_code = sqlc.arg(language_code) ORDER BY position,name,id LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: ListPagesInLanguage :many
SELECT p.* FROM pages p WHERE p.language_code=sqlc.arg(language_code)
AND (sqlc.arg(status)='' OR p.status=sqlc.arg(status))
AND (sqlc.arg(category_id)=0 OR EXISTS(SELECT 1 FROM page_categories pc WHERE pc.page_id=p.id AND pc.category_id=sqlc.arg(category_id)))
AND (sqlc.arg(tag_id)=0 OR EXISTS(SELECT 1 FROM page_tags pt WHERE pt.page_id=p.id AND pt.tag_id=sqlc.arg(tag_id)))
ORDER BY p.created_at DESC,p.id DESC LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: CountPagesInLanguage :one
SELECT COUNT(*) FROM pages p WHERE p.language_code=sqlc.arg(language_code)
AND (sqlc.arg(status)='' OR p.status=sqlc.arg(status))
AND (sqlc.arg(category_id)=0 OR EXISTS(SELECT 1 FROM page_categories pc WHERE pc.page_id=p.id AND pc.category_id=sqlc.arg(category_id)))
AND (sqlc.arg(tag_id)=0 OR EXISTS(SELECT 1 FROM page_tags pt WHERE pt.page_id=p.id AND pt.tag_id=sqlc.arg(tag_id)));

-- name: SearchAdminPagesByLanguage :many
SELECT * FROM pages WHERE language_code=sqlc.arg(language_code) AND ((title LIKE sqlc.arg(search_pattern) ESCAPE '!') OR (body LIKE sqlc.arg(search_pattern) ESCAPE '!')) ORDER BY updated_at DESC,id DESC LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: CountAdminSearchPagesByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_code=sqlc.arg(language_code) AND ((title LIKE sqlc.arg(search_pattern) ESCAPE '!') OR (body LIKE sqlc.arg(search_pattern) ESCAPE '!'));

-- name: ListGroundingLanguages :many
SELECT * FROM languages WHERE is_active=1 ORDER BY position,name,id LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
