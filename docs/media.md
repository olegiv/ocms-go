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

The quality setting applies to JPEG output only. PNG, GIF and WebP files are re-encoded without one (PNG and WebP losslessly).

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
| OG image (social sharing) | `og` | Sized 1200x630 for social platforms |
| Homepage listings | `thumbnail` as `src`, `small` and `medium` in `srcset` | The browser picks the size it needs |
| Category/tag archives | `thumbnail` as `src`, `small` and `medium` in `srcset` | The browser picks the size it needs |
| Search results | `thumbnail` | Fast loading, small previews |
| Admin media library grid | `grid` | Quick grid view (falls back to `thumbnail`, then the original) |
| Admin media picker | `thumbnail` | Quick selection |

OG image variant selection priority: `og` > `large` > `medium` > `thumbnail`.

## API Response

The REST API returns the URLs of the original, `thumbnail`, `medium` and `large` variants (the last three for every `image/*` file, even an ICO, which gets no variants, so those URLs 404). Add `?include=variants` to list every generated variant with its own URL, including `small`, `grid` and `og`:

```json
{
  "uuid": "885181c9-2d79-403d-a6e5-6e06ddb3f107",
  "filename": "photo.jpg",
  "urls": {
    "original": "/uploads/originals/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "large": "/uploads/large/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "medium": "/uploads/medium/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg",
    "thumbnail": "/uploads/thumbnail/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg"
  }
}
```

## Featured Image Requirements

When assigning a featured image to a page, the image must meet minimum size requirements:

- **Minimum dimensions**: 1200 x 800 pixels
- **Supported formats**: JPEG, PNG, GIF, WebP

Size validation is enforced in the admin page editor when creating a page or changing the featured image on an existing page; the REST API does not check it. If a page already has a featured image that is below the minimum size, saving other fields on that page will not trigger validation — the existing image is grandfathered.

## Supported Formats

- **Images**: JPEG, PNG, GIF, WebP
- **Icons**: ICO (stored without variants)
- **Video**: MP4, WebM (stored without variants)
- **Documents**: PDF (stored without variants)

The type is detected from the file content and the extension must match it; any other type is rejected. Files other than images are stored in `originals` only.

## Cache Headers

Uploaded files are served with cache headers for optimal performance:

- **Uploads**: `Cache-Control: public, max-age=604800` (1 week)

## Theme Templates

In theme templates, use the appropriate variant:

```html
<!-- Single page featured image -->
<img src="{{ .FeaturedImage }}" alt="Featured">

<!-- Listing page: .FeaturedImage is the thumbnail; .FeaturedImageSmall and
     .FeaturedImageMedium are available for srcset when those variants exist -->
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

Uploaded files are stored in the directory set by `OCMS_UPLOADS_DIR` (default: `./uploads`, relative to the working directory of the process).
