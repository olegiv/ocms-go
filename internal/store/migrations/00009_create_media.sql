-- +goose Up
CREATE TABLE media_folders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    parent_id INTEGER REFERENCES media_folders(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE media (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    width INTEGER,
    height INTEGER,
    alt TEXT DEFAULT '',
    caption TEXT DEFAULT '',
    folder_id INTEGER REFERENCES media_folders(id) ON DELETE SET NULL,
    uploaded_by INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE media_variants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    size INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(media_id, type)
);

CREATE INDEX idx_media_uuid ON media(uuid);
CREATE INDEX idx_media_folder ON media(folder_id);
CREATE INDEX idx_media_mime ON media(mime_type);
CREATE INDEX idx_media_variants_media ON media_variants(media_id);
CREATE INDEX idx_media_folders_parent ON media_folders(parent_id);

-- +goose Down
DROP TABLE media_variants;
DROP TABLE media;
DROP TABLE media_folders;
