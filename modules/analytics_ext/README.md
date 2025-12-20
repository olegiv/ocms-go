# External Analytics Module

A statistics tracking module for oCMS supporting Google Analytics 4 (GA4), Google Tag Manager (GTM), and Matomo.

## Features

- Google Analytics 4 (GA4) integration
- Google Tag Manager (GTM) integration
- Matomo (self-hosted analytics) integration
- Enable/disable each tracker independently
- Template functions for injecting tracking scripts
- Admin interface for configuration
- Settings persisted in database

## Supported Platforms

| Platform | ID Format | Example |
|----------|-----------|---------|
| Google Analytics 4 | G-XXXXXXXXXX | G-ABC123XYZ |
| Google Tag Manager | GTM-XXXXXXX | GTM-ABC1234 |
| Matomo | URL + Site ID | https://matomo.example.com + 1 |

## Admin Interface

Access at **Admin > Modules > External Analytics** or `/admin/external-analytics`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/external-analytics` | Settings dashboard |
| POST | `/admin/external-analytics` | Save settings |

## Configuration

### Google Analytics 4 (GA4)

1. Navigate to External Analytics settings
2. Enable GA4
3. Enter your Measurement ID (G-XXXXXXXXXX)
4. Save settings

The tracking script is automatically injected into `<head>` on all public pages.

### Google Tag Manager (GTM)

1. Navigate to External Analytics settings
2. Enable GTM
3. Enter your Container ID (GTM-XXXXXXX)
4. Save settings

GTM script is injected in `<head>` with noscript fallback at end of `<body>`.

> **Note**: When GTM is enabled, standalone GA4 script injection is disabled since GA4 is typically configured within GTM.

### Matomo

1. Navigate to External Analytics settings
2. Enable Matomo
3. Enter your Matomo server URL (e.g., https://matomo.example.com/)
4. Enter your Site ID
5. Save settings

Matomo tracking script is injected in `<head>` with image tracker fallback for noscript.

## Template Functions

Add these functions to your theme's base template:

```html
<!DOCTYPE html>
<html>
<head>
    ...
    {{ analyticsExtHead }}
</head>
<body>
    ...
    {{ analyticsExtBody }}
</body>
</html>
```

### `analyticsExtHead`

Returns tracking scripts for the `<head>` section:
- GTM script tag
- GA4 gtag.js (when GTM is disabled)
- Matomo tracking code

### `analyticsExtBody`

Returns tracking scripts for end of `<body>`:
- GTM noscript iframe fallback
- Matomo image tracker fallback

## Database Schema

Settings are stored in a single-row table:

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

## Module Structure

```
modules/analytics_ext/
├── module.go      # Module definition, lifecycle, template funcs, migrations
├── handlers.go    # HTTP handlers (dashboard, save settings)
├── settings.go    # Settings struct, load/save, script rendering
└── locales/       # Embedded i18n translations
    ├── en/messages.json
    └── ru/messages.json
```

## Script Injection Details

### GTM Head Script

```html
<!-- Google Tag Manager -->
<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':
new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],
j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src=
'https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);
})(window,document,'script','dataLayer','GTM-XXXXXXX');</script>
<!-- End Google Tag Manager -->
```

### GTM Body Fallback

```html
<!-- Google Tag Manager (noscript) -->
<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=GTM-XXXXXXX"
height="0" width="0" style="display:none;visibility:hidden"></iframe></noscript>
<!-- End Google Tag Manager (noscript) -->
```

### GA4 Script (standalone)

```html
<!-- Google Analytics 4 -->
<script async src="https://www.googletagmanager.com/gtag/js?id=G-XXXXXXXXXX"></script>
<script>
  window.dataLayer = window.dataLayer || [];
  function gtag(){dataLayer.push(arguments);}
  gtag('js', new Date());
  gtag('config', 'G-XXXXXXXXXX');
</script>
<!-- End Google Analytics 4 -->
```

### Matomo Script

```html
<!-- Matomo -->
<script>
  var _paq = window._paq = window._paq || [];
  _paq.push(['trackPageView']);
  _paq.push(['enableLinkTracking']);
  (function() {
    var u="https://matomo.example.com/";
    _paq.push(['setTrackerUrl', u+'matomo.php']);
    _paq.push(['setSiteId', '1']);
    var d=document, g=d.createElement('script'), s=d.getElementsByTagName('script')[0];
    g.async=true; g.src=u+'matomo.js'; s.parentNode.insertBefore(g,s);
  })();
</script>
<!-- End Matomo Code -->
```

## Internationalization

Translations are embedded and automatically loaded. Supported languages:

- English (en)
- Russian (ru)

Add new languages by creating `locales/{lang}/messages.json`.

## Module Active Status

The module can be enabled/disabled from **Admin > Modules**:

- **Active**: Routes accessible, template functions inject scripts
- **Inactive**: Routes return 404/redirect, no scripts injected

## Security

- All IDs are HTML-escaped before injection
- Settings require admin authentication
- CSRF protection on form submission
- Trailing slashes on Matomo URLs are normalized

## Validation

When saving settings:
- GA4 enabled requires Measurement ID
- GTM enabled requires Container ID
- Matomo enabled requires both URL and Site ID

Validation errors are displayed as flash messages.
