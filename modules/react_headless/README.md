# React Headless Module

Enables headless CMS mode for oCMS with CORS support and a ready-to-use React starter application. Use oCMS as a backend API while building your frontend with React.

## Features

- Configurable CORS settings for the REST API
- Admin dashboard for managing allowed origins
- Starter React app with TypeScript and Vite
- Pre-built components for pages, media, tags, and categories
- API client with authentication support
- Database-backed settings with sensible defaults

## Routes

### Admin Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/react-headless` | CORS settings dashboard |
| POST | `/admin/react-headless` | Save CORS settings |

This module does not register any public routes. The React app runs as a separate frontend.

## CORS Configuration

The module adds a CORS middleware to all API routes (`/api/v1/*`). Configure it from the admin panel:

| Setting | Default | Description |
|---------|---------|-------------|
| Allowed Origins | `http://localhost:5173` | Comma-separated list of allowed origins |
| Allow Credentials | `false` | Allow cookies and auth headers in cross-origin requests |
| Preflight Cache | `3600` | How long browsers cache preflight responses (seconds) |

### Wildcard Origins

Set allowed origins to `*` to allow all origins. This is **not recommended for production** as it disables origin-based access control.

## React Starter App

The module includes a complete React starter app in `frontend/`:

```
frontend/
├── src/
│   ├── api/client.ts           # API client with auth support
│   ├── components/
│   │   ├── Layout.tsx          # App layout with navigation
│   │   ├── PageList.tsx        # Paginated page listing
│   │   ├── PageDetail.tsx      # Single page view
│   │   ├── MediaGallery.tsx    # Media file gallery
│   │   ├── TagList.tsx         # Tag listing
│   │   └── CategoryTree.tsx    # Category tree view
│   │   └── Pagination.tsx      # Reusable pagination
│   ├── hooks/useApi.ts         # React hook for API calls
│   ├── types/index.ts          # TypeScript type definitions
│   ├── App.tsx                 # Router configuration
│   ├── main.tsx                # Entry point
│   └── styles.css              # Base styles
├── .env.example                # Environment variable template
├── index.html                  # HTML entry point
├── package.json                # Dependencies (React 19.2, Vite 7)
├── tsconfig.json               # TypeScript configuration
└── vite.config.ts              # Vite dev server with API proxy
```

### Tech Stack

- **React 19.2** with React Router 7
- **TypeScript 5.9** for type safety
- **Vite 7** for fast development and builds
- Built-in proxy for local development (avoids CORS during dev)

### Quick Start

```bash
# Copy the starter app to your project
cp -r modules/react_headless/frontend/ ~/my-react-app/

# Install dependencies
cd ~/my-react-app
npm install

# Configure environment
cp .env.example .env
# Edit .env with your oCMS API URL and key

# Start development server
npm run dev
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VITE_API_URL` | `/api/v1` | oCMS API base URL |
| `VITE_API_KEY` | (empty) | API key for authenticated endpoints |

### API Endpoints Used

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/v1/pages` | GET | Optional | List published pages with pagination |
| `/api/v1/pages/{id}` | GET | Optional | Get a single page by ID |
| `/api/v1/pages/slug/{slug}` | GET | Optional | Get a single page by slug |
| `/api/v1/media` | GET | Optional | List media files |
| `/api/v1/tags` | GET | None | List all tags |
| `/api/v1/categories` | GET | None | List categories as tree |

## Database Schema

The module creates a settings table:

```sql
CREATE TABLE react_headless_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    allowed_origins TEXT NOT NULL DEFAULT 'http://localhost:5173',
    allow_credentials INTEGER NOT NULL DEFAULT 0,
    max_age INTEGER NOT NULL DEFAULT 3600,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

The `CHECK (id = 1)` constraint ensures only one settings row exists.

## Module Structure

```
modules/react_headless/
├── module.go        # Module definition, lifecycle, settings, migrations
├── cors.go          # CORS middleware implementation
├── handlers.go      # Admin HTTP handlers (dashboard, save settings)
├── module_test.go   # Test suite
├── frontend/        # React starter application
└── locales/         # Embedded i18n translations
    ├── en/messages.json
    ├── de/messages.json
    ├── es/messages.json
    ├── fr/messages.json
    └── ru/messages.json
```

## Setup Guide

1. **Enable the module** at Admin > Modules
2. **Configure CORS** at Admin > React Headless
   - Add your React app's URL (e.g., `http://localhost:5173`)
   - Enable credentials if using session-based auth
3. **Create an API key** at Admin > API Keys
   - Grant `pages:read`, `media:read`, `tags:read`, `categories:read` permissions
4. **Set up the React app**
   - Copy `frontend/` directory
   - Set `VITE_API_URL` and `VITE_API_KEY` in `.env`
   - Run `npm install && npm run dev`

## Production Deployment

For production, update the CORS settings:

1. Replace `http://localhost:5173` with your production domain
2. Build the React app: `npm run build`
3. Serve the `dist/` directory from your web server or CDN
4. Point `VITE_API_URL` to your production oCMS API URL

## Internationalization

Translations are embedded and automatically loaded. Supported languages:

- English (en)
- German (de)
- Spanish (es)
- French (fr)
- Russian (ru)

Add new languages by creating `locales/{lang}/messages.json`.
