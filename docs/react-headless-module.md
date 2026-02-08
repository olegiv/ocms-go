# React Headless CMS Module

The React Headless module enables oCMS to function as a headless CMS, serving content via its REST API to a decoupled React frontend. It provides CORS configuration for cross-origin API access and ships a complete React starter application.

## Overview

Traditional CMS setups render pages server-side. In headless mode, oCMS provides content through its REST API while the frontend is a separate React application. This architecture enables:

- Independent frontend and backend deployments
- Modern React ecosystem (hooks, TypeScript, Vite)
- Multiple frontends consuming the same API
- Static site generation or server-side rendering with React frameworks

## Installation

The module is registered in `cmd/ocms/main.go`:

```go
import "ocms-go/modules/react_headless"

// In main()
if err := moduleRegistry.Register(react_headless.New()); err != nil {
    return fmt.Errorf("registering react_headless module: %w", err)
}
```

Enable it from **Admin > Modules** after installation.

## CORS Configuration

### What is CORS?

Cross-Origin Resource Sharing (CORS) is a browser security feature that blocks web pages from making requests to a different domain. When your React app runs on `http://localhost:5173` and the oCMS API is on `http://localhost:8080`, the browser blocks these cross-origin requests by default.

The React Headless module adds CORS headers to API responses, allowing your React app to access the oCMS API.

### Admin Settings

Navigate to **Admin > React Headless** to configure:

| Setting | Default | Description |
|---------|---------|-------------|
| **Allowed Origins** | `http://localhost:5173` | Comma-separated list of domains that can access the API |
| **Allow Credentials** | Disabled | Whether to allow cookies and authorization headers |
| **Preflight Cache** | 3600 seconds | How long browsers cache preflight (OPTIONS) responses |

### How It Works

1. The React app sends a request with an `Origin` header
2. The CORS middleware checks if the origin is in the allowed list
3. If allowed, it adds `Access-Control-Allow-Origin` and related headers
4. For preflight (`OPTIONS`) requests, it responds with `204 No Content` and allowed methods/headers
5. The browser allows the actual request to proceed

### Origin Configuration Examples

```
# Single origin (development)
http://localhost:5173

# Multiple origins
http://localhost:5173, https://mysite.com, https://staging.mysite.com

# Allow all origins (NOT recommended for production)
*
```

### Exposed Headers

The middleware exposes these response headers to the frontend:

- `X-Total-Count` - Total number of items
- `X-Page` - Current page number
- `X-Per-Page` - Items per page

## React Starter Application

### Getting Started

```bash
# 1. Copy the starter app
cp -r modules/react_headless/frontend/ ~/my-headless-site/

# 2. Install dependencies
cd ~/my-headless-site
npm install

# 3. Configure environment
cp .env.example .env
# Edit .env:
#   VITE_API_URL=/api/v1        (uses Vite proxy for local dev)
#   VITE_API_KEY=your-api-key   (optional for public endpoints)

# 4. Start development server
npm run dev
# React app: http://localhost:5173
# API proxy: http://localhost:5173/api -> http://localhost:8080/api
```

### Development Proxy

The Vite development server proxies API requests to oCMS, eliminating CORS issues during development:

```typescript
// vite.config.ts
export default defineConfig({
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/uploads': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
```

This means during development, you can use `VITE_API_URL=/api/v1` without configuring CORS.

### API Client

The starter app includes a typed API client (`src/api/client.ts`):

```typescript
import { fetchPages, fetchPageBySlug, fetchTags, fetchCategories, fetchMedia } from './api/client';

// List pages with pagination
const response = await fetchPages({ page: 1, per_page: 10 });
console.log(response.data);   // Page[]
console.log(response.meta);   // { total, page, per_page, pages }

// Get page by slug
const page = await fetchPageBySlug('hello-world');
console.log(page.data.title);

// List tags
const tags = await fetchTags();

// List categories (tree structure)
const categories = await fetchCategories();

// List media files
const media = await fetchMedia({ page: 1, per_page: 20 });
```

### TypeScript Types

All API response types are defined in `src/types/index.ts`:

```typescript
interface Page {
  id: number;
  title: string;
  slug: string;
  body: string;
  status: 'draft' | 'published';
  author?: Author;
  categories?: Category[];
  tags?: Tag[];
  // ... more fields
}

interface ListResponse<T> {
  data: T[];
  meta: PaginationMeta;
}

interface SingleResponse<T> {
  data: T;
}
```

### React Hook

The `useApi` hook handles loading states and errors:

