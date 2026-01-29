-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- Migration 00034: Change language_id INT to language_code TEXT
--
-- Converts all content tables from FK-based language_id to direct ISO 2-char
-- language codes (e.g., "en", "ru"). This eliminates JOINs to the languages
-- table and simplifies import/export.
-- ============================================================================

-- ============================================================================
-- PAGES TABLE
-- ============================================================================

-- Drop FTS triggers that reference pages table
DROP TRIGGER IF EXISTS pages_fts_ai;
DROP TRIGGER IF EXISTS pages_fts_bd;
DROP TRIGGER IF EXISTS pages_fts_au;

-- Create new pages table with language_code
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
    language_code TEXT NOT NULL,
    FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO pages_new (id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_code)
SELECT id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at,
    (SELECT code FROM languages WHERE id = pages.language_id)
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
CREATE INDEX idx_pages_language_code ON pages(language_code);
CREATE INDEX idx_pages_scheduled_status ON pages(scheduled_at, status) WHERE scheduled_at IS NOT NULL;
CREATE INDEX idx_pages_published_at ON pages(published_at) WHERE status = 'published';
CREATE INDEX idx_pages_language_status ON pages(language_code, status);
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

-- ============================================================================
-- TAGS TABLE
-- ============================================================================

CREATE TABLE tags_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_code TEXT NOT NULL
);

INSERT INTO tags_new (id, name, slug, created_at, updated_at, language_code)
SELECT id, name, slug, created_at, updated_at,
    (SELECT code FROM languages WHERE id = tags.language_id)
FROM tags;

DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;

CREATE INDEX idx_tags_slug ON tags(slug);
CREATE INDEX idx_tags_language_code ON tags(language_code);

-- ============================================================================
-- CATEGORIES TABLE
-- ============================================================================

CREATE TABLE categories_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    parent_id INTEGER REFERENCES categories_new(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_code TEXT NOT NULL
);

INSERT INTO categories_new (id, name, slug, description, parent_id, position, created_at, updated_at, language_code)
SELECT id, name, slug, description, parent_id, position, created_at, updated_at,
    (SELECT code FROM languages WHERE id = categories.language_id)
FROM categories;

DROP TABLE categories;
ALTER TABLE categories_new RENAME TO categories;

CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE INDEX idx_categories_language_code ON categories(language_code);

-- ============================================================================
-- MENUS TABLE
-- ============================================================================

CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_code TEXT NOT NULL
);

INSERT INTO menus_new (id, name, slug, created_at, updated_at, language_code)
SELECT id, name, slug, created_at, updated_at,
    (SELECT code FROM languages WHERE id = menus.language_id)
FROM menus;

DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;

CREATE INDEX idx_menus_language_code ON menus(language_code);
CREATE UNIQUE INDEX idx_menus_slug_language ON menus(slug, language_code);

-- ============================================================================
-- FORMS TABLE
-- ============================================================================

CREATE TABLE forms_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    success_message TEXT DEFAULT 'Thank you for your submission.',
    email_to TEXT DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT 1,
    language_code TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(slug, language_code)
);

INSERT INTO forms_new (id, name, slug, title, description, success_message, email_to, is_active, language_code, created_at, updated_at)
SELECT id, name, slug, title, description, success_message, email_to, is_active,
    (SELECT code FROM languages WHERE id = forms.language_id),
    created_at, updated_at
FROM forms;

DROP TABLE forms;
ALTER TABLE forms_new RENAME TO forms;

CREATE INDEX idx_forms_slug ON forms(slug);
CREATE INDEX idx_forms_language_code ON forms(language_code);

-- ============================================================================
-- FORM_FIELDS TABLE
-- ============================================================================

CREATE TABLE form_fields_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    label TEXT NOT NULL,
    placeholder TEXT DEFAULT '',
    help_text TEXT DEFAULT '',
    options TEXT DEFAULT '[]',
    validation TEXT DEFAULT '{}',
    is_required BOOLEAN NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    language_code TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_fields_new (id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, language_code, created_at, updated_at)
SELECT id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position,
    (SELECT code FROM languages WHERE id = form_fields.language_id),
    created_at, updated_at
FROM form_fields;

DROP TABLE form_fields;
ALTER TABLE form_fields_new RENAME TO form_fields;

CREATE INDEX idx_form_fields_form ON form_fields(form_id);
CREATE INDEX idx_form_fields_language_code ON form_fields(language_code);

-- ============================================================================
-- FORM_SUBMISSIONS TABLE
-- ============================================================================

