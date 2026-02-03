# Opossum CMS (oCMS) — Phase 2 Implementation Prompt

## Phase Overview

**Phase 2: Content Enrichment** builds upon the Phase 1 foundation to add taxonomy (tags/categories), media library, menu builder, breadcrumbs, and a form builder system.

---

## Critical Development Rules (Same as Phase 1)

### 1. Iterative Development
- **DO NOT implement everything at once**
- Work through iterations sequentially
- Each iteration must be testable and committable independently
- Wait for confirmation before proceeding to next iteration
- **You must do iteration by iteration, test by yourself and stop after each iteration for me to test & commit**

### 2. Dependency Management
- **Always use non-vulnerable module versions**
- Run `go mod tidy` after adding dependencies
- Check for known vulnerabilities with `govulncheck` before committing
- Pin specific versions in go.mod when necessary

### 3. Self-Testing Requirement
- **You must test everything you implement before asking me to test**
- Run the application and verify functionality works
- Check for compilation errors
- Test HTTP endpoints manually (curl or browser)
- Run any unit tests you create
- Only after confirming it works, present it for my review

### 4. Installation Verification
- After adding any dependency, verify:
  - `go build ./...` succeeds
  - `go run ./cmd/ocms` starts without errors
  - Basic functionality works as expected

---

## Technology Stack (Additions to Phase 1)

| Component | Technology |
|-----------|------------|
| Image Processing | libvips via `h2non/bimg` (requires libvips installed) |
| File Storage | Local filesystem (Phase 2), S3-compatible (Phase 3+) |
| UUID Generation | `google/uuid` for media file naming |
| Slug Generation | `gosimple/slug` (if not already using) |

### libvips Installation

**macOS (MacPorts):**
```bash
sudo port install vips
```

**Debian/Ubuntu:**
```bash
sudo apt install libvips-dev
```

**Note:** `bimg` requires CGO. Ensure `CGO_ENABLED=1` for builds.

---

## Project Structure (Additions)

```
opossum/
├── internal/
│   ├── handler/
│   │   ├── ...existing...
│   │   ├── taxonomy.go           # Tags and categories handlers
│   │   ├── media.go              # Media library handlers
│   │   ├── menus.go              # Menu builder handlers
│   │   └── forms.go              # Form builder handlers
│   ├── model/
│   │   ├── ...existing...
│   │   ├── tag.go
│   │   ├── category.go
│   │   ├── media.go
│   │   ├── menu.go
│   │   └── form.go
│   ├── store/
│   │   ├── queries/
│   │   │   ├── ...existing...
│   │   │   ├── tags.sql
│   │   │   ├── categories.sql
│   │   │   ├── media.sql
│   │   │   ├── menus.sql
│   │   │   └── forms.sql
│   ├── service/
│   │   ├── ...existing...
│   │   ├── media.go              # Image processing, file handling
│   │   └── forms.go              # Form submission processing
│   └── imaging/
│       └── processor.go          # libvips wrapper for image operations
├── migrations/
│   ├── ...existing...
│   ├── 00007_create_tags.sql
│   ├── 00008_create_categories.sql
│   ├── 00009_create_media.sql
│   ├── 00010_create_menus.sql
│   ├── 00011_create_forms.sql
│   └── 00012_create_form_submissions.sql
├── web/
│   ├── templates/
│   │   ├── admin/
│   │   │   ├── ...existing...
│   │   │   ├── tags_list.html
│   │   │   ├── tags_form.html
│   │   │   ├── categories_list.html
│   │   │   ├── categories_form.html
│   │   │   ├── media_library.html
│   │   │   ├── media_upload.html
│   │   │   ├── media_edit.html
│   │   │   ├── menus_list.html
│   │   │   ├── menus_form.html
│   │   │   ├── forms_list.html
│   │   │   ├── forms_builder.html
│   │   │   └── forms_submissions.html
│   │   └── partials/
│   │       ├── ...existing...
│   │       ├── breadcrumb.html
│   │       ├── media_picker.html
│   │       └── tag_selector.html
├── uploads/                       # Media storage (gitignored)
│   ├── originals/                 # Original uploaded files
│   ├── thumbnails/                # Generated thumbnails
│   ├── medium/                    # Medium-sized variants
│   └── .gitkeep
```

---

## Phase 2 Entities

### Tag
```go
type Tag struct {
    ID        int64
    Name      string     // display name
    Slug      string     // URL-safe, unique
    CreatedAt time.Time
    UpdatedAt time.Time
}

type PageTag struct {
    PageID int64
    TagID  int64
}
```

### Category
```go
type Category struct {
    ID          int64
    Name        string     // display name
    Slug        string     // URL-safe, unique
    Description string     // optional description
    ParentID    *int64     // nullable, for hierarchy
    Position    int        // sort order within parent
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type PageCategory struct {
    PageID     int64
    CategoryID int64
}
```

