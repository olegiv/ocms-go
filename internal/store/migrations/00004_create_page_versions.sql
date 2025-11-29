-- +goose Up
CREATE TABLE page_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    changed_by INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
    FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX idx_page_versions_page_id ON page_versions(page_id);
CREATE INDEX idx_page_versions_changed_by ON page_versions(changed_by);
CREATE INDEX idx_page_versions_created_at ON page_versions(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_page_versions_created_at;
DROP INDEX IF EXISTS idx_page_versions_changed_by;
DROP INDEX IF EXISTS idx_page_versions_page_id;
DROP TABLE IF EXISTS page_versions;
