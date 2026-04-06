# Video Embedding

oCMS supports embedding videos on pages as an optional field, rendered between the page header and body content.

## Supported Providers

| Provider | Status | URL Formats |
|----------|--------|-------------|
| YouTube  | Supported | `youtube.com/watch?v=ID`, `youtu.be/ID`, `youtube.com/embed/ID`, `youtube.com/shorts/ID`, `m.youtube.com/watch?v=ID` |
| Vimeo    | Planned | - |

## How It Works

1. In the admin page editor, expand the **Video** section and paste a video URL
2. Optionally add a **Video Title** (max 255 characters) displayed above the embed
3. The URL is validated server-side against registered providers
3. On the frontend, the video is rendered as a responsive iframe embed between the page header and body content
4. All three core themes (Default, Developer, Starter) include responsive video styling (16:9 aspect ratio)

## Security

- **Server-side URL parsing**: Video URLs are parsed on the server to extract video IDs. Raw URLs are never placed directly into iframes.
- **Privacy-enhanced mode**: YouTube embeds use `youtube-nocookie.com` to avoid tracking cookies.
- **Strict video ID validation**: YouTube video IDs are validated against a strict 11-character alphanumeric pattern (`[a-zA-Z0-9_-]{11}`).
- **CSP configuration**: The Content Security Policy `frame-src` directive includes `https://www.youtube-nocookie.com` to allow video iframes.
- **XSS prevention**: Malicious video IDs (containing HTML/JS) are rejected by the ID pattern validator.

## REST API

The `video_url` and `video_title` fields are available in the pages API:

```bash
# Create page with video and title
curl -X POST /api/v1/pages \
  -H "Authorization: Bearer API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title": "My Video Post", "slug": "video-post", "body": "...", "status": "draft", "video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "video_title": "Introduction Video"}'

# Update page video
curl -X PUT /api/v1/pages/1 \
  -H "Authorization: Bearer API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"video_url": "https://youtu.be/dQw4w9WgXcQ", "video_title": "Updated Title"}'

# Remove video (set to empty string)
curl -X PUT /api/v1/pages/1 \
  -H "Authorization: Bearer API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"video_url": ""}'
```

## Import/Export

The `video_url` and `video_title` fields are included in page exports and can be imported from JSON/ZIP files.

## Theme Integration

All core themes render the video embed with an optional title:

```html
{{if .Page.VideoEmbedHTML}}
<div class="page-video">
    {{if .Page.VideoTitle}}<h3 class="page-video-title">{{.Page.VideoTitle}}</h3>{{end}}
    {{.Page.VideoEmbedHTML}}
</div>
{{end}}
```

Custom themes should add similar markup and CSS for responsive video containers:

```css
.page-video {
    position: relative;
    width: 100%;
    padding-bottom: 56.25%; /* 16:9 aspect ratio */
    margin-bottom: 2rem;
    overflow: hidden;
}
.page-video iframe {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    border: 0;
}
```

## Adding New Providers

The video system is designed for extensibility. To add a new provider:

1. Create a new struct implementing `video.Provider` in `internal/video/video.go`
2. Add URL matching, ID extraction, and embed HTML generation
3. Register it in `NewRegistry()`
4. Add the provider's embed domain to CSP `frame-src` in `internal/middleware/security.go`
5. Update the validation hint text in translations
