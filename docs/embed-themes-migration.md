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

### Phase 8: Update Deployment and Sync Procedures

**Tasks:**

With embedded themes, the deployment model changes significantly. Core themes ship with the binary, so only custom themes need syncing.

#### 8.1 Update `scripts/deploy/deploy.sh`

**Current behavior:**
- Line 16: `LOCAL_THEMES_DIR="themes/"`
- Line 129: `REMOTE_THEMES_DIR="${VHOST}/ocms/themes"`
- Lines 193-198: rsync themes with `--delete`

**New behavior:**
- Only sync custom themes if `custom/themes/` exists and is non-empty
- Change local path from `themes/` to `custom/themes/`
- Change remote path from `{vhost}/ocms/themes` to `{vhost}/ocms/custom/themes`
- Make theme sync optional (skip if no custom themes)

**Code changes:**
```bash
# Old
LOCAL_THEMES_DIR="themes/"
REMOTE_THEMES_DIR="${VHOST}/ocms/themes"

# New
LOCAL_CUSTOM_DIR="custom/"
REMOTE_CUSTOM_DIR="${VHOST}/ocms/custom"
```

**New sync logic:**
```bash
# Step 5: Sync custom content (only if exists)
if [[ -d "${LOCAL_CUSTOM_DIR}/themes" ]] && [[ -n "$(ls -A ${LOCAL_CUSTOM_DIR}/themes 2>/dev/null)" ]]; then
    echo_step "Syncing custom themes..."
    rsync_cmd -avz --delete "${LOCAL_CUSTOM_DIR}/themes/" "${SSH_USER}@${SERVER}:${REMOTE_CUSTOM_DIR}/themes/"
    echo_ok "Custom themes synced"
else
    echo_info "No custom themes to sync (using embedded themes)"
fi
```

#### 8.2 Update `scripts/deploy/setup-site.sh`

**Current behavior:**
- Line 185: Creates `themes` directory
- Lines 208-209: Sets `OCMS_THEMES_DIR=./themes`
- Lines 263-282: Copies themes from `/opt/ocms/themes/`

**New behavior:**
- Create `custom/themes` instead of `themes`
- Replace `OCMS_THEMES_DIR` with `OCMS_CUSTOM_DIR`
- Remove theme copying (binary has embedded themes)
- Add note about custom themes

**Directory structure change:**
```bash
# Old
mkdir -p "$INSTANCE_DIR"/{data,uploads,themes,backups,logs}

# New
mkdir -p "$INSTANCE_DIR"/{data,uploads,custom/themes,custom/modules,backups,logs}
```

**Environment file change:**
```bash
# Old
OCMS_THEMES_DIR=./themes
OCMS_ACTIVE_THEME=default

# New
OCMS_CUSTOM_DIR=./custom
OCMS_ACTIVE_THEME=default
# Note: Core themes (default, developer) are embedded in binary
# Place custom themes in ./custom/themes/
```

**Remove theme copying section** (lines 262-282):
- Delete the `/opt/ocms/themes/` check and copy logic
- Binary includes default and developer themes
- Custom themes are optional

#### 8.3 Update `scripts/deploy/deploy-multi.sh`

**Current behavior:**
- Only handles binary deployment, no theme sync

**New behavior:**
- No changes needed (binary includes themes)
- Document that custom themes require separate sync

#### 8.4 Update `scripts/deploy/sync-prod-to-dev.sh`

**Current behavior:**
- Syncs data, uploads, logs (not themes)

**New behavior:**
- Add optional `--custom` flag to sync custom themes/modules
- Sync from `{vhost}/ocms/custom/` to local `./custom/`

**New option:**
```bash
--sync-custom          Sync custom themes/modules from production
```

**New function:**
```bash
sync_custom() {
    if [[ "$SYNC_CUSTOM" != true ]]; then
        echo_info "Skipping custom content sync (use --sync-custom to include)"
        return
    fi

    echo_step "Syncing custom content..."
    mkdir -p "${LOCAL_CUSTOM_DIR}"
    rsync_cmd -avz --progress \
        "${SSH_USER}@${SERVER}:${REMOTE_CUSTOM_DIR}/" \
        "${LOCAL_CUSTOM_DIR}/"
    echo_ok "Custom content synced"
}
```

#### 8.5 Update `scripts/deploy/backup-multi.sh`

**Current behavior:**
- Backs up database and uploads

**New behavior:**
- Add custom themes/modules to backup
- Only if `custom/` directory exists and is non-empty

**Add to backup_site function:**
```bash
# Backup custom content
local custom_path="$instance_dir/custom"
if [ -d "$custom_path" ] && [ "$(ls -A "$custom_path" 2>/dev/null)" ]; then
    local custom_backup="$backup_dir/custom_${TIMESTAMP}.tar.gz"
    tar -czf "$custom_backup" -C "$instance_dir" custom
    echo "  Custom: $custom_backup ($(ls -lh "$custom_backup" | awk '{print $5}'))"
fi
```

#### 8.6 Update `scripts/deploy/README.md`

**Major documentation changes:**

1. **Architecture section** - Update directory structure:
   ```
   # Old
   /opt/ocms/themes/               ← shared theme source (copied per site)

   # New
   # (removed - themes embedded in binary)
   ```

   Per-site structure:
   ```
   # Old
   ├── themes/                     ← theme files

   # New
   ├── custom/                     ← user content (optional)
   │   ├── themes/                 ← custom/override themes
   │   └── modules/                ← custom modules (future)
   ```

2. **Quick Start section** - Remove theme copying:
   ```bash
   # Old
   scp -r themes user@server:/tmp/ocms-themes
   sudo cp -r /tmp/ocms-themes/* /opt/ocms/themes/

   # New
   # (removed - themes are embedded in binary)
   ```

