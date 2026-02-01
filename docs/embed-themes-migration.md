# Migration Plan: Embed Core Themes in Binary

This document outlines the migration plan to embed core themes (`default`, `developer`) into the binary and create a unified `custom/` directory for user-created content.

## Goals

1. Ship a single binary with working default themes (no external files required)
2. Clear separation between core and user content
3. Allow users to override core themes without modifying embedded files
4. Simplify deployment and backups

## Current State

```
ocms-go/
├── themes/                  # External, not embedded
│   ├── default/
│   └── developer/
├── modules/                 # Embedded (locales only)
│   ├── analytics_ext/
│   ├── developer/
│   └── ...
└── data/                    # Runtime data
    └── ocms.db
```

**Problems:**
- Binary requires external `themes/` directory to function
- No clear place for user-created themes/modules
- Multiple env vars for different directories

## Target State

```
ocms-go/
├── internal/
│   └── themes/              # Embedded in binary
│       ├── embed.go         # //go:embed directive
│       ├── default/
│       │   ├── theme.json
│       │   ├── templates/
│       │   ├── static/
│       │   └── locales/
│       └── developer/
│           ├── theme.json
│           ├── templates/
│           ├── static/
│           └── locales/
│
├── modules/                 # Core modules (unchanged)
│
├── custom/                  # User content (external, gitignored)
│   ├── themes/              # User/third-party themes
│   └── modules/             # User modules (future)
│
└── data/                    # Runtime data (unchanged)
    └── ocms.db
```

## Migration Phases

### Phase 1: Prepare Embedded Themes Structure

**Tasks:**

1. Create `internal/themes/` directory
2. Move `themes/default/` to `internal/themes/default/`
3. Move `themes/developer/` to `internal/themes/developer/`
4. Create `internal/themes/embed.go`:
   ```go
   package themes

   import "embed"

   //go:embed all:default all:developer
   var FS embed.FS
   ```
5. Remove old `themes/` directory
6. Update `.gitignore` to ignore `custom/`

**Files to create:**
- `internal/themes/embed.go`

**Files to move:**
- `themes/default/*` → `internal/themes/default/*`
- `themes/developer/*` → `internal/themes/developer/*`

**Files to delete:**
- `themes/` (entire directory after move)

### Phase 2: Update Theme Manager

**Tasks:**

1. Modify `internal/theme/manager.go` to support dual-source loading:
   - Embedded themes from `internal/themes.FS`
   - External themes from `custom/themes/`

2. Implement loading priority:
   ```
   1. custom/themes/{name}     (user override - filesystem)
   2. internal/themes/{name}   (core - embedded)
   ```

3. Add helper to detect if theme is embedded or external

4. Update static file serving to handle embedded theme assets

**Functions to modify:**
- `NewManager()` - accept embed.FS parameter
- `LoadThemes()` - load from both sources
- `loadTheme()` - support fs.FS interface for both sources

**New functions:**
- `loadEmbeddedThemes()` - load themes from embed.FS
- `loadExternalThemes()` - load themes from custom/themes/
- `IsEmbedded(themeName)` - check if theme is embedded

### Phase 3: Update Static File Handler

**Tasks:**

1. Modify theme static file handler in `internal/handler/theme.go`
2. Serve embedded static files for core themes
3. Serve filesystem static files for custom themes
4. Maintain correct MIME types and caching headers

**Routing logic:**
```
GET /theme/static/{file}
  → If active theme is embedded: serve from embed.FS
  → If active theme is external: serve from custom/themes/{name}/static/
```

### Phase 4: Create Custom Directory Structure

**Tasks:**

1. Create `custom/` directory with subdirectories:
   ```
   custom/
   ├── themes/
   │   └── .gitkeep
   └── modules/
       └── .gitkeep
   ```

2. Add `custom/README.md` explaining the directory purpose

3. Update `.gitignore`:
   ```gitignore
   # User content
   custom/themes/*
   custom/modules/*
   !custom/themes/.gitkeep
   !custom/modules/.gitkeep
   ```

### Phase 5: Update Configuration

**Tasks:**

1. Add new environment variable:
   | Variable | Default | Description |
   |----------|---------|-------------|
   | `OCMS_CUSTOM_DIR` | `./custom` | Root directory for user content |

2. Deprecate old variable:
   | Variable | Status | Migration |
   |----------|--------|-----------|
   | `OCMS_THEMES_DIR` | Deprecated | Use `OCMS_CUSTOM_DIR` |

3. Update `internal/config/config.go`:
   - Add `CustomDir` field
   - Keep `ThemesDir` for backward compatibility (maps to `CustomDir/themes`)
   - Log deprecation warning if `OCMS_THEMES_DIR` is set

4. Update `cmd/ocms/main.go` to use new config

