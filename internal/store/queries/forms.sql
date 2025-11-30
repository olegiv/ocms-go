-- name: CreateForm :one
INSERT INTO forms (name, slug, title, description, success_message, email_to, is_active, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormByID :one
SELECT * FROM forms WHERE id = ?;

-- name: GetFormBySlug :one
SELECT * FROM forms WHERE slug = ?;

-- name: ListForms :many
SELECT * FROM forms ORDER BY name ASC LIMIT ? OFFSET ?;

-- name: UpdateForm :one
UPDATE forms SET name = ?, slug = ?, title = ?, description = ?, success_message = ?, email_to = ?, is_active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteForm :exec
DELETE FROM forms WHERE id = ?;

-- name: CountForms :one
SELECT COUNT(*) FROM forms;

-- name: CreateFormField :one
INSERT INTO form_fields (form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormFieldByID :one
SELECT * FROM form_fields WHERE id = ?;

-- name: GetFormFields :many
SELECT * FROM form_fields WHERE form_id = ? ORDER BY position ASC;

-- name: UpdateFormField :one
UPDATE form_fields SET type = ?, name = ?, label = ?, placeholder = ?, help_text = ?, options = ?, validation = ?, is_required = ?, position = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteFormField :exec
DELETE FROM form_fields WHERE id = ?;

-- name: DeleteFormFields :exec
DELETE FROM form_fields WHERE form_id = ?;

-- name: CreateFormSubmission :one
INSERT INTO form_submissions (form_id, data, ip_address, user_agent, is_read, created_at)
VALUES (?, ?, ?, ?, ?, ?)
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
    fs.id, fs.form_id, fs.data, fs.ip_address, fs.user_agent, fs.is_read, fs.created_at,
    f.name as form_name, f.slug as form_slug
FROM form_submissions fs
JOIN forms f ON f.id = fs.form_id
ORDER BY fs.created_at DESC
LIMIT ?;
