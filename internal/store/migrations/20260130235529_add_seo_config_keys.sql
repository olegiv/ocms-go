-- +goose Up
-- Add SEO-related config keys for Open Graph and social sharing

INSERT INTO config (key, value, type, description, language_code, updated_at)
VALUES ('site_url', '', 'string', 'Full site URL for canonical links and OG tags (e.g., https://example.com)',
        (SELECT code FROM languages WHERE is_default = 1 LIMIT 1), CURRENT_TIMESTAMP)
ON CONFLICT(key) DO NOTHING;

INSERT INTO config (key, value, type, description, language_code, updated_at)
VALUES ('default_og_image', '', 'string', 'Default Open Graph image URL for social sharing (1200x630px recommended)',
        (SELECT code FROM languages WHERE is_default = 1 LIMIT 1), CURRENT_TIMESTAMP)
ON CONFLICT(key) DO NOTHING;

-- +goose Down
DELETE FROM config WHERE key IN ('site_url', 'default_og_image');