```typescript
import { useApi } from './hooks/useApi';
import { fetchPages } from './api/client';

function PageList() {
  const { data, loading, error } = useApi(() => fetchPages({ page: 1 }));

  if (loading) return <p>Loading...</p>;
  if (error) return <p>Error: {error.message}</p>;

  return (
    <ul>
      {data?.data.map(page => (
        <li key={page.id}>{page.title}</li>
      ))}
    </ul>
  );
}
```

### Components

The starter app includes these components:

| Component | Route | Description |
|-----------|-------|-------------|
| `PageList` | `/` | Paginated list of published pages |
| `PageDetail` | `/page/:slug` | Single page view with author, tags, categories |
| `MediaGallery` | `/media` | Grid of media files with thumbnails |
| `TagList` | `/tags` | List of all tags with page counts |
| `CategoryTree` | `/categories` | Hierarchical category display |
| `Layout` | (wrapper) | Navigation bar and page structure |
| `Pagination` | (reusable) | Page navigation controls |

### Building for Production

```bash
# Build the React app
npm run build

# Output is in dist/ - deploy to any static host:
# - Nginx, Apache, Caddy
# - Vercel, Netlify, Cloudflare Pages
# - AWS S3 + CloudFront
```

For production, update `.env`:

```bash
VITE_API_URL=https://cms.example.com/api/v1
VITE_API_KEY=your-production-api-key
```

## API Key Setup

1. Go to **Admin > API Keys**
2. Click **New API Key**
3. Set permissions based on your frontend needs:

| Permission | Required | Description |
|------------|----------|-------------|
| `pages:read` | Yes | Read published pages |
| `media:read` | Recommended | Access media files and variants |
| `tags:read` | Optional | List tags |
| `categories:read` | Optional | List categories |

4. Copy the generated key to your React app's `.env` file

## Architecture

```
┌─────────────────────┐     ┌─────────────────────┐
│                     │     │                     │
│   React Frontend    │────▶│   oCMS REST API     │
│   (localhost:5173)  │     │   (localhost:8080)   │
│                     │◀────│                     │
│   - React 19.2      │     │   /api/v1/pages     │
│   - TypeScript 5.9  │     │   /api/v1/media     │
│   - Vite 7          │     │   /api/v1/tags      │
│   - React Router    │     │   /api/v1/categories│
│                     │     │                     │
└─────────────────────┘     └─────────────────────┘
        │                           │
        │    CORS Headers           │
        │    ◀──────────────        │
        │    Origin check           │
        │    Allow-Origin           │
        │    Allow-Methods          │
        │    Allow-Headers          │
        │    Max-Age                │
        └───────────────────────────┘
```

## Troubleshooting

### CORS Errors in Browser Console

**Symptom**: `Access to fetch at 'http://localhost:8080/api/v1/pages' from origin 'http://localhost:5173' has been blocked by CORS policy`

**Solutions**:
1. Ensure the module is enabled at Admin > Modules
2. Add `http://localhost:5173` to Allowed Origins
3. If using credentials, enable "Allow Credentials"
4. Check that the origin URL matches exactly (including port)

### API Returns 401 Unauthorized

**Symptom**: API calls return 401 even with an API key

**Solutions**:
1. Verify the API key is active at Admin > API Keys
2. Check the key has the required permissions
3. Ensure the `Authorization: Bearer <key>` header is being sent
4. If "Allow Credentials" is enabled, verify `credentials: 'include'` in fetch calls

### Preflight Requests Failing

**Symptom**: OPTIONS requests return errors or missing headers

**Solutions**:
1. Verify the CORS middleware is active (module must be enabled)
2. Check that the origin is in the allowed list
3. Increase the preflight cache duration to reduce OPTIONS requests

### Images Not Loading

**Symptom**: Media URLs return 404 or CORS errors

**Solutions**:
1. In development, ensure Vite proxy includes `/uploads` path
2. In production, serve uploads from the same domain or configure CORS
3. Check that media file paths are correct (use URLs from the API response)

## Database

### Settings Table

```sql
CREATE TABLE react_headless_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    allowed_origins TEXT NOT NULL DEFAULT 'http://localhost:5173',
    allow_credentials INTEGER NOT NULL DEFAULT 0,
    max_age INTEGER NOT NULL DEFAULT 3600,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Manual Settings Update

```sql
-- Update allowed origins
UPDATE react_headless_settings
SET allowed_origins = 'https://mysite.com, https://staging.mysite.com',
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- Enable credentials
UPDATE react_headless_settings
SET allow_credentials = 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;
```

Note: After manual database changes, restart oCMS to reload settings.
