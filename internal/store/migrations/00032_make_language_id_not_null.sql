-- +goose Up
-- +goose StatementBegin

-- Make language_id NOT NULL on pages, tags, categories, and menus.
-- SQLite does not support ALTER COLUMN, so each table must be recreated.

-- First, fill any NULL language_id with the default language
UPDATE pages SET language_id = (SELECT id FROM languages WHERE is_default = 1) WHERE language_id IS NULL;
UPDATE tags SET language_id = (SELECT id FROM languages WHERE is_default = 1) WHERE language_id IS NULL;
UPDATE categories SET language_id = (SELECT id FROM languages WHERE is_default = 1) WHERE language_id IS NULL;
UPDATE menus SET language_id = (SELECT id FROM languages WHERE is_default = 1) WHERE language_id IS NULL;

-- ========================================
-- PAGES: Recreate with language_id NOT NULL
-- ========================================

-- Drop FTS triggers that reference pages table
DROP TRIGGER IF EXISTS pages_fts_ai;
DROP TRIGGER IF EXISTS pages_fts_bd;
DROP TRIGGER IF EXISTS pages_fts_au;

-- Create new pages table with NOT NULL language_id
CREATE TABLE pages_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    author_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at DATETIME,
    featured_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL,
    meta_title TEXT NOT NULL DEFAULT '',
    meta_description TEXT NOT NULL DEFAULT '',
    meta_keywords TEXT NOT NULL DEFAULT '',
    og_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL,
    no_index INTEGER NOT NULL DEFAULT 0,
    no_follow INTEGER NOT NULL DEFAULT 0,
    canonical_url TEXT NOT NULL DEFAULT '',
    scheduled_at DATETIME,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO pages_new (id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_id)
SELECT id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_id
FROM pages;

DROP TABLE pages;
ALTER TABLE pages_new RENAME TO pages;

-- Recreate all pages indexes
CREATE INDEX idx_pages_slug ON pages(slug);
CREATE INDEX idx_pages_status ON pages(status);
CREATE INDEX idx_pages_author_id ON pages(author_id);
CREATE INDEX idx_pages_created_at ON pages(created_at);
CREATE INDEX idx_pages_featured_image ON pages(featured_image_id);
CREATE INDEX idx_pages_scheduled ON pages(scheduled_at) WHERE scheduled_at IS NOT NULL AND status = 'draft';
CREATE INDEX idx_pages_language_id ON pages(language_id);
CREATE INDEX idx_pages_scheduled_status ON pages(scheduled_at, status) WHERE scheduled_at IS NOT NULL;
CREATE INDEX idx_pages_published_at ON pages(published_at) WHERE status = 'published';
CREATE INDEX idx_pages_language_status ON pages(language_id, status);
CREATE INDEX idx_pages_updated_at ON pages(updated_at);

-- Recreate FTS triggers
CREATE TRIGGER pages_fts_ai AFTER INSERT ON pages
WHEN NEW.status = 'published'
BEGIN
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    VALUES(NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords);
END;

CREATE TRIGGER pages_fts_bd BEFORE DELETE ON pages BEGIN
    DELETE FROM pages_fts WHERE rowid = OLD.id;
END;

CREATE TRIGGER pages_fts_au AFTER UPDATE ON pages BEGIN
    DELETE FROM pages_fts WHERE rowid = OLD.id;
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    SELECT NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords
    WHERE NEW.status = 'published';
END;

-- ========================================
-- TAGS: Recreate with language_id NOT NULL
-- ========================================

CREATE TABLE tags_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER NOT NULL REFERENCES languages(id)
);

INSERT INTO tags_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM tags;

DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;

CREATE INDEX idx_tags_slug ON tags(slug);
CREATE INDEX idx_tags_language_id ON tags(language_id);

-- ========================================
-- CATEGORIES: Recreate with language_id NOT NULL
-- ========================================

CREATE TABLE categories_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    parent_id INTEGER REFERENCES categories_new(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER NOT NULL REFERENCES languages(id)
);

INSERT INTO categories_new (id, name, slug, description, parent_id, position, created_at, updated_at, language_id)
SELECT id, name, slug, description, parent_id, position, created_at, updated_at, language_id FROM categories;

