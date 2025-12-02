# Multi-Language Support

oCMS provides comprehensive multi-language support for both content translation and admin UI localization.

## Overview

The multi-language system consists of two parts:
1. **Content Translation**: Translate pages, categories, tags, and menus into multiple languages
2. **Admin UI Localization**: Display the admin interface in different languages (currently English and Russian)

## Setting Up Languages

### Adding Languages

1. Navigate to **Admin > Config > Languages**
2. Click **Add Language**
3. Fill in:
   - **Code**: ISO 639-1 language code (e.g., `ru`, `de`, `fr`)
   - **Name**: English name (e.g., "Russian")
   - **Native Name**: Name in the language (e.g., "Русский")
   - **Direction**: LTR (left-to-right) or RTL (right-to-left)
   - **Active**: Whether the language is available on the site
4. Click **Save**

### Default Language

One language must be set as the default. This is the fallback language when:
- A translation doesn't exist for the requested language
- No language preference is detected
- The URL doesn't include a language prefix

To set the default language:
1. Go to **Admin > Config > Languages**
2. Click the **Set Default** button next to the desired language

### Language Order

Languages appear in the language switcher in the order specified by the **Position** field. Drag and drop to reorder, or manually set position numbers.

## Translating Content

### Translating Pages

1. Open a page in the editor
2. Look for the **Translations** panel in the sidebar
3. Click **Add Translation** for the target language
4. A new page is created with:
   - The same title (editable)
   - Empty body content (to be translated)
   - Link to the original page
5. Translate the content and save

### Translation Links

Translation links connect content across languages. When you create a translation:
- The original page and translation are bidirectionally linked
- The language switcher shows available translations
- Deleting a page removes its translation links

### Viewing Translations

In the page list, translated pages show language badges indicating:
- The page's language
- Number of translations available

Filter pages by language using the language dropdown in the list view.

### Translating Categories and Tags

Categories and tags work the same way as pages:
1. Open the category/tag editor
2. Set the language for the item
3. Create translations using the Translations panel

## Frontend Language Handling

### URL Structure

Languages are indicated in URLs using prefixes:
- Default language: `/about-us`
- Other languages: `/ru/about-us`, `/de/about-us`

### Language Detection

The language is determined in this order:
1. **URL prefix**: `/ru/page-slug` → Russian
2. **Cookie preference**: Stored from previous selection
3. **Accept-Language header**: Browser preference
4. **Default language**: Fallback

### Language Switcher

The frontend language switcher (if enabled in your theme) shows:
- Current language
- Available translations for the current page
- Links to homepage in other languages (if no translation exists)

Include the language switcher in your theme:
```html
{{template "partials/language_switcher.html" .}}
```

### RTL Support

For RTL languages (Arabic, Hebrew, etc.):
- Set **Direction** to RTL when configuring the language
- The `<html>` tag automatically gets `dir="rtl"`
- Your theme should include RTL-specific CSS

## Admin UI Localization

### Changing Admin Language

1. Click your username in the admin header
2. Select **Language** from the dropdown
3. Choose your preferred language
4. The admin interface reloads in the selected language

### Supported Admin Languages

Currently supported:
- English (`en`)
- Russian (`ru`)

### Adding Admin Translations

Admin UI translations are stored in `internal/i18n/locales/`. To add a new language:

1. Create a new directory: `internal/i18n/locales/{lang}/`
2. Create `messages.json` with translations:
```json
{
    "language": "de",
    "messages": [
        {
            "id": "nav.dashboard",
            "message": "Dashboard",
            "translation": "Übersicht"
        },
        {
            "id": "btn.save",
            "message": "Save",
            "translation": "Speichern"
        }
    ]
}
```
3. Add the language code to `SupportedLanguages` in `internal/i18n/i18n.go`
4. Rebuild the application

## Theme Integration

### Accessing Language in Templates

In theme templates, you have access to:
```html
<!-- Current language code -->
{{.CurrentLanguage}}

<!-- Language direction (ltr/rtl) -->
{{.LanguageDirection}}

<!-- Available translations for current page -->
{{range .Translations}}
    <a href="{{.URL}}">{{.NativeName}}</a>
{{end}}
```

### hreflang Tags

The system automatically generates hreflang meta tags for SEO:
```html
<link rel="alternate" hreflang="en" href="https://example.com/about-us">
<link rel="alternate" hreflang="ru" href="https://example.com/ru/about-us">
<link rel="alternate" hreflang="x-default" href="https://example.com/about-us">
```

### Menu Translation

Create separate menus for each language:
1. Create a menu (e.g., "Main Menu - EN")
2. Set the menu's language to English
3. Create another menu (e.g., "Main Menu - RU")
4. Set the menu's language to Russian
5. In your theme, load the menu based on current language

## API Access

### Language Headers

API responses include language information in headers:
- `Content-Language`: The language of the response content

### Filtering by Language

Filter API responses by language:
```bash
curl "http://localhost:8080/api/v1/pages?language=ru"
```

### Translation Data

Pages include translation information:
```json
{
    "data": {
        "id": 1,
        "title": "About Us",
        "language_id": 1,
        "language_code": "en",
        "translations": [
            {
                "language_code": "ru",
                "page_id": 5,
                "slug": "o-nas"
            }
        ]
    }
}
```

## Best Practices

1. **Set up languages first**: Configure all languages before creating content
2. **Translate systematically**: Create a workflow for translating content
3. **Use consistent slugs**: Consider using transliterated slugs for better SEO
4. **Test RTL**: If supporting RTL languages, test thoroughly
5. **Plan navigation**: Decide how users will discover translated content
6. **Consider SEO**: Ensure hreflang tags are correctly implemented

## Troubleshooting

### Translation not showing in switcher
- Ensure the translated page is published
- Check that the translation link exists (in the Translations panel)
- Verify the target language is active

### Wrong language displayed
- Check URL for language prefix
- Clear browser cookies
- Verify language detection order

### Missing admin translations
- Check that `messages.json` exists for the language
- Ensure all required message keys are present
- Restart the server after adding translations
