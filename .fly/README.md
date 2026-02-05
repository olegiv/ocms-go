# oCMS Fly.io Deployment

This directory contains configuration and scripts for deploying oCMS to Fly.io.

## Prerequisites

1. [Fly.io CLI](https://fly.io/docs/hands-on/install-flyctl/) installed
2. Fly.io account (free tier is sufficient)

## Quick Start

```bash
# 1. Login to Fly.io
fly auth login

# 2. Launch the app (first time only)
fly launch --no-deploy --copy-config

# 3. Create persistent volume for database
fly volumes create ocms_data --size 1 --region iad

# 4. Set required secrets
fly secrets set OCMS_SESSION_SECRET="$(openssl rand -base64 32)"

# 5. Deploy
fly deploy

# 6. Open the app
fly open
```

## Configuration

### Environment Variables

Set via `fly secrets set`:

| Variable | Required | Description |
|----------|----------|-------------|
| `OCMS_SESSION_SECRET` | Yes | Min 32 bytes for session encryption |

Configured in `fly.toml`:

| Variable | Value | Description |
|----------|-------|-------------|
| `OCMS_ENV` | `production` | Production mode |
| `OCMS_DO_SEED` | `true` | Enable demo seeding |
| `OCMS_DEMO_MODE` | `true` | Demo mode indicator |

### Volumes

The app uses a single 1GB volume mounted at `/app/data`:

- Database: `/app/data/ocms.db`
- Uploads: `/app/data/uploads/`
- Backups: `/app/data/backups/`

## Commands

### Deployment

```bash
# Deploy latest version
fly deploy

# Deploy with specific Dockerfile
fly deploy --dockerfile Dockerfile

# Check deployment status
fly status

# View logs
fly logs
```

### Monitoring

```bash
# Check app status
fly status

# View live logs
fly logs

# View recent logs
fly logs --no-tail

# Check health
curl https://ocms-demo.fly.dev/health
```

### SSH Access

```bash
# Interactive shell
fly ssh console

# Run single command
fly ssh console -C "ls -la /app/data"

# Check database size
fly ssh console -C "ls -lh /app/data/ocms.db"
```

### Demo Reset

To reset the demo to initial state:

```bash
# Option 1: Run reset script
fly ssh console -C "/app/scripts/reset-demo.sh"
fly machines restart

# Option 2: Just restart (database recreated if missing)
fly ssh console -C "rm /app/data/ocms.db*"
fly machines restart
```

### Scaling

```bash
# Scale memory (free tier max: 256MB)
fly scale memory 256

# Check current scale
fly scale show

# Restart machines
fly machines restart
```

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   Fly.io                        │
│  ┌──────────────────────────────────────────┐  │
│  │         Fly Proxy (HTTPS/TLS)            │  │
│  └──────────────────┬───────────────────────┘  │
│                     │                           │
│  ┌──────────────────▼───────────────────────┐  │
│  │          oCMS Container                  │  │
│  │  ┌────────────────────────────────────┐  │  │
│  │  │    Go Binary (embedded assets)     │  │  │
│  │  │    - Admin UI                      │  │  │
│  │  │    - REST API                      │  │  │
│  │  │    - Theme rendering               │  │  │
│  │  └────────────────────────────────────┘  │  │
│  │                   │                       │  │
│  │  ┌────────────────▼────────────────────┐ │  │
│  │  │     Persistent Volume (/app/data)   │ │  │
│  │  │  - SQLite database                  │ │  │
│  │  │  - Uploaded media                   │ │  │
│  │  │  - Backups                          │ │  │
│  │  └─────────────────────────────────────┘ │  │
│  └──────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

## Default Credentials

When the demo starts fresh:

| Role | Email | Password |
|------|-------|----------|
| Admin | `admin@example.com` | `changeme1234` |

For demo with additional users, set `OCMS_SEED_DEMO_USERS=true`.

## Troubleshooting

### App not starting

```bash
# Check logs for errors
fly logs

# Check machine status
fly machines list

# Force restart
fly machines restart
```

### Database issues

```bash
# Check database exists
fly ssh console -C "ls -la /app/data/"

# Check database integrity
fly ssh console -C "sqlite3 /app/data/ocms.db 'PRAGMA integrity_check;'"

# View database size
fly ssh console -C "du -h /app/data/ocms.db"
```

### Volume issues

```bash
# List volumes
fly volumes list

# Check volume attachment
fly machines list --json | jq '.[].config.mounts'
```

### Health check failures

```bash
# Test health endpoints
curl -v https://ocms-demo.fly.dev/health/live
curl -v https://ocms-demo.fly.dev/health/ready

# Check internal health
fly ssh console -C "wget -qO- http://localhost:8080/health"
```

## Cost

Using Fly.io free tier:

| Resource | Usage | Free Tier Limit |
|----------|-------|-----------------|
| VM | shared-cpu-1x, 256MB | 3 VMs |
| Volume | 1GB | 3GB total |
| Bandwidth | ~10-50GB/month | 160GB/month |

**Estimated cost: $0/month** (within free tier limits)

## Files

```
.fly/
├── README.md           # This file
└── scripts/
    └── reset-demo.sh   # Demo reset script

fly.toml                # Fly.io configuration (project root)
```
