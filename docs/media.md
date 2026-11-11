# Media Library

OCMS includes a media library for managing uploaded files with automatic image processing.

## Uploading from an image picker

Featured Image, Open Graph Image, the content editor and theme image settings
each offer **Upload Image**, including when the library is empty. Choose one JPG,
PNG, GIF or WebP image (up to 20 MB). The content editor and theme pickers also
accept ICO. The upload runs inside the picker and shows progress; errors can be
retried by choosing the image again.

Theme settings, including favicons, accept ICO, and the content editor inserts
the original file when an image has no size variants. A Featured Image cannot be
set to an ICO; choose JPEG, PNG, GIF or WebP instead.

After upload, the picker highlights the new image. Confirm with **Select Image**
or **Insert Image**, then save the page or theme settings normally. Uploading
does not submit the surrounding form or discard unsaved changes. Featured images
still have the minimum dimensions described below.

Closing the picker cancels a pending client request and discards temporary
selection. An upload that has already completed remains in the library even if
selection is cancelled. Picker uploads are stored as uncategorized media.

The picker uses the existing authenticated, CSRF-protected
`POST /admin/media/upload` endpoint with `Accept: application/json` and exactly
one multipart `file`. It returns `201` with `{ "item": ... }`, the same item shape
as the media picker API, or a failure status with `{ "error": ... }`. Requests
refused before they reach the upload handler differ: a CSRF rejection returns
plain-text `403`, and an expired session redirects to login.

On the normal upload page and its HTMX requests, a failure while storing or
processing an upload, or while regenerating variants, shows a generic message for
server faults (HTMX `500`) and a specific message for problems the editor can fix,
such as a corrupt image (HTMX `400`); form submissions redirect with a flash.
Checks made before processing, such as demo mode, a missing media item or a
non-image file, keep their existing responses. Batch uploads still report a count
summary.

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

Size validation is shared by the admin editor, REST v2 and the AI draft engine when assigning a new featured image or changing an existing assignment. Unchanged historical assignments below the minimum and absent historical dimensions retain the existing compatibility behavior. The draft engine also requires a supported existing library image; see [Draft contract](draft-contract.md).

ICO cannot be assigned as a new or changed featured image, even though its
dimensions are unknown. An existing ICO featured image is kept when a page is
edited, or a version restored, without changing it. Publishing an AI-editable page
re-checks the image, so it is refused until a JPEG, PNG, GIF or WebP image is
chosen. ICO remains supported for theme settings and favicons.

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
