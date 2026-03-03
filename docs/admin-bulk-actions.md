# Admin Bulk Actions

Bulk multi-select and bulk delete/revoke are available on delete-capable paged admin lists.

## Supported Views

- Pages list (`/admin/pages`)
- Tags list (`/admin/tags`)
- Users list (`/admin/users`)
- API keys list (`/admin/api-keys`) as **bulk revoke** (deactivation)
- Media library (`/admin/media`)
- Form submissions list (`/admin/forms/{id}/submissions`)

Read-only/history pagers are unchanged:

- Events
- Page versions
- Webhook deliveries
- Scheduler task runs

## Behavior

- Selection scope is **current page only**.
- Selection is reset naturally when navigating to another page.
- Bulk operations use **partial success**:
  - valid IDs are processed;
  - missing/protected/invalid IDs are returned in `failed`.
- A confirmation dialog is shown before bulk delete/revoke.
- After success or partial success, the current page is reloaded to refresh counts and rows.
- Delete-capable views include an **items-per-page selector**.
- Selector state is **URL-only** via `per_page`; changing it resets `page=1`.
- Selector options are `10, 20, 50, 100` (media also includes `24` to preserve its legacy default).

## API Endpoints

All bulk endpoints accept:

```json
{"ids":[1,2,3]}
```

Success response:

```json
{"success":true,"deleted":2,"failed":[{"id":3,"reason":"..."}]}
```

Error response:

```json
{"success":false,"error":"..."}
```

Endpoints:

- `POST /admin/pages/bulk-delete`
- `POST /admin/tags/bulk-delete`
- `POST /admin/users/bulk-delete`
- `POST /admin/api-keys/bulk-delete` (revokes/deactivates keys)
- `POST /admin/media/bulk-delete`
- `POST /admin/forms/{id}/submissions/bulk-delete`
