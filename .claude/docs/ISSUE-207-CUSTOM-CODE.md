# Issue #207: Custom Code module (admin-managed head and footer snippets)

Status: plan, not started. Branch `feature/issue-207-custom-code`, cut from `origin/master` at 3931fd82.

Issue: https://github.com/olegiv/ocms-go/issues/207. The design we committed to is in the comment https://github.com/olegiv/ocms-go/issues/207#issuecomment-6641782122.

## Context

c-nv-s wants to add scripts and stylesheets to pages from the admin UI, both site-wide and per page, at the end of `<head>` and just before `</body>`. Typical uses are chart and widget libraries from a CDN, or page-specific JS. Only privileged users may set them.

A custom theme can do this today, but it means editing and redeploying the theme, and looking up page IDs by hand. A custom module cannot do it, because it cannot add template functions.

So this is a built-in, opt-in module. It does not move the privilege boundary: admins can already import a theme that contains any script, and they can already set `custom_css`. The module only removes the redeploy step.

### What we promised (each item is a requirement)

1. Site-wide head and footer snippets, edited on the module's admin page.
2. Per-page snippets reached from an admin-only button in the page editor, so nobody has to look up a page ID.
3. Snippets run in the admin preview, so drafts can be tried out. The draft fallback in `FrontendHandler.Page` (`internal/handler/frontend.go:928-936`) already renders drafts to admins and editors through the same layout.
4. Admins only. Editors, API keys and the MCP tools can neither read nor write snippets.
5. Every change is recorded in the event log.
6. Pasted `<script>` tags get the request's CSP nonce automatically, so CDN scripts run as written and stay pinned by their `integrity`.
7. Stylesheet hosts taken from `<link rel=stylesheet>` are allowed in `style-src`, and only on the pages that use them.
8. The editor warns about inline handlers (`on*=`), because the CSP blocks them.
9. The built-in themes render the snippets. A custom html theme needs one call per position in its layout.

### Out of scope for this branch

- Extra hosts per snippet, such as `connect-src` for third-party APIs or `frame-src`. They are a later iteration, and `img-src` already allows `https:`.
- Copying a snippet to a page's translations. Each translation is its own `pages` row, so snippets belong to one translation.
- Export and import of snippets. `internal/transfer` carries no module data today.
- Rebinding functions in html themes when a module is toggled at run time. This is an existing limitation for every module (see D6).

## Design decisions

**D1. Module identity.**
- Name `custom_code`, package `modules/customcode`, admin URL `/admin/custom-code`, sidebar label "Custom Code".
- `ActiveByDefault() false`, so it is opt-in.
- `ActivationNeedsPassword() true`, following the dbmanager precedent (`modules/dbmanager/module.go:65`).
- It does not implement `EditorAccessible`. `Registry.AdminRouteAll` therefore puts the admin routes behind `middleware.RequireAdmin()` (`internal/module/registry.go:780-794`), and `TestModuleAdminRoutesDenyEditorsUnlessAllowlisted` enforces it.

**D2. Storage.**
- One module table, created by an inline `module.Migration` in the informer style (`modules/informer/module.go:186-235`), using raw `database/sql` as other modules do:

  ```sql
  CREATE TABLE IF NOT EXISTS custom_code_snippets (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      page_id INTEGER REFERENCES pages(id) ON DELETE CASCADE, -- NULL = site-wide
      position TEXT NOT NULL CHECK (position IN ('head', 'body_end')),
      code TEXT NOT NULL,
      style_hosts TEXT NOT NULL DEFAULT '[]', -- JSON, derived and validated on save
      updated_by INTEGER,
      updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
  );
  CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_code_slot
      ON custom_code_snippets (COALESCE(page_id, 0), position);
  ```

- Saving an empty snippet deletes its row.
- The foreign key cascade is the only mechanism that covers every page-delete path:
  - `internal/handler/pages.go:1052,1116`
  - `internal/api/v2/pages/service.go:735`
  - `modules/developer/generator.go:1049`
  - migrator handlers and rollbacks
  - dbmanager

  Foreign keys are on for every connection (`internal/store/database.go:76`). `pages.id` is `AUTOINCREMENT`, so IDs are never reused.
