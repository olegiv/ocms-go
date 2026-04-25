# Embed Module

The Embed module integrates third-party embeddable widgets (currently Dify AI chat) into oCMS public pages. It ships:

- An admin UI to enable and configure providers
- A hardened public proxy for provider APIs (token-gated, origin-pinned, rate-limited)
- Template helpers that render provider scripts from theme base layouts
- Markdown exports of published content for use as an AI knowledge base

> **Not the same as [Video Embedding](video-embedding.md).** This module embeds
> external widgets into pages and brokers requests to their APIs. The
> separately-documented *video embedding* feature handles YouTube/Vimeo URLs
> pasted into the page editor and renders them inline as `<iframe>` tags at
> render time. They are unrelated features.

## Overview

### Supported Providers

| Provider | Features |
|----------|----------|
| Dify AI | Chat widget, token-minted proxy for chat messages, suggested replies |

More providers can be added by implementing the `providers.Provider` interface in `modules/embed/providers/`.

### Security posture

Because the widget proxies cost-incurring upstream calls (LLM tokens), the module applies layered controls:

- **Per-IP rate limit**: 1 req/s, burst 5 (HTML handler middleware)
- **Global rate limit**: 5 req/s, burst 10 (shared across all clients)
- **In-flight cap**: semaphore of 32 concurrent upstream requests
- **Origin allowlist**: browser origins must appear in `OCMS_EMBED_ALLOWED_ORIGINS`
- **Upstream host allowlist**: outbound host must appear in `OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS`
- **Signed render-time proxy token**: `OCMS_EMBED_PROXY_TOKEN` mints short-lived tokens bound to the page origin; required in production

In production, startup fails if any required allowlist or the proxy token is
missing. See the "Embed proxy" block in the environment-variable table in the
root `README.md` for the exact flags.

## Admin Interface

Access at **Admin > Embed** or `/admin/embed`. The dashboard lists each provider
with `enabled` / `configured` status.

### Routes

Admin (admin-only):

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/embed` | Provider list |
| GET | `/admin/embed/{provider}` | Provider settings form |
| POST | `/admin/embed/{provider}` | Save provider settings |
| POST | `/admin/embed/{provider}/toggle` | Enable/disable provider |
| GET | `/admin/embed/dify/kb/site-content.md` | Download site content as Markdown |
| GET | `/admin/embed/dify/kb/user-guide.md` | Download the packaged user guide |

Public (rate-limited, origin-pinned):

| Method | Path | Description |
|--------|------|-------------|
| GET | `/embed/dify/token` | Mint a short-lived proxy token for the current origin |
| POST | `/embed/dify/chat-messages` | Proxy Dify chat-messages API |
| GET | `/embed/dify/messages/{messageID}/suggested` | Proxy Dify suggested-replies API |

## Template Functions

Themes call these from their base layout:

| Function | Signature | Where to place it |
|----------|-----------|-------------------|
| `embedHead` | `(nonce, origin)` | Inside `<head>` |
| `embedBody` | `(nonce, origin)` | End of `<body>` |

`origin` is the normalized `scheme://host` of the page the widget runs on (the
theme typically passes `.PageOrigin`). Both helpers escape provider-supplied
values and add the CSP nonce to any emitted `<script>` tags.

Legacy themes that omit `origin` fall back to the single configured allowed
origin (if exactly one is set) and emit a one-shot warning otherwise. Update
themes to pass `.PageOrigin` for deterministic behaviour.

## Environment Variables

All embed-related env vars are summarised here; see `internal/config/config.go`
for authoritative defaults.

| Variable | Role |
|----------|------|
| `OCMS_EMBED_ALLOWED_ORIGINS` | Browser origins permitted to call the embed proxy |
| `OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS` | Allowed outbound upstream hosts (e.g., `api.dify.ai`) |
| `OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS` | Fail startup in production without an origin allowlist |
| `OCMS_REQUIRE_EMBED_ALLOWED_UPSTREAM_HOSTS` | Fail startup in production without an upstream allowlist |
| `OCMS_EMBED_PROXY_TOKEN` | HMAC secret used to mint signed proxy tokens |
| `OCMS_REQUIRE_EMBED_PROXY_TOKEN` | Enforce token requirement outside production too |
| `OCMS_REQUIRE_HTTPS_OUTBOUND` | Require HTTPS for upstream provider URLs |

## Knowledge Base Export

Dify (and similar RAG providers) expect a Markdown corpus. Two admin endpoints
export such corpora:

- `/admin/embed/dify/kb/site-content.md` — all published pages/posts/tags/categories as a single Markdown document (capped at 10000 pages).
- `/admin/embed/dify/kb/user-guide.md` — the packaged user-guide Markdown bundled with the module.

Upload these to your Dify knowledge base; the exports are static and can be
re-downloaded after content updates.

## Database

Single table, managed by module migration 1:

```sql
CREATE TABLE embed_settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NOT NULL UNIQUE,
    settings TEXT NOT NULL DEFAULT '{}',   -- JSON blob
    is_enabled INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_embed_settings_enabled ON embed_settings(is_enabled);
```

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/embed/...
```

Coverage includes origin-policy parsing, proxy-token minting/verification, route auditing, and Dify provider rendering.