CREATE TABLE form_submissions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    data TEXT NOT NULL,
    ip_address TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    is_read BOOLEAN NOT NULL DEFAULT 0,
    language_code TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_submissions_new (id, form_id, data, ip_address, user_agent, is_read, language_code, created_at)
SELECT id, form_id, data, ip_address, user_agent, is_read,
    (SELECT code FROM languages WHERE id = form_submissions.language_id),
    created_at
FROM form_submissions;

DROP TABLE form_submissions;
ALTER TABLE form_submissions_new RENAME TO form_submissions;

CREATE INDEX idx_form_submissions_form ON form_submissions(form_id);
CREATE INDEX idx_form_submissions_read ON form_submissions(is_read);
CREATE INDEX idx_form_submissions_language_code ON form_submissions(language_code);

-- ============================================================================
-- WIDGETS TABLE
-- ============================================================================

CREATE TABLE widgets_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    theme TEXT NOT NULL,
    area TEXT NOT NULL,
    widget_type TEXT NOT NULL,
    title TEXT,
    content TEXT,
    settings TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1,
    language_code TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO widgets_new (id, theme, area, widget_type, title, content, settings, position, is_active, language_code, created_at, updated_at)
SELECT id, theme, area, widget_type, title, content, settings, position, is_active,
    (SELECT code FROM languages WHERE id = widgets.language_id),
    created_at, updated_at
FROM widgets;

DROP TABLE widgets;
ALTER TABLE widgets_new RENAME TO widgets;

CREATE INDEX idx_widgets_theme_area ON widgets(theme, area);
CREATE INDEX idx_widgets_position ON widgets(position);
CREATE INDEX idx_widgets_language_code ON widgets(language_code);

-- ============================================================================
-- MEDIA TABLE
-- ============================================================================

