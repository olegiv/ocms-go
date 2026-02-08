# Demo Mode

Demo mode provides security restrictions for public demo deployments, preventing abuse while still allowing visitors to explore oCMS functionality.

## Overview

When `OCMS_DEMO_MODE=true` is set, the following restrictions apply:

| Feature | Normal Mode | Demo Mode |
|---------|-------------|-----------|
| Create/Edit pages | Allowed | Allowed |
| Delete pages | Allowed | **Blocked** |
| Create/Edit media | Allowed | Allowed |
| Delete media | Allowed | **Blocked** |
| User management | Full access | **Blocked** |
| Site configuration | Full access | **Blocked** |
| Language management | Full access | **Blocked** |
| API key management | Full access | **Blocked** |
| Webhook management | Full access | **Blocked** |
| Module management | Full access | **Blocked** |
| Cache clearing | Full access | **Blocked** |
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
- "User management is disabled in demo mode"
- "API key management is disabled in demo mode"

## Allowed Actions

Demo mode still allows users to:

- Browse all admin pages
- Create and edit pages
- Upload media (with size limit)
- Create and edit tags, categories
- Create and edit menus, menu items
- Create and edit forms
- View all settings and configurations
- Use the REST API for read operations

## Upload Size Limit

In demo mode, file uploads are limited to **2MB** to prevent storage abuse.

```go
const DemoUploadMaxSize = 2 * 1024 * 1024 // 2MB
```

## Scheduled Reset

For public demos, combine demo mode with scheduled database resets:

```bash
# Reset demo daily at 3 AM UTC
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
- **Storage exhaustion** - Upload size limits

It does **not** prevent:

- Creating spam content (use scheduled reset)
- Accessing existing admin features (use separate demo credentials)
- Viewing sensitive information in UI (don't use real data)

## Fly.io Demo Deployment

See [Fly.io README](./.fly/README.md) for complete deployment instructions.

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
