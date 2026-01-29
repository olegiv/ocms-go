-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- Migration 00033: Add language_id NOT NULL to forms, form_fields,
-- form_submissions, widgets, media, and config tables.
-- ============================================================================

-- Note: SQLite does not support ALTER COLUMN, so we must recreate each table.
-- Order matters due to FK constraints: forms -> form_fields/form_submissions

-- ============================================================================
-- FORMS TABLE
-- ============================================================================

-- Create new forms table with language_id
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

-- Copy data with default language
INSERT INTO forms_new (id, name, slug, title, description, success_message, email_to, is_active, language_id, created_at, updated_at)
SELECT id, name, slug, title, description, success_message, email_to, is_active,
       (SELECT id FROM languages WHERE is_default = 1),
       created_at, updated_at
FROM forms;

-- Drop old table and rename
DROP TABLE forms;
ALTER TABLE forms_new RENAME TO forms;

-- Recreate indexes
CREATE INDEX idx_forms_slug ON forms(slug);
CREATE INDEX idx_forms_language_id ON forms(language_id);

-- ============================================================================
-- FORM_FIELDS TABLE
-- ============================================================================

-- Create new form_fields table with language_id
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

-- Copy data with default language
INSERT INTO form_fields_new (id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, language_id, created_at, updated_at)
SELECT id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position,
       (SELECT id FROM languages WHERE is_default = 1),
       created_at, updated_at
FROM form_fields;

-- Drop old table and rename
DROP TABLE form_fields;
ALTER TABLE form_fields_new RENAME TO form_fields;

-- Recreate indexes
CREATE INDEX idx_form_fields_form ON form_fields(form_id);
CREATE INDEX idx_form_fields_language_id ON form_fields(language_id);

-- ============================================================================
-- FORM_SUBMISSIONS TABLE
-- ============================================================================

-- Create new form_submissions table with language_id
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

-- Copy data with default language
INSERT INTO form_submissions_new (id, form_id, data, ip_address, user_agent, is_read, language_id, created_at)
SELECT id, form_id, data, ip_address, user_agent, is_read,
       (SELECT id FROM languages WHERE is_default = 1),
       created_at
FROM form_submissions;

-- Drop old table and rename
DROP TABLE form_submissions;
ALTER TABLE form_submissions_new RENAME TO form_submissions;

-- Recreate indexes
CREATE INDEX idx_form_submissions_form ON form_submissions(form_id);
CREATE INDEX idx_form_submissions_read ON form_submissions(is_read);
CREATE INDEX idx_form_submissions_language_id ON form_submissions(language_id);

-- ============================================================================
-- WIDGETS TABLE
-- ============================================================================

-- Create new widgets table with language_id
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

-- Copy data with default language
INSERT INTO widgets_new (id, theme, area, widget_type, title, content, settings, position, is_active, language_id, created_at, updated_at)
SELECT id, theme, area, widget_type, title, content, settings, position, is_active,
       (SELECT id FROM languages WHERE is_default = 1),
       created_at, updated_at
FROM widgets;

-- Drop old table and rename
DROP TABLE widgets;
ALTER TABLE widgets_new RENAME TO widgets;

-- Recreate indexes
CREATE INDEX idx_widgets_theme_area ON widgets(theme, area);
CREATE INDEX idx_widgets_position ON widgets(position);
CREATE INDEX idx_widgets_language_id ON widgets(language_id);

-- ============================================================================
-- MEDIA TABLE
-- ============================================================================

-- Note: media_variants and media_translations have FK to media(id) with CASCADE.
-- With PRAGMA foreign_keys = OFF (goose default), dropping media is safe.
-- The FKs will automatically reference the new table after rename.

-- Create new media table with language_id
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

-- Copy data with default language
INSERT INTO media_new (id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, language_id, created_at, updated_at)
SELECT id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by,
       (SELECT id FROM languages WHERE is_default = 1),
       created_at, updated_at
FROM media;

