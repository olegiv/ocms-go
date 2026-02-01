# oCMS Multi-Instance Deployment for Plesk

Deploy multiple oCMS instances on a single Plesk server, one per vhost/domain.

## Architecture

```
/opt/ocms/bin/ocms              ← shared binary (all sites)
/opt/ocms/bin/ocmsctl           ← CLI management tool
/opt/ocms/themes/               ← shared theme source (copied per site)
/etc/ocms/sites.conf            ← site registry (auto-managed)
/etc/systemd/system/ocms@.service  ← systemd template

Per site:
/var/www/vhosts/example.com/ocms/
├── data/ocms.db                ← SQLite database
├── uploads/                    ← media files
├── themes/                     ← theme files
├── backups/                    ← automated backups
├── logs/ocms.log               ← app log (ocmsctl mode)
├── .env                        ← environment config
└── ocms.pid                    ← PID file (ocmsctl mode)

/etc/systemd/system/ocms@example_com.service.d/
└── instance.conf               ← per-site systemd override
```

Each site runs on its own port (8081, 8082, ...) behind Nginx reverse proxy.

## Quick Start

### 1. Install shared binary, tools, and themes

```bash
# Local machine:
make build-linux-amd64
scp bin/ocms-linux-amd64 user@server:/tmp/ocms
scp scripts/deploy/* user@server:/tmp/ocms-setup/
scp -r themes user@server:/tmp/ocms-themes

# On the server:
sudo mkdir -p /opt/ocms/bin /etc/ocms /opt/ocms/themes
sudo cp /tmp/ocms /opt/ocms/bin/ocms
sudo chmod 755 /opt/ocms/bin/ocms
sudo cp /tmp/ocms-setup/ocmsctl /opt/ocms/bin/
sudo chmod 755 /opt/ocms/bin/ocmsctl
sudo cp /tmp/ocms-setup/ocms@.service /etc/systemd/system/
for script in setup-site.sh deploy-multi.sh backup-multi.sh healthcheck-multi.sh helper.sh; do
    sudo cp /tmp/ocms-setup/$script /opt/ocms/
    sudo chmod 755 /opt/ocms/$script
done
sudo cp -r /tmp/ocms-themes/* /opt/ocms/themes/
sudo systemctl daemon-reload
```

**Important:** Themes are loaded from disk at runtime, not embedded in the binary. Without themes at `/opt/ocms/themes/`, new sites will show "No active theme". The `setup-site.sh` script copies themes from `/opt/ocms/themes/` into each site's `themes/` directory during provisioning.

### 2. Provision a site

```bash
sudo /opt/ocms/setup-site.sh example.com example_com
```

The script creates all directories, generates a unique session secret, creates the systemd drop-in, and prints the Nginx snippet to paste into Plesk.

Arguments:
- `domain` — the domain name (must exist as a Plesk vhost)
- `system-user` — the Plesk vhost system user
- `port` (optional) — auto-assigned from 8081 if omitted
- `group` (optional) — defaults to `psaserv`

### 3. Configure Nginx in Plesk

Go to **Websites & Domains → example.com → Apache & nginx Settings → Additional nginx directives** and paste the snippet printed by `setup-site.sh`.

### 4. Start the site

**For testing** (manual start/stop via terminal):
```bash
sudo ocmsctl start example_com
curl http://127.0.0.1:8081/health
sudo ocmsctl logs example_com --follow
sudo ocmsctl stop example_com
```

**For production** (systemd with auto-restart):
```bash
sudo systemctl enable --now ocms@example_com
sudo journalctl -u ocms@example_com -f
```

### 5. Login

Navigate to `https://example.com/admin/` and login with:
- Email: `admin@example.com`
- Password: `changeme1234`

Change the password immediately.

## ocmsctl Reference

```
ocmsctl <command> <site-id> [options]

Commands:
  start   <site-id> [--foreground]  Start instance (nohup or foreground)
  stop    <site-id>                 Stop instance (SIGTERM)
  restart <site-id>                 Restart instance
  status  <site-id>                 Show status and health
  logs    <site-id> [--follow|-f]   View logs (journal or file)
  list                              List all sites with status
  health  <site-id>                 Check health endpoint
```

`ocmsctl` detects if a site is managed by systemd and will direct you to use `systemctl` instead when appropriate.

## Deploying Updates

### Single Instance (from local machine)

Use `deploy.sh` to build, transfer, and restart a single instance:

```bash
./scripts/deploy/deploy.sh <server> <instance> -v <vhost> -o <owner> [options]

# Examples:
./scripts/deploy/deploy.sh server.example.com my_site \
  -v /var/www/vhosts/example.com -o hosting

./scripts/deploy/deploy.sh server.example.com my_site \
  -v /var/www/vhosts/example.com -o hosting --skip-build

./scripts/deploy/deploy.sh server.example.com my_site \
  -v /var/www/vhosts/example.com -o hosting -g mygroup --dry-run
```

Required:
- `-v, --vhost PATH` — vhost path for themes sync (e.g., `/var/www/vhosts/example.com`)
- `-o, --owner USER` — vhost owner for chown (e.g., `hosting`)

Options:
- `-g, --group GROUP` — vhost group for chown (default: `psaserv`)
- `-u, --user USER` — SSH user (default: `root`)
- `--skip-build` — skip `make build-linux-amd64`, use existing binary
- `--dry-run` — print commands without executing

