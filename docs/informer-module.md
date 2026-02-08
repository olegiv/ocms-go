# Informer Module

The Informer module displays a dismissible notification bar with a spinning indicator at the top of the page. It is useful for showing maintenance notices, announcements, or alerts to visitors.

## Features

- **Notification bar** fixed at the top of the page with a spinning indicator
- **Dismissible** - visitors can close the bar; their preference is stored in a cookie
- **Customizable colors** - configurable background and text colors via the admin panel
- **Translatable** - the notification text can be translated via locale files
- **Admin control** - enable/disable and update text from `/admin/informer`

## Admin Settings

Navigate to **Modules > Informer** in the admin panel to configure:

| Setting | Description | Default |
|---------|-------------|---------|
| Enabled | Show/hide the notification bar | Off |
| Notification Text | The message displayed in the bar | Empty |
| Background Color | Bar background color | `#1e40af` (blue) |
| Text Color | Text and icon color | `#ffffff` (white) |

The admin page includes a live preview that updates as you change settings.

## Cookie-Based Dismissal

When a visitor clicks the close button:

1. The bar is hidden immediately
2. A cookie (`ocms_informer_dismissed`) is set with a 365-day expiration
3. On subsequent visits, the bar remains hidden as long as the cookie exists

The bar is rendered with `display:none` by default and shown via JavaScript only if the dismissal cookie is not present. This prevents a flash of the bar for returning visitors.

## Translating the Bar Text

The notification text entered in the admin panel is used as-is. For multi-language sites, you can provide translated versions of the text by adding entries to your theme or global locale files:

```json
{
    "messages": [
        {"id": "informer.bar_text", "message": "Maintenance in progress", "translation": "Wartungsarbeiten..."}
    ]
}
```

The admin UI labels and messages are already translated in `en` and `ru` locales within the module.

## Theme Integration

The informer bar is injected into the page via the `informerBar` template function, which is called in the theme's `base.html` layout:

```html
<body>
    {{if informerBar}}{{informerBar}}{{end}}
    ...
</body>
```

Both the `default` and `developer` themes include this integration. Custom themes should add the same line after the opening `<body>` tag.

## Technical Details

- **Module name**: `informer`
- **Database table**: `informer_settings` (single row, id=1)
- **Cookie name**: `ocms_informer_dismissed`
- **Admin URL**: `/admin/informer`
- **Template function**: `informerBar` - returns the full HTML/CSS/JS for the bar

The bar uses a CSS animation (`@keyframes informer-spin`) for the spinning indicator and inline styles for color customization. It is positioned as `position: fixed; top: 0; z-index: 9999` to overlay all page content.
