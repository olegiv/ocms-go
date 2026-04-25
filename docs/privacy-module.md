# Privacy Module

The Privacy module adds cookie-consent management to oCMS using [Klaro](https://klaro.org/) plus Google Consent Mode v2 (GCM). When enabled, it injects the Klaro consent banner on public pages, defers non-essential tracking scripts until the visitor consents, and signals consent state to Google services via GCM.

Pair this module with **External Analytics** or any third-party tracker that must respect visitor consent. The Internal Analytics module does not require consent because it stores no personal data (see `docs/analytics-int-module.md`).

## Overview

### Features

- Klaro-based cookie banner with configurable theme (`light` / `dark`) and corner position (`bottom-right`, `bottom-left`, `top-right`, `top-left`)
- Google Consent Mode v2 default-state bootstrap before any analytics loads
- Per-service consent rules (name, title, description, purposes, cookie patterns)
- Configurable cookie name, expiry, and debug mode
- Link to the configured privacy policy URL
- Template helper for a footer "Manage consent" link
- Embedded i18n (English, Russian)

### How It Works

1. `privacyHead` is called by the theme *before* any tracker injection — it emits:
   - The GCM `dataLayer.push(['consent','default',...])` block reflecting the configured defaults, and
   - The Klaro configuration and loader.
2. Visitor interaction with the Klaro banner updates the consent cookie, which in turn triggers Klaro's `updateGtagConsent` so GCM-aware scripts (e.g. GA4 via External Analytics) start tracking once consent is granted.
3. The `privacyFooterLink` helper renders a themed `<a>` element that reopens the consent modal from the site footer.

Essential cookies (Klaro's own consent cookie plus oCMS session / language cookies) are declared as a non-opt-out service so visitors cannot turn them off.

## Admin Interface

Access at **Admin > Privacy** or `/admin/privacy`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/privacy` | Settings dashboard |
| POST | `/admin/privacy` | Save settings |

### Settings

| Group | Setting | Description |
|-------|---------|-------------|
| General | Enabled | Toggle the consent banner on/off |
| General | Privacy policy URL | Linked from the banner footer |
| General | Debug | Enables Klaro debug logging in the browser console |
| Cookie | Cookie name | Name of the Klaro consent cookie (default `klaro`) |
| Cookie | Cookie expires (days) | Default `365` |
| Appearance | Theme | `light` or `dark` |
| Appearance | Position | Banner placement |
| Google Consent Mode v2 | GCM enabled | Emit the GCM default block |
| Google Consent Mode v2 | Default analytics / ad storage / ad user data / ad personalization | Default `denied` (0); flip to granted (1) per purpose |
| Google Consent Mode v2 | Wait for update (ms) | How long GCM waits for consent before applying defaults (default `500`) |
| Services | JSON array | Declare each tracked service — name, purposes, cookie patterns, required flag |

## Template Functions

Themes call these from the base layout:

| Function | Where | Notes |
|----------|-------|-------|
| `privacyHead nonce` | Inside `<head>` | **Must be emitted BEFORE `analyticsExtHead`** so GCM defaults initialize before GA4 loads. |
| `privacyFooterLink langCode` | Anywhere in the footer | Returns `""` if the module is disabled. |

Both helpers return `template.HTML` and respect the CSP nonce.

## Database Schema

Single-row settings table, three migrations:

```sql
CREATE TABLE privacy_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    privacy_policy_url TEXT NOT NULL DEFAULT '',
    cookie_name TEXT NOT NULL DEFAULT 'klaro',
    cookie_expires_days INTEGER NOT NULL DEFAULT 365,
    theme TEXT NOT NULL DEFAULT 'light',
    position TEXT NOT NULL DEFAULT 'bottom-right',
    gcm_enabled INTEGER NOT NULL DEFAULT 1,
    gcm_default_analytics INTEGER NOT NULL DEFAULT 0,
    gcm_default_ad_storage INTEGER NOT NULL DEFAULT 0,
    gcm_default_ad_user_data INTEGER NOT NULL DEFAULT 0,
    gcm_default_ad_personalization INTEGER NOT NULL DEFAULT 0,
    gcm_wait_for_update INTEGER NOT NULL DEFAULT 500,
    services TEXT NOT NULL DEFAULT '[]',        -- JSON array of Service
    debug INTEGER NOT NULL DEFAULT 0,           -- migration 2
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Migration 3 renames and normalizes the built-in Essential Cookies service so it consistently covers `^klaro`, `^ocms_lang$`, `^(__Host-)?session$`, and `^ocms_informer_dismissed`.

## Interaction with Other Modules

- **External Analytics**: declare each tracker (GA4, GTM, Matomo) as a privacy service so consent gates injection. With GCM enabled, GA4/GTM respond to consent updates without re-rendering the page.
- **Internal Analytics**: does **not** require consent because no personal data is stored. No service entry needed.
- **Forms (captcha)**: if hCaptcha is enabled, add it as a service with required purposes so the visitor understands what the challenge loads.

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/privacy/...
```

Coverage includes settings load/save, services JSON round-trip, and migration 3's normalization of the essential-cookies service.