- The flip side: a future migration that rebuilds `pages`, as `00034` did, must keep this table, just as it keeps `page_versions`. Add `custom_code_snippets` to the CLAUDE.md note about rebuilding `pages`.

**D3. In-memory copy.**
- The table is small, so the module keeps all of it in memory as `map[pageID]slots`, with key 0 for site-wide.
- It is loaded in `Init` and reloaded after every save, under a `sync.RWMutex`, following the informer settings pattern. A `settings_lock_drift_test.go` asserts that every access holds the lock.
- Rows removed by the cascade stay in memory until the next reload. That is harmless, because a deleted page's ID never renders again.
- Edits made outside the module, for example through dbmanager, appear after a restart or the next save. Document this.

**D4. Page context for module functions.**
- `getBaseTemplateData` runs before the page is known: it is called at `frontend.go:999`, and `pageToView` at 1002. `BaseTemplateData.Page` (`:203`) is never set.
- Add `BaseTemplateData.PageID int64`.
  - In `FrontendHandler.Page`, once the page row is resolved, including the draft fallback, store its ID in the request context with a small typed context key.
  - `getBaseTemplateData` reads that key into `data.PageID`.
- Pass the ID as a third positional argument through `callModuleHTMLFuncs`: `f(nonce, origin, pageID)`.
  - Existing functions read only the first argument through `firstStringArg`, so they are unaffected.
  - Update the comment at `frontend.go:521` to match.
- Every other route leaves `PageID` at 0, so only site-wide snippets render there. That includes the homepage: on master, `Home` lists recent posts and never renders a `pages` row (`frontend.go:651-700`), so it has no per-page slot. If a static front page is added later, it must set the same context key.

**D5. Rendering.**
- Two template functions with the module convention `func(args ...any) template.HTML`:
  - `customCodeHead(nonce, origin, pageID)`
  - `customCodeBodyEnd(nonce, origin, pageID)`
- Each returns the site-wide slot followed by the page slot.
- Nonces are stamped with the existing `util.AddNonceToScriptTags` (`internal/util/csp.go:26-42`). Do not write a second stamper.
- If the module is inactive, both functions return `""`. They check a narrow `ActiveChecker` that the registry wires into `module.Context` during `InitAll`, as it already does for `PublicRouteChecker`, backed by `Registry.IsActive` (`registry.go:412`). This matters for html themes, which keep the real function bound after the module is deactivated at run time (`cmd/ocms/main.go:2003-2010`). Without the check, scripts would keep running until a restart.
- A nil `ActiveChecker` means the module is treated as inactive, so it renders nothing. This applies in tests and in embedders that don't wire the checker. A missing wire then fails closed.
- The drift tests build every module from `allModules()` without calling `Init`, then call `TemplateFuncs()` and `CSPSources()` directly (`module_template_funcs_test.go`, `module_csp_drift_test.go`). So the template functions, `CSPSources` and `PageCSPSources` must all nil-guard `m.ctx` and the in-memory copy.
- Placement:
  - In the templ layout, append the names last in the head and body-end lists at `frontend.go:2369-2371`.
  - In the html layouts, put `{{customCodeHead .CSPNonce .PageOrigin .PageID}}` just before `</head>` and `{{customCodeBodyEnd .CSPNonce .PageOrigin .PageID}}` just before `</body>`.

**D6. Html-theme binding.**
- Html themes bind module functions once at startup (`main.go:2003-2010`, `SetFuncMap` at 1558). Activating the module therefore needs a restart before html themes render snippets. The templ default theme renders them per request.
- This is an existing limitation for every module. Document it here; fixing it is out of scope. D5's active check covers the reverse case, deactivation.

**D7. Content-Security-Policy.**
- **Site-wide stylesheet hosts.** The module implements the existing `CSPSourcer`, which `Registry.ActiveCSPSources` merges (`registry.go:424-455`). It returns the hosts from the in-memory copy, read under the lock, so the call stays cheap.
  - Directives: `style-src`, plus the same hosts in `font-src`, because CSS often loads fonts relative to itself.
  - Like analytics, these hosts appear on every response, admin pages included.
