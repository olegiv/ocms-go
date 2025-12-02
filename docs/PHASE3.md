# Opossum CMS (oCMS) — Phase 3 Implementation Prompt

## Phase Overview

**Phase 3: Polish & Extensibility** focuses on making oCMS production-ready with a module system, theme switching, SEO tools, scheduled publishing, full-text search, and a REST API.

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

## Technology Stack (Additions to Phase 2)

| Component | Technology |
|-----------|------------|
| Full-text Search | SQLite FTS5 (built-in) |
| Scheduled Tasks | `robfig/cron/v3` |
| API Authentication | API keys (database-stored) |
| Sitemap Generation | Custom implementation |
| Rate Limiting | `golang.org/x/time/rate` |

---

## Project Structure (Additions)

```
opossum/
├── internal/
│   ├── handler/
│   │   ├── ...existing...
│   │   └── api/                    # REST API handlers
│   │       ├── pages.go
│   │       ├── media.go
│   │       ├── taxonomy.go
│   │       └── auth.go
│   ├── model/
│   │   ├── ...existing...
│   │   ├── api_key.go
│   │   └── scheduled_task.go
│   ├── store/
│   │   ├── queries/
│   │   │   ├── ...existing...
│   │   │   ├── api_keys.sql
│   │   │   └── search.sql
│   ├── module/                     # Module system
│   │   ├── registry.go             # Module registration
│   │   ├── module.go               # Module interface
│   │   └── hooks.go                # Hook system
│   ├── theme/                      # Theme system
│   │   ├── manager.go              # Theme loading/switching
│   │   ├── theme.go                # Theme interface
│   │   └── functions.go            # Theme template functions
│   ├── scheduler/
│   │   └── scheduler.go            # Cron-based task scheduler
│   └── seo/
│       ├── sitemap.go              # Sitemap generation
│       └── robots.go               # robots.txt generation
├── migrations/
│   ├── ...existing...
│   ├── 00014_create_api_keys.sql
│   ├── 00015_add_seo_fields.sql
│   ├── 00016_add_scheduled_publish.sql
│   └── 00017_create_search_index.sql
├── modules/                        # Custom modules directory
│   └── example/                    # Example module
│       ├── module.go
│       ├── handlers.go
│       ├── models.go
│       └── migrations/
├── themes/                         # Theme directory
│   ├── default/                    # Default theme
│   │   ├── theme.json              # Theme metadata
│   │   ├── templates/
│   │   │   ├── layouts/
│   │   │   │   └── base.html
│   │   │   ├── pages/
│   │   │   │   ├── home.html
│   │   │   │   ├── page.html
│   │   │   │   └── list.html
│   │   │   └── partials/
│   │   │       ├── header.html
│   │   │       ├── footer.html
│   │   │       └── sidebar.html
│   │   └── static/
│   │       ├── css/
│   │       ├── js/
│   │       └── images/
│   └── developer/                  # Alternative theme example
│       ├── theme.json
│       ├── templates/
│       └── static/
```

---

## Phase 3 Entities

### APIKey
```go
type APIKey struct {
    ID          int64
    Name        string     // descriptive name
    Key         string     // the actual key (hashed for storage)
    KeyPrefix   string     // first 8 chars for identification
    Permissions string     // JSON array of permissions
    LastUsedAt  *time.Time
    ExpiresAt   *time.Time // nullable, for non-expiring keys
    IsActive    bool
    CreatedBy   int64
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

### Page (SEO additions)
```go
// Add to existing Page struct
type PageSEO struct {
    MetaTitle       string  // custom title tag
    MetaDescription string  // meta description
    MetaKeywords    string  // meta keywords (optional)
    OGImage         *int64  // media ID for og:image
    NoIndex         bool    // robots noindex
    NoFollow        bool    // robots nofollow
    CanonicalURL    string  // custom canonical URL
    ScheduledAt     *time.Time // scheduled publish time
}
```

### Theme
```go
type ThemeConfig struct {
    Name        string            `json:"name"`
    Version     string            `json:"version"`
    Author      string            `json:"author"`
    Description string            `json:"description"`
    Screenshot  string            `json:"screenshot"`
    Templates   map[string]string `json:"templates"`  // template overrides
    Settings    []ThemeSetting    `json:"settings"`   // configurable options
}

