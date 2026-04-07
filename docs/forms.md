# Forms

oCMS includes a built-in form builder for creating contact forms, surveys, and other data collection forms. Forms support multiple field types, multi-language translations, honeypot spam protection, hCaptcha integration, webhook notifications, and CSV export.

## Overview

The forms system consists of three parts:

1. **Form builder** -- admin UI for creating forms and configuring fields
2. **Public rendering** -- themed form pages at `/forms/{slug}` with validation and spam protection
3. **Submission management** -- admin UI for viewing, filtering, and exporting submissions

## Form Fields

### Supported Types

| Type | Description | Options | Validation |
|------|-------------|---------|------------|
| `text` | Single-line text input | -- | minLength, maxLength, pattern |
| `email` | Email input | -- | Built-in email format check |
| `textarea` | Multi-line text (5 rows) | -- | minLength, maxLength |
| `number` | Numeric input | -- | Built-in float parsing |
| `date` | Date picker (YYYY-MM-DD) | -- | Built-in date format check |
| `select` | Dropdown menu | JSON array of options | -- |
| `radio` | Radio button group | JSON array of options | -- |
| `checkbox` | Checkbox group (multi-select) | JSON array of options | -- |
| `file` | File upload input | -- | Parsed but not processed |
| `captcha` | hCaptcha widget | -- | Verified via hook (not stored) |

### Options Format

For `select`, `radio`, and `checkbox` fields, options are stored as a JSON array:

```json
["Option 1", "Option 2", "Option 3"]
```

### Validation Format

Custom validation rules are stored as a JSON object:

```json
{
  "minLength": 5,
  "maxLength": 200,
  "pattern": "^[a-zA-Z ]+$"
}
```

- `minLength` -- minimum character count
- `maxLength` -- maximum character count
- `pattern` -- regex pattern the value must match

Validation is applied server-side. Required fields use the `is_required` flag.

## Public Forms

Forms are accessible at `GET /forms/{slug}` and submitted via `POST /forms/{slug}`.

### Submission Flow

1. User loads form page (rendered via active theme)
2. User fills in fields and submits
3. Server validates payload size, honeypot, captcha, and field rules
4. On success: stores submission, dispatches webhook, renders success message
5. On failure: re-renders form with errors and preserved field values

### Success Message

Each form has a configurable `success_message` field. Default: "Thank you for your submission."

## Security

### Honeypot Protection

All public forms include a hidden `_website` field (invisible to users via CSS). Bots that fill this field are silently rejected -- they receive a fake success response.

When triggered:
- A `WARNING`-level security event is logged
- The `security.honeypot_triggered` hook fires
- If Sentinel module is active, the IP is automatically banned (see [Sentinel docs](sentinel-module.md))

### CSRF Protection

Forms use the global CSRF middleware (`filippo.io/csrf/gorilla`) which validates requests via Fetch metadata headers. No token is embedded in the form HTML.

### Rate Limiting

Public form submissions are rate-limited at **1 request per second** with a **burst of 5** per IP address. Excess submissions receive a 429 Too Many Requests response.

### Payload Size Limits

| Limit | Value | Description |
|-------|-------|-------------|
| Total body | 64 KB | Maximum request body size |
| Per field | 4 KB | Maximum single field value |
| Total data | 16 KB | Maximum combined submission data |

Oversized submissions are rejected with a security warning event logged.

## Captcha Integration

Forms support hCaptcha via the optional hCaptcha module.

### Setup

1. Enable the hCaptcha module and configure site key / secret key
2. Add a `captcha` field to the form (only one per form)
3. Optionally set `requireCaptcha` to mandate captcha on all forms with a captcha field

### Behavior

- The captcha widget is rendered via the `form.captcha_widget` hook
- Verification uses the `form.captcha_verify` hook
- Captcha responses are **not stored** in submission data
- If `requireCaptcha` is enabled but the captcha field or hCaptcha module is missing, submissions return 503

## Submissions Management

### Admin List

Access submissions at **Admin > Forms > [Form] > Submissions**.

Features:
- Sortable by: ID, created date, read status, IP address
- Default sort: newest first
- Pagination with configurable per-page count
- Preview of first 2 field values (truncated to 30 chars)
- Unread submission count badge
- Read status tracking (auto-marked when viewed)

### View Submission

Viewing a submission displays all field labels and values, IP address, user agent, and timestamp. The submission is automatically marked as read.

### Bulk Delete

Select multiple submissions and delete them in bulk from the submissions list.

### CSV Export

Export all submissions for a form as CSV. Columns include:
- ID, Submitted At, IP Address, Read status
- All field labels as column headers with corresponding values

