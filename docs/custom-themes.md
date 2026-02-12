# Custom Themes

Custom themes extend or replace oCMS core themes without modifying core code. They live in `custom/themes/` and are loaded automatically at startup.

## Quick Start

Create a custom theme in three steps:

### 1. Create the theme directory

```
custom/themes/mytheme/
├── theme.json              # Theme configuration (required)
├── templates/
│   ├── layouts/
│   │   └── base.html       # Base HTML layout
│   ├── pages/
│   │   ├── home.html       # Homepage
│   │   ├── page.html       # Single page/post
│   │   ├── list.html       # Post listing/archive
│   │   ├── 404.html        # Error page
│   │   ├── category.html   # Category archive
│   │   ├── tag.html        # Tag archive
│   │   ├── search.html     # Search results
│   │   └── form.html       # Public form
│   └── partials/
│       ├── header.html     # Site header
│       ├── footer.html     # Site footer
│       ├── sidebar.html    # Sidebar widgets
│       ├── pagination.html # Page navigation
│       ├── post-card.html  # Reusable card component
│       └── language-switcher.html
├── static/
│   ├── css/theme.css       # Theme styles
│   └── js/theme.js         # Theme scripts
└── locales/                # Optional translation overrides
    ├── en/messages.json
    └── ru/messages.json
```

### 2. Configure the theme

Create `theme.json`:

```json
{
    "name": "My Theme",
    "version": "1.0.0",
    "author": "Your Name",
    "description": "A brief description of the theme",
    "screenshot": "screenshot.svg",
    "templates": {
        "home": "pages/home.html",
        "page": "pages/page.html",
        "list": "pages/list.html",
        "404": "pages/404.html",
        "category": "pages/category.html",
        "tag": "pages/tag.html",
        "search": "pages/search.html"
    },
    "settings": [],
    "widget_areas": []
}
```

### 3. Activate the theme

Set the environment variable:

```env
OCMS_ACTIVE_THEME=mytheme
```

Or activate from **Admin > Themes** in the dashboard.

## How It Works

At startup, the theme manager:

1. Loads embedded core themes (`default`, `developer`) from the binary
2. Scans `custom/themes/` for external theme directories
3. External themes override embedded themes with the same name
4. Parses all templates with the registered function map
5. Loads theme-specific translations from `locales/` directories

Custom themes are filesystem-based — no Go code is needed (unlike custom modules).

## Theme Configuration

### theme.json

The `theme.json` file is the only required file. It defines the theme's metadata, template mappings, settings, and widget areas.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Display name |
| `version` | string | Yes | Semantic version |
| `author` | string | Yes | Theme author |
| `description` | string | Yes | Brief description |
| `screenshot` | string | No | Preview image filename |
| `templates` | object | Yes | Maps page types to template files |
| `settings` | array | No | Configurable theme settings |
| `widget_areas` | array | No | Widget placement areas |

### Template Mapping

The `templates` object maps page types to template file paths (relative to `templates/`):

```json
{
    "templates": {
        "home": "pages/home.html",
        "page": "pages/page.html",
        "list": "pages/list.html",
        "404": "pages/404.html",
        "category": "pages/category.html",
        "tag": "pages/tag.html",
        "search": "pages/search.html"
    }
}
```

### Settings

Theme settings appear in **Admin > Themes > Settings** and allow users to customize the theme without editing code.

Supported setting types: `color`, `select`, `image`, `text`.

```json
{
    "settings": [
        {
            "key": "accent_color",
            "label": "Accent Color",
            "type": "color",
            "default": "#3b82f6"
        },
        {
            "key": "show_sidebar",
            "label": "Show Sidebar",
            "type": "select",
            "default": "yes",
            "options": ["yes", "no"]
        },
        {
            "key": "hero_image",
            "label": "Hero Image",
            "type": "image",
            "default": ""
        }
    ]
}
```

Access settings in templates via `.ThemeSettings`:

```html
{{if eq (index .ThemeSettings "show_sidebar") "yes"}}
    {{template "sidebar.html" .}}
{{end}}
```

### Widget Areas

Define areas where admins can place widgets:

