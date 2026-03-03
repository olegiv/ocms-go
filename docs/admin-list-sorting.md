# Admin List Sorting

Column sorting is available on delete-capable admin pagers.

## Supported Views

- Pages (`/admin/pages`)
- Tags (`/admin/tags`)
- Users (`/admin/users`)
- API keys (`/admin/api-keys`)
- Media library (`/admin/media`)
- Form submissions (`/admin/forms/{id}/submissions`)

Read-only/history pagers are intentionally unchanged:

- Events
- Page versions
- Webhook deliveries
- Scheduler task runs

## Query Parameters

- `sort=<field>`
- `dir=asc|desc`

The current sort state is URL-driven and is preserved across pagination and
items-per-page changes.

## UI Behavior

- Table views use sortable column headers.
- Media (grid view) exposes sort controls in the filter bar.
- Active sorting is highlighted with a visible pill-style header state (not just an arrow icon).
- Clicking the active column toggles direction.
- Changing sort field resets `page=1`.
- Invalid/unknown `sort` or `dir` values safely fall back to per-view defaults.

## Pages Default Sort

- `/admin/pages` defaults to `sort=updated_at&dir=desc` when sort params are absent.

## Sort Safety

Sorting is whitelist-based per view. Only predefined fields are accepted for
ORDER BY. User input is never interpolated directly into SQL clauses.
