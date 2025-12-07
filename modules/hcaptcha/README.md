# hCaptcha Module

A bot protection module for oCMS integrating hCaptcha on the login form.

## Features

- hCaptcha widget integration on login form
- Server-side token verification with hCaptcha API
- Environment variable overrides for keys
- Configurable theme (light/dark) and size (normal/compact)
- Template functions for custom integration
- Hook-based architecture for extensibility
- Admin interface for configuration
- Settings persisted in database

## Admin Interface

Access at **Admin > Modules > hCaptcha** or `/admin/hcaptcha`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/hcaptcha` | Settings dashboard |
| POST | `/admin/hcaptcha` | Save settings |

## Configuration

### Via Admin Interface

1. Navigate to hCaptcha settings
2. Enter your Site Key (from hCaptcha dashboard)
3. Enter your Secret Key (from hCaptcha dashboard)
4. Choose theme and size preferences
5. Enable hCaptcha
6. Save settings

### Via Environment Variables

```bash
OCMS_HCAPTCHA_SITE_KEY=your-site-key
OCMS_HCAPTCHA_SECRET_KEY=your-secret-key
```

Environment variables override database settings for keys.

## Test Keys

For development and testing, use hCaptcha's test keys:

| Key | Value |
|-----|-------|
| Site Key | `10000000-ffff-ffff-ffff-000000000001` |
| Secret Key | `0x0000000000000000000000000000000000000000` |

With test keys, the widget auto-passes without showing a challenge.

## Template Functions

### `hcaptchaEnabled`

Returns `true` if hCaptcha is enabled and properly configured.

```html
{{if hcaptchaEnabled}}
    <!-- Show captcha-related content -->
{{end}}
```

### `hcaptchaWidget`

Returns the hCaptcha widget HTML (script + div).

```html
{{if hcaptchaEnabled}}
<div class="captcha-container">
    {{hcaptchaWidget}}
</div>
{{end}}
```

Output:

```html
<script src="https://js.hcaptcha.com/1/api.js" async defer></script>
<div class="h-captcha" data-sitekey="..." data-theme="light" data-size="normal"></div>
```

## Hooks

The module registers two hooks for integration:

### `auth.login_widget`

Called to render the captcha widget in the login form.

### `auth.before_login`

Called before login to verify the captcha response. Receives a `VerifyRequest` struct:

```go
type VerifyRequest struct {
    Response  string // h-captcha-response from form
    RemoteIP  string // Client IP address
    Verified  bool   // Set to true after successful verification
    Error     string // Error message if verification failed
    ErrorCode string // Error code for i18n
}
```

## Database Schema

Settings are stored in a single-row table:

```sql
CREATE TABLE hcaptcha_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    site_key TEXT NOT NULL DEFAULT '',
    secret_key TEXT NOT NULL DEFAULT '',
    theme TEXT NOT NULL DEFAULT 'light',
    size TEXT NOT NULL DEFAULT 'normal',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## Module Structure

```
modules/hcaptcha/
├── module.go      # Module definition, lifecycle, hooks, template funcs, migrations
├── handlers.go    # HTTP handlers (dashboard, save settings)
├── settings.go    # Settings struct, load/save, widget rendering
├── verify.go      # hCaptcha API verification
├── README.md      # This file
└── locales/       # Embedded i18n translations
    ├── en/messages.json
    └── ru/messages.json
```

## Verification Flow

1. User loads login page with hCaptcha widget
2. User completes captcha challenge
3. Form submission includes `h-captcha-response` field
4. Server extracts response and client IP
5. `auth.before_login` hook is called
6. Module verifies token with hCaptcha API (`https://api.hcaptcha.com/siteverify`)
7. Verification result determines if login proceeds

## API Verification

The module sends a POST request to hCaptcha:

```
POST https://api.hcaptcha.com/siteverify
Content-Type: application/x-www-form-urlencoded

secret=your-secret-key&response=token&remoteip=client-ip
```

Response:

```json
{
  "success": true,
  "challenge_ts": "2024-01-01T00:00:00Z",
  "hostname": "example.com",
  "error-codes": []
}
```

## Error Codes

| Code | Description |
|------|-------------|
| `hcaptcha.error_required` | User didn't complete captcha |
| `hcaptcha.error_verification` | API request failed |
| `hcaptcha.error_invalid` | Token rejected by hCaptcha |

## Internationalization

Translations are embedded and automatically loaded. Supported languages:

- English (en)
- Russian (ru)

Add new languages by creating `locales/{lang}/messages.json`.

See [docs/i18n.md](../../docs/i18n.md) for the translation file format and guidelines.

## Module Active Status

The module can be enabled/disabled from **Admin > Modules**:

- **Active**: Hooks registered, widget renders, verification enforced
- **Inactive**: No widget shown, verification skipped

Note: Even when the module is active, hCaptcha protection only applies when `enabled=true` in settings AND both keys are configured.

## Security

- Secret key is masked in admin display (shows `xxxx****xxxx`)
- All keys are HTML-escaped before injection
- Settings require admin authentication
- CSRF protection on form submission
- Client IP extraction supports reverse proxy headers (X-Forwarded-For, X-Real-IP)
- Verification timeout of 10 seconds prevents hanging

## Troubleshooting

### Locked out after enabling

Disable via database:

```bash
sqlite3 ./data/ocms.db "UPDATE hcaptcha_settings SET enabled = 0"
```

### Widget not appearing

1. Check `enabled = 1` in database
2. Verify both site_key and secret_key are set
3. Check browser console for JavaScript errors
4. Ensure hCaptcha script can load (no firewall/ad blocker)

### Verification always fails

1. Verify secret key is correct
2. Check server can reach `https://api.hcaptcha.com`
3. Check server logs for specific error codes
4. Ensure client IP is correctly detected (check proxy headers)

## Dependencies

- hCaptcha API: `https://api.hcaptcha.com/siteverify`
- hCaptcha JS: `https://js.hcaptcha.com/1/api.js`