- **Per-page stylesheet hosts.** `SecurityHeaders` writes the header before the handler runs (`internal/middleware/security.go:313`). Add one call site that has both `w` and the page ID: `FrontendHandler.Page`, after the Markdown-negotiation early return (`frontend.go:962-982`), so a non-HTML response is never widened, and before the first write.
  - New optional interface `module.PageCSPSourcer { PageCSPSources(pageID int64) map[string][]string }`.
  - New `Registry.ActivePageCSPSources(pageID)`, which reuses `moduleCSPSources`' panic recovery and `IsCSPHostSource` filter.
  - New `middleware.ExtendResponseCSP(w, sources)`, which applies the existing `addCSPSources` to the header already set. Only directives in `cspExtendableDirectives` that the policy already has can be widened.
  - `FrontendHandler` gets the function through a setter wired in `main.go`, next to the other `Set*` calls.
- **Scripts.** Pasted scripts need no hosts, because the nonce allows a script from any host. `script-src` is never widened.
- **Test.** `ExtendResponseCSP` must still parse and widen correctly after `applyCSPNonce`, in both the development and the production policy.

**D8. Admin UI.** Both views are templ, in `adminviews.AdminLayout`, following `modules/informer/views.templ` and `handlers.go`.
- `GET/POST /admin/custom-code`:
  - textareas for the site-wide head and body-end snippets;
  - a table of pages that have snippets (title, language, status, link to edit).
- `GET/POST /admin/custom-code/pages/{id}`:
  - textareas for the page's head and body-end snippets;
  - the page title, links to the page editor and the public page, and a note that the snippets belong to this translation only.
  - An unknown ID returns 404.
- **Entry from the page editor.** Add an optional interface `module.AdminPageEditorLinker { AdminPageEditorLinks(pageID int64) []PageEditorLink }`, aggregated by the registry for active modules.
  - `PagesHandler.EditForm` adds the links to `PageFormViewData` only when `middleware.GetUser(r).Role == model.RoleAdmin`.
  - `pages.templ` renders them next to the Preview button (`pages.templ:856-861`).
  - The button appears only after the first save, because `NewForm` has no page ID yet.
  - It is a link to the module's form, not fields on the page form. The page form is open to editors and posts through the guarded page writers, so adding fields there would risk an editor's save clearing or changing an admin's snippets.
- **Save handlers:**
  - Demo-mode guard: `middleware.IsDemoMode()` with `DemoModeMessageDetailed(middleware.RestrictionModuleSettings)`.
  - Blocking errors: a snippet larger than 64 KiB, or invalid UTF-8.
  - A non-blocking warning flash for:
    - `on*` attributes;
    - `javascript:` URLs;
    - a `<script>` that already has a `nonce`, which would be skipped by stamping and blocked;
    - a stylesheet or script `src`/`href` that is not `https:`;
    - a stylesheet host that `IsCSPHostSource` rejects.
  - Parse the snippet with `golang.org/x/net/html` (already in `go.mod`) to collect stylesheet hosts and warnings.
  - Then post-redirect-get with a flash.
- **Event log.** Call `m.ctx.Events.LogConfigEvent` with metadata: scope, `page_id`, position, byte count, the first 12 hex characters of the SHA-256, and the stylesheet hosts. The code itself is not logged. The failure branch logs with `service.WithoutEventLog(ctx)`, as `TestEventWriteFailuresStayOutOfTheEventLog` requires.
- **Placeholder text.** It must contain no literal `https://` URLs, because `TestModuleThirdPartyHostsAreInCSPSources` would treat them as hosts the module loads. Describe the expected shape in words instead.

## Iterations

Each iteration builds, passes the full gate (`scripts/test-gate.sh`), and gets its own approved commit.

### 1. Core extension points (no module yet)

- `internal/handler/frontend.go`:
  - add `PageID` to `BaseTemplateData`;
  - add the page-ID context key, set in `Page()` and read in `getBaseTemplateData`;
  - pass the third argument in `callModuleHTMLFuncs` and fix its comment;
  - add the `SetPageCSPSources` setter and its call in `Page()`.
- `internal/middleware/security.go`: `ExtendResponseCSP`.
- `internal/module/module.go`:
  - `PageCSPSourcer` and `AdminPageEditorLinker` with a `PageEditorLink{Label, URL}`;
  - an `ActiveChecker` field in `Context`, with its doc comment.
