-- +goose Up
CREATE TABLE webhook_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event TEXT NOT NULL,
    payload TEXT NOT NULL,
    response_code INTEGER,
    response_body TEXT DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_retry_at DATETIME,
    delivered_at DATETIME,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id);
CREATE INDEX idx_webhook_deliveries_status ON webhook_deliveries(status);
CREATE INDEX idx_webhook_deliveries_retry ON webhook_deliveries(next_retry_at)
    WHERE status = 'pending' AND next_retry_at IS NOT NULL;
CREATE INDEX idx_webhook_deliveries_created ON webhook_deliveries(created_at);

-- +goose Down
DROP INDEX idx_webhook_deliveries_created;
DROP INDEX idx_webhook_deliveries_retry;
DROP INDEX idx_webhook_deliveries_status;
DROP INDEX idx_webhook_deliveries_webhook;
DROP TABLE webhook_deliveries;
