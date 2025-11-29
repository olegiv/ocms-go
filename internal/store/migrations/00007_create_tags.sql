-- +goose Up
CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE page_tags (
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (page_id, tag_id)
);

CREATE INDEX idx_tags_slug ON tags(slug);
CREATE INDEX idx_page_tags_page ON page_tags(page_id);
CREATE INDEX idx_page_tags_tag ON page_tags(tag_id);

-- +goose Down
DROP INDEX IF EXISTS idx_page_tags_tag;
DROP INDEX IF EXISTS idx_page_tags_page;
DROP INDEX IF EXISTS idx_tags_slug;
DROP TABLE IF EXISTS page_tags;
DROP TABLE IF EXISTS tags;
