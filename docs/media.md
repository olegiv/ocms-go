# Media Library

OCMS includes a media library for managing uploaded files with automatic image processing.

## Image Variants

When an image is uploaded, OCMS automatically generates multiple size variants:

| Variant | Dimensions | Purpose |
|---------|------------|---------|
| `originals` | Original size | Archive/download |
| `large` | 1920x1080 max | Single page featured images, OG images |
| `medium` | 800x600 max | Listing pages (homepage, categories, tags) |
| `thumbnail` | 150x150 | Search results, admin previews |

### Storage Structure

```
uploads/
├── originals/{UUID}/{filename}
├── large/{UUID}/{filename}
├── medium/{UUID}/{filename}
└── thumbnail/{UUID}/{filename}
```

### URL Format

```
/uploads/{variant}/{UUID}/{filename}
```

Examples:
- `/uploads/originals/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/large/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/medium/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`
- `/uploads/thumbnail/885181c9-2d79-403d-a6e5-6e06ddb3f107/photo.jpg`

## Frontend Usage

Different contexts use different image variants for optimal performance:

| Context | Variant | Reason |
|---------|---------|--------|
| Single page featured image | `large` | High quality for main content |
| OG image (social sharing) | `large` | Social platforms resize anyway |
| Homepage listings | `medium` | Balance of quality and speed |
| Category/tag archives | `medium` | Balance of quality and speed |
| Search results | `thumbnail` | Fast loading, small previews |
| Admin media picker | `thumbnail` | Quick grid view |

## API Response

The REST API returns all variant URLs:

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

<!-- OG meta tag -->
<meta property="og:image" content="{{ .OGImage }}">
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OCMS_UPLOADS_DIR` | `./uploads` | Base directory for uploaded files |
