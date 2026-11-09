-- name: CreateEvent :one
INSERT INTO events (level, category, message, user_id, metadata, ip_address, request_url, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetEvent :one
SELECT * FROM events WHERE id = ?;

-- name: ListEvents :many
SELECT * FROM events
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsByLevel :many
SELECT * FROM events
WHERE level = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsByCategory :many
SELECT * FROM events
WHERE category = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsByLevelAndCategory :many
SELECT * FROM events
WHERE level = ? AND category = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsWithUser :many
SELECT e.id, e.level, e.category, e.message, e.user_id, e.metadata, e.ip_address, e.request_url, e.created_at,
       u.name as user_name, u.email as user_email
FROM events e
LEFT JOIN users u ON e.user_id = u.id
ORDER BY e.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsWithUserByLevel :many
SELECT e.id, e.level, e.category, e.message, e.user_id, e.metadata, e.ip_address, e.request_url, e.created_at,
       u.name as user_name, u.email as user_email
FROM events e
LEFT JOIN users u ON e.user_id = u.id
WHERE e.level = ?
ORDER BY e.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsWithUserByCategory :many
SELECT e.id, e.level, e.category, e.message, e.user_id, e.metadata, e.ip_address, e.request_url, e.created_at,
       u.name as user_name, u.email as user_email
FROM events e
LEFT JOIN users u ON e.user_id = u.id
WHERE e.category = ?
ORDER BY e.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventsWithUserByLevelAndCategory :many
SELECT e.id, e.level, e.category, e.message, e.user_id, e.metadata, e.ip_address, e.request_url, e.created_at,
       u.name as user_name, u.email as user_email
FROM events e
LEFT JOIN users u ON e.user_id = u.id
WHERE e.level = ? AND e.category = ?
ORDER BY e.created_at DESC
LIMIT ? OFFSET ?;

-- name: CountEvents :one
SELECT COUNT(*) FROM events;

-- name: CountEventsByLevel :one
SELECT COUNT(*) FROM events WHERE level = ?;

-- name: CountEventsByCategory :one
SELECT COUNT(*) FROM events WHERE category = ?;

-- name: CountEventsByLevelAndCategory :one
SELECT COUNT(*) FROM events WHERE level = ? AND category = ?;

-- name: DeleteExpiredEvents :execrows
DELETE FROM events
WHERE id IN (
    SELECT expired.id FROM events AS expired
    CROSS JOIN (SELECT CAST(sqlc.arg(messages) AS TEXT) AS messages) AS producer_filter
    WHERE expired.category = sqlc.arg(category) AND expired.created_at < CAST(sqlc.arg(cutoff) AS TEXT)
      AND (producer_filter.messages = 'null'
           OR expired.message IN (SELECT value FROM json_each(producer_filter.messages)))
    ORDER BY expired.created_at, expired.id
    LIMIT sqlc.arg(batch_limit)
);

-- name: CountExpiredEvents :one
SELECT COUNT(*) FROM (
    SELECT expired.id FROM events AS expired
    CROSS JOIN (SELECT CAST(sqlc.arg(messages) AS TEXT) AS messages) AS producer_filter
    WHERE expired.category = sqlc.arg(category) AND expired.created_at < CAST(sqlc.arg(cutoff) AS TEXT)
      AND (producer_filter.messages = 'null'
           OR expired.message IN (SELECT value FROM json_each(producer_filter.messages)))
    LIMIT sqlc.arg(count_limit)
);