DROP TABLE categories;
ALTER TABLE categories_new RENAME TO categories;

CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE INDEX idx_categories_language_id ON categories(language_id);

-- ========================================
-- MENUS: Recreate with language_id NOT NULL
-- ========================================

CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER NOT NULL REFERENCES languages(id)
);

INSERT INTO menus_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM menus;

DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;

CREATE INDEX idx_menus_language_id ON menus(language_id);
CREATE UNIQUE INDEX idx_menus_slug_language ON menus(slug, language_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Revert pages to nullable language_id
DROP TRIGGER IF EXISTS pages_fts_ai;
DROP TRIGGER IF EXISTS pages_fts_bd;
DROP TRIGGER IF EXISTS pages_fts_au;

CREATE TABLE pages_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    author_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at DATETIME,
    featured_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL,
    meta_title TEXT NOT NULL DEFAULT '',
    meta_description TEXT NOT NULL DEFAULT '',
    meta_keywords TEXT NOT NULL DEFAULT '',
    og_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL,
    no_index INTEGER NOT NULL DEFAULT 0,
    no_follow INTEGER NOT NULL DEFAULT 0,
    canonical_url TEXT NOT NULL DEFAULT '',
    scheduled_at DATETIME,
    language_id INTEGER REFERENCES languages(id),
    FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO pages_new (id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_id)
SELECT id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_id
FROM pages;

DROP TABLE pages;
ALTER TABLE pages_new RENAME TO pages;

CREATE INDEX idx_pages_slug ON pages(slug);
CREATE INDEX idx_pages_status ON pages(status);
CREATE INDEX idx_pages_author_id ON pages(author_id);
CREATE INDEX idx_pages_created_at ON pages(created_at);
CREATE INDEX idx_pages_featured_image ON pages(featured_image_id);
CREATE INDEX idx_pages_scheduled ON pages(scheduled_at) WHERE scheduled_at IS NOT NULL AND status = 'draft';
CREATE INDEX idx_pages_language_id ON pages(language_id);
CREATE INDEX idx_pages_scheduled_status ON pages(scheduled_at, status) WHERE scheduled_at IS NOT NULL;
CREATE INDEX idx_pages_published_at ON pages(published_at) WHERE status = 'published';
CREATE INDEX idx_pages_language_status ON pages(language_id, status);
CREATE INDEX idx_pages_updated_at ON pages(updated_at);

CREATE TRIGGER pages_fts_ai AFTER INSERT ON pages
WHEN NEW.status = 'published'
BEGIN
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    VALUES(NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords);
END;

CREATE TRIGGER pages_fts_bd BEFORE DELETE ON pages BEGIN
    DELETE FROM pages_fts WHERE rowid = OLD.id;
END;

CREATE TRIGGER pages_fts_au AFTER UPDATE ON pages BEGIN
    DELETE FROM pages_fts WHERE rowid = OLD.id;
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    SELECT NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords
    WHERE NEW.status = 'published';
END;

-- Revert tags to nullable language_id
CREATE TABLE tags_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER REFERENCES languages(id)
);

INSERT INTO tags_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM tags;

DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;

CREATE INDEX idx_tags_slug ON tags(slug);
CREATE INDEX idx_tags_language_id ON tags(language_id);

-- Revert categories to nullable language_id
CREATE TABLE categories_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    parent_id INTEGER REFERENCES categories_new(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER REFERENCES languages(id)
);

INSERT INTO categories_new (id, name, slug, description, parent_id, position, created_at, updated_at, language_id)
SELECT id, name, slug, description, parent_id, position, created_at, updated_at, language_id FROM categories;

DROP TABLE categories;
ALTER TABLE categories_new RENAME TO categories;

CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE INDEX idx_categories_language_id ON categories(language_id);

-- Revert menus to nullable language_id
CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER REFERENCES languages(id)
);

INSERT INTO menus_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM menus;

DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;

CREATE INDEX idx_menus_language_id ON menus(language_id);
CREATE UNIQUE INDEX idx_menus_slug_language ON menus(slug, language_id);

-- +goose StatementEnd