### Media
```go
type Media struct {
    ID           int64
    UUID         string     // unique identifier for file paths
    Filename     string     // original filename
    MimeType     string     // image/jpeg, application/pdf, etc.
    Size         int64      // file size in bytes
    Width        *int       // nullable, for images only
    Height       *int       // nullable, for images only
    Alt          string     // alt text for accessibility
    Caption      string     // optional caption
    FolderID     *int64     // nullable, for organization
    UploadedBy   int64      // user ID
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type MediaFolder struct {
    ID        int64
    Name      string
    ParentID  *int64     // nullable, for nested folders
    Position  int        // sort order
    CreatedAt time.Time
}

type MediaVariant struct {
    ID        int64
    MediaID   int64
    Type      string     // thumbnail, medium, large, etc.
    Width     int
    Height    int
    Size      int64      // file size in bytes
    CreatedAt time.Time
}
```

### Menu
```go
type Menu struct {
    ID        int64
    Name      string     // internal name (main, footer, sidebar)
    Slug      string     // unique identifier
    CreatedAt time.Time
    UpdatedAt time.Time
}

type MenuItem struct {
    ID         int64
    MenuID     int64
    ParentID   *int64     // nullable, for nested items
    Title      string     // display text
    URL        string     // can be relative or absolute
    Target     string     // _self, _blank
    PageID     *int64     // nullable, link to internal page
    Position   int        // sort order within parent
    CSSClass   string     // optional custom CSS class
    IsActive   bool       // enabled/disabled
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### Form
```go
type Form struct {
    ID              int64
    Name            string     // internal name
    Slug            string     // URL identifier
    Title           string     // display title
    Description     string     // optional intro text
    SuccessMessage  string     // shown after submission
    EmailTo         string     // notification email (optional)
    IsActive        bool
    CreatedAt       time.Time
    UpdatedAt       time.Time
}