- `internal/module/registry.go`:
  - `ActivePageCSPSources`, `ActiveAdminPageEditorLinks`;
  - wire `ActiveChecker` in `InitAll`.
- `internal/handler/pages.go`, `internal/handler/templ.go`, `internal/views/admin/pages.templ`: admin-only editor links. Run `templ generate`.
- `cmd/ocms/main.go`: wire both setters.
- Tests:
  - `ExtendResponseCSP`: widens only extendable directives the policy already has, rejects non-host values, works after nonce substitution in development and production;
  - registry aggregation, including panic recovery and inactive modules;
  - `PageID` is set on page routes, including draft fallback, and is 0 on home, list and 404;
  - `callModuleHTMLFuncs` passes three arguments (extend `internal/handler/frontend_test.go:2002-2075`);
  - editor links: shown to admins, absent for editors, absent on the new-page form.

### 2. The module

- `modules/customcode/`:
  - `module.go`: metadata, migrations up and down, `Init` load, `TemplateFuncs`, `CSPSources`, `PageCSPSources`, `AdminPageEditorLinks`, `ActiveByDefault`, `ActivationNeedsPassword`;
  - `store.go`: queries and the in-memory copy;
  - `parse.go`: host extraction and warnings;
  - `handlers.go`;
  - `views.templ`;
  - `locales/{en,ru}/messages.json`.
- Register it in `registerModules()` (`cmd/ocms/main.go:1058-1102`) and `allModules()` (`cmd/ocms/module_template_funcs_test.go:38-54`), because `TestAllModulesMirrorsRegisterModules` checks they match.
- No-op placeholders for both functions in `internal/render/render.go` (415-445), required by `TestEveryModuleTemplateFuncHasRendererPlaceholder`.
- Tests:
  - migration down then up;
  - unique slot constraint;
  - cascade on page delete, with foreign keys on;
  - nonce stamping, including a pasted `nonce`;
  - host extraction (`rel` lists such as `preload stylesheet`, ports, `http:`, protocol-relative `//host`) and warnings;
  - size and UTF-8 limits;
  - the lock drift test;
  - functions return `""` when inactive and when `ActiveChecker` is nil, and render nothing for `pageID` 0 beyond the site-wide slot;
  - an uninitialized module, with no `Init` and a nil `ctx`, returns empty results from `TemplateFuncs()` closures, `CSPSources()` and `PageCSPSources()` without panicking;
  - a Markdown-negotiated page response keeps the base CSP;
  - handlers: admin gets 200, demo mode is blocked, the event is recorded, and an editor gets 403 (the existing drift test covers this);
  - `CSPSources` and `PageCSPSources` results.

### 3. Themes and hand-kept lists

- Templ list at `internal/handler/frontend.go:2369-2371`: `customCodeHead` last in head, `customCodeBodyEnd` last in body-end.
- Html layouts:
  - `internal/themes/developer/templates/layouts/base.html`
  - `internal/themes/default/templates/layouts/base.html`
  - `custom/themes/starter/templates/layouts/base.html`
- Function lists:
  - `internal/testutil/testutil.go:112-144` (`MinimalThemeFuncMap`)
  - `internal/handler/frontend_test.go:694-699`
  - `custom/themes/starter/starter_test.go:341-348` (required integrations)
- New drift test `TestBuiltInLayoutsCallEveryModuleHTMLFunc`: every name in the three `callModuleHTMLFuncs` lists appears in each built-in html `base.html`. It must fail when a call is removed from any layout. This keeps future module functions from skipping html themes.

### 4. Docs and wiki

- New `docs/custom-code-module.md`, in the `docs/analytics-ext-module.md` structure. It covers:
  - what the module is for, and the privilege note;
  - the admin UI and the editor button;
  - CSP behaviour: nonce, stylesheet hosts, what stays blocked (inline handlers, third-party `fetch`), and the planned extra hosts;
  - template functions, and the two calls a custom html theme adds to its layout: one before `</head>`, one before `</body>`. The #207 comment said "one call", so state two plainly;
  - the restart needed for html themes after activation;
  - snippets apply to one translation, are not exported, and run regardless of the privacy consent banner unless written in the Klaro `type="text/plain"` form;
  - the database schema.
