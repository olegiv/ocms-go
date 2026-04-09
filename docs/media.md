# Media Library

OCMS includes a media library for managing uploaded files with automatic image processing.

## Image Variants

When an image is uploaded, OCMS automatically generates multiple size variants:

| Variant | Dimensions | Quality | Mode | Purpose |
|---------|------------|---------|------|---------|
| `originals` | Original size | 95% | — | Archive/download |
| `large` | 1920x1080 max | 90% | Fit | Single page featured images |
| `og` | 1200x630 max | 85% | Fit | Open Graph / social sharing previews |
| `medium` | 800x600 max | 85% | Fit | Listing pages (homepage, categories, tags) |
| `small` | 400x300 max | 85% | Fit | Compact listings |
| `grid` | 256x256 | 85% | Crop | Admin grid views |
| `thumbnail` | 150x150 | 80% | Crop | Search results, admin previews |

### Storage Structure

```
uploads/
├── originals/{UUID}/{filename}
├── large/{UUID}/{filename}
├── og/{UUID}/{filename}
├── medium/{UUID}/{filename}
├── small/{UUID}/{filename}
├── grid/{UUID}/{filename}
└── thumbnail/{UUID}/{filename}
```

### URL Format

```
/uploads/{variant}/{UUID}/{filename}
```

Examples:
- `/uploads/originals/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/large/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/og/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/medium/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/thumbnail/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`

## Frontend Usage

Different contexts use different image variants for optimal performance:

| Context | Variant | Reason |
|---------|---------|--------|
| Single page featured image | `large` | High quality for main content |
| OG image (social sharing) | `og` | Optimized 1200x630 for social platforms (< 200KB) |
| Homepage listings | `medium` | Balance of quality and speed |
| Category/tag archives | `medium` | Balance of quality and speed |
| Search results | `thumbnail` | Fast loading, small previews |
| Admin media picker | `grid` | Quick grid view |

OG image variant selection priority: `og` > `large` > `medium` > `thumbnail`.

## API Response

The REST API returns all variant URLs:

```json
{
  "uuid": "885181c9-2d79-403d-a6e5-6e06ddb3f107",
  "filename": "photo.jpg",
  "urls": {
    "original": "/uploads/originals/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "large": "/uploads/large/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "og": "/uploads/og/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "medium": "/uploads/medium/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "thumbnail": "/uploads/thumbnail/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg"
  }
}
```

## Featured Image Requirements

When assigning a featured image to a page, the image must meet minimum size requirements:

- **Minimum dimensions**: 1200 x 800 pixels
- **Supported formats**: JPEG, PNG, GIF, WebP

Size validation is enforced when creating a page or changing the featured image on an existing page. If a page already has a featured image that is below the minimum size, saving other fields on that page will not trigger validation — the existing image is grandfathered.

## Supported Formats

- **Images**: JPEG, PNG, GIF, WebP
- **Documents**: PDF (stored without variants)
- **Other**: Files are stored in `originals` only

## Cache Headers

Uploaded files are served with cache headers for optimal performance:

- **Uploads**: `Cache-Control: public, max-age=604800` (1 week)

## Theme Templates

In theme templates, use the appropriate variant:

```html
<!-- Single page featured image -->
<img src="{{ .FeaturedImage }}" alt="Featured">

<!-- Listing page (use medium via pageToView) -->
{{ range .Pages }}
<img src="{{ .FeaturedImage }}" alt="{{ .Title }}">
{{ end }}

<!-- OG meta tags (rendered automatically in base.html) -->
<meta property="og:image" content="{{ .OGImage }}">
<meta property="og:image:width" content="{{ .OGImageWidth }}">
<meta property="og:image:height" content="{{ .OGImageHeight }}">
<meta property="og:image:type" content="{{ .OGImageType }}">
```

## Storage Location

Uploaded files are stored in the `./uploads` directory relative to the application root. This path is not configurable via environment variables.
