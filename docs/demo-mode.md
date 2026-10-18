# Demo Mode

Demo mode provides security restrictions for public demo deployments, preventing abuse while still allowing visitors to explore oCMS functionality.

## Overview

When `OCMS_DEMO_MODE=true` is set, the following restrictions apply:

| Feature | Normal Mode | Demo Mode |
|---------|-------------|-----------|
| Create/Edit pages | Allowed | **Blocked** |
| Publish/unpublish pages | Allowed | **Blocked** |
| Delete pages | Allowed | **Blocked** |
| Upload/Edit media | Allowed | **Blocked** |
| Delete media | Allowed | **Blocked** |
| Create/Edit tags, categories, menus, forms, widgets, redirects | Allowed | **Blocked** |
| User management | Full access | **Blocked** |
| Site configuration | Full access | **Blocked** |
| Language management | Full access | **Blocked** |
| API key management | Full access | **Blocked** |
| Webhook management | Full access | **Blocked** |
| Module management (toggle, module settings) | Full access | **Blocked** |
| Theme settings | Editor access | **Blocked** |
| Theme import/deletion | Admin access | **Blocked** |
| Cache clearing | Full access | **Blocked** |
| Scheduler management | Full access | **Blocked** |
| SQL execution (DB Manager) | Full access | **Blocked** |
| Data export | Full access | **Blocked** |
| Data import | Full access | **Blocked** |
| Delete tags/categories | Allowed | **Blocked** |
| Delete menus/items | Allowed | **Blocked** |
| Delete forms/widgets | Allowed | **Blocked** |

## Enabling Demo Mode

Set the environment variable:

```bash
export OCMS_DEMO_MODE=true
```

Or in `fly.toml`:

```toml
[env]
  OCMS_DEMO_MODE = "true"
```

## User Experience

When a user attempts a blocked action in demo mode:

1. **Web UI**: User is redirected back with a flash message explaining the restriction
2. **API**: Returns HTTP 403 Forbidden with explanation message

### Example Flash Messages

- "Deleting pages is disabled in demo mode"
- "Creating users is disabled in demo mode"
- "API key management is disabled in demo mode"

## Allowed Actions

Demo mode still allows users to:

- Browse all admin pages
- View all settings and configurations
- Switch the active theme
- Use the REST API for read operations

Content is read-only: pages, media, tags, categories, menus, forms, widgets and redirects cannot be created, edited, published or deleted, and media uploads are blocked entirely (the seeded demo content is the only media).

## Scheduled Reset

In demo mode the application resets itself:

- A scheduler job `demo_reset` runs daily at 01:00 server time, UTC on Fly.io (it has no manual trigger). It deletes the database and uploads, then sends SIGTERM so the process restarts and re-seeds.
- At startup, if more than 24 hours have passed since the last reset (for example when a Fly.io machine was stopped overnight), the same reset runs before the database is opened.

You can also reset by hand:

```bash
fly ssh console -C "/app/scripts/reset-demo.sh"
fly machines restart
```

## Implementation Details

### Middleware Location

Demo mode checks are implemented in:
- `internal/middleware/demo.go` - Core middleware and helpers
- `internal/handler/demo.go` - Handler-level guard functions

### Adding Demo Guards to New Handlers

Use the `demoGuard` helper:

```go
func (h *MyHandler) DangerousAction(w http.ResponseWriter, r *http.Request) {
    // Block in demo mode
    if demoGuard(w, r, h.renderer, middleware.RestrictionMyAction, "/admin/myroute") {
        return
    }
    // Normal handler logic...
}
```

For JSON/HTMX responses:

```go
if middleware.IsDemoMode() {
    writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionMyAction))
    return
}
```

### Adding New Restrictions

1. Add a constant in `internal/middleware/demo.go`:

```go
const RestrictionMyAction DemoRestriction = "my_action"
```

2. Add the message in `DemoModeMessageDetailed`:

```go
RestrictionMyAction: "This action is disabled in demo mode",
```

3. Add the guard in the handler

## Security Considerations

Demo mode is designed to prevent:

- **Data deletion** - Cannot delete any content
- **Configuration tampering** - Cannot change site settings
- **User privilege escalation** - Cannot create admin users
- **External service abuse** - Cannot configure webhooks
- **Data exfiltration** - Cannot export site data
- **Storage exhaustion** - Media uploads are blocked

It does **not** prevent:

- Spam or junk arriving through public forms and other visitor traffic (the scheduled reset clears it)
- Accessing existing admin features (use separate demo credentials)
- Viewing sensitive information in UI (don't use real data)

## Fly.io Demo Deployment

See [Fly.io README](../.fly/README.md) for complete deployment instructions.

Quick setup:

```bash
# Set demo mode in fly.toml
[env]
  OCMS_DEMO_MODE = "true"
  OCMS_DO_SEED = "true"

# Deploy
fly deploy

# Optional: Set up scheduled reset
# (Configure GitHub Actions or external cron)
```
