# Opossum CMS (oCMS) — Phase 4 Implementation Prompt

## Phase Overview

**Phase 4: Advanced** completes oCMS with multi-language support (content translation + UI localization), webhooks with retry logic, Redis caching option, and a generic JSON import/export system.

---

## Critical Development Rules

### 1. Iterative Development
- **DO NOT implement everything at once**
- Work through iterations sequentially
- Each iteration must be testable and committable independently
- **STOP after each iteration** for me to test and confirm before proceeding

### 2. Self-Testing Requirement
- **You must test everything you implement before asking me to test**
- **Always restart the server before final testing**
- Run the application and verify functionality works
- Check for compilation errors
- Test HTTP endpoints manually (curl or browser)
- Run any unit tests you create
- Only after confirming it works, present it for my review

### 3. Dependency Management
- **Always use non-vulnerable module versions**
- Run `go mod tidy` after adding dependencies
- Check for known vulnerabilities with `govulncheck` before committing

### 4. Server Restart Reminder
- **Before presenting work for review, always:**
  1. Stop the running server
  2. Rebuild: `go build ./cmd/ocms`
  3. Start fresh: `./bin/ocms` or `go run ./cmd/ocms`
  4. Test the feature
  5. Then present for review

---

## Technology Stack (Additions to Phase 3)

| Component | Technology |
|-----------|------------|
| UI Localization | `golang.org/x/text` (message, language) |
| Cache (optional) | Redis via `redis/go-redis/v9` |
| Webhook Delivery | Background goroutines with retry |
| JSON Schema | Custom oCMS export format |
| HTTP Client | stdlib `net/http` with timeouts |

---

## Project Structure (Additions)

```
opossum/
├── internal/
│   ├── handler/
│   │   ├── ...existing...
│   │   ├── languages.go          # Language management
│   │   ├── translations.go       # Content translation
│   │   ├── webhooks.go           # Webhook management
│   │   └── importexport.go       # Import/export handlers
│   ├── model/
│   │   ├── ...existing...
│   │   ├── language.go
│   │   ├── translation.go
│   │   ├── webhook.go
│   │   └── webhook_delivery.go
│   ├── store/
│   │   ├── queries/
│   │   │   ├── ...existing...
│   │   │   ├── languages.sql
│   │   │   ├── translations.sql
│   │   │   ├── webhooks.sql
│   │   │   └── webhook_deliveries.sql
│   ├── i18n/                       # Internationalization
│   │   ├── i18n.go                 # Core i18n setup
│   │   ├── catalog.go              # Message catalog
│   │   └── middleware.go           # Language detection middleware
│   ├── webhook/                    # Webhook system
│   │   ├── dispatcher.go           # Event dispatcher
│   │   ├── delivery.go             # HTTP delivery with retry
│   │   └── events.go               # Event definitions
│   ├── transfer/                   # Import/Export
│   │   ├── exporter.go             # Export to JSON
│   │   ├── importer.go             # Import from JSON
│   │   └── schema.go               # oCMS JSON schema
│   └── cache/
│       ├── cache.go                # Cache interface
│       ├── memory.go               # In-memory implementation
│       └── redis.go                # Redis implementation (optional)
├── migrations/
│   ├── ...existing...
│   ├── 00018_create_languages.sql
│   ├── 00019_create_translations.sql
│   ├── 00020_create_webhooks.sql
│   └── 00021_create_webhook_deliveries.sql
├── locales/                        # UI translation files
│   ├── en/
│   │   └── messages.gotext.json
│   ├── ru/
│   │   └── messages.gotext.json
│   └── catalog.go                  # Generated catalog
├── web/
│   ├── templates/
│   │   ├── admin/
│   │   │   ├── ...existing...
│   │   │   ├── languages_list.html
│   │   │   ├── languages_form.html
│   │   │   ├── translations.html
│   │   │   ├── webhooks_list.html
│   │   │   ├── webhooks_form.html
│   │   │   ├── webhooks_deliveries.html
│   │   │   ├── import.html
│   │   │   └── export.html
```

---

## Phase 4 Entities

### Language
```go
type Language struct {
    ID        int64
    Code      string     // ISO 639-1: en, ru, de, fr
    Name      string     // English, Russian, German, French
    NativeName string    // English, Русский, Deutsch, Français
    IsDefault bool       // only one can be default
    IsActive  bool       // enabled for site
    Direction string     // ltr, rtl
    Position  int        // sort order in language switcher
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

### Translation (Linked Content)
```go
type Translation struct {
    ID           int64
    EntityType   string    // page, category, tag, menu_item
    EntityID     int64     // ID of the entity
    LanguageID   int64     // Language this translation is for
    TranslationID int64    // ID of the translated entity
    CreatedAt    time.Time
}

