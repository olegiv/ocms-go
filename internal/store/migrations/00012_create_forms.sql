-- +goose Up
CREATE TABLE forms (
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

CREATE TABLE form_fields (
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

CREATE INDEX idx_forms_slug ON forms(slug);
CREATE INDEX idx_form_fields_form ON form_fields(form_id);

-- +goose Down
DROP TABLE form_fields;
DROP TABLE forms;