```json
{
    "widget_areas": [
        {
            "id": "sidebar",
            "name": "Sidebar",
            "description": "Widgets displayed in the sidebar"
        },
        {
            "id": "footer-1",
            "name": "Footer Column 1",
            "description": "First footer widget area"
        }
    ]
}
```

Render widgets in templates:

```html
{{range (index .Widgets "sidebar")}}
    {{renderWidget . $.ThemeSettings $.LangCode}}
{{end}}
```

## Templates

### Template Types

Templates are organized into three categories:

**Layouts** (`templates/layouts/`) — Named by relative path (e.g., `layouts/base.html`). The base layout wraps all page content.

**Partials** (`templates/partials/`) — Named by filename only (e.g., `header.html`). Reusable components included with `{{template "header.html" .}}`.

**Pages** (`templates/pages/`) — Each page template defines a `{{define "content"}}` block that gets injected into the base layout. Internally parsed with a `content_` prefix (e.g., `content_home`).

### Base Layout

The base layout (`layouts/base.html`) provides the HTML shell. Required elements:

```html
<!DOCTYPE html>
<html lang="{{.LangCode}}">
<head>
    <!-- SEO meta tags -->
    <meta name="description" content="{{.MetaDescription}}">
    <link rel="canonical" href="{{.Canonical}}">

    <!-- Open Graph -->
    <meta property="og:type" content="{{.OGType}}">
    <meta property="og:title" content="{{.Title}}">

    <!-- Theme assets -->
    <link rel="stylesheet" href="/themes/mytheme/static/css/theme.css">

    <!-- Integration hooks (required) -->
    {{privacyHead}}
    {{analyticsExtHead}}
    {{embedHead}}
</head>
<body>
    {{template "header.html" .}}

    <main>
        {{template "content" .}}
    </main>

    {{template "sidebar.html" .}}
    {{template "footer.html" .}}

    <!-- Integration hooks (required) -->
    {{analyticsExtBody}}
    {{embedBody}}
    {{informerBar}}

    <script src="/themes/mytheme/static/js/theme.js"></script>
</body>
</html>
```

### Page Templates

Each page template must define a `"content"` block:

```html
{{define "content"}}
<article>
    <h1>{{.Page.Title}}</h1>
    <div>{{.Page.Body | safeHTML}}</div>
</article>
{{end}}
```

### Partials

Partials define a named block matching their filename:

```html
{{define "header.html"}}
<header>
    <a href="/">{{.SiteName}}</a>
    <nav>
        {{range buildMenuTree .HeaderMenuItems}}
            <a href="{{.URL}}">{{.Title}}</a>
        {{end}}
    </nav>
</header>
{{end}}
```

### Integration Hooks

Themes must include these function calls for oCMS features to work:

| Hook | Location | Purpose |
|------|----------|---------|
| `{{privacyHead}}` | `<head>` | Privacy consent scripts |
| `{{analyticsExtHead}}` | `<head>` | External analytics head scripts |
| `{{analyticsExtBody}}` | Before `</body>` | External analytics body scripts |
| `{{embedHead}}` | `<head>` | Embedded head content |
| `{{embedBody}}` | Before `</body>` | Embedded body content |
| `{{informerBar}}` | In `<body>` | Informer bar widget |

### Template Data

Common data available in all templates:

| Field | Type | Description |
|-------|------|-------------|
| `.Title` | string | Page title |
| `.SiteName` | string | Site name |
| `.LangCode` | string | Current language code |
| `.LangPrefix` | string | URL prefix for current language |
| `.MetaDescription` | string | Page meta description |
| `.Canonical` | string | Canonical URL |
| `.Page` | object | Current page data |
| `.Pages` | array | List of pages (for list/archive views) |
| `.Categories` | array | Available categories |
| `.Tags` | array | Available tags |
| `.HeaderMenuItems` | array | Header navigation items |
| `.FooterMenuItems` | array | Footer navigation items |
| `.ThemeSettings` | map | Theme setting values |
| `.Widgets` | map | Widgets by area |
| `.Pagination` | object | Pagination state |
| `.SearchQuery` | string | Current search term |
| `.Year` | int | Current year |