-- Drop old table and rename
DROP TABLE media;
ALTER TABLE media_new RENAME TO media;

-- Recreate indexes
CREATE INDEX idx_media_uuid ON media(uuid);
CREATE INDEX idx_media_folder ON media(folder_id);
CREATE INDEX idx_media_mime ON media(mime_type);
CREATE INDEX idx_media_language_id ON media(language_id);

-- ============================================================================
-- CONFIG TABLE
-- ============================================================================

-- Config uses TEXT PRIMARY KEY (key). language_id is an attribute, not part of PK.
-- The ON CONFLICT(key) in UpsertConfig will still work.

-- Create new config table with language_id
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

-- Copy data with default language
INSERT INTO config_new (key, value, type, description, updated_at, updated_by, language_id)
SELECT key, value, type, description, updated_at, updated_by,
       (SELECT id FROM languages WHERE is_default = 1)
FROM config;

-- Drop old table and rename
DROP TABLE config;
ALTER TABLE config_new RENAME TO config;

-- Recreate indexes
CREATE INDEX idx_config_type ON config(type);
CREATE INDEX idx_config_language_id ON config(language_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- ============================================================================
-- Reverse migration: Remove language_id from all tables
-- ============================================================================

-- CONFIG TABLE (reverse)
CREATE TABLE config_new (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'string',
    description TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by INTEGER,
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
);

INSERT INTO config_new (key, value, type, description, updated_at, updated_by)
SELECT key, value, type, description, updated_at, updated_by FROM config;

DROP TABLE config;
ALTER TABLE config_new RENAME TO config;
CREATE INDEX idx_config_type ON config(type);

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
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO media_new (id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, created_at, updated_at)
SELECT id, uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, created_at, updated_at FROM media;

DROP TABLE media;
ALTER TABLE media_new RENAME TO media;
CREATE INDEX idx_media_uuid ON media(uuid);
CREATE INDEX idx_media_folder ON media(folder_id);
CREATE INDEX idx_media_mime ON media(mime_type);

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
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO widgets_new (id, theme, area, widget_type, title, content, settings, position, is_active, created_at, updated_at)
SELECT id, theme, area, widget_type, title, content, settings, position, is_active, created_at, updated_at FROM widgets;

DROP TABLE widgets;
ALTER TABLE widgets_new RENAME TO widgets;
CREATE INDEX idx_widgets_theme_area ON widgets(theme, area);
CREATE INDEX idx_widgets_position ON widgets(position);

-- FORM_SUBMISSIONS TABLE (reverse)
CREATE TABLE form_submissions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    data TEXT NOT NULL,
    ip_address TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    is_read BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_submissions_new (id, form_id, data, ip_address, user_agent, is_read, created_at)
SELECT id, form_id, data, ip_address, user_agent, is_read, created_at FROM form_submissions;

DROP TABLE form_submissions;
ALTER TABLE form_submissions_new RENAME TO form_submissions;
CREATE INDEX idx_form_submissions_form ON form_submissions(form_id);
CREATE INDEX idx_form_submissions_read ON form_submissions(is_read);

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
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO form_fields_new (id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, created_at, updated_at)
SELECT id, form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, created_at, updated_at FROM form_fields;

DROP TABLE form_fields;
ALTER TABLE form_fields_new RENAME TO form_fields;
CREATE INDEX idx_form_fields_form ON form_fields(form_id);

-- FORMS TABLE (reverse)
CREATE TABLE forms_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    success_message TEXT DEFAULT 'Thank you for your submission.',
    email_to TEXT DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO forms_new (id, name, slug, title, description, success_message, email_to, is_active, created_at, updated_at)
SELECT id, name, slug, title, description, success_message, email_to, is_active, created_at, updated_at FROM forms;

DROP TABLE forms;
ALTER TABLE forms_new RENAME TO forms;
CREATE INDEX idx_forms_slug ON forms(slug);

-- +goose StatementEnd
