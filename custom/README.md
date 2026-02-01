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

Place custom themes in `custom/themes/`. Each theme should have its own directory:

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