- Update:
  - `docs/custom-themes.md:365-379` (integration hooks table)
  - `docs/custom-modules.md:385-393` (`callModuleHTMLFuncs` name list and the new third argument)
  - the CLAUDE.md documentation list and its `pages`-rebuild note (D2)
- Wiki: the submodule is not checked out in this worktree, so run `git submodule update --init wiki` first. Then add `wiki/Module:-Custom-Code.md`, and update the matching custom-themes and custom-modules wiki pages, plus the sidebar or module index if they list modules. Publish with `/update-wiki`, after its own approvals.

## Hand-kept lists checklist

- [ ] `registerModules()` and `allModules()`
- [ ] `internal/render/render.go` placeholders
- [ ] `internal/handler/frontend.go:2369-2371`
- [ ] `internal/testutil/testutil.go` `MinimalThemeFuncMap`
- [ ] `internal/handler/frontend_test.go` theme manager function list
- [ ] `custom/themes/starter/starter_test.go` required integrations
- [ ] Three html `base.html` layouts
- [ ] `docs/custom-themes.md`, `docs/custom-modules.md`, CLAUDE.md docs list
- [ ] `modules/customcode/locales/{en,ru}`, checked by `TestTranslationKeysExist`

## New drift tests

- `TestBuiltInLayoutsCallEveryModuleHTMLFunc` (iteration 3).
- `TestCustomCodeTableStaysInsideTheModule`: no non-test file outside `modules/customcode/` names `custom_code_snippets`. This keeps API, MCP, transfer and handler code from gaining a path to the table. It must fail when such a reference is added.
- `TestOnlyFrontendPageExtendsResponseCSP`: `ExtendResponseCSP` has exactly one non-test caller, `FrontendHandler.Page`, so per-page widening cannot spread to admin or API responses.

Following the CLAUDE.md drift-test rule, each new test is shown to fail on the bug state before the fix is restored.

## Verification

- **Full gate.** `scripts/test-gate.sh`, uncached, on a fresh snapshot of each commit's tree. Also `go vet`, `staticcheck` and `errcheck -ignoregenerated`.
- **Local server.**
  - Run the worktree on a scratch port, not 8080 or 8099. Activate the module; the password prompt must appear.
  - `curl` checks:
    - `/`, `/admin/` and `/health` load;
    - the site-wide stylesheet host is in `style-src` and `font-src` on every response;
    - a page's host appears only on that page's response;
    - an editor session gets 403 on `/admin/custom-code` and sees no editor button.
- **Browser** (chrome-devtools MCP, cache-busted URLs). Paste c-nv-s's three examples (Leaflet CSS, ApexCharts, Sortable) as one page's snippets, plus a small inline script that draws a chart and a Leaflet map. Then check:
  - everything renders, with no console CSP errors and no `/csp-report` entries;
  - other pages are unchanged;
  - a draft page shows its snippets in preview;
  - an `onclick` snippet produces the warning flash, and is blocked in the browser as documented;
  - deactivating the module stops the snippets at once on the templ default theme, and on an html theme thanks to D5's active check.
- **Event log.** Each save shows one entry with the hash and size, and no code.
- **Page deletion.** Deleting a page with snippets removes its rows.

## Risks and notes for later

- **R1.x branches are not on master.** `codex/phase5-r1.6-editorial-review` brings the page-write boundary, MCP draft tools and anonymous share-link previews. When it merges:
  - share-link previews go through their own rendering path. Decide there whether they render snippets, and whether they get the per-page CSP call (D7).
  - MCP and API stay unable to reach snippets; `TestCustomCodeTableStaysInsideTheModule` covers that.
- **Admin-entered scripts are a deliberate stored-script surface.** Admin MFA is required in production, and activation needs the admin's password. The release notes should say so plainly.
- **Follow-ups**, each its own issue if c-nv-s confirms the need:
  - per-snippet extra CSP hosts (`connect-src`, `frame-src`);
  - copying snippets to translations;
  - export and import of snippets;
  - rebinding functions in html themes on module toggle.