The script:
1. Builds `bin/ocms-linux-amd64`
2. Backs up current binary on server
3. Stops the instance via `ocmsctl`
4. Transfers binary via `scp`
5. Syncs themes to `{vhost}/ocms/themes/` via `rsync --delete`
6. Sets themes ownership to `{owner}:{group}`
7. Starts the instance
8. Checks instance status

### Multi-Instance (on server)

For updating all instances on a server:

```bash
# Local machine:
make build-linux-amd64
scp bin/ocms-linux-amd64 user@server:/tmp/ocms

# On the server:
sudo /opt/ocms/deploy-multi.sh /tmp/ocms
```

This backs up the current binary, stops all instances, replaces the binary, restarts all instances, and runs health checks. On failure it prints rollback instructions.

Or manually:
```bash
sudo cp /tmp/ocms /opt/ocms/bin/ocms
sudo systemctl restart 'ocms@*'
```

## Copying Local Data

To deploy a pre-populated database and uploads to a site:

```bash
# Flush WAL on local machine first:
sqlite3 data/ocms.db "PRAGMA wal_checkpoint(TRUNCATE);"

# Local machine:
scp data/ocms.db user@server:/tmp/ocms.db
scp -r uploads user@server:/tmp/ocms-uploads

# On the server:
sudo ocmsctl stop example_com
sudo cp /tmp/ocms.db /var/www/vhosts/example.com/ocms/data/ocms.db
sudo cp -r /tmp/ocms-uploads/* /var/www/vhosts/example.com/ocms/uploads/
sudo chown -R example_com:psaserv /var/www/vhosts/example.com/ocms/data /var/www/vhosts/example.com/ocms/uploads
sudo ocmsctl start example_com
```

The service must be stopped before replacing the database. If using systemd, use `systemctl stop/start` instead of `ocmsctl`.

## Syncing Production to Development

Use `sync-prod-to-dev.sh` to pull production data (database, uploads, logs) to your local development environment:

```bash
./scripts/deploy/sync-prod-to-dev.sh <server> <instance> -v <vhost> [options]

# Examples:
./scripts/deploy/sync-prod-to-dev.sh server.example.com my_site \
  -v /var/www/vhosts/example.com

./scripts/deploy/sync-prod-to-dev.sh server.example.com my_site \
  -v /var/www/vhosts/example.com --no-logs

./scripts/deploy/sync-prod-to-dev.sh server.example.com my_site \
  -v /var/www/vhosts/example.com --dry-run
```

Required:
- `-v, --vhost PATH` — vhost path on server (e.g., `/var/www/vhosts/example.com`)

Options:
- `-u, --user USER` — SSH user (default: `root`)
- `-p, --port PORT` — Local server port to stop (default: `8080`)
- `--no-db` — Skip database sync
- `--no-uploads` — Skip uploads sync
- `--no-logs` — Skip logs sync
- `--dry-run` — Print commands without executing

The script:
1. Stops local development server (port 8080)
2. Stops remote instance via `ocmsctl`
3. Checkpoints SQLite WAL on server (flushes to main file)
4. Syncs database via `rsync`
5. Syncs uploads via `rsync --delete` (mirrors production exactly)
6. Syncs logs via `rsync` (keeps local logs not on prod)
7. Restarts remote instance

**WARNING:** This overwrites your local `data/`, `uploads/`, and `logs/` directories with production data!

## Backups

```bash
# Backup all sites
sudo /opt/ocms/backup-multi.sh

# Backup one site
sudo /opt/ocms/backup-multi.sh example_com
```

Backups are stored in each site's `backups/` directory with 30-day retention.

## Health Checks

```bash
# Check all sites (manual)
sudo /opt/ocms/healthcheck-multi.sh

# Check one site
sudo /opt/ocms/healthcheck-multi.sh example_com
```

Auto-restarts failed systemd-managed instances (max 3 attempts, 5-min cooldown). Supports optional Slack and email alerting — edit the script to configure.

## Cron Jobs

```bash
# /etc/cron.d/ocms-multi
0 3 * * * root /opt/ocms/backup-multi.sh >> /var/log/ocms-backup.log 2>&1
*/5 * * * * root /opt/ocms/healthcheck-multi.sh 2>&1 | grep -v "^$"
```

## File Permissions

| Path | Owner | Mode |
|------|-------|------|
| `/opt/ocms/bin/ocms` | `root:root` | `755` |
| `/opt/ocms/bin/ocmsctl` | `root:root` | `755` |
| `/opt/ocms/themes/` | `root:root` | `755` |
| `{vhost}/ocms/` | `{user}:psaserv` | `750` |
| `{vhost}/ocms/.env` | `{user}:psaserv` | `600` |
| `{vhost}/ocms/data/` | `{user}:psaserv` | `755` |
| `{vhost}/ocms/uploads/` | `{user}:psaserv` | `755` |

## Site Registry

All sites are tracked in `/etc/ocms/sites.conf`:

```
# Format: SITE_ID VHOST_PATH SYSTEM_USER PORT
example_com /var/www/vhosts/example.com example_com 8081
blog_example_com /var/www/vhosts/blog.example.com bloguser 8082
```

## Removing a Site

```bash
# Stop the service
sudo systemctl disable --now ocms@example_com

# Remove systemd drop-in
sudo rm -rf /etc/systemd/system/ocms@example_com.service.d
sudo systemctl daemon-reload

# Remove site data (WARNING: deletes database and uploads)
sudo rm -rf /var/www/vhosts/example.com/ocms

# Remove from sites.conf (manually edit the file)
sudo nano /etc/ocms/sites.conf
```
