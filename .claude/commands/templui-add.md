Add one or more templUI components to the oCMS project. Handles CLI installation, project initialization, component download, and post-install steps.

Usage: /templui-add button card dialog
       /templui-add "*"

Steps:
1. Check if `templui` CLI is installed (`which templui`). If not, install it:
   ```bash
   go install github.com/templui/templui/cmd/templui@latest
   ```
   Verify with `templui -v`.

2. Check if `.templui.json` exists in project root. If not, create it with this oCMS-specific config:
   ```json
   {
     "componentsDir": "internal/ui",
     "utilsDir": "internal/ui/utils",
     "moduleName": "github.com/olegiv/ocms-go",
     "jsDir": "web/static/js/templui",
     "jsPublicPath": "/static/dist/js/templui"
   }
   ```
   Also create required directories:
   ```bash
   mkdir -p internal/ui internal/ui/utils web/static/js/templui
   ```

3. Check if `web/static/css/admin-tw.css` has a `@source` directive for `internal/ui/`. If the line `@source "../../../internal/ui/**/*.templ";` is missing, add it after the existing `@source` lines.

4. Add the requested component(s) using force mode (non-interactive):
   ```bash
   templui -f add <component-names>
   ```

5. Run post-install steps:
   ```bash
   templ generate
   go mod tidy
   make assets
   ```

6. If the component has JavaScript files (check `web/static/js/templui/` for new .js files):
   - Verify `scripts/build-assets.sh` has a copy step for `web/static/js/templui/` to `web/static/dist/js/templui/`. If missing, add it.
   - Remind: add `@<component>.Script()` call to `internal/views/admin/layout.templ` if not already present.

7. Report results:
   - List new files created in `internal/ui/<component>/`
   - Show the import path: `"github.com/olegiv/ocms-go/internal/ui/<component>"`
   - Show a basic usage example

Note: Use `templui -f add` (force flag) since Claude Code runs non-interactively. To update existing components, use the same command - it will overwrite with the latest version.
