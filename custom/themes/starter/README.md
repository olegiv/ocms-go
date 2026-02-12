# Starter Theme

A clean, magazine-style custom theme for oCMS with card-based layouts and warm typography.

## Overview

The Starter theme demonstrates how to create a custom theme for oCMS. It features:

- **Serif headings** with sans-serif body text for a magazine feel
- **Card-based grid layout** for post listings
- **Warm, earthy color palette** with a sage green accent
- **Responsive design** with mobile-first approach
- **Full template coverage** (home, page, list, 404, category, tag, search, form)
- **Multi-language support** with English and Russian translations
- **Theme settings** for accent color, sidebar toggle, and hero style

## Directory Structure

```
starter/
├── theme.json                     # Theme configuration and settings
├── README.md                      # This file
├── templates/
│   ├── layouts/
│   │   └── base.html              # Base layout (HTML shell, head, scripts)
│   ├── pages/
│   │   ├── home.html              # Homepage with hero and post grid
│   │   ├── page.html              # Single page/post with author box
│   │   ├── list.html              # Post archive listing
│   │   ├── 404.html               # Error page
│   │   ├── category.html          # Category archive
│   │   ├── tag.html               # Tag archive
│   │   ├── search.html            # Search results
│   │   └── form.html              # Public form submission
│   └── partials/
│       ├── header.html            # Site header and navigation
│       ├── footer.html            # Site footer with widgets
│       ├── sidebar.html           # Sidebar widgets
│       ├── pagination.html        # Pagination controls
│       ├── language-switcher.html # Language picker dropdown
│       └── post-card.html         # Reusable post card component
├── static/
│   ├── css/
│   │   └── theme.css              # Complete theme stylesheet
│   ├── js/
│   │   └── theme.js               # Theme JavaScript
│   └── screenshot.svg             # Theme preview image
└── locales/
    ├── en/
    │   └── messages.json          # English translation overrides
    └── ru/
        └── messages.json          # Russian translation overrides
```

## Activation

Set the active theme via environment variable:

```bash
OCMS_ACTIVE_THEME=starter
```

Or activate through the admin panel at **Admin > Themes**.

## Theme Settings

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `favicon` | image | - | Custom favicon image |
| `accent_color` | color | `#2d6a4f` | Primary accent color (sage green) |
| `show_sidebar` | select | `yes` | Show or hide the sidebar |
| `hero_style` | select | `gradient` | Hero section style (gradient, minimal, none) |

Settings can be configured at **Admin > Themes > Settings**.

## Widget Areas

| Area | Description |
|------|-------------|
| `sidebar` | Sidebar widgets (categories, tags, recent posts) |
| `footer-1` | First footer column |
| `footer-2` | Second footer column |

## Customization

### CSS Variables

Override any CSS variable to customize the theme appearance:

```css
:root {
    --st-accent: #2d6a4f;       /* Accent color */
    --st-bg: #fafaf8;           /* Background color */
    --st-bg-card: #ffffff;      /* Card background */
    --st-text: #2c2c2c;         /* Text color */
    --st-font-heading: Georgia, serif;
    --st-font-body: system-ui, sans-serif;
}
```

### Adding Translations

Create translation files in `locales/{lang}/messages.json`:

```json
{
    "$schema": "../../../../.schema/i18n-schema.json",
    "language": "fr",
    "messages": [
        {
            "id": "frontend.read_more",
            "message": "Read more",
            "translation": "Lire la suite"
        }
    ]
}
```

Use `TTheme` in templates for theme-aware translations with global fallback:

```html
{{TTheme .LangCode "frontend.read_more"}}
```

## CSS Class Naming

All CSS classes use the `st-` prefix (short for "starter") to avoid collisions
with other themes or admin styles. BEM-like naming is used throughout:

- `.st-card` - Block
- `.st-card__title` - Element
- `.st-card--small` - Modifier
