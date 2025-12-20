-- +goose Up
ALTER TABLE modules ADD COLUMN show_in_sidebar BOOLEAN NOT NULL DEFAULT 0;

CREATE INDEX idx_modules_show_in_sidebar ON modules(show_in_sidebar);

-- +goose Down
DROP INDEX idx_modules_show_in_sidebar;
ALTER TABLE modules DROP COLUMN show_in_sidebar;