## Translations

Theme translations override global translations for specific keys. The `TTheme` template function checks the active theme's translations first, then falls back to the global i18n catalog.

### File Format

Place translations in `locales/{lang}/messages.json`:

```json
{
    "$schema": "../../../../.schema/i18n-schema.json",
    "language": "en",
    "messages": [
        {
            "id": "frontend.read_more",
            "message": "Read more",
            "translation": "Read the full article"
        }
    ]
}
```

Each message has:
- `id` — Translation key (must match keys used in templates)
- `message` — Original English text (reference)
- `translation` — The theme's custom translation

### Using Translations in Templates

Use `TTheme` for theme-aware translations:

```html
<!-- Theme translation with global fallback -->
<a href="{{.Page.Slug}}">{{TTheme .LangCode "frontend.read_more"}}</a>

<!-- Regular global translation (no theme override) -->
<span>{{T .LangCode "nav.dashboard"}}</span>
```

### Supported Languages

oCMS supports `en` (English) and `ru` (Russian). Theme translations for both languages must contain the same set of keys.

### Common Override Keys

Themes typically override these frontend-facing keys:

| Key | Default Text | Purpose |
|-----|-------------|---------|
| `frontend.read_more` | Read more | Post card link |
| `frontend.all_posts` | All Posts | Archive page title |
| `frontend.recent_posts` | Recent Posts | Homepage section |
| `frontend.view_all_posts` | View All Posts | Homepage link |
| `frontend.page_not_found` | Page Not Found | 404 title |
| `frontend.go_home` | Go Home | 404 link |
| `frontend.related_posts` | Related Posts | Post page section |
| `search.title` | Search | Search page title |
| `search.placeholder` | Search... | Search input hint |
| `sidebar.categories` | Categories | Sidebar heading |
| `sidebar.tags` | Tags | Sidebar heading |

## Static Assets

Static files (CSS, JS, images) live in `static/` and are served at `/themes/{name}/static/`.

```html
<!-- CSS -->
<link rel="stylesheet" href="/themes/mytheme/static/css/theme.css">

<!-- JS -->
<script src="/themes/mytheme/static/js/theme.js"></script>

<!-- Images -->
<img src="/themes/mytheme/static/img/logo.svg">
```

### CSS Best Practices

- Use a unique class prefix (e.g., `mt-` for "my theme") to avoid collisions
- Define CSS custom properties in `:root` for easy customization
- Include responsive breakpoints for mobile support
- Use the BEM naming convention within your prefix

### JavaScript Best Practices

- Wrap all code in an IIFE to avoid global namespace pollution
- Use `DOMContentLoaded` for initialization
- Keep dependencies minimal — Alpine.js and htmx are already loaded by oCMS

## Overriding Core Themes

To customize a core theme (`default` or `developer`), create a theme with the same name:

```
custom/themes/default/    # Overrides the embedded 'default' theme
```

The custom version completely replaces the core version. Copy the core theme's templates from `internal/themes/default/` as a starting point.

## Testing

Theme tests verify the theme's structure, configuration, and template parsing.

Run starter theme tests:

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./custom/themes/starter/...
```

Key areas to test:
- `theme.json` is valid and parseable
- All required template files exist
- Page templates define `{{define "content"}}` blocks
- Partials define named blocks matching their filenames
- Base layout includes required HTML structure and integration hooks
- Static assets exist and are non-empty
- Locale files are valid JSON with consistent keys across languages
- Theme loads successfully via the theme manager
- Templates parse without errors
- Translations are loaded and retrievable

## Reference Implementation

See the starter theme at `custom/themes/starter/` for a complete working example with:

- Full template coverage (home, page, list, 404, category, tag, search, form)
- Card-based grid layout with magazine-style typography
- Theme settings (accent color, sidebar toggle, hero style, favicon)
- Widget areas (sidebar, footer columns)
- Multi-language support (English, Russian)
- Responsive design with mobile navigation
- CSS custom properties for easy customization
- Comprehensive test suite

Activate it with: `OCMS_ACTIVE_THEME=starter`
