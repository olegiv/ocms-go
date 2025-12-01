-- name: CreateWebhook :one
INSERT INTO webhooks (name, url, secret, events, is_active, headers, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetWebhookByID :one
SELECT * FROM webhooks WHERE id = ?;

-- name: ListWebhooks :many
SELECT * FROM webhooks ORDER BY name ASC;

-- name: ListWebhooksPaginated :many
SELECT * FROM webhooks ORDER BY name ASC LIMIT ? OFFSET ?;

-- name: ListActiveWebhooks :many
SELECT * FROM webhooks WHERE is_active = 1 ORDER BY name ASC;

-- name: ListWebhooksForEvent :many
SELECT * FROM webhooks
WHERE is_active = 1 AND events LIKE '%' || ? || '%';

-- name: UpdateWebhook :one
UPDATE webhooks SET name = ?, url = ?, secret = ?, events = ?, is_active = ?, headers = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteWebhook :exec
DELETE FROM webhooks WHERE id = ?;

-- name: CountWebhooks :one
SELECT COUNT(*) FROM webhooks;

-- name: CountActiveWebhooks :one
SELECT COUNT(*) FROM webhooks WHERE is_active = 1;

-- Webhook Deliveries

-- name: CreateWebhookDelivery :one
INSERT INTO webhook_deliveries (webhook_id, event, payload, status, created_at, updated_at)
VALUES (?, ?, ?, 'pending', ?, ?)
RETURNING *;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries WHERE id = ?;

-- name: ListWebhookDeliveries :many
SELECT * FROM webhook_deliveries WHERE webhook_id = ?
ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: GetPendingDeliveries :many
SELECT * FROM webhook_deliveries
WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= ?)
ORDER BY created_at ASC LIMIT ?;

-- name: UpdateDeliverySuccess :exec
UPDATE webhook_deliveries
SET status = 'delivered', response_code = ?, response_body = ?, delivered_at = ?, attempts = attempts + 1, updated_at = ?
WHERE id = ?;

-- name: UpdateDeliveryRetry :exec
UPDATE webhook_deliveries
SET status = 'pending', response_code = ?, response_body = ?, error_message = ?, attempts = attempts + 1, next_retry_at = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateDeliveryDead :exec
UPDATE webhook_deliveries
SET status = 'dead', error_message = ?, attempts = attempts + 1, updated_at = ?
WHERE id = ?;

-- name: CountWebhookDeliveries :one
SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?;

-- name: CountDeliveriesByStatus :one
SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND status = ?;

-- name: DeleteOldDeliveries :exec
DELETE FROM webhook_deliveries WHERE created_at < ? AND status IN ('delivered', 'dead');

-- name: GetRecentDeliveries :many
SELECT * FROM webhook_deliveries
ORDER BY created_at DESC LIMIT ?;

-- name: GetRecentFailedDeliveries :many
SELECT * FROM webhook_deliveries
WHERE status IN ('failed', 'dead')
ORDER BY created_at DESC LIMIT ?;

-- name: GetDeliveryStats :one
SELECT
    COUNT(*) as total,
    SUM(CASE WHEN status = 'delivered' THEN 1 ELSE 0 END) as delivered,
    SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) as pending,
    SUM(CASE WHEN status = 'dead' THEN 1 ELSE 0 END) as dead
FROM webhook_deliveries WHERE webhook_id = ?;

-- name: ResetDeliveryForRetry :exec
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_retry_at = NULL, error_message = '', updated_at = ?
WHERE id = ?;

-- name: GetDeliveryStatsLast24h :one
SELECT
    COUNT(*) as total,
    SUM(CASE WHEN status = 'delivered' THEN 1 ELSE 0 END) as delivered,
    SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) as pending,
    SUM(CASE WHEN status = 'dead' THEN 1 ELSE 0 END) as dead
FROM webhook_deliveries
WHERE webhook_id = ? AND created_at >= ?;

-- name: GetLastSuccessfulDelivery :one
SELECT * FROM webhook_deliveries
WHERE webhook_id = ? AND status = 'delivered'
ORDER BY delivered_at DESC LIMIT 1;

-- name: GetWebhookHealthSummary :many
SELECT
    w.id,
    w.name,
    w.is_active,
    COUNT(wd.id) as total_deliveries,
    SUM(CASE WHEN wd.status = 'delivered' THEN 1 ELSE 0 END) as delivered_count,
    SUM(CASE WHEN wd.status = 'pending' THEN 1 ELSE 0 END) as pending_count,
    SUM(CASE WHEN wd.status = 'dead' THEN 1 ELSE 0 END) as dead_count
FROM webhooks w
LEFT JOIN webhook_deliveries wd ON wd.webhook_id = w.id AND wd.created_at >= ?
GROUP BY w.id
ORDER BY w.name ASC;

-- name: GetRecentFailedDeliveriesWithWebhook :many
SELECT
    wd.*,
    w.name as webhook_name
FROM webhook_deliveries wd
INNER JOIN webhooks w ON w.id = wd.webhook_id
WHERE wd.status IN ('dead', 'failed')
ORDER BY wd.created_at DESC LIMIT ?;