### Phase 6: Update Application Bootstrap

**Tasks:**

1. Modify `cmd/ocms/main.go`:
   - Pass embedded themes FS to theme manager
   - Create custom directories on startup if missing

2. Update theme manager initialization:
   ```go
   themeMgr := theme.NewManager(
       themes.FS,           // embedded themes
       cfg.CustomDir,       // custom directory root
       logger,
   )
   ```

### Phase 7: Update Documentation

**Tasks:**

1. Update `CLAUDE.md`:
   - Remove `OCMS_THEMES_DIR` from env vars table
   - Add `OCMS_CUSTOM_DIR` to env vars table
   - Update architecture overview
   - Update theme system description

2. Update `docs/` files:
   - Create `docs/custom-content.md` for user themes/modules guide
   - Update any references to `themes/` directory

3. Update `README.md` if it references theme directory

### Phase 8: Testing

**Tasks:**

1. Unit tests for theme manager:
   - Test loading embedded themes
   - Test loading external themes
   - Test override priority (external over embedded)
   - Test theme switching between embedded and external

2. Integration tests:
   - Test static file serving from embedded themes
   - Test static file serving from external themes
   - Test template rendering from both sources

3. Manual testing:
   - Fresh install with no custom/ directory
   - Override default theme in custom/themes/default/
   - Add new theme to custom/themes/
   - Switch between embedded and custom themes

## File Changes Summary

### New Files
| File | Description |
|------|-------------|
| `internal/themes/embed.go` | Embed directive for core themes |
| `internal/themes/default/*` | Moved from themes/default |
| `internal/themes/developer/*` | Moved from themes/developer |
| `custom/.gitkeep` | Placeholder for user content root |
| `custom/themes/.gitkeep` | Placeholder for user themes |
| `custom/modules/.gitkeep` | Placeholder for user modules |
| `custom/README.md` | User content documentation |
| `docs/custom-content.md` | Guide for custom themes/modules |

### Modified Files
| File | Changes |
|------|---------|
| `internal/theme/manager.go` | Dual-source loading (embed + filesystem) |
| `internal/theme/manager_test.go` | Tests for new loading logic |
| `internal/handler/theme.go` | Serve embedded static files |
| `internal/config/config.go` | Add CustomDir, deprecate ThemesDir |
| `cmd/ocms/main.go` | Pass embed.FS to theme manager |
| `.gitignore` | Add custom/ patterns |
| `CLAUDE.md` | Update env vars and architecture |

### Deleted Files
| File | Reason |
|------|--------|
| `themes/` | Moved to internal/themes/ |

## Backward Compatibility

1. **OCMS_THEMES_DIR**: Continue to work but log deprecation warning
   - If set, use as custom themes directory
   - Recommend migration to OCMS_CUSTOM_DIR

2. **Existing custom themes**: Users with themes in old location
   - Provide migration script or clear instructions
   - Simply move `themes/mytheme/` to `custom/themes/mytheme/`

3. **Docker volumes**: Update documentation for new mount points
   - Old: `-v ./themes:/app/themes`
   - New: `-v ./custom:/app/custom`

## Rollback Plan

If issues arise:

1. Revert theme manager changes
2. Move themes back from `internal/themes/` to `themes/`
3. Remove embed.go
4. Restore original config handling

Keep the old `themes/` directory structure in a branch until migration is verified stable.

## Success Criteria

1. Binary runs without external themes directory
2. Default theme renders correctly from embedded files
3. Developer theme renders correctly from embedded files
4. Custom themes in `custom/themes/` load and work
5. Custom themes can override embedded themes by name
6. Static assets serve correctly from both sources
7. All existing tests pass
8. No performance regression in theme loading

## Estimated Effort

| Phase | Complexity | Dependencies |
|-------|------------|--------------|
| Phase 1: Prepare structure | Low | None |
| Phase 2: Update manager | Medium | Phase 1 |
| Phase 3: Static handler | Medium | Phase 2 |
| Phase 4: Custom directory | Low | None |
| Phase 5: Configuration | Low | None |
| Phase 6: Bootstrap | Low | Phases 2, 5 |
| Phase 7: Documentation | Low | All phases |
| Phase 8: Testing | Medium | All phases |

## Open Questions

1. **Theme settings persistence**: Theme settings are stored in database. No migration needed, but verify settings remain associated correctly after theme is embedded.

2. **Theme screenshots**: Currently in `static/screenshot.svg`. Ensure these are accessible for admin theme list after embedding.

3. **Live reload in development**: With embedded themes, how to support template hot-reload during development? Consider build tag to use filesystem in dev mode.

4. **Module custom directory**: This plan includes `custom/modules/` for future use. Defer implementation until module system needs external modules.