type FormField struct {
    ID           int64
    FormID       int64
    Type         string     // text, email, textarea, select, checkbox, radio, file
    Name         string     // field name (for form data)
    Label        string     // display label
    Placeholder  string     // optional placeholder
    HelpText     string     // optional help text
    Options      string     // JSON array for select/radio/checkbox options
    Validation   string     // JSON object with validation rules
    IsRequired   bool
    Position     int        // sort order
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type FormSubmission struct {
    ID        int64
    FormID    int64
    Data      string     // JSON object with field values
    IPAddress string
    UserAgent string
    IsRead    bool       // for admin tracking
    CreatedAt time.Time
}
```

---

## Image Variant Configuration

Define standard image variants:

```go
var ImageVariants = map[string]struct {
    Width   int
    Height  int
    Quality int
    Crop    bool  // true = crop to exact size, false = fit within bounds
}{
    "thumbnail": {Width: 150, Height: 150, Quality: 80, Crop: true},
    "medium":    {Width: 800, Height: 600, Quality: 85, Crop: false},
    "large":     {Width: 1920, Height: 1080, Quality: 90, Crop: false},
}
```

---

## Iteration Plan

### Iteration 1: Tags - Model & Store
**Goal:** Tag entity with database operations

**Tasks:**
1. Create migration `00007_create_tags.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE tags (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       slug TEXT NOT NULL UNIQUE,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE TABLE page_tags (
       page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
       tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
       PRIMARY KEY (page_id, tag_id)
   );

   CREATE INDEX idx_tags_slug ON tags(slug);
   CREATE INDEX idx_page_tags_page ON page_tags(page_id);
   CREATE INDEX idx_page_tags_tag ON page_tags(tag_id);

   -- +goose Down
   DROP TABLE page_tags;
   DROP TABLE tags;
   ```
2. Create `internal/model/tag.go`
3. Create `internal/store/queries/tags.sql`:
   ```sql
   -- name: CreateTag :one
   INSERT INTO tags (name, slug, created_at, updated_at)
   VALUES (?, ?, ?, ?)
   RETURNING *;

   -- name: GetTagByID :one
   SELECT * FROM tags WHERE id = ?;

   -- name: GetTagBySlug :one
   SELECT * FROM tags WHERE slug = ?;

   -- name: ListTags :many
   SELECT * FROM tags ORDER BY name ASC LIMIT ? OFFSET ?;

   -- name: SearchTags :many
   SELECT * FROM tags WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

   -- name: UpdateTag :one
   UPDATE tags SET name = ?, slug = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteTag :exec
   DELETE FROM tags WHERE id = ?;

   -- name: CountTags :one
   SELECT COUNT(*) FROM tags;

   -- name: AddTagToPage :exec
   INSERT OR IGNORE INTO page_tags (page_id, tag_id) VALUES (?, ?);

   -- name: RemoveTagFromPage :exec
   DELETE FROM page_tags WHERE page_id = ? AND tag_id = ?;

   -- name: GetTagsForPage :many
   SELECT t.* FROM tags t
   INNER JOIN page_tags pt ON pt.tag_id = t.id
   WHERE pt.page_id = ?
   ORDER BY t.name ASC;

   -- name: GetPagesForTag :many
   SELECT p.* FROM pages p
   INNER JOIN page_tags pt ON pt.page_id = p.id
   WHERE pt.tag_id = ?
   ORDER BY p.created_at DESC
   LIMIT ? OFFSET ?;

   -- name: ClearPageTags :exec
   DELETE FROM page_tags WHERE page_id = ?;
   ```
4. Run migration and sqlc generate

**Verification:**
- Migration runs successfully
- sqlc generates without errors
- Generated code compiles

**Commit message:** `feat: tag model and database operations`

---

### Iteration 2: Tags - CRUD Handlers
**Goal:** Complete tag management UI

**Tasks:**
1. Create `internal/handler/taxonomy.go` with tag handlers:
   - `GET /admin/tags` — list tags
   - `GET /admin/tags/new` — new tag form
   - `POST /admin/tags` — create tag
   - `GET /admin/tags/{id}` — edit tag form
   - `PUT /admin/tags/{id}` — update tag
   - `DELETE /admin/tags/{id}` — delete tag
   - `GET /admin/tags/search?q=` — AJAX search for autocomplete
2. Create templates:
   - `web/templates/admin/tags_list.html` — table with pagination
   - `web/templates/admin/tags_form.html` — create/edit form
3. Auto-generate slug from name
4. Validate unique slug
5. Show tag usage count in list (how many pages use this tag)
6. Add to admin navigation

**Verification:**
- Tag list displays correctly
- Can create/edit/delete tags
- Slug auto-generates
- Validation works
- Navigation updated

**Commit message:** `feat: tag management CRUD`

---

### Iteration 3: Tags - Page Integration
**Goal:** Associate tags with pages

**Tasks:**
1. Create `web/templates/partials/tag_selector.html`:
   - Multi-select tag input with autocomplete
   - Create new tags inline
   - Use HTMX for search
2. Update page form (`pages_form.html`):
   - Add tag selector below body editor
   - Pre-populate with existing tags when editing
3. Update page handlers:
   - Save tags on page create/update
   - Clear and re-add tags (simpler than diff)
4. Display tags in page list (as badges)
5. Filter pages by tag (optional, add to page list filters)

**Verification:**
- Can add tags to pages
- Tags save correctly
- Tags display in page list
- Tag autocomplete works
- Can create new tags inline

**Commit message:** `feat: tag integration with pages`

---

### Iteration 4: Categories - Model & Store
**Goal:** Category entity with hierarchy support

**Tasks:**
1. Create migration `00008_create_categories.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE categories (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       slug TEXT NOT NULL UNIQUE,
       description TEXT DEFAULT '',
       parent_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
       position INTEGER NOT NULL DEFAULT 0,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE TABLE page_categories (
       page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
       category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
       PRIMARY KEY (page_id, category_id)
   );

   CREATE INDEX idx_categories_slug ON categories(slug);
   CREATE INDEX idx_categories_parent ON categories(parent_id);
   CREATE INDEX idx_page_categories_page ON page_categories(page_id);
   CREATE INDEX idx_page_categories_category ON page_categories(category_id);

   -- +goose Down
   DROP TABLE page_categories;
   DROP TABLE categories;
   ```
2. Create `internal/model/category.go`
3. Create `internal/store/queries/categories.sql`:
   ```sql
   -- name: CreateCategory :one
   INSERT INTO categories (name, slug, description, parent_id, position, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetCategoryByID :one
   SELECT * FROM categories WHERE id = ?;

   -- name: GetCategoryBySlug :one
   SELECT * FROM categories WHERE slug = ?;

   -- name: ListCategories :many
   SELECT * FROM categories ORDER BY position ASC, name ASC;

   -- name: ListRootCategories :many
   SELECT * FROM categories WHERE parent_id IS NULL ORDER BY position ASC, name ASC;

   -- name: ListChildCategories :many
   SELECT * FROM categories WHERE parent_id = ? ORDER BY position ASC, name ASC;

   -- name: UpdateCategory :one
   UPDATE categories SET name = ?, slug = ?, description = ?, parent_id = ?, position = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteCategory :exec
   DELETE FROM categories WHERE id = ?;

   -- name: CountCategories :one
   SELECT COUNT(*) FROM categories;

   -- name: AddCategoryToPage :exec
   INSERT OR IGNORE INTO page_categories (page_id, category_id) VALUES (?, ?);

   -- name: RemoveCategoryFromPage :exec
   DELETE FROM page_categories WHERE page_id = ? AND category_id = ?;

   -- name: GetCategoriesForPage :many
   SELECT c.* FROM categories c
   INNER JOIN page_categories pc ON pc.category_id = c.id
   WHERE pc.page_id = ?
   ORDER BY c.name ASC;

   -- name: ClearPageCategories :exec
   DELETE FROM page_categories WHERE page_id = ?;

   -- name: GetCategoryPath :many
   WITH RECURSIVE category_path AS (
       SELECT id, name, slug, parent_id, 0 as depth
       FROM categories WHERE id = ?
       UNION ALL
       SELECT c.id, c.name, c.slug, c.parent_id, cp.depth + 1
       FROM categories c
       INNER JOIN category_path cp ON c.id = cp.parent_id
   )
   SELECT * FROM category_path ORDER BY depth DESC;
   ```
4. Run migration and sqlc generate

**Verification:**
- Migration runs successfully
- sqlc generates without errors
- Recursive CTE works for path

**Commit message:** `feat: category model with hierarchy support`

---

### Iteration 5: Categories - CRUD Handlers
**Goal:** Category management with tree display

**Tasks:**
1. Add category handlers to `taxonomy.go`:
   - `GET /admin/categories` — tree view
   - `GET /admin/categories/new` — new category form
   - `POST /admin/categories` — create category
   - `GET /admin/categories/{id}` — edit category form
   - `PUT /admin/categories/{id}` — update category
   - `DELETE /admin/categories/{id}` — delete category
   - `POST /admin/categories/reorder` — reorder via drag-drop (HTMX)
2. Create templates:
   - `web/templates/admin/categories_list.html` — tree view with indentation
   - `web/templates/admin/categories_form.html` — form with parent selector
3. Build tree structure in handler for display
4. Parent selector should show tree with indentation
5. Prevent setting parent to self or descendant
6. Add to admin navigation

**Verification:**
- Category tree displays correctly
- Hierarchy shows proper indentation
- Can create nested categories
- Cannot create circular references
- Reorder works (if implemented)

**Commit message:** `feat: category management with tree view`

---

### Iteration 6: Categories - Page Integration
**Goal:** Associate categories with pages

**Tasks:**
1. Update page form:
   - Add category selector (tree view with checkboxes)
   - Pre-populate with existing categories when editing
2. Update page handlers:
   - Save categories on page create/update
3. Display categories in page list
4. Filter pages by category (dropdown in page list)
5. Show full category path (breadcrumb style) in page editor

**Verification:**
- Can assign categories to pages
- Categories save correctly
- Category filter works
- Path displays correctly

**Commit message:** `feat: category integration with pages`

---

### Iteration 7: Media - Model & Store
**Goal:** Media entity with folders and variants

**Tasks:**
1. Create migration `00009_create_media.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE media_folders (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       parent_id INTEGER REFERENCES media_folders(id) ON DELETE CASCADE,
       position INTEGER NOT NULL DEFAULT 0,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE TABLE media (
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

   CREATE TABLE media_variants (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
       type TEXT NOT NULL,
       width INTEGER NOT NULL,
       height INTEGER NOT NULL,
       size INTEGER NOT NULL,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       UNIQUE(media_id, type)
   );

   CREATE INDEX idx_media_uuid ON media(uuid);
   CREATE INDEX idx_media_folder ON media(folder_id);
   CREATE INDEX idx_media_mime ON media(mime_type);
   CREATE INDEX idx_media_variants_media ON media_variants(media_id);

   -- +goose Down
   DROP TABLE media_variants;
   DROP TABLE media;
   DROP TABLE media_folders;
   ```
2. Create `internal/model/media.go`
3. Create `internal/store/queries/media.sql`:
   ```sql
   -- name: CreateMedia :one
   INSERT INTO media (uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetMediaByID :one
   SELECT * FROM media WHERE id = ?;

   -- name: GetMediaByUUID :one
   SELECT * FROM media WHERE uuid = ?;

   -- name: ListMedia :many
   SELECT * FROM media ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: ListMediaInFolder :many
   SELECT * FROM media WHERE folder_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: ListMediaByType :many
   SELECT * FROM media WHERE mime_type LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: SearchMedia :many
   SELECT * FROM media WHERE filename LIKE ? OR alt LIKE ? ORDER BY created_at DESC LIMIT ?;

   -- name: UpdateMedia :one
   UPDATE media SET filename = ?, alt = ?, caption = ?, folder_id = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteMedia :exec
   DELETE FROM media WHERE id = ?;

   -- name: CountMedia :one
   SELECT COUNT(*) FROM media;

   -- name: CreateMediaVariant :one
   INSERT INTO media_variants (media_id, type, width, height, size, created_at)
   VALUES (?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetMediaVariants :many
   SELECT * FROM media_variants WHERE media_id = ?;

   -- name: CreateMediaFolder :one
   INSERT INTO media_folders (name, parent_id, position, created_at)
   VALUES (?, ?, ?, ?)
   RETURNING *;

   -- name: ListMediaFolders :many
   SELECT * FROM media_folders ORDER BY position ASC, name ASC;

   -- name: DeleteMediaFolder :exec
   DELETE FROM media_folders WHERE id = ?;
   ```
4. Create upload directories structure
5. Run migration and sqlc generate

**Verification:**
- Migration runs successfully
- Upload directories created
- sqlc generates without errors

**Commit message:** `feat: media model and database operations`

---

### Iteration 8: Media - Image Processing Service
**Goal:** libvips integration for image variants

**Tasks:**
1. Install `h2non/bimg`:
   ```bash
   go get github.com/h2non/bimg
   ```
2. Create `internal/imaging/processor.go`:
   ```go
   type Processor struct {
       uploadDir string
   }

   func (p *Processor) ProcessImage(file io.Reader, filename string) (*ProcessResult, error)
   func (p *Processor) CreateVariant(sourcePath string, variant VariantConfig) (*VariantResult, error)
   func (p *Processor) GetImageDimensions(path string) (width, height int, err error)
   func (p *Processor) IsImage(mimeType string) bool
   func (p *Processor) IsSupportedType(mimeType string) bool
   ```
3. Implement image processing:
   - Read uploaded file
   - Detect dimensions
   - Generate variants (thumbnail, medium, large)
   - Preserve EXIF orientation
   - Strip sensitive EXIF data
   - Convert to WebP optionally
4. Create `internal/service/media.go`:
   - Handle file upload
   - Generate UUID
   - Save original
   - Process variants (for images)
   - Store metadata in DB
5. Configure upload limits:
   - Max file size: 20MB
   - Allowed types: images (jpg, png, gif, webp), documents (pdf), video (mp4, webm)

**Verification:**
- Can process test image
- Variants generated correctly
- Dimensions detected
- Error handling works

**Commit message:** `feat: image processing service with libvips`

---

### Iteration 9: Media - Upload & Library UI
**Goal:** Media library interface

**Tasks:**
1. Create `internal/handler/media.go`:
   - `GET /admin/media` — library grid view
   - `GET /admin/media/upload` — upload form
   - `POST /admin/media/upload` — handle upload (multipart)
   - `GET /admin/media/{id}` — edit media details
   - `PUT /admin/media/{id}` — update media details
   - `DELETE /admin/media/{id}` — delete media and files
2. Create templates:
   - `web/templates/admin/media_library.html`:
     - Grid view of media (thumbnails)
     - List view option
     - Folder navigation sidebar
     - Filter by type (images, documents, videos)
     - Search
     - Bulk select
   - `web/templates/admin/media_upload.html`:
     - Drag-drop upload zone
     - Multiple file support
     - Progress indicator
     - HTMX for async upload
   - `web/templates/admin/media_edit.html`:
     - Preview (image/video/icon)
     - Edit alt text, caption
     - Move to folder
     - Show all variants
     - File info (size, dimensions, type)
3. Serve uploaded files:
   - `GET /uploads/originals/{uuid}/{filename}`
   - `GET /uploads/{variant}/{uuid}/{filename}`
4. Add to admin navigation

**Verification:**
- Can upload single file
- Can upload multiple files
- Variants generated for images
- Library displays files
- Can edit media details
- Can delete media (files removed)

**Commit message:** `feat: media library UI with upload`

---

### Iteration 10: Media - Folder Management
**Goal:** Organize media in folders

**Tasks:**
1. Add folder handlers:
   - `POST /admin/media/folders` — create folder
   - `PUT /admin/media/folders/{id}` — rename folder
   - `DELETE /admin/media/folders/{id}` — delete folder
2. Update media library UI:
   - Folder tree in sidebar
   - Create folder button
   - Rename folder (inline edit)
   - Delete folder (with confirmation)
   - Move media to folder (drag-drop or select)
3. Nested folders support
4. Show item count per folder
5. "All Media" and "Uncategorized" virtual folders

**Verification:**
- Can create folders
- Can nest folders
- Can move media between folders
- Delete folder moves contents to parent/root
- Folder navigation works

**Commit message:** `feat: media folder management`

---

### Iteration 11: Media - Picker Integration
**Goal:** Select media from pages/forms

**Tasks:**
1. Create `web/templates/partials/media_picker.html`:
   - Modal dialog
   - Shows media library (grid)
   - Filter and search
   - Select single or multiple
   - Returns selected media ID(s) and URLs
2. Create JavaScript for media picker:
   ```javascript
   // Open picker
   openMediaPicker({
       multiple: false,
       types: ['image/*'],
       onSelect: (media) => { ... }
   });
   ```
3. Update page form:
   - Add "Featured Image" field
   - Use media picker
   - Show thumbnail preview
4. Add featured_image_id to pages table (migration)
5. Display featured image in page list

**Verification:**
- Media picker opens in modal
- Can browse and search media
- Selection returns to parent form
- Featured image saves correctly
- Preview displays

**Commit message:** `feat: media picker integration`

---

### Iteration 12: Menus - Model & Store
**Goal:** Menu entity with nested items

**Tasks:**
1. Create migration `00010_create_menus.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE menus (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       name TEXT NOT NULL,
       slug TEXT NOT NULL UNIQUE,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE TABLE menu_items (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       menu_id INTEGER NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
       parent_id INTEGER REFERENCES menu_items(id) ON DELETE CASCADE,
       title TEXT NOT NULL,
       url TEXT DEFAULT '',
       target TEXT DEFAULT '_self',
       page_id INTEGER REFERENCES pages(id) ON DELETE SET NULL,
       position INTEGER NOT NULL DEFAULT 0,
       css_class TEXT DEFAULT '',
       is_active BOOLEAN NOT NULL DEFAULT 1,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
       updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE INDEX idx_menus_slug ON menus(slug);
   CREATE INDEX idx_menu_items_menu ON menu_items(menu_id);
   CREATE INDEX idx_menu_items_parent ON menu_items(parent_id);
   CREATE INDEX idx_menu_items_page ON menu_items(page_id);

   -- +goose Down
   DROP TABLE menu_items;
   DROP TABLE menus;
   ```
2. Create `internal/model/menu.go`
3. Create `internal/store/queries/menus.sql`:
   ```sql
   -- name: CreateMenu :one
   INSERT INTO menus (name, slug, created_at, updated_at)
   VALUES (?, ?, ?, ?)
   RETURNING *;

   -- name: GetMenuByID :one
   SELECT * FROM menus WHERE id = ?;

   -- name: GetMenuBySlug :one
   SELECT * FROM menus WHERE slug = ?;

   -- name: ListMenus :many
   SELECT * FROM menus ORDER BY name ASC;

   -- name: UpdateMenu :one
   UPDATE menus SET name = ?, slug = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteMenu :exec
   DELETE FROM menus WHERE id = ?;

   -- name: CreateMenuItem :one
   INSERT INTO menu_items (menu_id, parent_id, title, url, target, page_id, position, css_class, is_active, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetMenuItemByID :one
   SELECT * FROM menu_items WHERE id = ?;

   -- name: ListMenuItems :many
   SELECT * FROM menu_items WHERE menu_id = ? ORDER BY position ASC;

   -- name: UpdateMenuItem :one
   UPDATE menu_items SET parent_id = ?, title = ?, url = ?, target = ?, page_id = ?, position = ?, css_class = ?, is_active = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteMenuItem :exec
   DELETE FROM menu_items WHERE id = ?;

   -- name: UpdateMenuItemPosition :exec
   UPDATE menu_items SET parent_id = ?, position = ?, updated_at = ?
   WHERE id = ?;
   ```
4. Run migration and sqlc generate
5. Seed default menus: main, footer

**Verification:**
- Migration runs successfully
- Default menus created
- sqlc generates without errors

**Commit message:** `feat: menu model and database operations`

---

### Iteration 13: Menus - Builder UI
**Goal:** Visual menu builder with drag-drop

**Tasks:**
1. Create `internal/handler/menus.go`:
   - `GET /admin/menus` — list menus
   - `GET /admin/menus/{id}` — menu builder
   - `POST /admin/menus` — create menu
   - `PUT /admin/menus/{id}` — update menu
   - `DELETE /admin/menus/{id}` — delete menu
   - `POST /admin/menus/{id}/items` — add item
   - `PUT /admin/menus/{id}/items/{itemId}` — update item
   - `DELETE /admin/menus/{id}/items/{itemId}` — delete item
   - `POST /admin/menus/{id}/reorder` — reorder items (receives JSON tree)
2. Create templates:
   - `web/templates/admin/menus_list.html` — list of menus
   - `web/templates/admin/menus_form.html` — menu builder:
     - Left panel: available pages, custom link form
     - Right panel: current menu structure (nested sortable)
     - Drag to add items
     - Drag to reorder
     - Click to edit item
     - Nested items (indentation)
3. Use SortableJS (via CDN) for drag-drop:
   ```html
   <script src="https://cdn.jsdelivr.net/npm/sortablejs@1.15.0/Sortable.min.js"></script>
   ```
4. Build nested tree structure for display
5. Handle reorder with HTMX/AJAX

**Verification:**
- Can create new menu
- Can add pages as menu items
- Can add custom links
- Drag-drop reordering works
- Nested items work
- Can edit item properties
- Can delete items

**Commit message:** `feat: menu builder UI with drag-drop`

---

### Iteration 14: Menus - Frontend Rendering
**Goal:** Render menus in templates

**Tasks:**
1. Create template function `getMenu(slug string)`:
   - Returns nested menu structure
   - Caches result (simple in-memory cache)
2. Create `web/templates/partials/menu.html`:
   - Recursive template for nested items
   - Adds active class for current page
   - Respects is_active flag
3. Update base layout to include menus:
   - Header: main menu
   - Footer: footer menu
4. Handle internal page links:
   - If page_id set, generate URL from page slug
   - Mark active based on current URL

**Verification:**
- Menus render in layout
- Nested items display correctly
- Active states work
- Internal links resolve

**Commit message:** `feat: menu rendering in templates`

---

### Iteration 15: Breadcrumbs
**Goal:** Automatic breadcrumb generation

**Tasks:**
1. Create `web/templates/partials/breadcrumb.html`:
   - Home link
   - Current section
   - Current page
   - Schema.org markup for SEO
2. Create breadcrumb data structure:
   ```go
   type Breadcrumb struct {
       Title string
       URL   string
   }
   ```
3. Add breadcrumb helper to render context:
   - For admin: Dashboard > Section > Page
   - For pages: Home > Category > Page
4. Update all admin templates to include breadcrumbs
5. Style breadcrumbs (separator, current item)

**Verification:**
- Breadcrumbs appear on all admin pages
- Correct hierarchy shown
- Links work
- Current page not linked

**Commit message:** `feat: breadcrumb navigation`

---

### Iteration 16: Forms - Model & Store
**Goal:** Form builder entities

**Tasks:**
1. Create migration `00011_create_forms.sql`:
   ```sql
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
   ```
2. Create migration `00012_create_form_submissions.sql`:
   ```sql
   -- +goose Up
   CREATE TABLE form_submissions (
       id INTEGER PRIMARY KEY AUTOINCREMENT,
       form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
       data TEXT NOT NULL,
       ip_address TEXT DEFAULT '',
       user_agent TEXT DEFAULT '',
       is_read BOOLEAN NOT NULL DEFAULT 0,
       created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );

   CREATE INDEX idx_form_submissions_form ON form_submissions(form_id);
   CREATE INDEX idx_form_submissions_read ON form_submissions(is_read);

   -- +goose Down
   DROP TABLE form_submissions;
   ```
3. Create `internal/model/form.go`
4. Create `internal/store/queries/forms.sql`:
   ```sql
   -- name: CreateForm :one
   INSERT INTO forms (name, slug, title, description, success_message, email_to, is_active, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetFormByID :one
   SELECT * FROM forms WHERE id = ?;

   -- name: GetFormBySlug :one
   SELECT * FROM forms WHERE slug = ?;

   -- name: ListForms :many
   SELECT * FROM forms ORDER BY name ASC LIMIT ? OFFSET ?;

   -- name: UpdateForm :one
   UPDATE forms SET name = ?, slug = ?, title = ?, description = ?, success_message = ?, email_to = ?, is_active = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteForm :exec
   DELETE FROM forms WHERE id = ?;

   -- name: CountForms :one
   SELECT COUNT(*) FROM forms;

   -- name: CreateFormField :one
   INSERT INTO form_fields (form_id, type, name, label, placeholder, help_text, options, validation, is_required, position, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetFormFields :many
   SELECT * FROM form_fields WHERE form_id = ? ORDER BY position ASC;

   -- name: UpdateFormField :one
   UPDATE form_fields SET type = ?, name = ?, label = ?, placeholder = ?, help_text = ?, options = ?, validation = ?, is_required = ?, position = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: DeleteFormField :exec
   DELETE FROM form_fields WHERE id = ?;

   -- name: DeleteFormFields :exec
   DELETE FROM form_fields WHERE form_id = ?;

   -- name: CreateFormSubmission :one
   INSERT INTO form_submissions (form_id, data, ip_address, user_agent, is_read, created_at)
   VALUES (?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetFormSubmissions :many
   SELECT * FROM form_submissions WHERE form_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: CountFormSubmissions :one
   SELECT COUNT(*) FROM form_submissions WHERE form_id = ?;

   -- name: CountUnreadSubmissions :one
   SELECT COUNT(*) FROM form_submissions WHERE form_id = ? AND is_read = 0;

   -- name: MarkSubmissionRead :exec
   UPDATE form_submissions SET is_read = 1 WHERE id = ?;

   -- name: DeleteFormSubmission :exec
   DELETE FROM form_submissions WHERE id = ?;
   ```
5. Run migrations and sqlc generate

**Verification:**
- Migrations run successfully
- sqlc generates without errors

**Commit message:** `feat: form builder model and database operations`

---

### Iteration 17: Forms - Builder UI
**Goal:** Visual form builder

**Tasks:**
1. Create `internal/handler/forms.go`:
   - `GET /admin/forms` — list forms
   - `GET /admin/forms/new` — new form
   - `POST /admin/forms` — create form
   - `GET /admin/forms/{id}` — form builder
   - `PUT /admin/forms/{id}` — update form
   - `DELETE /admin/forms/{id}` — delete form
2. Create templates:
   - `web/templates/admin/forms_list.html`:
     - List with submission counts
     - Unread count badge
   - `web/templates/admin/forms_builder.html`:
     - Form settings (name, title, success message, email)
     - Field list (sortable)
     - Add field button → field type selector
     - Edit field modal/panel
     - Field preview
3. Support field types:
   - text (single line)
   - email
   - textarea
   - number
   - select (dropdown)
   - radio (single choice)
   - checkbox (multiple choice)
   - date
   - file (upload)
4. Field editor:
   - Label
   - Name (auto-generate from label)
   - Placeholder
   - Help text
   - Required toggle
   - Options (for select/radio/checkbox)
   - Validation rules (min/max length, pattern)
5. Reorder fields with drag-drop

**Verification:**
- Can create form
- Can add all field types
- Can configure field options
- Can reorder fields
- Preview shows form layout

**Commit message:** `feat: form builder UI`

---

### Iteration 18: Forms - Frontend Rendering
**Goal:** Render forms for submission

**Tasks:**
1. Create form rendering service:
   - Generate HTML form from form definition
   - Add CSRF token
   - Add honeypot field (spam protection)
2. Create public form route:
   - `GET /forms/{slug}` — render form
   - `POST /forms/{slug}` — handle submission
3. Create form template:
   - Render each field type
   - Show validation errors
   - Show success message
   - HTMX for async submission (optional)
4. Validation:
   - Required fields
   - Email format
   - Min/max length
   - File type/size
5. Save submission to database
6. Send notification email (optional, can stub)
7. Add shortcode/template function to embed form in pages

**Verification:**
- Form renders correctly
- All field types work
- Validation works
- Submission saves to DB
- Success message shows
- Can embed in page

**Commit message:** `feat: form frontend rendering and submission`

---

### Iteration 19: Forms - Submission Management
**Goal:** View and manage submissions

**Tasks:**
1. Add handlers:
   - `GET /admin/forms/{id}/submissions` — list submissions
   - `GET /admin/forms/{id}/submissions/{subId}` — view submission
   - `DELETE /admin/forms/{id}/submissions/{subId}` — delete submission
   - `POST /admin/forms/{id}/submissions/export` — export CSV
2. Create template:
   - `web/templates/admin/forms_submissions.html`:
     - Table with submissions
     - Show key fields in columns
     - Unread indicator
     - Click to view full submission
     - Bulk delete
     - Export button
3. Mark as read on view
4. Show submission details:
   - All field values
   - Submission date
   - IP address
   - User agent
5. Export to CSV

**Verification:**
- Submissions list shows all submissions
- Can view submission details
- Unread marking works
- Can delete submissions
- CSV export works

**Commit message:** `feat: form submission management`

---

### Iteration 20: Dashboard Updates
**Goal:** Update dashboard with new stats

**Tasks:**
1. Update dashboard stats:
   - Total media files
   - Total forms
   - Unread form submissions
2. Add recent submissions widget
3. Add quick links to new features
4. Update navigation order:
   - Dashboard
   - Pages
   - Media
   - Categories
   - Tags
   - Menus
   - Forms
   - Events
   - Users
   - Config

**Verification:**
- Dashboard shows all stats
- Recent submissions display
- Navigation order correct

**Commit message:** `feat: dashboard updates for phase 2`

---

### Iteration 21: Testing & Documentation
**Goal:** Tests and documentation for Phase 2

**Tasks:**
1. Write unit tests:
   - Slug generation with special characters
   - Image processing
   - Category tree building
   - Menu tree building
   - Form validation
2. Write integration tests:
   - Tag CRUD
   - Category CRUD with hierarchy
   - Media upload and variants
   - Menu builder operations
   - Form submission
3. Update README.md:
   - Add Phase 2 features
   - Document media configuration
   - Document form builder usage
4. Run `govulncheck` and fix issues
5. Test all features end-to-end

**Verification:**
- All tests pass
- No vulnerabilities
- README updated
- All features work together

**Commit message:** `feat: Phase 2 tests and documentation`

---

## Additional Routes (Phase 2)

```
# Tags
GET    /admin/tags                    # List tags
GET    /admin/tags/new                # New tag form
POST   /admin/tags                    # Create tag
GET    /admin/tags/{id}               # Edit tag form
PUT    /admin/tags/{id}               # Update tag
DELETE /admin/tags/{id}               # Delete tag
GET    /admin/tags/search             # Search tags (AJAX)

# Categories
GET    /admin/categories              # Category tree
GET    /admin/categories/new          # New category form
POST   /admin/categories              # Create category
GET    /admin/categories/{id}         # Edit category form
PUT    /admin/categories/{id}         # Update category
DELETE /admin/categories/{id}         # Delete category
POST   /admin/categories/reorder      # Reorder categories

# Media
GET    /admin/media                   # Media library
GET    /admin/media/upload            # Upload form
POST   /admin/media/upload            # Handle upload
GET    /admin/media/{id}              # Edit media
PUT    /admin/media/{id}              # Update media
DELETE /admin/media/{id}              # Delete media
POST   /admin/media/folders           # Create folder
PUT    /admin/media/folders/{id}      # Update folder
DELETE /admin/media/folders/{id}      # Delete folder
GET    /uploads/*                     # Serve uploaded files

# Menus
GET    /admin/menus                   # List menus
GET    /admin/menus/{id}              # Menu builder
POST   /admin/menus                   # Create menu
PUT    /admin/menus/{id}              # Update menu
DELETE /admin/menus/{id}              # Delete menu
POST   /admin/menus/{id}/items        # Add menu item
PUT    /admin/menus/{id}/items/{iid}  # Update menu item
DELETE /admin/menus/{id}/items/{iid}  # Delete menu item
POST   /admin/menus/{id}/reorder      # Reorder items

# Forms
GET    /admin/forms                   # List forms
GET    /admin/forms/new               # New form
POST   /admin/forms                   # Create form
GET    /admin/forms/{id}              # Form builder
PUT    /admin/forms/{id}              # Update form
DELETE /admin/forms/{id}              # Delete form
GET    /admin/forms/{id}/submissions  # List submissions
GET    /admin/forms/{id}/submissions/{sid}    # View submission
DELETE /admin/forms/{id}/submissions/{sid}    # Delete submission
POST   /admin/forms/{id}/submissions/export   # Export CSV

# Public forms
GET    /forms/{slug}                  # Render form
POST   /forms/{slug}                  # Submit form
```

---

## New Migrations Summary

```
00007_create_tags.sql
00008_create_categories.sql
00009_create_media.sql
00010_create_menus.sql
00011_create_forms.sql
00012_create_form_submissions.sql
00013_add_featured_image_to_pages.sql
```

---

## Environment Variables (Additions)

```
OCMS_UPLOAD_DIR=./uploads
OCMS_MAX_UPLOAD_SIZE=20971520    # 20MB in bytes
OCMS_IMAGE_QUALITY=85
```

---

## Success Criteria

Phase 2 is complete when:
- [ ] Can create/edit/delete tags
- [ ] Can assign tags to pages
- [ ] Can create/edit/delete categories with hierarchy
- [ ] Can assign categories to pages
- [ ] Can upload media files
- [ ] Image variants generated automatically (libvips)
- [ ] Media library with folders works
- [ ] Media picker integrates with page editor
- [ ] Can build menus with drag-drop
- [ ] Menus render in frontend templates
- [ ] Breadcrumbs display on all pages
- [ ] Can create forms with field builder
- [ ] Forms render and accept submissions
- [ ] Can view and export form submissions
- [ ] All Phase 1 features still work
- [ ] No regressions
- [ ] Tests pass
- [ ] No vulnerabilities

---

## Per-Iteration Checklist

Before marking any iteration complete, verify:
- [ ] Code compiles: `go build ./...`
- [ ] Server starts: `go run ./cmd/ocms`
- [ ] Feature works as expected (manual testing)
- [ ] No console errors in browser
- [ ] No panics or unhandled errors
- [ ] Tests pass (if applicable): `go test ./...`
- [ ] Ready for commit with descriptive message

---

Begin with **Iteration 1**. Complete each iteration fully before moving to the next. Ask clarifying questions if any requirements are ambiguous.
