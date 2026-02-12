# Custom Content Directory

This directory contains user-created content that extends or customizes oCMS.

## Directory Structure

```
custom/
├── themes/     # Custom themes (override or extend core themes)
├── modules/    # Custom modules (future use)
└── README.md   # This file
```

## Themes

Place custom themes in `custom/themes/`. Each theme should have its own directory.

### Sample Theme: Starter

A complete sample custom theme is included at `custom/themes/starter/`. It demonstrates:
- Full template coverage (home, page, list, 404, category, tag, search, form)
- Card-based grid layout with magazine-style typography
- Theme settings (accent color, sidebar toggle, hero style)
- Widget areas (sidebar, footer columns)
- Multi-language support (English, Russian)
- Responsive design with mobile navigation

Activate it with: `OCMS_ACTIVE_THEME=starter`

See `custom/themes/starter/README.md` for full documentation.

### Theme Structure

```
custom/themes/
└── mytheme/
    ├── theme.json       # Theme configuration
    ├── templates/
    │   ├── layouts/
    │   │   └── base.html
    │   ├── pages/
    │   │   ├── home.html
    │   │   ├── page.html
    │   │   └── 404.html
    │   └── partials/
    │       ├── header.html
    │       └── footer.html
    ├── static/
    │   ├── css/
    │   └── js/
    └── locales/         # Optional translations
        ├── en/
        │   └── messages.json
        └── ru/
            └── messages.json
```

### Overriding Core Themes

To customize a core theme (default, developer), create a theme with the same name:

```
custom/themes/default/    # Overrides the embedded 'default' theme
```

Custom themes with the same name as core themes take priority.

## Configuration

Set the custom directory path via environment variable:

```env
OCMS_CUSTOM_DIR=./custom    # Default value
```

## Git

The content of this directory (except .gitkeep files) is gitignored by default.
To version control your custom themes, remove the relevant patterns from `.gitignore`.