CREATE TABLE media_new (
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
    language_code TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO media_new (id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, language_code, created_at, updated_at)
SELECT id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by,
    (SELECT code FROM languages WHERE id = media.language_id),
    created_at, updated_at
FROM media;

DROP TABLE media;
ALTER TABLE media_new RENAME TO media;

CREATE INDEX idx_media_uuid ON media(uuid);
CREATE INDEX idx_media_folder ON media(folder_id);
CREATE INDEX idx_media_mime ON media(mime_type);
CREATE INDEX idx_media_language_code ON media(language_code);

-- ============================================================================
-- CONFIG TABLE
-- ============================================================================

CREATE TABLE config_new (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'string',
    description TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by INTEGER,
    language_code TEXT NOT NULL,
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
);

INSERT INTO config_new (key, value, type, description, updated_at, updated_by, language_code)
SELECT key, value, type, description, updated_at, updated_by,
    (SELECT code FROM languages WHERE id = config.language_id)
FROM config;

DROP TABLE config;
ALTER TABLE config_new RENAME TO config;

CREATE INDEX idx_config_type ON config(type);
CREATE INDEX idx_config_language_code ON config(language_code);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- ============================================================================
-- Reverse migration: Change language_code TEXT back to language_id INT
-- ============================================================================

-- CONFIG TABLE (reverse)
CREATE TABLE config_new (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'string',
    description TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by INTEGER,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
);

INSERT INTO config_new (key, value, type, description, updated_at, updated_by, language_id)
SELECT key, value, type, description, updated_at, updated_by,
    (SELECT id FROM languages WHERE code = config.language_code)
FROM config;

DROP TABLE config;
ALTER TABLE config_new RENAME TO config;
CREATE INDEX idx_config_type ON config(type);
CREATE INDEX idx_config_language_id ON config(language_id);

-- MEDIA TABLE (reverse)
CREATE TABLE media_new (
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
    language_id INTEGER NOT NULL REFERENCES languages(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO media_new (id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, language_id, created_at, updated_at)
SELECT id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by,
    (SELECT id FROM languages WHERE code = media.language_code),
    created_at, updated_at
FROM media;

DROP TABLE media;
ALTER TABLE media_new RENAME TO media;
CREATE INDEX idx_media_uuid ON media(uuid);
CREATE INDEX idx_media_folder ON media(folder_id);
CREATE INDEX idx_media_mime ON media(mime_type);
CREATE INDEX idx_media_language_id ON media(language_id);

-- WIDGETS TABLE (reverse)
CREATE TABLE widgets_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    theme TEXT NOT NULL,
    area TEXT NOT NULL,
    widget_type TEXT NOT NULL,
    title TEXT,
    content TEXT,
    settings TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO widgets_new (id, theme, area, widget_type, title, content, settings, position, is_active, language_id, created_at, updated_at)
SELECT id, theme, area, widget_type, title, content, settings, position, is_active,
    (SELECT id FROM languages WHERE code = widgets.language_code),
    created_at, updated_at
FROM widgets;

DROP TABLE widgets;
ALTER TABLE widgets_new RENAME TO widgets;
CREATE INDEX idx_widgets_theme_area ON widgets(theme, area);
CREATE INDEX idx_widgets_position ON widgets(position);
CREATE INDEX idx_widgets_language_id ON widgets(language_id);

-- FORM_SUBMISSIONS TABLE (reverse)
CREATE TABLE form_submissions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    data TEXT NOT NULL,
    ip_address TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    is_read BOOLEAN NOT NULL DEFAULT 0,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_submissions_new (id, form_id, data, ip_address, user_agent, is_read, language_id, created_at)
SELECT id, form_id, data, ip_address, user_agent, is_read,
    (SELECT id FROM languages WHERE code = form_submissions.language_code),
    created_at
FROM form_submissions;

DROP TABLE form_submissions;
ALTER TABLE form_submissions_new RENAME TO form_submissions;
CREATE INDEX idx_form_submissions_form ON form_submissions(form_id);
CREATE INDEX idx_form_submissions_read ON form_submissions(is_read);
CREATE INDEX idx_form_submissions_language_id ON form_submissions(language_id);

-- FORM_FIELDS TABLE (reverse)
CREATE TABLE form_fields_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    label TEXT NOT NULL,
    placeholder TEXT DEFAULT '',
    help_text TEXT DEFAULT '',
    options TEXT DEFAULT '[]',
    validation TEXT DEFAULT '{}',
    is_required BOOLEAN NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_fields_new (id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, language_id, created_at, updated_at)
SELECT id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position,
    (SELECT id FROM languages WHERE code = form_fields.language_code),
    created_at, updated_at
FROM form_fields;

DROP TABLE form_fields;
ALTER TABLE form_fields_new RENAME TO form_fields;
CREATE INDEX idx_form_fields_form ON form_fields(form_id);
CREATE INDEX idx_form_fields_language_id ON form_fields(language_id);

-- FORMS TABLE (reverse)
CREATE TABLE forms_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    success_message TEXT DEFAULT 'Thank you for your submission.',
    email_to TEXT DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT 1,
    language_id INTEGER NOT NULL REFERENCES languages(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(slug, language_id)
);

INSERT INTO forms_new (id, name, slug, title, description, success_message, email_to, is_active, language_id, created_at, updated_at)
SELECT id, name, slug, title, description, success_message, email_to, is_active,
    (SELECT id FROM languages WHERE code = forms.language_code),
    created_at, updated_at
FROM forms;

DROP TABLE forms;
ALTER TABLE forms_new RENAME TO forms;
CREATE INDEX idx_forms_slug ON forms(slug);
CREATE INDEX idx_forms_language_id ON forms(language_id);

-- MENUS TABLE (reverse)
CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER NOT NULL REFERENCES languages(id)
);

INSERT INTO menus_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at,
    (SELECT id FROM languages WHERE code = menus.language_code)
FROM menus;

DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;
CREATE INDEX idx_menus_language_id ON menus(language_id);
CREATE UNIQUE INDEX idx_menus_slug_language ON menus(slug, language_id);

-- CATEGORIES TABLE (reverse)
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
SELECT id, name, slug, description, parent_id, position, created_at, updated_at,
    (SELECT id FROM languages WHERE code = categories.language_code)
FROM categories;

DROP TABLE categories;
ALTER TABLE categories_new RENAME TO categories;
CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE INDEX idx_categories_language_id ON categories(language_id);

-- TAGS TABLE (reverse)
CREATE TABLE tags_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER NOT NULL REFERENCES languages(id)
);

INSERT INTO tags_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at,
    (SELECT id FROM languages WHERE code = tags.language_code)
FROM tags;

DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;
CREATE INDEX idx_tags_slug ON tags(slug);
CREATE INDEX idx_tags_language_id ON tags(language_id);

-- PAGES TABLE (reverse)
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
    language_id INTEGER NOT NULL REFERENCES languages(id),
    FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO pages_new (id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at, language_id)
SELECT id, title, slug, body, status, author_id, created_at, updated_at, published_at,
    featured_image_id, meta_title, meta_description, meta_keywords, og_image_id,
    no_index, no_follow, canonical_url, scheduled_at,
    (SELECT id FROM languages WHERE code = pages.language_code)
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

-- +goose StatementEnd
