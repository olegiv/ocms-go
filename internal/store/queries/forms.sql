-- name: CreateForm :one
INSERT INTO forms (name, slug, title, description, success_message, email_to, is_active, language_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormByID :one
SELECT * FROM forms WHERE id = ?;

-- name: GetFormBySlug :one
SELECT * FROM forms WHERE slug = ?;

-- name: GetFormBySlugAndLanguage :one
SELECT * FROM forms WHERE slug = ? AND language_code = ?;

-- name: ListForms :many
SELECT * FROM forms ORDER BY name LIMIT ? OFFSET ?;

-- name: ListFormsByLanguage :many
SELECT * FROM forms WHERE language_code = ? ORDER BY name LIMIT ? OFFSET ?;

-- name: UpdateForm :one
UPDATE forms SET name = ?, slug = ?, title = ?, description = ?, success_message = ?, email_to = ?, is_active = ?, language_code = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteForm :exec
DELETE FROM forms WHERE id = ?;

-- name: CountForms :one
SELECT COUNT(*) FROM forms;

-- name: CountFormsByLanguage :one
SELECT COUNT(*) FROM forms WHERE language_code = ?;

-- name: FormSlugExistsForLanguage :one
SELECT EXISTS(SELECT 1 FROM forms WHERE slug = ? AND language_code = ?);

-- name: FormSlugExistsExcludingForLanguage :one
SELECT EXISTS(SELECT 1 FROM forms WHERE slug = ? AND language_code = ? AND id != ?);

-- name: CreateFormField :one
INSERT INTO form_fields (form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, language_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormFieldByID :one
SELECT * FROM form_fields WHERE id = ?;

-- name: GetFormFields :many
SELECT * FROM form_fields WHERE form_id = ? ORDER BY position;

-- name: UpdateFormField :one
UPDATE form_fields SET type = ?, name = ?, label = ?, placeholder = ?, help_text = ?, options = ?, validation = ?, is_required = ?, position = ?, language_code = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteFormField :exec
DELETE FROM form_fields WHERE id = ?;

-- name: DeleteFormFields :exec
DELETE FROM form_fields WHERE form_id = ?;

-- name: CreateFormSubmission :one
INSERT INTO form_submissions (form_id, data, ip_address, user_agent, is_read, language_code, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormSubmissionByID :one
SELECT * FROM form_submissions WHERE id = ?;

-- name: GetFormSubmissions :many
SELECT * FROM form_submissions WHERE form_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CountFormSubmissions :one
SELECT COUNT(*) FROM form_submissions WHERE form_id = ?;

-- name: CountUnreadSubmissions :one
SELECT COUNT(*) FROM form_submissions WHERE form_id = ? AND is_read = 0;

-- name: CountAllUnreadSubmissions :one
SELECT COUNT(*) FROM form_submissions WHERE is_read = 0;

-- name: MarkSubmissionRead :exec
UPDATE form_submissions SET is_read = 1 WHERE id = ?;

-- name: DeleteFormSubmission :exec
DELETE FROM form_submissions WHERE id = ?;

-- name: GetRecentSubmissionsWithForm :many
SELECT
    fs.id, fs.form_id, fs.data, fs.ip_address, fs.user_agent, fs.is_read, fs.language_code, fs.created_at,
    f.name as form_name, f.slug as form_slug
FROM form_submissions fs
JOIN forms f ON f.id = fs.form_id
ORDER BY fs.created_at DESC
LIMIT ?;

-- name: FormSlugExists :one
SELECT EXISTS(SELECT 1 FROM forms WHERE slug = ?);

-- Get all available translations for a form (for language switcher)
-- Note: translations table still uses language_id to reference the target language
-- name: GetFormAvailableTranslations :many
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction,
    l.is_default as is_default,
    COALESCE(f.id, 0) as form_id,
    COALESCE(f.slug, '') as form_slug,
    COALESCE(f.name, '') as form_name
FROM languages l
LEFT JOIN (
    -- Get forms that are translations of the source form
    SELECT f.id, f.slug, f.name, f.language_code
    FROM forms f
    INNER JOIN translations t ON t.translation_id = f.id
    WHERE t.entity_type = 'form' AND t.entity_id = ?
    UNION
    -- Get the source form itself
    SELECT f.id, f.slug, f.name, f.language_code
    FROM forms f
    WHERE f.id = ?
    UNION
    -- Get forms where current form is a translation (sibling translations)
    SELECT f2.id, f2.slug, f2.name, f2.language_code
    FROM translations t
    INNER JOIN forms f2 ON (f2.id = t.entity_id OR f2.id = t.translation_id)
    WHERE t.entity_type = 'form'
    AND (t.entity_id = ? OR t.translation_id = ?)
) f ON f.language_code = l.code
WHERE l.is_active = 1
ORDER BY l.position;
