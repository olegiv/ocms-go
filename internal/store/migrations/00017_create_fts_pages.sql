-- +goose Up
-- +goose StatementBegin

-- Create FTS5 virtual table for pages search
-- Uses a regular (non-external content) FTS table for reliability
CREATE VIRTUAL TABLE pages_fts USING fts5(
    title,
    body,
    meta_title,
    meta_description,
    meta_keywords,
    tokenize='porter unicode61'
);

-- Populate the FTS index with existing published pages
INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
SELECT id, title, body, meta_title, meta_description, meta_keywords
FROM pages
WHERE status = 'published';

-- Trigger to update FTS when a page is inserted
CREATE TRIGGER pages_fts_ai AFTER INSERT ON pages
WHEN NEW.status = 'published'
BEGIN
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    VALUES(NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords);
END;

-- Trigger to update FTS when a page is deleted
CREATE TRIGGER pages_fts_bd BEFORE DELETE ON pages BEGIN
    DELETE FROM pages_fts WHERE rowid = OLD.id;
END;

-- Trigger to update FTS when a page is updated
CREATE TRIGGER pages_fts_au AFTER UPDATE ON pages BEGIN
    -- Delete old entry
    DELETE FROM pages_fts WHERE rowid = OLD.id;
    -- Insert new entry if it's now published
    INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
    SELECT NEW.id, NEW.title, NEW.body, NEW.meta_title, NEW.meta_description, NEW.meta_keywords
    WHERE NEW.status = 'published';
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS pages_fts_au;
DROP TRIGGER IF EXISTS pages_fts_bd;
DROP TRIGGER IF EXISTS pages_fts_ai;
DROP TABLE IF EXISTS pages_fts;

-- +goose StatementEnd
