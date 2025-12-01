-- +goose Up
-- Performance optimization indexes for Phase 4

-- Pages: index for scheduled publishing queries (scheduled_at + status)
CREATE INDEX IF NOT EXISTS idx_pages_scheduled_status ON pages(scheduled_at, status) WHERE scheduled_at IS NOT NULL;

-- Pages: index for published pages sorted by published_at
CREATE INDEX IF NOT EXISTS idx_pages_published_at ON pages(published_at) WHERE status = 'published';

-- Pages: composite index for language + status filtering with ORDER BY
CREATE INDEX IF NOT EXISTS idx_pages_language_status ON pages(language_id, status);

-- Pages: updated_at for chronological queries
CREATE INDEX IF NOT EXISTS idx_pages_updated_at ON pages(updated_at);

-- Webhook deliveries: composite index for retry worker queries
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_pending_retry ON webhook_deliveries(status, next_retry_at) WHERE status = 'pending';

-- Webhook deliveries: updated_at for chronological queries
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_updated_at ON webhook_deliveries(updated_at);

-- Translations: composite index for getting all translations related to an entity
CREATE INDEX IF NOT EXISTS idx_translations_entity_full ON translations(entity_type, entity_id, translation_id);

-- Categories: language filtering
CREATE INDEX IF NOT EXISTS idx_categories_language_id ON categories(language_id);

-- Tags: language filtering
CREATE INDEX IF NOT EXISTS idx_tags_language_id ON tags(language_id);

-- Menus: language filtering
CREATE INDEX IF NOT EXISTS idx_menus_language_id ON menus(language_id);

-- Menu items: composite index for menu + position ordering
CREATE INDEX IF NOT EXISTS idx_menu_items_menu_position ON menu_items(menu_id, position);

-- Media: folder filtering (for library view)
CREATE INDEX IF NOT EXISTS idx_media_folder_id ON media(folder_id);

-- Media: type filtering
CREATE INDEX IF NOT EXISTS idx_media_mime_type ON media(mime_type);

-- Form submissions: created_at for chronological queries
CREATE INDEX IF NOT EXISTS idx_form_submissions_created_at ON form_submissions(created_at);

-- API keys: key_hash lookup optimization
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);

-- Events: created_at for chronological queries
CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_created_at;
DROP INDEX IF EXISTS idx_api_keys_key_hash;
DROP INDEX IF EXISTS idx_form_submissions_created_at;
DROP INDEX IF EXISTS idx_media_mime_type;
DROP INDEX IF EXISTS idx_media_folder_id;
DROP INDEX IF EXISTS idx_menu_items_menu_position;
DROP INDEX IF EXISTS idx_menus_language_id;
DROP INDEX IF EXISTS idx_tags_language_id;
DROP INDEX IF EXISTS idx_categories_language_id;
DROP INDEX IF EXISTS idx_translations_entity_full;
DROP INDEX IF EXISTS idx_webhook_deliveries_updated_at;
DROP INDEX IF EXISTS idx_webhook_deliveries_pending_retry;
DROP INDEX IF EXISTS idx_pages_updated_at;
DROP INDEX IF EXISTS idx_pages_language_status;
DROP INDEX IF EXISTS idx_pages_published_at;
DROP INDEX IF EXISTS idx_pages_scheduled_status;
