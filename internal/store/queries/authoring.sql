-- name: ListOwnAIDrafts :many
SELECT p.* FROM pages p JOIN page_ai_access a ON a.page_id=p.id AND a.ai_editable=1
WHERE p.author_id=sqlc.arg(user_id) AND p.status='draft' AND p.scheduled_at IS NULL
AND (sqlc.arg(language_code)='' OR p.language_code=sqlc.arg(language_code))
ORDER BY p.updated_at DESC,p.id DESC LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);

-- name: CountOwnAIDrafts :one
SELECT COUNT(*) FROM pages p JOIN page_ai_access a ON a.page_id=p.id AND a.ai_editable=1
WHERE p.author_id=sqlc.arg(user_id) AND p.status='draft' AND p.scheduled_at IS NULL
AND (sqlc.arg(language_code)='' OR p.language_code=sqlc.arg(language_code));

-- name: CountRecentDraftMutations :one
SELECT COUNT(*) FROM idempotency_keys WHERE user_id=sqlc.arg(user_id)
AND tool IN ('create_draft_page','update_draft_page','create_translation_draft')
AND created_at > sqlc.arg(since);

-- name: EarliestRecentDraftMutation :one
SELECT created_at FROM idempotency_keys WHERE user_id=sqlc.arg(user_id)
AND tool IN ('create_draft_page','update_draft_page','create_translation_draft')
AND created_at > sqlc.arg(since) ORDER BY created_at LIMIT 1;

-- name: GetLatestTranslationProvenance :one
SELECT * FROM content_provenance WHERE page_id=sqlc.arg(page_id)
AND revision<=sqlc.arg(revision)
AND json_extract(validated_json,'$.translation_basis.source_page_id') IS NOT NULL
ORDER BY revision DESC LIMIT 1;