## Webhook Integration

Form submissions trigger a `form.submitted` webhook event.

### Payload Data Modes

Controlled by the `OCMS_WEBHOOK_FORM_DATA_MODE` environment variable:

| Mode | Description |
|------|-------------|
| `redacted` (default) | Sensitive fields masked as `[REDACTED]`, others truncated to 1024 chars |
| `none` | No form data included in webhook payload |
| `full` | All data included, values truncated to 1024 chars |

### Sensitive Field Detection

Fields are considered sensitive if their name (lowercased, hyphens replaced with underscores) contains any of these tokens:

`password`, `passwd`, `token`, `secret`, `api_key`, `apikey`, `authorization`, `auth`, `ssn`, `credit`, `card`, `cvv`

### Production Safety

Set `OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION=true` to prevent startup when `OCMS_WEBHOOK_FORM_DATA_MODE=full` in production environments. This is enabled by default in production when the variable is unset.

## Multi-Language Support

Forms support translations via the `language_code` field:

- Each form belongs to a specific language
- The same slug can exist across different languages
- Translations are linked via the `translations` table
- Creating a translation copies the form structure to the target language
- The admin UI shows available and missing translations per form

## Admin Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/forms` | List all forms |
| GET | `/admin/forms/new` | New form page |
| POST | `/admin/forms` | Create form |
| GET | `/admin/forms/{id}` | Edit form / field builder |
| PUT | `/admin/forms/{id}` | Update form metadata |
| DELETE | `/admin/forms/{id}` | Delete form |
| POST | `/admin/forms/{id}/fields` | Add field (JSON) |
| PUT | `/admin/forms/{id}/fields/{fieldId}` | Update field (JSON) |
| DELETE | `/admin/forms/{id}/fields/{fieldId}` | Delete field |
| POST | `/admin/forms/{id}/fields/reorder` | Reorder fields (JSON) |
| GET | `/admin/forms/{id}/submissions` | List submissions |
| GET | `/admin/forms/{id}/submissions/{subId}` | View submission |
| DELETE | `/admin/forms/{id}/submissions/{subId}` | Delete submission |
| POST | `/admin/forms/{id}/submissions/bulk-delete` | Bulk delete submissions |
| POST | `/admin/forms/{id}/submissions/export` | Export submissions as CSV |
| POST | `/admin/forms/{id}/translate` | Create form translation |

## Public Routes

| Method | Path | Rate Limited | Description |
|--------|------|-------------|-------------|
| GET | `/forms/{slug}` | No | Display form |
| POST | `/forms/{slug}` | Yes (1/s, burst 5) | Submit form |

## Database Schema

### forms

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment ID |
| `name` | TEXT | Form display name |
| `slug` | TEXT | URL slug (unique per language) |
| `title` | TEXT | Title shown to users |
| `description` | TEXT | Optional subtitle/description |
| `success_message` | TEXT | Message after submission |
| `email_to` | TEXT | Notification email (reserved) |
| `is_active` | BOOLEAN | Whether publicly accessible |
| `language_code` | TEXT | ISO language code |
| `created_at` | DATETIME | Creation timestamp |
| `updated_at` | DATETIME | Last update timestamp |

### form_fields

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment ID |
| `form_id` | INTEGER FK | Reference to forms table |
| `type` | TEXT | Field type (see supported types) |
| `name` | TEXT | HTML field name |
| `label` | TEXT | Display label |
| `placeholder` | TEXT | Placeholder text |
| `help_text` | TEXT | Helper text below field |
| `options` | TEXT | JSON array for select/radio/checkbox |
| `validation` | TEXT | JSON validation rules |
| `is_required` | BOOLEAN | Whether field is required |
| `position` | INTEGER | Sort order |
| `language_code` | TEXT | ISO language code |
| `created_at` | DATETIME | Creation timestamp |
| `updated_at` | DATETIME | Last update timestamp |

### form_submissions

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment ID |
| `form_id` | INTEGER FK | Reference to forms table |
| `data` | TEXT | JSON string of field values |
| `ip_address` | TEXT | Submitter's IP address |
| `user_agent` | TEXT | Submitter's user agent |
| `is_read` | BOOLEAN | Read status |
| `language_code` | TEXT | ISO language code |
| `created_at` | DATETIME | Submission timestamp |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OCMS_WEBHOOK_FORM_DATA_MODE` | `redacted` | Webhook payload mode: `redacted`, `none`, `full` |
| `OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION` | `true` in prod | Block startup if mode is `full` in production |