type ThemeSetting struct {
    Key         string `json:"key"`
    Label       string `json:"label"`
    Type        string `json:"type"`    // text, color, image, select
    Default     string `json:"default"`
    Options     []string `json:"options,omitempty"`
}
```

### Module
```go
type Module interface {
    // Metadata
    Name() string
    Version() string
    Description() string
    
    // Lifecycle
    Init(app *App) error
    Migrations() []Migration
    
    // Routes
    RegisterRoutes(r chi.Router)
    RegisterAdminRoutes(r chi.Router)
    
    // Hooks
    RegisterHooks(h *HookRegistry)
    
    // Templates
    TemplateFuncs() template.FuncMap
}
```

---

## Iteration Plan

### Iteration 1: Theme System - Foundation
**Goal:** Theme loading and switching infrastructure

**Tasks:**
1. Create `internal/theme/theme.go`:
   ```go
   type Theme struct {
       Path       string
       Config     ThemeConfig
       Templates  *template.Template
       StaticPath string
   }
   
   type Manager struct {
       themesDir    string
       activeTheme  *Theme
       themes       map[string]*Theme
       adminRender  *render.Render  // keep admin separate
   }
   
   func (m *Manager) LoadThemes() error
   func (m *Manager) SetActiveTheme(name string) error
   func (m *Manager) GetTheme(name string) (*Theme, error)
   func (m *Manager) ListThemes() []*ThemeConfig
   func (m *Manager) RenderPage(w io.Writer, template string, data any) error
   ```
2. Create `internal/theme/manager.go`:
   - Scan themes directory
   - Load theme.json for each theme
   - Parse theme templates
   - Handle template inheritance within theme
3. Create `themes/default/theme.json`:
   ```json
   {
       "name": "Default",
       "version": "1.0.0",
       "author": "oCMS",
       "description": "Clean, minimal default theme",
       "screenshot": "screenshot.png",
       "templates": {
           "home": "pages/home.html",
           "page": "pages/page.html",
           "list": "pages/list.html",
           "404": "pages/404.html"
       },
       "settings": [
           {
               "key": "primary_color",
               "label": "Primary Color",
               "type": "color",
               "default": "#3b82f6"
           },
           {
               "key": "show_sidebar",
               "label": "Show Sidebar",
               "type": "select",
               "default": "yes",
               "options": ["yes", "no"]
           }
       ]
   }
   ```
4. Add config option for active theme:
   - Add `theme` to site config
   - Default to "default"
5. Update render package to use theme manager for frontend

**Verification:**
- Themes directory scanned on startup
- Theme config loaded correctly
- Theme can be retrieved by name
- **Restart server and verify**

**Commit message:** `feat: theme system foundation`

---

### Iteration 2: Theme Templates - Default Theme
**Goal:** Complete default theme with all templates

**Tasks:**
1. Create `themes/default/templates/layouts/base.html`:
   - HTML5 doctype
   - Head with meta, title, CSS
   - Body with header, main, footer
   - Template blocks for content
2. Create `themes/default/templates/partials/`:
   - `header.html` — site header with navigation
   - `footer.html` — site footer
   - `sidebar.html` — optional sidebar
   - `pagination.html` — page navigation
3. Create `themes/default/templates/pages/`:
   - `home.html` — homepage template
   - `page.html` — single page view
   - `list.html` — page listing (blog/archive style)
   - `404.html` — not found page
   - `category.html` — category archive
   - `tag.html` — tag archive
4. Create `themes/default/static/`:
   - `css/theme.css` — theme styles
   - `js/theme.js` — theme JavaScript (minimal)
5. Serve theme static files at `/themes/{themeName}/static/*`

**Verification:**
- All templates parse without error
- Static files served correctly
- **Restart server and verify theme loads**

**Commit message:** `feat: default theme templates`

---

### Iteration 3: Theme System - Admin UI
**Goal:** Theme selection in admin

**Tasks:**
1. Create `internal/handler/themes.go`:
   - `GET /admin/themes` — list available themes
   - `POST /admin/themes/activate` — activate theme
   - `GET /admin/themes/{name}/settings` — theme settings
   - `PUT /admin/themes/{name}/settings` — save theme settings
2. Create `web/templates/admin/themes_list.html`:
   - Grid of theme cards
   - Screenshot preview
   - Name, author, description
   - "Activate" button
   - "Settings" button (if theme has settings)
   - Active indicator
3. Create `web/templates/admin/themes_settings.html`:
   - Dynamic form based on theme.json settings
   - Color picker for color type
   - File upload for image type
   - Save button
4. Store theme settings in config table:
   - Key: `theme_settings_{themeName}`
   - Value: JSON object
5. Add to admin navigation under Config section

**Verification:**
- Theme list shows all themes
- Can activate different theme
- Theme settings form works
- **Restart server and verify theme switch persists**

**Commit message:** `feat: theme management admin UI`

---

### Iteration 4: Theme System - Second Theme
**Goal:** Create alternative theme to prove system works

**Tasks:**
1. Create `themes/developer/theme.json`:
   ```json
   {
       "name": "Developer",
       "version": "1.0.0",
       "author": "oCMS",
       "description": "Dark theme for developers and technical blogs",
       "screenshot": "screenshot.png",
       "templates": {},
       "settings": [
           {
               "key": "code_theme",
               "label": "Code Highlighting Theme",
               "type": "select",
               "default": "monokai",
               "options": ["monokai", "github", "dracula"]
           }
       ]
   }
   ```
2. Create complete template set for developer theme:
   - Different layout structure
   - Dark color scheme
   - Monospace fonts
   - Code-friendly styling
3. Create theme-specific static files
4. Test switching between themes
5. Verify both themes render correctly

**Verification:**
- Can switch between default and developer themes
- Each theme has distinct appearance
- Theme settings work independently
- **Restart server and verify both themes work**

**Commit message:** `feat: developer theme`

---

### Iteration 5: Public Frontend Routes
**Goal:** Render pages using active theme

**Tasks:**
1. Create `internal/handler/frontend.go`:
   - `GET /` — homepage
   - `GET /{slug}` — page by slug
   - `GET /category/{slug}` — category archive
   - `GET /tag/{slug}` — tag archive
   - `GET /page/{page}` — pagination
2. Build page data for templates:
   ```go
   type PageData struct {
       Page       *model.Page
       Categories []*model.Category
       Tags       []*model.Tag
       Author     *model.User
       Related    []*model.Page
       SEO        *PageSEO
   }
   
   type ListData struct {
       Pages      []*model.Page
       Category   *model.Category  // if filtered
       Tag        *model.Tag       // if filtered
       Pagination *Pagination
   }
   
   type SiteData struct {
       SiteName    string
       Description string
       Menu        *Menu
       Theme       *ThemeConfig
       Settings    map[string]string
   }
   ```
3. Pass site-wide data to all templates
4. Render using active theme templates
5. Handle 404 with theme's 404 template
6. Add caching headers for static content

**Verification:**
- Homepage renders with theme
- Individual pages render correctly
- Category/tag archives work
- 404 page uses theme
- **Restart server and test all frontend routes**

**Commit message:** `feat: public frontend with theme rendering`

---

### Iteration 6: SEO - Page Fields
**Goal:** SEO metadata for pages

**Tasks:**
1. Create migration `00015_add_seo_fields.sql`:
   ```sql
   -- +goose Up
   ALTER TABLE pages ADD COLUMN meta_title TEXT DEFAULT '';
   ALTER TABLE pages ADD COLUMN meta_description TEXT DEFAULT '';
   ALTER TABLE pages ADD COLUMN meta_keywords TEXT DEFAULT '';
   ALTER TABLE pages ADD COLUMN og_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL;
   ALTER TABLE pages ADD COLUMN no_index BOOLEAN NOT NULL DEFAULT 0;
   ALTER TABLE pages ADD COLUMN no_follow BOOLEAN NOT NULL DEFAULT 0;
   ALTER TABLE pages ADD COLUMN canonical_url TEXT DEFAULT '';
   
   -- +goose Down
   -- SQLite doesn't support DROP COLUMN easily, would need table rebuild
   ```
2. Update page model with SEO fields
3. Update page form with SEO section:
   - Collapsible "SEO Settings" panel
   - Meta title (with character counter, ~60 chars)
   - Meta description (with character counter, ~160 chars)
   - OG Image picker (using media picker)
   - NoIndex/NoFollow checkboxes
   - Canonical URL field
4. Update sqlc queries for SEO fields
5. Add SEO preview in editor (Google-style snippet)

**Verification:**
- SEO fields save correctly
- SEO section shows in page editor
- Character counters work
- **Restart server and verify SEO fields persist**

**Commit message:** `feat: page SEO fields`

---

### Iteration 7: SEO - Meta Rendering
**Goal:** Render SEO meta tags in frontend

**Tasks:**
1. Create `internal/seo/meta.go`:
   ```go
   type Meta struct {
       Title         string
       Description   string
       Keywords      string
       Canonical     string
       OGTitle       string
       OGDescription string
       OGImage       string
       OGType        string
       Robots        string
       TwitterCard   string
   }
   
   func BuildMeta(page *Page, site *SiteConfig) *Meta
   ```
2. Update theme base template:
   - Add meta tags section
   - Open Graph tags
   - Twitter Card tags
   - Canonical link
   - Robots meta
3. Build meta from page SEO fields with fallbacks:
   - Title: meta_title → page title + site name
   - Description: meta_description → truncated body
   - OG Image: og_image → featured_image → site default
4. Add JSON-LD structured data:
   - Article schema for pages
   - BreadcrumbList schema
   - Organization schema on homepage

**Verification:**
- Meta tags render in page source
- Open Graph tags present
- Structured data valid (test with Google Rich Results)
- **Restart server and inspect page source**

**Commit message:** `feat: SEO meta tag rendering`

---

### Iteration 8: SEO - Sitemap
**Goal:** Auto-generated sitemap.xml

**Tasks:**
1. Create `internal/seo/sitemap.go`:
   ```go
   type SitemapURL struct {
       Loc        string
       LastMod    time.Time
       ChangeFreq string  // always, hourly, daily, weekly, monthly, yearly, never
       Priority   float64 // 0.0 to 1.0
   }
   
   func GenerateSitemap(pages []*Page, categories []*Category, tags []*Tag) []byte
   ```
2. Add route `GET /sitemap.xml`:
   - Generate sitemap dynamically
   - Include published pages only
   - Include category archives
   - Include tag archives
   - Respect noindex pages (exclude them)
3. Add sitemap index for large sites (optional):
   - `/sitemap-pages.xml`
   - `/sitemap-categories.xml`
4. Cache sitemap (regenerate on content change or hourly)
5. Add sitemap URL to robots.txt

**Verification:**
- `/sitemap.xml` returns valid XML
- Only published, indexable pages included
- Validate with online sitemap validator
- **Restart server and test sitemap**

**Commit message:** `feat: sitemap.xml generation`

---

### Iteration 9: SEO - Robots.txt
**Goal:** Configurable robots.txt

**Tasks:**
1. Create `internal/seo/robots.go`:
   ```go
   func GenerateRobots(config *SiteConfig) string
   ```
2. Add route `GET /robots.txt`:
   - Dynamic generation
   - Include sitemap reference
   - Disallow admin paths
   - Respect site-wide settings
3. Add config options:
   - `robots_txt_extra` — additional rules
   - `robots_disallow_all` — block all crawlers (for staging)
4. Add admin UI for robots.txt:
   - Preview current robots.txt
   - Add custom rules
   - Toggle "block all" for staging

**Verification:**
- `/robots.txt` returns correct content
- Sitemap referenced
- Admin paths disallowed
- **Restart server and verify robots.txt**

**Commit message:** `feat: robots.txt generation`

---

### Iteration 10: Scheduled Publishing
**Goal:** Publish pages at scheduled time

**Tasks:**
1. Create migration `00016_add_scheduled_publish.sql`:
   ```sql
   -- +goose Up
   ALTER TABLE pages ADD COLUMN scheduled_at DATETIME;
   CREATE INDEX idx_pages_scheduled ON pages(scheduled_at) WHERE scheduled_at IS NOT NULL AND status = 'draft';
   
   -- +goose Down
   DROP INDEX idx_pages_scheduled;
   ```
2. Update page model and queries
3. Add scheduler service using `robfig/cron`:
   ```go
   type Scheduler struct {
       cron   *cron.Cron
       store  *store.Queries
       logger *slog.Logger
   }
   
   func (s *Scheduler) Start()
   func (s *Scheduler) Stop()
   func (s *Scheduler) PublishScheduledPages()
   ```
4. Run scheduler check every minute
5. Update page form:
   - Add "Schedule" option alongside "Publish"
   - Date/time picker for scheduled_at
   - Show scheduled status in page list
6. Log scheduled publishes to events

**Verification:**
- Can set scheduled publish time
- Page publishes automatically at scheduled time
- Event logged
- **Restart server, schedule a page for 2 minutes later, wait and verify**

**Commit message:** `feat: scheduled page publishing`

---

### Iteration 11: Full-Text Search - Index
**Goal:** SQLite FTS5 search index

**Tasks:**
1. Create migration `00017_create_search_index.sql`:
   ```sql
   -- +goose Up
   CREATE VIRTUAL TABLE search_index USING fts5(
       title,
       body,
       slug,
       content='pages',
       content_rowid='id'
   );
   
   -- Triggers to keep index in sync
   CREATE TRIGGER pages_ai AFTER INSERT ON pages BEGIN
       INSERT INTO search_index(rowid, title, body, slug)
       VALUES (new.id, new.title, new.body, new.slug);
   END;
   
   CREATE TRIGGER pages_ad AFTER DELETE ON pages BEGIN
       INSERT INTO search_index(search_index, rowid, title, body, slug)
       VALUES ('delete', old.id, old.title, old.body, old.slug);
   END;
   
   CREATE TRIGGER pages_au AFTER UPDATE ON pages BEGIN
       INSERT INTO search_index(search_index, rowid, title, body, slug)
       VALUES ('delete', old.id, old.title, old.body, old.slug);
       INSERT INTO search_index(rowid, title, body, slug)
       VALUES (new.id, new.title, new.body, new.slug);
   END;
   
   -- Initial index population
   INSERT INTO search_index(rowid, title, body, slug)
   SELECT id, title, body, slug FROM pages;
   
   -- +goose Down
   DROP TRIGGER pages_au;
   DROP TRIGGER pages_ad;
   DROP TRIGGER pages_ai;
   DROP TABLE search_index;
   ```
2. Create `internal/store/queries/search.sql`:
   ```sql
   -- name: SearchPages :many
   SELECT p.* FROM pages p
   INNER JOIN search_index si ON si.rowid = p.id
   WHERE search_index MATCH ? AND p.status = 'published'
   ORDER BY rank
   LIMIT ? OFFSET ?;
   
   -- name: CountSearchResults :one
   SELECT COUNT(*) FROM pages p
   INNER JOIN search_index si ON si.rowid = p.id
   WHERE search_index MATCH ? AND p.status = 'published';
   ```
3. Run migration and sqlc generate

**Verification:**
- Migration runs successfully
- FTS5 index created
- Triggers work (insert a page, check index)
- **Restart server and verify index populated**

**Commit message:** `feat: full-text search index with FTS5`

---

### Iteration 12: Full-Text Search - UI
**Goal:** Search functionality in frontend and admin

**Tasks:**
1. Add frontend search:
   - `GET /search?q={query}` — search results page
   - Search form in theme header
   - Results page with pagination
   - Highlight matched terms in results
2. Add admin search:
   - `GET /admin/pages?search={query}` — filter pages
   - Quick search in admin header
   - Search across pages, with results dropdown
3. Create search results template in theme
4. Sanitize search input (prevent FTS5 injection)
5. Add search suggestions (optional, recent searches)

**Verification:**
- Frontend search returns results
- Admin search filters pages
- Pagination works
- No errors on special characters
- **Restart server and test search functionality**

**Commit message:** `feat: search UI for frontend and admin`

---

### Iteration 13: Module System - Foundation
**Goal:** Module registration and lifecycle

**Tasks:**
1. Create `internal/module/module.go`:
   ```go
   type Module interface {
       Name() string
       Version() string
       Description() string
       Dependencies() []string
       
       Init(ctx *ModuleContext) error
       RegisterRoutes(r chi.Router)
       RegisterAdminRoutes(r chi.Router)
       TemplateFuncs() template.FuncMap
       Shutdown() error
   }
   
   type ModuleContext struct {
       DB        *sql.DB
       Store     *store.Queries
       Logger    *slog.Logger
       Config    *config.Config
       Render    *render.Render
       Events    *service.EventService
   }
   ```
2. Create `internal/module/registry.go`:
   ```go
   type Registry struct {
       modules map[string]Module
       order   []string  // initialization order
   }
   
   func (r *Registry) Register(m Module) error
   func (r *Registry) Get(name string) (Module, bool)
   func (r *Registry) InitAll(ctx *ModuleContext) error
   func (r *Registry) ShutdownAll() error
   func (r *Registry) RouteAll(router chi.Router)
   func (r *Registry) AdminRouteAll(router chi.Router)
   func (r *Registry) AllTemplateFuncs() template.FuncMap
   ```
3. Create `internal/module/hooks.go`:
   ```go
   type HookRegistry struct {
       hooks map[string][]HookFunc
   }
   
   type HookFunc func(ctx context.Context, data any) (any, error)
   
   func (h *HookRegistry) Register(name string, fn HookFunc)
   func (h *HookRegistry) Call(name string, ctx context.Context, data any) (any, error)
   
   // Predefined hooks
   const (
       HookPageBeforeSave   = "page.before_save"
       HookPageAfterSave    = "page.after_save"
       HookPageBeforeDelete = "page.before_delete"
       HookUserBeforeSave   = "user.before_save"
       HookRenderPage       = "render.page"
   )
   ```
4. Update main.go to initialize module registry
5. Call hooks at appropriate points in handlers

**Verification:**
- Module registry initializes
- Hooks can be registered
- Hook calls work
- **Restart server and verify no errors**

**Commit message:** `feat: module system foundation`

---

### Iteration 14: Module System - Example Module
**Goal:** Create example module to prove system works

**Tasks:**
1. Create `modules/example/module.go`:
   ```go
   package example
   
   type ExampleModule struct {
       ctx *module.ModuleContext
   }
   
   func New() *ExampleModule {
       return &ExampleModule{}
   }
   
   func (m *ExampleModule) Name() string { return "example" }
   func (m *ExampleModule) Version() string { return "1.0.0" }
   func (m *ExampleModule) Description() string { 
       return "Example module demonstrating the module system" 
   }
   func (m *ExampleModule) Dependencies() []string { return nil }
   
   func (m *ExampleModule) Init(ctx *module.ModuleContext) error {
       m.ctx = ctx
       m.ctx.Logger.Info("Example module initialized")
       return nil
   }
   
   func (m *ExampleModule) RegisterRoutes(r chi.Router) {
       r.Get("/example", m.handleExample)
   }
   
   func (m *ExampleModule) RegisterAdminRoutes(r chi.Router) {
       r.Get("/example", m.handleAdminExample)
   }
   
   func (m *ExampleModule) TemplateFuncs() template.FuncMap {
       return template.FuncMap{
           "exampleFunc": func() string { return "Hello from example module" },
       }
   }
   
   func (m *ExampleModule) Shutdown() error {
       m.ctx.Logger.Info("Example module shutting down")
       return nil
   }
   ```
2. Create handlers in `modules/example/handlers.go`
3. Register example module in main.go:
   ```go
   import "github.com/user/opossum/modules/example"
   
   // In main():
   moduleRegistry.Register(example.New())
   ```
4. Add admin UI to list registered modules:
   - `GET /admin/modules` — list modules
   - Show name, version, description, status
5. Document module creation process

**Verification:**
- Example module loads
- Routes accessible (`/example`, `/admin/example`)
- Template function works
- Module shows in admin list
- **Restart server and test module functionality**

**Commit message:** `feat: example module implementation`

---

### Iteration 15: Module System - Migrations Support
**Goal:** Modules can have their own migrations

**Tasks:**
1. Update Module interface:
   ```go
   type Migration struct {
       Version     int64
       Description string
       Up          func(db *sql.DB) error
       Down        func(db *sql.DB) error
   }
   
   func (m *Module) Migrations() []Migration
   ```
2. Create module migration runner:
   - Track module migrations separately
   - Table: `module_migrations (module, version, applied_at)`
   - Run pending migrations on Init
3. Update example module with a migration:
   ```go
   func (m *ExampleModule) Migrations() []module.Migration {
       return []module.Migration{
           {
               Version:     1,
               Description: "Create example_items table",
               Up: func(db *sql.DB) error {
                   _, err := db.Exec(`
                       CREATE TABLE example_items (
                           id INTEGER PRIMARY KEY AUTOINCREMENT,
                           name TEXT NOT NULL,
                           created_at DATETIME DEFAULT CURRENT_TIMESTAMP
                       )
                   `)
                   return err
               },
               Down: func(db *sql.DB) error {
                   _, err := db.Exec(`DROP TABLE example_items`)
                   return err
               },
           },
       }
   }
   ```
4. Show migration status in admin modules list

**Verification:**
- Module migrations run on startup
- Migration tracked in database
- Can add new migrations to module
- **Restart server and verify migration ran**

**Commit message:** `feat: module migration support`

---

### Iteration 16: REST API - Foundation
**Goal:** API infrastructure with authentication

**Tasks:**
1. Create migration `00014_create_api_keys.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE api_keys (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       key_hash TEXT NOT NULL,
       key_prefix TEXT NOT NULL,
       permissions TEXT NOT NULL DEFAULT '[]',
       last_used_at DATETIME,
       expires_at DATETIME,
       is_active BOOLEAN NOT NULL DEFAULT 1,
       created_by INTEGER NOT NULL REFERENCES users(id),
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );
   
   CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix);
   CREATE INDEX idx_api_keys_active ON api_keys(is_active);
   
   -- +goose Down
   DROP TABLE api_keys;
   ```
2. Create `internal/model/api_key.go`
3. Create `internal/store/queries/api_keys.sql`
4. Create `internal/handler/api/auth.go`:
   - API key validation middleware
   - Permission checking
   - Rate limiting per key
5. Create `internal/middleware/api.go`:
   ```go
   func APIKeyAuth(store *store.Queries) func(next http.Handler) http.Handler
   func APIRateLimit(rps float64) func(next http.Handler) http.Handler
   ```
6. Mount API routes at `/api/v1/`:
   - Public routes (no auth): GET endpoints
   - Protected routes (API key required): POST/PUT/DELETE

**Verification:**
- API key table created
- Middleware validates API key header
- Invalid/missing key returns 401
- **Restart server and test with curl**

**Commit message:** `feat: REST API foundation with API keys`

---

### Iteration 17: REST API - Key Management
**Goal:** Admin UI for API keys

**Tasks:**
1. Create `internal/handler/api_keys.go`:
   - `GET /admin/api-keys` — list keys
   - `GET /admin/api-keys/new` — create form
   - `POST /admin/api-keys` — create key
   - `DELETE /admin/api-keys/{id}` — revoke key
2. Create templates:
   - `web/templates/admin/api_keys_list.html`
   - `web/templates/admin/api_keys_form.html`
3. Key creation:
   - Generate random key (32 bytes, base64)
   - Show full key ONCE on creation
   - Store hashed version
   - Keep prefix for identification
4. Permissions selection:
   - `pages:read`, `pages:write`
   - `media:read`, `media:write`
   - `taxonomy:read`, `taxonomy:write`
5. Optional expiration date
6. Add to admin navigation

**Verification:**
- Can create API key
- Key shown once, then only prefix visible
- Can revoke key
- Permissions can be set
- **Restart server and create a test key**

**Commit message:** `feat: API key management UI`

---

### Iteration 18: REST API - Pages Endpoints
**Goal:** CRUD API for pages

**Tasks:**
1. Create `internal/handler/api/pages.go`:
   ```
   GET    /api/v1/pages           # List pages (public: published only)
   GET    /api/v1/pages/{id}      # Get page (public: published only)
   GET    /api/v1/pages/slug/{s}  # Get by slug (public)
   POST   /api/v1/pages           # Create page (auth: pages:write)
   PUT    /api/v1/pages/{id}      # Update page (auth: pages:write)
   DELETE /api/v1/pages/{id}      # Delete page (auth: pages:write)
   ```
2. API response format:
   ```json
   {
       "data": { ... },
       "meta": {
           "total": 100,
           "page": 1,
           "per_page": 20
       }
   }
   ```
3. Error response format:
   ```json
   {
       "error": {
           "code": "validation_error",
           "message": "Invalid input",
           "details": { "field": "error message" }
       }
   }
   ```
4. Support query parameters:
   - `?status=published|draft`
   - `?category={id}`
   - `?tag={id}`
   - `?page=1&per_page=20`
   - `?sort=created_at&order=desc`
5. Include related data:
   - `?include=author,categories,tags`

**Verification:**
- Public GET returns published pages
- Auth required for POST/PUT/DELETE
- Query filters work
- Response format correct
- **Restart server and test with curl**

**Commit message:** `feat: REST API pages endpoints`

---

### Iteration 19: REST API - Media Endpoints
**Goal:** API for media files

**Tasks:**
1. Create `internal/handler/api/media.go`:
   ```
   GET    /api/v1/media           # List media (public)
   GET    /api/v1/media/{id}      # Get media details (public)
   POST   /api/v1/media           # Upload media (auth: media:write)
   PUT    /api/v1/media/{id}      # Update metadata (auth: media:write)
   DELETE /api/v1/media/{id}      # Delete media (auth: media:write)
   ```
2. Media response includes:
   - All metadata
   - URLs for all variants
   - Dimensions
3. Upload via multipart/form-data
4. Support batch upload (multiple files)
5. Filter by type: `?type=image|document|video`

**Verification:**
- Can list media via API
- Can upload via API
- Variants generated
- URLs correct
- **Restart server and test upload with curl**

**Commit message:** `feat: REST API media endpoints`

---

### Iteration 20: REST API - Taxonomy Endpoints
**Goal:** API for tags and categories

**Tasks:**
1. Create `internal/handler/api/taxonomy.go`:
   ```
   # Tags
   GET    /api/v1/tags            # List tags (public)
   GET    /api/v1/tags/{id}       # Get tag (public)
   POST   /api/v1/tags            # Create tag (auth)
   PUT    /api/v1/tags/{id}       # Update tag (auth)
   DELETE /api/v1/tags/{id}       # Delete tag (auth)
   
   # Categories
   GET    /api/v1/categories      # List categories (public, tree)
   GET    /api/v1/categories/{id} # Get category (public)
   POST   /api/v1/categories      # Create category (auth)
   PUT    /api/v1/categories/{id} # Update category (auth)
   DELETE /api/v1/categories/{id} # Delete category (auth)
   ```
2. Categories return nested tree structure:
   ```json
   {
       "data": [
           {
               "id": 1,
               "name": "Tech",
               "children": [
                   { "id": 2, "name": "Go", "children": [] }
               ]
           }
       ]
   }
   ```
3. Include page count per tag/category

**Verification:**
- All endpoints work
- Category tree structure correct
- Page counts accurate
- **Restart server and test endpoints**

**Commit message:** `feat: REST API taxonomy endpoints`

---

### Iteration 21: REST API - Documentation
**Goal:** Auto-generated API documentation

**Tasks:**
1. Create API documentation page:
   - `GET /api/v1/docs` — HTML documentation
   - Or serve static OpenAPI spec
2. Document all endpoints:
   - Method, path
   - Parameters
   - Request body schema
   - Response schema
   - Authentication requirements
   - Example requests/responses
3. Create `web/templates/api/docs.html`:
   - Clean, readable documentation
   - Grouped by resource
   - Try-it functionality (optional)
4. Add link to API docs in admin

**Verification:**
- Documentation page loads
- All endpoints documented
- Examples accurate
- **Restart server and review documentation**

**Commit message:** `feat: REST API documentation`

---

### Iteration 22: Cache Layer
**Goal:** Simple in-memory caching

**Tasks:**
1. Create `internal/cache/cache.go`:
   ```go
   type Cache struct {
       data sync.Map
       ttl  time.Duration
   }
   
   type cacheEntry struct {
       value     any
       expiresAt time.Time
   }
   
   func (c *Cache) Get(key string) (any, bool)
   func (c *Cache) Set(key string, value any)
   func (c *Cache) Delete(key string)
   func (c *Cache) Clear()
   func (c *Cache) StartCleanup(interval time.Duration)
   ```
2. Add caching for:
   - Site config (clear on change)
   - Menus (clear on change)
   - Sitemap (TTL 1 hour)
   - Theme settings (clear on change)
3. Add cache invalidation hooks
4. Add admin cache controls:
   - Show cache stats
   - Clear all cache button

**Verification:**
- Cached values returned correctly
- Cache invalidates on changes
- Clear cache works
- **Restart server and verify caching behavior**

**Commit message:** `feat: in-memory cache layer`

---

### Iteration 23: Performance & Polish
**Goal:** Final optimizations and polish

**Tasks:**
1. Add response compression:
   - Gzip middleware for HTML/JSON
   - Skip for already compressed (images)
2. Add ETag headers for static content
3. Optimize database queries:
   - Add missing indexes
   - Review N+1 queries
4. Add health check endpoint:
   - `GET /health` — returns status
   - Check database connection
   - Check disk space for uploads
5. Add graceful shutdown:
   - Handle SIGTERM/SIGINT
   - Wait for requests to complete
   - Close database connections
   - Stop scheduler
6. Add request timeout middleware

**Verification:**
- Compression working (check headers)
- Health check returns correct status
- Graceful shutdown works
- **Restart server multiple times to verify stability**

**Commit message:** `feat: performance optimizations and graceful shutdown`

---

### Iteration 24: Testing & Documentation
**Goal:** Comprehensive tests and documentation

**Tasks:**
1. Write unit tests:
   - Theme loading
   - Module registration
   - API key validation
   - Search queries
   - Cache operations
2. Write integration tests:
   - Theme switching
   - API endpoints
   - Scheduled publishing
3. Update README.md:
   - Add Phase 3 features
   - Document theme creation
   - Document module development
   - Document API usage
4. Create CHANGELOG.md
5. Run `govulncheck` and fix issues
6. Final end-to-end testing

**Verification:**
- All tests pass
- No vulnerabilities
- Documentation complete
- All features work together
- **Full restart and manual testing of all features**

**Commit message:** `feat: Phase 3 tests and documentation`

---

## Additional Routes (Phase 3)

```
# Frontend (theme-rendered)
GET    /                          # Homepage
GET    /{slug}                    # Page by slug
GET    /category/{slug}           # Category archive
GET    /tag/{slug}                # Tag archive
GET    /search                    # Search results

# SEO
GET    /sitemap.xml               # Sitemap
GET    /robots.txt                # Robots

# Themes Admin
GET    /admin/themes              # List themes
POST   /admin/themes/activate     # Activate theme
GET    /admin/themes/{n}/settings # Theme settings
PUT    /admin/themes/{n}/settings # Save settings

# Modules Admin
GET    /admin/modules             # List modules

# API Keys Admin
GET    /admin/api-keys            # List keys
GET    /admin/api-keys/new        # Create form
POST   /admin/api-keys            # Create key
DELETE /admin/api-keys/{id}       # Revoke key

# REST API v1
GET    /api/v1/docs               # Documentation

## Pages
GET    /api/v1/pages              # List (public)
GET    /api/v1/pages/{id}         # Get (public)
GET    /api/v1/pages/slug/{s}     # Get by slug (public)
POST   /api/v1/pages              # Create (auth)
PUT    /api/v1/pages/{id}         # Update (auth)
DELETE /api/v1/pages/{id}         # Delete (auth)

## Media
GET    /api/v1/media              # List (public)
GET    /api/v1/media/{id}         # Get (public)
POST   /api/v1/media              # Upload (auth)
PUT    /api/v1/media/{id}         # Update (auth)
DELETE /api/v1/media/{id}         # Delete (auth)

## Taxonomy
GET    /api/v1/tags               # List (public)
GET    /api/v1/tags/{id}          # Get (public)
POST   /api/v1/tags               # Create (auth)
PUT    /api/v1/tags/{id}          # Update (auth)
DELETE /api/v1/tags/{id}          # Delete (auth)
GET    /api/v1/categories         # List tree (public)
GET    /api/v1/categories/{id}    # Get (public)
POST   /api/v1/categories         # Create (auth)
PUT    /api/v1/categories/{id}    # Update (auth)
DELETE /api/v1/categories/{id}    # Delete (auth)

# Health
GET    /health                    # Health check
```

---

## New Migrations Summary

```
00014_create_api_keys.sql
00015_add_seo_fields.sql
00016_add_scheduled_publish.sql
00017_create_search_index.sql
```

---

## Environment Variables (Additions)

```
OCMS_THEMES_DIR=./themes
OCMS_ACTIVE_THEME=default
OCMS_API_RATE_LIMIT=100          # requests per minute
OCMS_CACHE_TTL=3600              # seconds
```

---

## Success Criteria

Phase 3 is complete when:
- [ ] Theme system works with multiple themes
- [ ] Can switch themes site-wide
- [ ] Theme settings configurable
- [ ] All SEO fields functional
- [ ] Sitemap and robots.txt generated
- [ ] Scheduled publishing works
- [ ] Full-text search works
- [ ] Module system loads modules
- [ ] Example module functional
- [ ] Module migrations work
- [ ] REST API authenticated correctly
- [ ] All API endpoints functional
- [ ] API documentation available
- [ ] Cache layer working
- [ ] Graceful shutdown implemented
- [ ] Health check endpoint works
- [ ] All Phase 1 & 2 features still work
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

---

Begin with **Iteration 1**. Complete it fully, restart server, test, then **STOP and wait for my confirmation** before proceeding to Iteration 2.
