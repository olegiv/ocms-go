-- +goose Up
CREATE TABLE pages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    author_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at DATETIME,
    FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX idx_pages_slug ON pages(slug);
CREATE INDEX idx_pages_status ON pages(status);
CREATE INDEX idx_pages_author_id ON pages(author_id);
CREATE INDEX idx_pages_created_at ON pages(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_pages_created_at;
DROP INDEX IF EXISTS idx_pages_author_id;
DROP INDEX IF EXISTS idx_pages_status;
DROP INDEX IF EXISTS idx_pages_slug;
DROP TABLE IF EXISTS pages;