3. **Remove the "Important" note** about themes loaded from disk

4. **Update deploy.sh section**:
   - Remove `-v` vhost requirement for themes
   - Document that only custom themes are synced
   - Add `--no-custom` flag documentation

5. **Add new section: Custom Themes**:
   ```markdown
   ## Custom Themes

   Core themes (default, developer) are embedded in the binary.
   To add custom themes:

   1. Create theme in `custom/themes/mytheme/` locally
   2. Deploy with: `./deploy.sh ... --sync-custom`
   3. Or manually copy to server: `{vhost}/ocms/custom/themes/`

   Custom themes override embedded themes with the same name.
   ```

6. **Update Copying Local Data section** - remove theme references

7. **Update sync-prod-to-dev section** - add `--sync-custom` option

8. **Update File Permissions table**:
   ```
   # Old
   | `/opt/ocms/themes/` | `root:root` | `755` |

   # New (removed, add instead):
   | `{vhost}/ocms/custom/` | `{user}:psaserv` | `755` |
   ```

#### 8.7 Remove `/opt/ocms/themes/` from Server

After migration, the shared themes directory is no longer needed:

```bash
# On server, after all sites are updated:
sudo rm -rf /opt/ocms/themes
```

**Files to modify:**
| File | Changes |
|------|---------|
| `scripts/deploy/deploy.sh` | Change theme sync to custom sync, make optional |
| `scripts/deploy/setup-site.sh` | Remove theme copying, update env vars, change directory structure |
| `scripts/deploy/sync-prod-to-dev.sh` | Add `--sync-custom` option |
| `scripts/deploy/backup-multi.sh` | Add custom directory backup |
| `scripts/deploy/README.md` | Major updates for new architecture |

### Phase 9: Testing

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

4. Deployment testing:
   - Test `deploy.sh` with no custom themes
   - Test `deploy.sh` with custom themes
   - Test `setup-site.sh` creates correct directory structure
   - Test `sync-prod-to-dev.sh --sync-custom`
   - Test `backup-multi.sh` includes custom content

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
| `scripts/deploy/deploy.sh` | Change theme sync to optional custom sync |
| `scripts/deploy/setup-site.sh` | Remove theme copying, update directory structure |
| `scripts/deploy/sync-prod-to-dev.sh` | Add `--sync-custom` option |
| `scripts/deploy/backup-multi.sh` | Add custom directory backup |
| `scripts/deploy/README.md` | Major updates for embedded themes architecture |

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

4. **Existing server deployments**: Sites with themes in `{vhost}/ocms/themes/`
   - Binary update works immediately (embedded themes)
   - Custom themes need migration: `mv themes custom/themes`
   - Update `.env`: replace `OCMS_THEMES_DIR` with `OCMS_CUSTOM_DIR`

5. **deploy.sh usage**: Script interface changes
   - Old: Required `-v` (vhost) for theme sync
   - New: `-v` only needed if syncing custom themes
   - Add migration notes to script help text

## Rollback Plan

If issues arise:

1. Revert theme manager changes
2. Move themes back from `internal/themes/` to `themes/`
3. Remove embed.go
4. Restore original config handling
5. Revert deployment script changes
6. On servers: restore `/opt/ocms/themes/` and move custom themes back

Keep the old `themes/` directory structure in a branch until migration is verified stable.

**Server rollback steps:**
```bash
# Restore shared themes directory
sudo mkdir -p /opt/ocms/themes
sudo cp -r /path/to/backup/themes/* /opt/ocms/themes/

# Per site: move custom themes back
sudo mv {vhost}/ocms/custom/themes/* {vhost}/ocms/themes/
sudo rmdir {vhost}/ocms/custom/themes {vhost}/ocms/custom/modules {vhost}/ocms/custom

# Update .env: replace OCMS_CUSTOM_DIR with OCMS_THEMES_DIR
```

## Success Criteria

1. Binary runs without external themes directory
2. Default theme renders correctly from embedded files
3. Developer theme renders correctly from embedded files
4. Custom themes in `custom/themes/` load and work
5. Custom themes can override embedded themes by name
6. Static assets serve correctly from both sources
7. All existing tests pass
8. No performance regression in theme loading
9. `deploy.sh` works with and without custom themes
10. `setup-site.sh` provisions sites without requiring `/opt/ocms/themes/`
11. `backup-multi.sh` includes custom content in backups
12. `sync-prod-to-dev.sh --sync-custom` syncs custom themes correctly

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
| Phase 8: Deployment/Sync | Medium | Phases 4, 5 |
| Phase 9: Testing | Medium | All phases |

## Open Questions

1. **Theme settings persistence**: Theme settings are stored in database. No migration needed, but verify settings remain associated correctly after theme is embedded.

2. **Theme screenshots**: Currently in `static/screenshot.svg`. Ensure these are accessible for admin theme list after embedding.

3. **Live reload in development**: With embedded themes, how to support template hot-reload during development? Consider build tag to use filesystem in dev mode.

4. **Module custom directory**: This plan includes `custom/modules/` for future use. Defer implementation until module system needs external modules.

5. **Server migration automation**: Should we provide a migration script for existing servers?
   - Moves `{vhost}/ocms/themes/` to `{vhost}/ocms/custom/themes/`
   - Updates `.env` files
   - Removes `/opt/ocms/themes/`

6. **deploy.sh vhost requirement**: Currently `-v` (vhost) is required. Options:
   - Make it optional (only for custom themes)
   - Keep required for ownership setting on custom content
   - Add `--custom-only` flag for targeted sync
