# External Analytics Module

The External Analytics module injects third-party tracking scripts into public pages. It supports Google Analytics 4 (GA4), Google Tag Manager (GTM), and Matomo. All three trackers can be enabled independently and configured from the admin UI.

This is the operator-facing overview. For theme integration details (script snippets, noscript fallbacks, exact injection locations), see the module's own README at `modules/analytics_ext/README.md`.

## Overview

### Features

- Google Analytics 4 (GA4) — standalone `gtag.js` injection
- Google Tag Manager (GTM) — head script + `<noscript>` fallback
- Matomo — self-hosted analytics with image-tracker fallback
- Per-tracker enable/disable from a single settings page
- Settings persisted in a single-row `analytics_settings` table
- Embedded i18n (English, Russian)

### How It Works

On initialization the module loads its settings, registers two admin routes, and exposes two template helpers (`analyticsExtHead`, `analyticsExtBody`) that themes call from the base layout. Public routes are intentionally empty; tracking happens client-side in the visitor's browser.

When GTM is enabled, the standalone GA4 script is suppressed — GA4 is expected to be configured inside GTM to avoid double-counting.

## Admin Interface

Access at **Admin > Modules > External Analytics** or `/admin/external-analytics`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/external-analytics` | Settings dashboard |
| POST | `/admin/external-analytics` | Save settings |

### Supported trackers

| Platform | Identifier format | Example |
|----------|-------------------|---------|
| Google Analytics 4 | `G-XXXXXXXXXX` | `G-ABC123XYZ` |
| Google Tag Manager | `GTM-XXXXXXX` | `GTM-ABC1234` |
| Matomo | Server URL + Site ID | `https://matomo.example.com/` + `1` |

### Validation rules

When saving:

- GA4 enabled requires a non-empty Measurement ID.
- GTM enabled requires a non-empty Container ID.
- Matomo enabled requires both a server URL and a Site ID.
- Trailing slashes on Matomo URLs are normalized before storage.

Errors appear as flash messages.

## Template Functions

Themes call these from the base template:

| Function | Location | Returns |
|----------|----------|---------|
| `analyticsExtHead` | Inside `<head>` | GA4 / GTM / Matomo head scripts |
| `analyticsExtBody` | End of `<body>` | GTM `<noscript>` iframe, Matomo image fallback |

Both functions return `template.HTML`; all IDs are HTML-escaped before injection.

## Database Schema

Single-row table; all settings live together:

```sql
CREATE TABLE analytics_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    ga4_enabled INTEGER NOT NULL DEFAULT 0,
    ga4_measurement_id TEXT NOT NULL DEFAULT '',
    gtm_enabled INTEGER NOT NULL DEFAULT 0,
    gtm_container_id TEXT NOT NULL DEFAULT '',
    matomo_enabled INTEGER NOT NULL DEFAULT 0,
    matomo_url TEXT NOT NULL DEFAULT '',
    matomo_site_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## IP Exclusion

External trackers run in the visitor's browser, so oCMS cannot exclude IPs server-side for GA4/GTM/Matomo. Configure exclusions directly in each platform:

- **Google Analytics 4**: Admin > Data Streams > Configure Tag Settings > Define Internal Traffic.
- **Google Tag Manager**: use built-in IP variables to filter inside tag triggers.
- **Matomo**: Administration > Websites > Excluded IPs.

For server-side exclusion of internal or pen-testing traffic, use the **Internal Analytics** module's `Excluded IPs` setting (see `docs/analytics-int-module.md`).

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/analytics_ext/...
```

Covers settings load/save, script rendering, and HTML escaping of IDs.
