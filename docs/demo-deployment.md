# Demo Mode & Fly.io Deployment

This guide covers oCMS demo mode for showcasing features, and deploying to Fly.io as a public demo instance.

## Demo Mode

Demo mode seeds sample content (users, pages, categories, tags, media, and menus) when the application starts. It's designed for public demos and evaluations.

### Enabling Demo Mode

Both environment variables are required (demo seeding is gated on `OCMS_DO_SEED`):

```bash
OCMS_DO_SEED=true        # Required: enable base seeding (admin user, config, menus)
OCMS_DEMO_MODE=true      # Enable demo content seeding (requires DO_SEED=true)
```

### Demo Content

When enabled, `SeedDemo` creates:

| Content | Count | Details |
|---------|-------|---------|
| Users | 2 | Admin and editor with demo credentials |
| Categories | 4 | Blog, Portfolio, Services, Resources |
| Tags | 7 | Tutorial, News, Featured, Go, Web Development, Design, Open Source |
| Pages | 9 | Home, About, Contact, 3 blog posts, 2 portfolio items, 1 services page |
| Media | 10 | Placeholder images (2400x1600) with all variants (thumbnail, small, medium, large) |
| Menu items | 6 | Home, Blog, Portfolio, Services, About, Contact |

### Demo Credentials

| Role | Email | Password |
|------|-------|----------|
| Admin | `demo@example.com` | `demo1234demo` |
| Editor | `editor@example.com` | `demo1234demo` |
| Default admin | `admin@example.com` | `changeme1234` |

### Demo Mode Restrictions

In demo mode, certain actions are restricted to prevent abuse while still allowing visitors to explore the admin interface:

| Area | Allowed | Blocked |
|------|---------|---------|
| Content (pages, tags, categories, menus, media, forms, widgets) | View | Create, edit, delete, unpublish |
| Themes | View, switch active theme | Edit theme settings (colors, options) |
| Users | View | Create, edit, delete, change roles |
| Site config | View | Edit |
| Languages | View | Edit |
| API keys, webhooks | View | Create, edit, delete |
| Import/export | — | Export, import |
| Modules | View | Toggle, change settings |
| Cache | View stats | Clear cache |
| Media uploads | Upload up to 2MB | Upload over 2MB |
| DB Manager module | — | SQL execution |
| Form submissions | View | CSV export |

### Idempotent Seeding

Demo seeding is idempotent - running it multiple times won't create duplicates. Each seed function checks for existing data before creating new records.

To re-seed with updated content, delete the database first:

```bash
rm ./data/ocms.db*
```

### Uploads Directory

Demo media files are stored in the uploads directory. Configure with:

```bash
OCMS_UPLOADS_DIR=./uploads    # Default
OCMS_UPLOADS_DIR=/app/data/uploads  # Example for containerized deployments
```

## Fly.io Deployment

### Prerequisites

1. [Fly.io CLI](https://fly.io/docs/hands-on/install-flyctl/) installed
2. Fly.io account (free tier is sufficient)

### Quick Start

```bash
# Login to Fly.io
fly auth login

# Launch the app (first time only)
fly launch --no-deploy --copy-config

# Create persistent volume for database
fly volumes create ocms_data --size 1 --region fra

# Set required secrets
fly secrets set OCMS_SESSION_SECRET="$(openssl rand -base64 32)" --app ocms-demo

# Deploy
fly deploy
```

### Configuration

The `fly.toml` in the project root configures:

- **Region**: `fra` (Frankfurt, customizable)
- **VM**: shared-cpu-1x, 256MB RAM
- **Volume**: 1GB persistent storage at `/app/data`
- **Health checks**: Liveness (`/health/live`) and readiness (`/health/ready`)
- **Auto-scaling**: Stops when idle, auto-starts on HTTP requests

Environment variables are set in `fly.toml` under `[env]`:

```toml
[env]
  OCMS_ENV = 'production'
  OCMS_DO_SEED = 'true'
  OCMS_DEMO_MODE = 'true'
  OCMS_DB_PATH = '/app/data/ocms.db'
  OCMS_UPLOADS_DIR = '/app/data/uploads'
```

Secrets (set via `fly secrets set`):

| Variable | Required | Description |
|----------|----------|-------------|
| `OCMS_SESSION_SECRET` | Yes | Min 32 bytes for session encryption |

### Deployment Scripts

```bash
# Standard deployment
./.fly/scripts/deploy.sh

# Deploy with database reset (re-seeds fresh content)
./.fly/scripts/deploy.sh --reset
```

### Demo Reset

To reset the demo to its initial state:

```bash
# Option 1: Run reset script
fly ssh console -C "/app/scripts/reset-demo.sh"
fly machines restart -a ocms-demo

# Option 2: Delete database and restart
fly ssh console -C "rm /app/data/ocms.db*" -a ocms-demo
fly machines restart -a ocms-demo
```

### Monitoring

```bash
# View live logs
fly logs

# Check health
curl https://ocms-demo.fly.dev/health

# SSH into the container
fly ssh console
```

### Cost

Using Fly.io free tier:

| Resource | Usage | Free Tier Limit |
|----------|-------|-----------------|
| VM | shared-cpu-1x, 256MB | 3 VMs |
| Volume | 1GB | 3GB total |
| Bandwidth | ~10-50GB/month | 160GB/month |

## Docker

The project includes a multi-stage `Dockerfile` that:

1. Builds the Go binary with embedded assets
2. Creates a minimal Alpine-based runtime image
3. Includes the demo reset script at `/app/scripts/reset-demo.sh`

Build and run locally:

```bash
docker build -t ocms-demo .
docker run -p 8080:8080 \
  -e OCMS_SESSION_SECRET="$(openssl rand -base64 32)" \
  -e OCMS_DO_SEED=true \
  -e OCMS_DEMO_MODE=true \
  -v ocms_data:/app/data \
  ocms-demo
```

## Files

```
.fly/
  README.md              # Detailed Fly.io deployment guide
  scripts/
    deploy.sh            # Build and deploy script
    reset-demo.sh        # Demo reset script

fly.toml                 # Fly.io configuration
Dockerfile               # Multi-stage Docker build
internal/store/
  seed_demo.go           # Demo content seeding logic
```
