# Custom Content Directory

This directory contains user-created content that extends or customizes oCMS.

## Directory Structure

```
custom/
├── themes/     # Custom themes (override or extend core themes)
├── modules/    # Custom modules (extend oCMS with plugins)
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

## Modules

Place custom modules in `custom/modules/`. Each module is a self-contained Go package.

A reference implementation is included at `custom/modules/bookmarks/`.

To add a custom module:

1. Create your module package in `custom/modules/mymodule/`
2. Add a registration file, one per module:
   ```go
   // custom/modules/imports_mymodule.go
   package modules

   import _ "github.com/olegiv/ocms-go/custom/modules/mymodule"
   ```
3. Build and run — the module appears at **Admin > Modules**

Do not implement `TemplateFuncs()`: every module template function needs a
matching placeholder in `internal/render/render.go`, and editing core is exactly
what `custom/` exists to avoid. Expose data over a route instead.

See `docs/custom-modules.md` for the full guide.

## Configuration

Set the custom directory path via environment variable:

```env
OCMS_CUSTOM_DIR=./custom    # Default value
```

## Git

Most content in this directory is gitignored. The following are tracked:

- `README.md` — this file
- `themes/starter/` — sample custom theme (reference implementation)
- `themes/.gitkeep`, `modules/.gitkeep` — keep the empty directories present
- `modules/bookmarks/` — sample custom module (reference implementation)
- `modules/doc.go` — declares the `modules` package (must exist; never edited)
- `modules/imports_bookmarks.go` — registers the sample module

User-created themes and modules are automatically ignored by git, and so is
your own `modules/imports_<name>.go`. Nothing shared needs editing, so `git
pull` can never conflict over module registration.
