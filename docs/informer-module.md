# Informer Module

The Informer module displays a dismissible notification bar with a spinning indicator at the top of the page. It is useful for showing maintenance notices, announcements, or alerts to visitors.

## Features

- **Notification bar** above the page content with a spinning indicator
- **Dismissible** - visitors can close the bar; their preference is stored in a cookie
- **Auto-reset** - saving settings in admin resets all dismissals so everyone sees the updated bar
- **HTML support** - notification text supports sanitized HTML for links, bold, and other formatting
- **Customizable colors** - configurable background and text colors via the admin panel
- **Translated admin UI** - admin labels and messages are translated (`en`, `ru`); the bar text itself is shown as entered
- **Admin control** - enable/disable and update text from `/admin/informer`
- **Demo mode** - automatically enabled with admin access info when `OCMS_DEMO_MODE=true`

## Admin Settings

Navigate to **Modules > Informer** in the admin panel to configure:

| Setting | Description | Default |
|---------|-------------|---------|
| Enabled | Show/hide the notification bar | Off |
| Notification Text | The message displayed in the bar (HTML allowed) | Empty |
| Background Color | Bar background color | `#1e40af` (blue) |
| Text Color | Text and icon color | `#ffffff` (white) |

The admin page includes a live preview that updates as you change settings.

### HTML in Notification Text

The notification text supports HTML tags for rich formatting:

```html
Check our <a href="/sale">sale page</a> for <strong>50% off</strong>!
```

The text is sanitized before it is rendered, with the same bluemonday UGC policy used for page HTML (`security.SanitizePageHTML`): scripts, event-handler attributes and inline `style` attributes are removed, and links get `rel="nofollow"`. Set the colors with the Background Color and Text Color settings instead of inline styles. The colors are HTML-escaped, not sanitized.

## Cookie-Based Dismissal

When a visitor clicks the close button:

1. The bar is hidden immediately
2. A cookie (`ocms_informer_dismissed`) is set with the current settings version and a 365-day expiration
3. On subsequent visits, the bar remains hidden as long as the cookie version matches

When an admin saves settings (any change — text, colors, enable/disable), an internal version counter increments. This invalidates all existing dismissal cookies, so the bar reappears for all visitors.

The bar is rendered with `display:none` by default and shown via JavaScript only if the cookie version does not match the current settings version.

## Demo Mode

When `OCMS_DEMO_MODE=true`, the informer bar is automatically enabled on every startup with a message showing how to access the admin dashboard, including the demo admin email. Because each startup saves the settings again, the version counter increments and earlier dismissals are reset.

The demo password is not part of the page HTML. The bar's JavaScript fetches it at page-view time from the public endpoint `GET /api/informer/demo-credentials` (JSON `{"password": "..."}`, `Cache-Control: no-store`), which answers 404 unless `OCMS_DEMO_MODE=true`. In demo mode the admin settings form refuses to save.

## Translating the Bar Text

The notification text entered in the admin panel is rendered as-is: the module does not look it up in locale files. (The admin help text mentions an `informer.bar_text` locale key, but nothing reads it.) For multi-language sites, put the translated wording in the text itself.

The admin UI labels and messages are translated in `en` and `ru` locales within the module.

## Theme Integration

The informer bar is injected into the page via the `informerBar` template function, which is called in the theme's `base.html` layout. The CSP nonce is passed so the bar's inline scripts are allowed by the Content-Security-Policy:

```html
<body>
    {{informerBar .CSPNonce}}
    ...
</body>
```

Both the `default` and `developer` themes include this integration. Custom themes should add the same line after the opening `<body>` tag.

## Technical Details

- **Module name**: `informer`
- **Database table**: `informer_settings` (single row, id=1)
- **Cookie name**: `ocms_informer_dismissed`
- **Cookie value**: settings version counter (integer)
- **Admin URL**: `/admin/informer`
- **Public route**: `GET /api/informer/demo-credentials` (demo mode only)
- **Template function**: `informerBar` - returns the full HTML/CSS/JS for the bar (takes the CSP nonce as its optional argument)

The bar renders in normal document flow above the page content. It uses a CSS animation (`@keyframes informer-spin`) for the spinning indicator and inline styles for color customization.
