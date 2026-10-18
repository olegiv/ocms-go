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
| Pages | 10 | Home, About, Contact, Cookie Policy, 3 blog posts, 2 portfolio items, 1 services page |
| Media | 10 | Placeholder images (2400x1600) with all variants (thumbnail, grid, small, medium, large, og) |
| Menu items | 7 | Home, Blog, Portfolio, Services, About, Contact, Cookie Policy |

### Demo Credentials

| Role | Email | Password |
|------|-------|----------|
| Admin | `demo@example.com` | Random, regenerated on every startup; shown in the informer bar at the top of the site |
| Editor | `editor@example.com` | `demo1234demo` |
| Default admin | `admin@example.com` | Seeded with `changeme1234`, but rotated to a random password at startup in demo mode, so it cannot be used |

### Demo Mode Restrictions

In demo mode, certain actions are restricted to prevent abuse while still allowing visitors to explore the admin interface:

| Area | Allowed | Blocked |
|------|---------|---------|
| Content (pages, tags, categories, menus, media, forms, widgets) | View | Create, edit, delete, unpublish |
| Themes | View, switch active theme | Edit theme settings (colors, options), import, delete |
| Users | View | Create, edit, delete, change roles |
| Site config | View | Edit |
| Languages | View | Edit |
| API keys, webhooks | View | Create, edit, delete |
| Import/export | — | Export, import |
| Modules | View | Toggle, change settings |
| Cache | View stats | Clear cache |
| Scheduler | View jobs | Edit schedules, reset, trigger, custom-task actions |
| Media uploads | — | All uploads blocked |
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

Fly deploys with the project Dockerfile. The image build generates
`web/static/dist` from source, so committed dist files and GitHub release
archives are not required.

### Configuration

The `fly.toml` in the project root configures:

- **Region**: `fra` (Frankfurt, customizable)
- **Stop timeout**: `kill_timeout = 60`, so the app can drain requests (up to 30 seconds) and shut down before Fly stops it
- **VM**: shared-cpu-1x, 256MB RAM
- **Volume**: 1GB persistent storage at `/app/data`
- **Health checks**: Liveness (`/health/live`) and readiness (`/health/ready`)
- **Auto-scaling**: Stops when idle, auto-starts on HTTP requests

Environment variables are set in `fly.toml` under `[env]` (excerpt):

```toml
[env]
  OCMS_ENV = 'production'
  OCMS_DO_SEED = 'true'
  OCMS_DEMO_MODE = 'true'
  OCMS_DB_PATH = '/app/data/ocms.db'
  OCMS_UPLOADS_DIR = '/app/data/uploads'
```

The file also sets `OCMS_ACTIVE_THEME` (`developer`), `OCMS_SITE_URL`, and switches off the production `OCMS_REQUIRE_*` checks that need configuration the demo does not have (trusted proxies, API CIDRs, form captcha and so on). It sets `OCMS_MIGRATOR_ALLOWED_DB_HOSTS` to the unresolvable `db.invalid` to satisfy the Migrator startup audit.

Secrets (set via `fly secrets set`):

| Variable | Required | Description |
|----------|----------|-------------|
| `OCMS_SESSION_SECRET` | Yes | Min 32 bytes; keys CSRF protection (sessions are stored server-side) |

### Deployment Scripts

```bash
# Standard deployment
./.fly/scripts/deploy.sh

# Deploy with database reset (re-seeds fresh content)
./.fly/scripts/deploy.sh --reset
```

### Demo Reset

In demo mode the application resets itself: the `demo_reset` scheduler job runs daily at 01:00 UTC, deleting the database and uploads and sending SIGTERM so the machine restarts and re-seeds, and a startup check runs the same reset when more than 24 hours have passed since the last one.

To reset the demo to its initial state by hand:

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
# --stop-timeout 60 lets the app drain requests (up to 30 seconds) and shut down on docker stop
# The image defaults to OCMS_ENV=production, which refuses to start without
# OCMS_TRUSTED_PROXIES and OCMS_API_ALLOWED_CIDRS; use development for a local try-out
docker run -p 8080:8080 --stop-timeout 60 \
  -e OCMS_SESSION_SECRET="$(openssl rand -base64 32)" \
  -e OCMS_ENV=development \
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
