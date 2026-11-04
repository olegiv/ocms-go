-- +goose Up
-- Search over the text a visitor reads instead of the stored HTML (issue
-- #203). pages_fts indexes raw bodies, so a query matched tag names,
-- attribute values and inline styles, and snippet() quoted that markup in
-- the result excerpt.
--
-- page_search holds every page's readable text, which oCMS computes in Go.
-- The triggers below are plain SQL: they only queue the IDs of changed pages,
-- and oCMS indexes the queue before it searches. Any program can still write
-- pages, and older oCMS versions keep using pages_fts, which stays as it was.
-- +goose StatementBegin
CREATE VIRTUAL TABLE page_search USING fts5(
    title,
    body,
    meta_title,
    meta_description,
    meta_keywords,
    tokenize='porter unicode61'
);

CREATE TABLE page_search_queue (
    page_id INTEGER PRIMARY KEY
);

CREATE TRIGGER page_search_queue_ai AFTER INSERT ON pages BEGIN
    INSERT OR IGNORE INTO page_search_queue(page_id) VALUES (NEW.id);
END;

-- Only the indexed columns: a status, schedule or featured-image change
-- leaves the searchable text as it was.
CREATE TRIGGER page_search_queue_au
AFTER UPDATE OF id, title, body, meta_title, meta_description, meta_keywords ON pages
BEGIN
    INSERT OR IGNORE INTO page_search_queue(page_id) VALUES (OLD.id);
    INSERT OR IGNORE INTO page_search_queue(page_id) VALUES (NEW.id);
END;

CREATE TRIGGER page_search_queue_ad AFTER DELETE ON pages BEGIN
    INSERT OR IGNORE INTO page_search_queue(page_id) VALUES (OLD.id);
END;

INSERT INTO page_search_queue(page_id) SELECT id FROM pages;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS page_search_queue_ad;
DROP TRIGGER IF EXISTS page_search_queue_au;
DROP TRIGGER IF EXISTS page_search_queue_ai;
DROP TABLE IF EXISTS page_search_queue;
DROP TABLE IF EXISTS page_search;
-- +goose StatementEnd
