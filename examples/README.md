# Examples Directory

This directory contains ready-to-use samples for trying oCMS features. Unlike
`custom/`, nothing here is loaded at startup: each sample is used by hand, for
example by uploading it in the admin dashboard. The directory is not shipped in
the Docker image.

## Directory Structure

```
examples/
├── themes/
│   └── tiny-sample.tar.gz   # Minimal HTML theme, ready to import
└── README.md                # This file
```

## Themes

### Tiny Sample

`themes/tiny-sample.tar.gz` is a minimal HTML theme packaged the way the theme
importer expects: one named directory, `tiny-sample/`, with `theme.json`, the
seven required page templates (home, page, list, category, tag, search, 404),
one CSS file, an SVG preview, and a README. It has no JavaScript and no external
asset dependencies. Its identifier `tiny-sample` is distinct from the `starter`
theme installed in `custom/themes/`, so importing it never collides with an
existing theme.

The sample is meant for trying the import flow and for small content sites. It
omits public forms, widgets, custom menus, and theme settings, and its labels are
English only.

#### Try it

1. Open **Admin > Themes**. The import card links to this archive, or download
   it from this directory.
2. Choose the archive, enter your current password, and click **Import**. The
   theme is installed inactive under `custom/themes/tiny-sample/`.
3. Click **Activate** on **Tiny Sample** and open the site.
4. To remove it, activate another theme first, then click **Delete**.

Import and deletion need an administrator account and are blocked in demo mode.
Archives are limited to 20 MiB compressed, 100 MiB extracted, and 2,000 entries,
and an existing theme cannot be replaced by an import.

#### Edit and repackage

Extract the archive, edit the templates and CSS, then rebuild it from the parent
directory so the archive contains exactly one top-level theme directory:

```bash
tar -xzf tiny-sample.tar.gz
# edit tiny-sample/...
COPYFILE_DISABLE=1 tar -czf tiny-sample.tar.gz tiny-sample
```

`COPYFILE_DISABLE=1` keeps macOS from adding `._*` metadata entries; the importer
ignores them, but the archive stays cleaner without them.

## See Also

- [Custom Themes](../docs/custom-themes.md): archive format, theme structure,
  settings, and translations
- [Theme System](https://github.com/olegiv/ocms-go/wiki/Theme-System) on the wiki
- [custom/README.md](../custom/README.md): themes and modules that are loaded at
  startup
