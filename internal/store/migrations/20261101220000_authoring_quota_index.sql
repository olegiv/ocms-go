-- +goose Up
CREATE INDEX idx_idempotency_user_created ON idempotency_keys(user_id,created_at);

-- +goose Down
DROP INDEX idx_idempotency_user_created;