// Example: Page 1 (English) linked to Page 2 (Russian)
// Translation { EntityType: "page", EntityID: 1, LanguageID: 2, TranslationID: 2 }
```

### Webhook
```go
type Webhook struct {
    ID          int64
    Name        string     // descriptive name
    URL         string     // delivery endpoint
    Secret      string     // HMAC signing secret
    Events      string     // JSON array of event types
    IsActive    bool
    Headers     string     // JSON object of custom headers
    CreatedBy   int64
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// Event types
const (
    EventPageCreated     = "page.created"
    EventPageUpdated     = "page.updated"
    EventPageDeleted     = "page.deleted"
    EventPagePublished   = "page.published"
    EventPageUnpublished = "page.unpublished"
    EventMediaUploaded   = "media.uploaded"
    EventMediaDeleted    = "media.deleted"
    EventFormSubmitted   = "form.submitted"
    EventUserCreated     = "user.created"
    EventUserDeleted     = "user.deleted"
)
```

### WebhookDelivery
```go
type WebhookDelivery struct {
    ID            int64
    WebhookID     int64
    Event         string     // event type
    Payload       string     // JSON payload sent
    ResponseCode  *int       // HTTP response code (null if not delivered)
    ResponseBody  string     // Response body (truncated)
    Attempts      int        // number of delivery attempts
    NextRetryAt   *time.Time // next retry time (null if delivered or dead)
    DeliveredAt   *time.Time // successful delivery time
    Status        string     // pending, delivered, failed, dead
    ErrorMessage  string     // last error message
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

### Export Schema
```go
type ExportData struct {
    Version   string              `json:"version"`    // oCMS export version
    ExportedAt time.Time          `json:"exported_at"`
    Site      ExportSite          `json:"site"`
    Languages []ExportLanguage    `json:"languages,omitempty"`
    Users     []ExportUser        `json:"users,omitempty"`
    Pages     []ExportPage        `json:"pages,omitempty"`
    Categories []ExportCategory   `json:"categories,omitempty"`
    Tags      []ExportTag         `json:"tags,omitempty"`
    Media     []ExportMedia       `json:"media,omitempty"`
    Menus     []ExportMenu        `json:"menus,omitempty"`
    Forms     []ExportForm        `json:"forms,omitempty"`
    Config    map[string]string   `json:"config,omitempty"`
}

type ExportPage struct {
    ID          int64               `json:"id"`
    Title       string              `json:"title"`
    Slug        string              `json:"slug"`
    Body        string              `json:"body"`
    Status      string              `json:"status"`
    AuthorEmail string              `json:"author_email"`  // reference by email
    Categories  []string            `json:"categories"`    // slugs
    Tags        []string            `json:"tags"`          // slugs
    SEO         *ExportPageSEO      `json:"seo,omitempty"`
    Translations map[string]int64   `json:"translations,omitempty"` // lang_code -> page_id
    CreatedAt   time.Time           `json:"created_at"`
    UpdatedAt   time.Time           `json:"updated_at"`
    PublishedAt *time.Time          `json:"published_at,omitempty"`
}
```

---

## Iteration Plan

### Iteration 1: Language Configuration - Model & Store
**Goal:** Language entity for multi-language support

**Tasks:**
1. Create migration `00018_create_languages.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE languages (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       code TEXT NOT NULL UNIQUE,
       name TEXT NOT NULL,
       native_name TEXT NOT NULL,
       is_default BOOLEAN NOT NULL DEFAULT 0,
       is_active BOOLEAN NOT NULL DEFAULT 1,
       direction TEXT NOT NULL DEFAULT 'ltr',
       position INTEGER NOT NULL DEFAULT 0,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE INDEX idx_languages_code ON languages(code);
   CREATE INDEX idx_languages_active ON languages(is_active);
   CREATE INDEX idx_languages_default ON languages(is_default);

   -- Seed default language
   INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position)
   VALUES ('en', 'English', 'English', 1, 1, 'ltr', 0);

   -- +goose Down
   DROP TABLE languages;
   ```
2. Create `internal/model/language.go`
3. Create `internal/store/queries/languages.sql`:
   ```sql
   -- name: CreateLanguage :one
   INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetLanguageByID :one
   SELECT * FROM languages WHERE id = ?;

   -- name: GetLanguageByCode :one
   SELECT * FROM languages WHERE code = ?;

   -- name: GetDefaultLanguage :one
   SELECT * FROM languages WHERE is_default = 1;

   -- name: ListLanguages :many
   SELECT * FROM languages ORDER BY position ASC, name ASC;

   -- name: ListActiveLanguages :many
   SELECT * FROM languages WHERE is_active = 1 ORDER BY position ASC, name ASC;

   -- name: UpdateLanguage :one
   UPDATE languages SET code = ?, name = ?, native_name = ?, is_default = ?, is_active = ?, direction = ?, position = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteLanguage :exec
   DELETE FROM languages WHERE id = ?;

   -- name: ClearDefaultLanguage :exec
   UPDATE languages SET is_default = 0 WHERE is_default = 1;

   -- name: SetDefaultLanguage :exec
   UPDATE languages SET is_default = 1, updated_at = ? WHERE id = ?;

   -- name: CountLanguages :one
   SELECT COUNT(*) FROM languages;
   ```
4. Run migration and sqlc generate

**Verification:**
- Migration runs successfully
- Default language (English) seeded
- sqlc generates without errors
- **Restart server and verify**

**Commit message:** `feat: language model and database operations`

---

### Iteration 2: Language Management - Admin UI
**Goal:** Manage languages in admin

**Tasks:**
1. Create `internal/handler/languages.go`:
   - `GET /admin/languages` — list languages
   - `GET /admin/languages/new` — new language form
   - `POST /admin/languages` — create language
   - `GET /admin/languages/{id}` — edit form
   - `PUT /admin/languages/{id}` — update language
   - `DELETE /admin/languages/{id}` — delete language
   - `POST /admin/languages/{id}/default` — set as default
2. Create templates:
   - `web/templates/admin/languages_list.html`:
     - Table with code, name, native name, status
     - Default indicator
     - RTL/LTR indicator
     - Reorder capability
   - `web/templates/admin/languages_form.html`:
     - Code (ISO 639-1 dropdown or text)
     - Name
     - Native name
     - Direction (LTR/RTL)
     - Active toggle
3. Common language codes dropdown:
   ```go
   var CommonLanguages = []struct {
       Code       string
       Name       string
       NativeName string
   }{
       {"en", "English", "English"},
       {"ru", "Russian", "Русский"},
       {"de", "German", "Deutsch"},
       {"fr", "French", "Français"},
       {"es", "Spanish", "Español"},
       {"zh", "Chinese", "中文"},
       {"ja", "Japanese", "日本語"},
       {"ar", "Arabic", "العربية"},
       // ... more
   }
   ```
4. Validation:
   - Cannot delete default language
   - Cannot deactivate default language
   - Code must be unique
5. Add to admin navigation under Config section

**Verification:**
- Can add new languages
- Can set default language
- Cannot delete default
- **Restart server and test all operations**

**Commit message:** `feat: language management admin UI`

---

### Iteration 3: Content Translation - Model & Linking
**Goal:** Link content across languages

**Tasks:**
1. Create migration `00019_create_translations.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE translations (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       entity_type TEXT NOT NULL,
       entity_id INTEGER NOT NULL,
       language_id INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
       translation_id INTEGER NOT NULL,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       UNIQUE(entity_type, entity_id, language_id)
   );

   CREATE INDEX idx_translations_entity ON translations(entity_type, entity_id);
   CREATE INDEX idx_translations_language ON translations(language_id);
   CREATE INDEX idx_translations_target ON translations(entity_type, translation_id);

   -- Add language_id to pages
   ALTER TABLE pages ADD COLUMN language_id INTEGER REFERENCES languages(id);

   -- Set existing pages to default language
   UPDATE pages SET language_id = (SELECT id FROM languages WHERE is_default = 1);

   -- +goose Down
   DROP TABLE translations;
   ```
2. Create `internal/model/translation.go`
3. Create `internal/store/queries/translations.sql`:
   ```sql
   -- name: CreateTranslation :one
   INSERT INTO translations (entity_type, entity_id, language_id, translation_id, created_at)
   VALUES (?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetTranslation :one
   SELECT * FROM translations 
   WHERE entity_type = ? AND entity_id = ? AND language_id = ?;

   -- name: GetTranslationsForEntity :many
   SELECT t.*, l.code, l.name, l.native_name
   FROM translations t
   INNER JOIN languages l ON l.id = t.language_id
   WHERE t.entity_type = ? AND t.entity_id = ?
   ORDER BY l.position ASC;

   -- name: GetAllTranslationsOfEntity :many
   SELECT t.*, l.code, l.name
   FROM translations t
   INNER JOIN languages l ON l.id = t.language_id
   WHERE t.entity_type = ? AND (t.entity_id = ? OR t.translation_id = ?)
   ORDER BY l.position ASC;

   -- name: DeleteTranslation :exec
   DELETE FROM translations WHERE id = ?;

   -- name: DeleteTranslationsForEntity :exec
   DELETE FROM translations WHERE entity_type = ? AND entity_id = ?;

   -- name: GetPageByLanguage :one
   SELECT p.* FROM pages p
   INNER JOIN translations t ON t.translation_id = p.id
   WHERE t.entity_type = 'page' AND t.entity_id = ? AND t.language_id = ?;
   ```
4. Update page queries to include language_id
5. Run migration and sqlc generate

**Verification:**
- Migration runs, existing pages get default language
- Translation linking works
- **Restart server and verify**

**Commit message:** `feat: content translation model and linking`

---

### Iteration 4: Content Translation - Page UI
**Goal:** Translate pages in admin

**Tasks:**
1. Update page editor (`pages_form.html`):
   - Show current language
   - "Translations" panel showing:
     - Linked translations (with links to edit)
     - "Add translation" for missing languages
2. Add translation handlers to `pages.go`:
   - When creating page, set language_id
   - `POST /admin/pages/{id}/translate/{langCode}` — create translation
3. Translation creation workflow:
   - User clicks "Add Russian translation"
   - Creates new page with:
     - Same title (can be edited)
     - Empty body (to be translated)
     - language_id set to Russian
   - Creates translation link between pages
   - Redirects to new page editor
4. Show language badge in page list
5. Filter pages by language in list view
6. Update page duplicate to preserve language

**Verification:**
- Can see current page language
- Can create translation for another language
- Translation links correctly
- Language filter works
- **Restart server and test translation workflow**

**Commit message:** `feat: page translation UI`

---

### Iteration 5: Content Translation - Frontend
**Goal:** Language switcher and translated content

**Tasks:**
1. Create language detection middleware:
   ```go
   // Priority:
   // 1. URL prefix (/ru/page-slug)
   // 2. Cookie preference
   // 3. Accept-Language header
   // 4. Default language
   func LanguageMiddleware(languages *store.Queries) func(http.Handler) http.Handler
   ```
2. Update frontend routes to support language prefix:
   - `GET /{lang}/{slug}` — page in specific language
   - `GET /{slug}` — page in default/detected language
3. Create language switcher partial:
   - Shows available translations for current page
   - Falls back to homepage in other language if no translation
4. Update theme templates:
   - Include language switcher
   - Set `lang` attribute on `<html>`
   - Set `dir` attribute for RTL languages
5. Store language preference in cookie
6. Add hreflang meta tags for SEO

**Verification:**
- Language prefix routing works
- Language switcher shows available translations
- RTL languages display correctly
- hreflang tags present
- **Restart server and test multi-language frontend**

**Commit message:** `feat: frontend multi-language support`

---

### Iteration 6: UI Localization - Setup
**Goal:** Admin UI translation infrastructure

**Tasks:**
1. Create `internal/i18n/i18n.go`:
   ```go
   package i18n

   import (
       "golang.org/x/text/language"
       "golang.org/x/text/message"
   )

   var (
       matcher  language.Matcher
       printers map[string]*message.Printer
   )

   func Init(supportedLangs []string) error
   func GetPrinter(lang string) *message.Printer
   func T(lang, key string, args ...interface{}) string
   ```
2. Create `locales/en/messages.gotext.json`:
   ```json
   {
       "language": "en",
       "messages": [
           {
               "id": "dashboard.title",
               "message": "Dashboard",
               "translation": "Dashboard"
           },
           {
               "id": "pages.title",
               "message": "Pages",
               "translation": "Pages"
           },
           {
               "id": "btn.save",
               "message": "Save",
               "translation": "Save"
           },
           {
               "id": "btn.cancel",
               "message": "Cancel",
               "translation": "Cancel"
           },
           {
               "id": "btn.delete",
               "message": "Delete",
               "translation": "Delete"
           },
           {
               "id": "msg.saved",
               "message": "Changes saved successfully",
               "translation": "Changes saved successfully"
           },
           {
               "id": "msg.deleted",
               "message": "{Item} deleted successfully",
               "translation": "{Item} deleted successfully",
               "placeholders": [{"id": "Item", "string": "%s"}]
           }
       ]
   }
   ```
3. Create Russian translations `locales/ru/messages.gotext.json`
4. Generate catalog with `gotext`:
   ```bash
   go generate ./locales/...
   ```
5. Add `T` function to template functions
6. Store admin language preference per user or in session

**Verification:**
- Message catalog loads
- T function works in templates
- **Restart server and verify**

**Commit message:** `feat: UI localization infrastructure`

---

### Iteration 7: UI Localization - Admin Templates
**Goal:** Translate admin interface

**Tasks:**
1. Update admin templates to use `T` function:
   ```html
   <!-- Before -->
   <h1>Dashboard</h1>
   <button>Save</button>

   <!-- After -->
   <h1>{{T .Lang "dashboard.title"}}</h1>
   <button>{{T .Lang "btn.save"}}</button>
   ```
2. Add messages for all admin UI strings:
   - Navigation items
   - Page titles
   - Button labels
   - Form labels
   - Flash messages
   - Confirmation dialogs
   - Validation errors
3. Create Russian translations for all messages
4. Add admin language selector:
   - In user profile or header dropdown
   - Store preference in session/cookie
5. Pass language to all template renders

**Verification:**
- Admin UI displays in selected language
- All strings translated
- Language switch works
- **Restart server and test Russian admin**

**Commit message:** `feat: admin UI Russian localization`

---

### Iteration 8: Webhooks - Model & Store
**Goal:** Webhook configuration entities

**Tasks:**
1. Create migration `00020_create_webhooks.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE webhooks (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       url TEXT NOT NULL,
       secret TEXT NOT NULL,
       events TEXT NOT NULL DEFAULT '[]',
       is_active BOOLEAN NOT NULL DEFAULT 1,
       headers TEXT NOT NULL DEFAULT '{}',
       created_by INTEGER NOT NULL REFERENCES users(id),
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE INDEX idx_webhooks_active ON webhooks(is_active);

   -- +goose Down
   DROP TABLE webhooks;
   ```
2. Create migration `00021_create_webhook_deliveries.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE webhook_deliveries (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
       event TEXT NOT NULL,
       payload TEXT NOT NULL,
       response_code INTEGER,
       response_body TEXT DEFAULT '',
       attempts INTEGER NOT NULL DEFAULT 0,
       next_retry_at DATETIME,
       delivered_at DATETIME,
       status TEXT NOT NULL DEFAULT 'pending',
       error_message TEXT DEFAULT '',
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id);
   CREATE INDEX idx_webhook_deliveries_status ON webhook_deliveries(status);
   CREATE INDEX idx_webhook_deliveries_retry ON webhook_deliveries(next_retry_at) 
       WHERE status = 'pending' AND next_retry_at IS NOT NULL;

   -- +goose Down
   DROP TABLE webhook_deliveries;
   ```
3. Create `internal/model/webhook.go`
4. Create `internal/store/queries/webhooks.sql`:
   ```sql
   -- name: CreateWebhook :one
   INSERT INTO webhooks (name, url, secret, events, is_active, headers, created_by, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetWebhookByID :one
   SELECT * FROM webhooks WHERE id = ?;

   -- name: ListWebhooks :many
   SELECT * FROM webhooks ORDER BY name ASC;

   -- name: ListActiveWebhooks :many
   SELECT * FROM webhooks WHERE is_active = 1;

   -- name: ListWebhooksForEvent :many
   SELECT * FROM webhooks 
   WHERE is_active = 1 AND events LIKE '%' || ? || '%';

   -- name: UpdateWebhook :one
   UPDATE webhooks SET name = ?, url = ?, secret = ?, events = ?, is_active = ?, headers = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteWebhook :exec
   DELETE FROM webhooks WHERE id = ?;

   -- name: CreateWebhookDelivery :one
   INSERT INTO webhook_deliveries (webhook_id, event, payload, status, created_at, updated_at)
   VALUES (?, ?, ?, 'pending', ?, ?)
   RETURNING *;

   -- name: GetWebhookDelivery :one
   SELECT * FROM webhook_deliveries WHERE id = ?;

   -- name: ListWebhookDeliveries :many
   SELECT * FROM webhook_deliveries WHERE webhook_id = ?
   ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: GetPendingDeliveries :many
   SELECT * FROM webhook_deliveries 
   WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= ?)
   ORDER BY created_at ASC LIMIT ?;

   -- name: UpdateDeliverySuccess :exec
   UPDATE webhook_deliveries 
   SET status = 'delivered', response_code = ?, response_body = ?, delivered_at = ?, attempts = attempts + 1, updated_at = ?
   WHERE id = ?;

   -- name: UpdateDeliveryRetry :exec
   UPDATE webhook_deliveries 
   SET status = 'pending', response_code = ?, response_body = ?, error_message = ?, attempts = attempts + 1, next_retry_at = ?, updated_at = ?
   WHERE id = ?;

   -- name: UpdateDeliveryDead :exec
   UPDATE webhook_deliveries 
   SET status = 'dead', error_message = ?, attempts = attempts + 1, updated_at = ?
   WHERE id = ?;

   -- name: CountWebhookDeliveries :one
   SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?;

   -- name: CountDeliveriesByStatus :one
   SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND status = ?;

   -- name: DeleteOldDeliveries :exec
   DELETE FROM webhook_deliveries WHERE created_at < ? AND status IN ('delivered', 'dead');
   ```
5. Run migrations and sqlc generate

**Verification:**
- Migrations run successfully
- sqlc generates without errors
- **Restart server and verify**

**Commit message:** `feat: webhook model and database operations`

---

### Iteration 9: Webhooks - Management UI
**Goal:** Create and manage webhooks

**Tasks:**
1. Create `internal/handler/webhooks.go`:
   - `GET /admin/webhooks` — list webhooks
   - `GET /admin/webhooks/new` — new webhook form
   - `POST /admin/webhooks` — create webhook
   - `GET /admin/webhooks/{id}` — edit form
   - `PUT /admin/webhooks/{id}` — update webhook
   - `DELETE /admin/webhooks/{id}` — delete webhook
   - `GET /admin/webhooks/{id}/deliveries` — delivery history
   - `POST /admin/webhooks/{id}/test` — send test event
2. Create templates:
   - `web/templates/admin/webhooks_list.html`:
     - Table with name, URL, events, status
     - Success/failure stats
     - Active toggle
   - `web/templates/admin/webhooks_form.html`:
     - Name
     - URL (validated)
     - Secret (auto-generated option)
     - Events checkboxes
     - Custom headers (key-value editor)
     - Active toggle
   - `web/templates/admin/webhooks_deliveries.html`:
     - Delivery history table
     - Status badges (pending, delivered, failed, dead)
     - Attempt count
     - Response code
     - Expand to see payload/response
3. Event types with descriptions:
   ```go
   var WebhookEvents = []struct {
       Type        string
       Description string
   }{
       {"page.created", "When a new page is created"},
       {"page.updated", "When a page is updated"},
       {"page.deleted", "When a page is deleted"},
       {"page.published", "When a page is published"},
       {"page.unpublished", "When a page is unpublished"},
       {"media.uploaded", "When media is uploaded"},
       {"media.deleted", "When media is deleted"},
       {"form.submitted", "When a form is submitted"},
       {"user.created", "When a user is created"},
       {"user.deleted", "When a user is deleted"},
   }
   ```
4. Add to admin navigation

**Verification:**
- Can create webhook
- Events selectable
- Secret auto-generates
- Test webhook works (returns pending delivery)
- **Restart server and test all operations**

**Commit message:** `feat: webhook management UI`

---

### Iteration 10: Webhooks - Dispatcher
**Goal:** Trigger webhooks on events

**Tasks:**
1. Create `internal/webhook/events.go`:
   ```go
   type Event struct {
       Type      string    `json:"type"`
       Timestamp time.Time `json:"timestamp"`
       Data      any       `json:"data"`
   }

   type PageEventData struct {
       ID        int64  `json:"id"`
       Title     string `json:"title"`
       Slug      string `json:"slug"`
       Status    string `json:"status"`
       AuthorID  int64  `json:"author_id"`
   }

   // Similar for other event types
   ```
2. Create `internal/webhook/dispatcher.go`:
   ```go
   type Dispatcher struct {
       store   *store.Queries
       logger  *slog.Logger
       queue   chan *QueuedDelivery
       workers int
   }

   func (d *Dispatcher) Dispatch(ctx context.Context, eventType string, data any) error
   func (d *Dispatcher) Start(ctx context.Context)
   func (d *Dispatcher) Stop()
   ```
3. Dispatch logic:
   - Find all active webhooks subscribed to event
   - Create delivery record for each
   - Queue for async processing
4. Add dispatch calls to handlers:
   - Page create/update/delete/publish
   - Media upload/delete
   - Form submission
   - User create/delete
5. Include HMAC signature header:
   ```go
   signature := hmac.New(sha256.New, []byte(webhook.Secret))
   signature.Write(payloadBytes)
   headers["X-Webhook-Signature"] = hex.EncodeToString(signature.Sum(nil))
   ```

**Verification:**
- Events dispatch when actions occur
- Delivery records created
- Signature header correct
- **Restart server, create webhook, trigger event**

**Commit message:** `feat: webhook event dispatcher`

---

### Iteration 11: Webhooks - Delivery with Retry
**Goal:** HTTP delivery with exponential backoff

**Tasks:**
1. Create `internal/webhook/delivery.go`:
   ```go
   const (
       MaxAttempts     = 5
       InitialBackoff  = 1 * time.Minute
       MaxBackoff      = 24 * time.Hour
       RequestTimeout  = 30 * time.Second
   )

   func (d *Dispatcher) processDelivery(ctx context.Context, delivery *WebhookDelivery) error
   func calculateBackoff(attempts int) time.Duration
   ```
2. Delivery process:
   - Create HTTP request with:
     - POST method
     - JSON payload
     - Custom headers from webhook
     - X-Webhook-Signature header
     - User-Agent: oCMS/1.0
   - Set timeout (30s)
   - Send request
   - Check response:
     - 2xx: Mark delivered
     - 4xx: Mark dead (client error, don't retry)
     - 5xx: Schedule retry
     - Timeout: Schedule retry
3. Exponential backoff:
   ```go
   // Attempts: 1=1min, 2=2min, 3=4min, 4=8min, 5=dead
   func calculateBackoff(attempts int) time.Duration {
       backoff := InitialBackoff * time.Duration(math.Pow(2, float64(attempts-1)))
       if backoff > MaxBackoff {
           backoff = MaxBackoff
       }
       return backoff
   }
   ```
4. Background worker:
   - Process pending deliveries
   - Respect next_retry_at
   - Run every 30 seconds
5. Dead letter handling:
   - After MaxAttempts, mark as dead
   - Keep in database for inspection
   - Clean up old deliveries (> 30 days) via scheduler

**Verification:**
- Successful delivery marks as delivered
- Failed delivery schedules retry
- Backoff timing correct
- Max attempts leads to dead status
- **Restart server and test with failing webhook URL**

**Commit message:** `feat: webhook delivery with exponential backoff`

---

### Iteration 12: Webhooks - Monitoring
**Goal:** Webhook health monitoring

**Tasks:**
1. Add webhook stats to list:
   - Total deliveries (last 24h)
   - Success rate
   - Last successful delivery
   - Pending count
   - Dead count
2. Add delivery detail view:
   - Full request payload
   - Response body (truncated to 10KB)
   - Headers sent
   - Timing information
3. Add manual retry for dead deliveries:
   - `POST /admin/webhooks/{id}/deliveries/{did}/retry`
   - Resets status to pending
   - Resets attempt count
4. Add webhook health indicator:
   - Green: > 95% success
   - Yellow: 80-95% success
   - Red: < 80% success
5. Dashboard widget:
   - Recent failed deliveries
   - Webhook health summary

**Verification:**
- Stats accurate in list
- Can view delivery details
- Manual retry works
- Health indicators correct
- **Restart server and verify monitoring**

**Commit message:** `feat: webhook monitoring and manual retry`

---

### Iteration 13: Cache - Interface & Memory Implementation
**Goal:** Abstracted cache with in-memory default

**Tasks:**
1. Update `internal/cache/cache.go`:
   ```go
   type Cache interface {
       Get(ctx context.Context, key string) ([]byte, error)
       Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
       Delete(ctx context.Context, key string) error
       Clear(ctx context.Context) error
       Has(ctx context.Context, key string) (bool, error)
   }

   type TypedCache[T any] struct {
       cache Cache
   }

   func (c *TypedCache[T]) Get(ctx context.Context, key string) (*T, error)
   func (c *TypedCache[T]) Set(ctx context.Context, key string, value *T, ttl time.Duration) error
   ```
2. Update `internal/cache/memory.go`:
   ```go
   type MemoryCache struct {
       data    sync.Map
       maxSize int
       stats   CacheStats
   }

   type CacheStats struct {
       Hits   uint64
       Misses uint64
       Size   int
   }

   func NewMemoryCache(maxSize int) *MemoryCache
   func (c *MemoryCache) Stats() CacheStats
   ```
3. Add cache factory:
   ```go
   func NewCache(cfg *config.Config) (Cache, error) {
       if cfg.RedisURL != "" {
           return NewRedisCache(cfg.RedisURL)
       }
       return NewMemoryCache(cfg.CacheMaxSize), nil
   }
   ```
4. Cache common data:
   - Site config: key=`config:site`, TTL=1h
   - Menus: key=`menu:{slug}`, TTL=1h
   - Active theme: key=`theme:active`, TTL=1h
   - Language list: key=`languages:active`, TTL=1h
5. Add cache invalidation on changes

**Verification:**
- Memory cache stores/retrieves correctly
- TTL expires entries
- Invalidation works
- **Restart server and verify caching behavior**

**Commit message:** `feat: cache interface with memory implementation`

---

### Iteration 14: Cache - Redis Implementation (Optional)
**Goal:** Redis cache for production scalability

**Tasks:**
1. Create `internal/cache/redis.go`:
   ```go
   type RedisCache struct {
       client *redis.Client
       prefix string
   }

   func NewRedisCache(url string, prefix string) (*RedisCache, error)
   ```
2. Add environment variable:
   ```
   OCMS_REDIS_URL=redis://localhost:6379/0
   OCMS_CACHE_PREFIX=ocms:
   ```
3. Implement Cache interface for Redis:
   - Use SET with EX for TTL
   - Use GET with proper error handling
   - Use DEL for delete
   - Use KEYS + DEL for clear (with prefix)
4. Add connection health check
5. Graceful fallback to memory if Redis unavailable
6. Add admin cache info:
   - Show cache type (memory/redis)
   - Show stats
   - Clear cache button

**Verification:**
- Redis cache works when configured
- Falls back to memory when unavailable
- Stats display correctly
- **Restart server with Redis configured**

**Commit message:** `feat: Redis cache implementation`

---

### Iteration 15: Export - Schema & Exporter
**Goal:** Export site content to JSON

**Tasks:**
1. Create `internal/transfer/schema.go`:
   ```go
   const ExportVersion = "1.0"

   type ExportData struct {
       Version    string            `json:"version"`
       ExportedAt time.Time         `json:"exported_at"`
       Site       ExportSite        `json:"site"`
       Languages  []ExportLanguage  `json:"languages,omitempty"`
       Users      []ExportUser      `json:"users,omitempty"`
       Pages      []ExportPage      `json:"pages,omitempty"`
       Categories []ExportCategory  `json:"categories,omitempty"`
       Tags       []ExportTag       `json:"tags,omitempty"`
       Media      []ExportMedia     `json:"media,omitempty"`
       Menus      []ExportMenu      `json:"menus,omitempty"`
       Forms      []ExportForm      `json:"forms,omitempty"`
       Config     map[string]string `json:"config,omitempty"`
   }

   // Individual export types...
   ```
2. Create `internal/transfer/exporter.go`:
   ```go
   type Exporter struct {
       store  *store.Queries
       logger *slog.Logger
   }

   type ExportOptions struct {
       IncludeUsers      bool
       IncludePages      bool
       IncludeCategories bool
       IncludeTags       bool
       IncludeMedia      bool  // metadata only
       IncludeMenus      bool
       IncludeForms      bool
       IncludeConfig     bool
       IncludeLanguages  bool
       PageStatus        string // all, published, draft
   }

   func (e *Exporter) Export(ctx context.Context, opts ExportOptions) (*ExportData, error)
   func (e *Exporter) ExportToFile(ctx context.Context, opts ExportOptions, path string) error
   ```
3. Export logic:
   - Resolve references (author by email, categories by slug)
   - Include translation links
   - Handle circular references in categories
   - Export media metadata (not files)
4. JSON output with pretty formatting

**Verification:**
- Export produces valid JSON
- All selected entities included
- References resolved correctly
- **Restart server and test export**

**Commit message:** `feat: JSON export system`

---

### Iteration 16: Export - Admin UI
**Goal:** Export interface in admin

**Tasks:**
1. Create `internal/handler/importexport.go`:
   - `GET /admin/export` — export form
   - `POST /admin/export` — generate and download export
2. Create `web/templates/admin/export.html`:
   - Checkboxes for what to include:
     - [x] Pages (with status filter)
     - [x] Categories
     - [x] Tags
     - [x] Media (metadata)
     - [x] Menus
     - [x] Forms (with submissions option)
     - [x] Users (emails only, no passwords)
     - [x] Site configuration
     - [x] Languages & translations
   - Export button
   - Progress indicator for large exports
3. Generate download:
   - Filename: `ocms-export-{date}.json`
   - Content-Disposition: attachment
   - Content-Type: application/json
4. Add to admin navigation under Config

**Verification:**
- Export form shows all options
- Download works
- JSON file valid
- **Restart server and export test data**

**Commit message:** `feat: export admin UI`

---

### Iteration 17: Import - Importer
**Goal:** Import content from JSON

**Tasks:**
1. Create `internal/transfer/importer.go`:
   ```go
   type Importer struct {
       store  *store.Queries
       db     *sql.DB
       logger *slog.Logger
   }

   type ImportOptions struct {
       DryRun            bool   // validate without importing
       ConflictStrategy  string // skip, overwrite, rename
       ImportUsers       bool
       ImportPages       bool
       ImportCategories  bool
       ImportTags        bool
       ImportMedia       bool
       ImportMenus       bool
       ImportForms       bool
       ImportConfig      bool
       ImportLanguages   bool
   }

   type ImportResult struct {
       Success   bool
       DryRun    bool
       Created   map[string]int  // entity type -> count
       Updated   map[string]int
       Skipped   map[string]int
       Errors    []ImportError
   }

   type ImportError struct {
       Entity  string
       ID      interface{}
       Message string
   }

   func (i *Importer) Import(ctx context.Context, data *ExportData, opts ImportOptions) (*ImportResult, error)
   func (i *Importer) ImportFromFile(ctx context.Context, path string, opts ImportOptions) (*ImportResult, error)
   func (i *Importer) Validate(data *ExportData) []ImportError
   ```
2. Import logic:
   - Validate JSON schema
   - Import in order (languages → users → categories → tags → pages → media → menus → forms)
   - Handle conflicts:
     - Skip: ignore if exists (by slug/email)
     - Overwrite: update if exists
     - Rename: add suffix if slug exists
   - Resolve references (find by slug/email)
   - Create translation links
   - Run in transaction (rollback on error)
3. Support partial imports

**Verification:**
- Import parses JSON correctly
- Validation catches errors
- Conflict strategies work
- Transaction rollback on error
- **Restart server and test import**

**Commit message:** `feat: JSON import system`

---

### Iteration 18: Import - Admin UI
**Goal:** Import interface in admin

**Tasks:**
1. Add import handlers:
   - `GET /admin/import` — import form
   - `POST /admin/import/validate` — validate file
   - `POST /admin/import` — perform import
2. Create `web/templates/admin/import.html`:
   - File upload (JSON only)
   - Validate button (dry run)
   - Validation results display:
     - Entities found
     - Potential conflicts
     - Errors
   - Import options:
     - Conflict strategy dropdown
     - Entity type checkboxes
   - Import button
   - Progress indicator
   - Result summary
3. Two-step process:
   1. Upload and validate
   2. Review and confirm import
4. Show import summary:
   - Created: X pages, Y categories, etc.
   - Skipped: X items
   - Errors: list with details

**Verification:**
- Can upload JSON file
- Validation shows preview
- Import executes correctly
- Results display properly
- **Restart server and test full import/export cycle**

**Commit message:** `feat: import admin UI`

---

### Iteration 19: Import/Export - Media Files
**Goal:** Include media files in export/import

**Tasks:**
1. Update export to include media files:
   - Option: "Include media files"
   - Create zip archive with:
     - `export.json` — the JSON export
     - `media/` — directory with media files
   - Media paths in JSON reference zip paths
2. Update exporter:
   ```go
   func (e *Exporter) ExportWithMedia(ctx context.Context, opts ExportOptions, zipPath string) error
   ```
3. Update import to handle zip:
   - Detect zip vs JSON upload
   - Extract and process
   - Copy media files to uploads directory
   - Create media records with correct paths
4. Progress indication for large archives
5. Size limits and warnings

**Verification:**
- Export with media creates valid zip
- Import from zip restores media files
- Media URLs work after import
- **Restart server and test with media**

**Commit message:** `feat: media files in import/export`

---

### Iteration 20: Taxonomy Translation
**Goal:** Translate categories and tags

**Tasks:**
1. Add language_id to categories and tags (migration):
   ```sql
   ALTER TABLE categories ADD COLUMN language_id INTEGER REFERENCES languages(id);
   ALTER TABLE tags ADD COLUMN language_id INTEGER REFERENCES languages(id);
   
   UPDATE categories SET language_id = (SELECT id FROM languages WHERE is_default = 1);
   UPDATE tags SET language_id = (SELECT id FROM languages WHERE is_default = 1);
   ```
2. Update category/tag forms:
   - Language selector
   - Translations panel (like pages)
3. Update category/tag handlers for translations
4. Frontend:
   - Filter categories/tags by current language
   - Language switcher shows translated taxonomy

**Verification:**
- Can create translated categories
- Can create translated tags
- Frontend filters by language
- **Restart server and test taxonomy translations**

**Commit message:** `feat: taxonomy translation support`

---

### Iteration 21: Menu Translation
**Goal:** Translate menus

**Tasks:**
1. Add language_id to menus and menu_items:
   ```sql
   ALTER TABLE menus ADD COLUMN language_id INTEGER REFERENCES languages(id);
   ALTER TABLE menu_items ADD COLUMN language_id INTEGER REFERENCES languages(id);
   ```
2. Update menu builder:
   - Language selector per menu
   - Create translated menus separately
3. Frontend:
   - Load menu for current language
   - Fallback to default language menu
4. Menu template function:
   ```go
   // Gets menu in current language or falls back
   func getMenuForLanguage(slug, langCode string) *Menu
   ```

**Verification:**
- Can create menu per language
- Frontend loads correct language menu
- Fallback works
- **Restart server and test menu translations**

**Commit message:** `feat: menu translation support`

---

### Iteration 22: Dashboard Updates
**Goal:** Phase 4 dashboard additions

**Tasks:**
1. Update dashboard stats:
   - Languages count
   - Webhook health summary
   - Recent webhook failures
   - Cache hit rate
2. Add language quick switch
3. Add import/export quick links
4. Show translation coverage:
   - "5/10 pages translated to Russian"
5. Update admin header:
   - Language selector
   - Current language indicator

**Verification:**
- All new stats display
- Language switch works
- Quick links functional
- **Restart server and review dashboard**

**Commit message:** `feat: dashboard updates for Phase 4`

---

### Iteration 23: Performance & Optimization
**Goal:** Final performance improvements

**Tasks:**
1. Add database indexes review:
   - Check query plans for slow queries
   - Add missing indexes
2. Optimize translation queries:
   - Batch load translations
   - Cache translation maps
3. Add response compression:
   - Gzip for JSON API responses
   - Skip for already compressed
4. Add request batching for webhooks:
   - Debounce rapid-fire events
   - Batch similar events
5. Add connection pooling optimization
6. Profile and optimize hot paths

**Verification:**
- No slow queries in logs
- Response times acceptable
- Memory usage stable
- **Restart server and performance test**

**Commit message:** `feat: Phase 4 performance optimization`

---

### Iteration 24: Testing & Documentation
**Goal:** Comprehensive tests and documentation

**Tasks:**
1. Write unit tests:
   - Language detection
   - Translation linking
   - Webhook signature
   - Retry backoff calculation
   - Export/import serialization
   - Cache operations
2. Write integration tests:
   - Multi-language content flow
   - Webhook delivery cycle
   - Full import/export cycle
3. Update README.md:
   - Multi-language setup
   - Webhook configuration
   - Import/export usage
   - Redis cache setup
4. Create docs:
   - `docs/multi-language.md`
   - `docs/webhooks.md`
   - `docs/import-export.md`
5. Run `govulncheck` and fix issues
6. End-to-end testing of all features

**Verification:**
- All tests pass
- No vulnerabilities
- Documentation complete
- All features work together
- **Full restart and comprehensive manual testing**

**Commit message:** `feat: Phase 4 tests and documentation`

---

## Additional Routes (Phase 4)

```
# Languages Admin
GET    /admin/languages              # List languages
GET    /admin/languages/new          # New language form
POST   /admin/languages              # Create language
GET    /admin/languages/{id}         # Edit language
PUT    /admin/languages/{id}         # Update language
DELETE /admin/languages/{id}         # Delete language
POST   /admin/languages/{id}/default # Set default

# Page Translations
POST   /admin/pages/{id}/translate/{lang}  # Create translation

# Webhooks Admin
GET    /admin/webhooks               # List webhooks
GET    /admin/webhooks/new           # New webhook form
POST   /admin/webhooks               # Create webhook
GET    /admin/webhooks/{id}          # Edit webhook
PUT    /admin/webhooks/{id}          # Update webhook
DELETE /admin/webhooks/{id}          # Delete webhook
POST   /admin/webhooks/{id}/test     # Test webhook
GET    /admin/webhooks/{id}/deliveries           # Delivery history
POST   /admin/webhooks/{id}/deliveries/{did}/retry  # Retry delivery

# Import/Export Admin
GET    /admin/export                 # Export form
POST   /admin/export                 # Generate export
GET    /admin/import                 # Import form
POST   /admin/import/validate        # Validate import file
POST   /admin/import                 # Perform import

# Frontend (language prefixed)
GET    /{lang}/                      # Homepage in language
GET    /{lang}/{slug}                # Page in language
GET    /{lang}/category/{slug}       # Category in language
GET    /{lang}/tag/{slug}            # Tag in language
```

---

## New Migrations Summary

```
00018_create_languages.sql
00019_create_translations.sql
00020_create_webhooks.sql
00021_create_webhook_deliveries.sql
00022_add_language_to_taxonomy.sql (categories + tags)
00023_add_language_to_menus.sql
```

---

## Environment Variables (Additions)

```
OCMS_REDIS_URL=                      # Optional: redis://localhost:6379/0
OCMS_CACHE_PREFIX=ocms:              # Redis key prefix
OCMS_WEBHOOK_WORKERS=3               # Concurrent webhook deliveries
OCMS_WEBHOOK_TIMEOUT=30s             # Webhook request timeout
OCMS_DEFAULT_LANGUAGE=en             # Default language code
OCMS_ADMIN_LANGUAGE=en               # Admin UI language
```

---

## Success Criteria

Phase 4 is complete when:
- [ ] Languages can be configured
- [ ] Content can be translated (pages, categories, tags)
- [ ] Translation links work correctly
- [ ] Language switcher works on frontend
- [ ] Admin UI localized (English + Russian)
- [ ] Webhooks can be created and configured
- [ ] Webhook events dispatch correctly
- [ ] Retry logic with exponential backoff works
- [ ] Dead letter queue captures failed deliveries
- [ ] Manual retry works
- [ ] Cache abstraction works (memory + Redis)
- [ ] Export generates valid JSON/zip
- [ ] Import restores content correctly
- [ ] Media files included in export/import
- [ ] All Phase 1-3 features still work
- [ ] No regressions
- [ ] Tests pass
- [ ] No vulnerabilities
- [ ] Documentation complete

---

## Per-Iteration Checklist

Before marking any iteration complete, verify:
- [ ] Code compiles: `go build ./...`
- [ ] **Server restarted**: stop, rebuild, start fresh
- [ ] Feature works as expected (manual testing)
- [ ] No console errors in browser
- [ ] No panics or unhandled errors
- [ ] Tests pass (if applicable): `go test ./...`
- [ ] Ready for commit with descriptive message
- [ ] **STOP and wait for confirmation before next iteration**

---

## Important Reminders

1. **Always restart server before final testing**
2. **Stop after each iteration** — do not proceed to next without confirmation
3. Test thoroughly before presenting
4. Check for regressions in existing features
5. Verify database migrations apply cleanly
6. Test multi-language features with at least 2 languages
7. Test webhooks with external endpoint (use webhook.site or similar)

---

Begin with **Iteration 1**. Complete it fully, restart server, test, then **STOP and wait for my confirmation** before proceeding to Iteration 2.
