# oCMS Multi-Instance Deployment for Plesk

Deploy multiple oCMS instances on a single Plesk server, one per vhost/domain.

## Architecture

**Core themes are embedded in the binary.** The `default` and `developer` themes are compiled into the binary and don't need to be deployed separately. Custom themes can optionally be placed in the `custom/themes/` directory to override or extend core themes.

```
/opt/ocms/bin/ocms              ← shared binary (includes embedded themes)
/opt/ocms/bin/ocmsctl           ← CLI management tool
/etc/ocms/sites.conf            ← site registry (auto-managed)
/etc/systemd/system/ocms@.service  ← systemd template

Per site:
/var/www/vhosts/example.com/ocms/
├── data/ocms.db                ← SQLite database
├── uploads/                    ← media files
├── custom/                     ← user content (optional)
│   └── themes/                 ← custom themes (override embedded)
├── backups/                    ← automated backups
├── logs/ocms.log               ← app log (ocmsctl mode)
│   └── error.log               ← error-only log (optional)
├── .env                        ← environment config
└── ocms.pid                    ← PID file (ocmsctl mode)

/etc/systemd/system/ocms@example_com.service.d/
└── instance.conf               ← per-site systemd override
```

Each site runs on its own port (8081, 8082, ...) behind Nginx reverse proxy.

## Quick Start

### 1. Install shared binary and tools

```bash
# Local machine:
make build-linux-amd64
scp bin/ocms-linux-amd64 user@server:/tmp/ocms
scp scripts/deploy/* user@server:/tmp/ocms-setup/

# On the server:
sudo mkdir -p /opt/ocms/bin /etc/ocms
sudo cp /tmp/ocms /opt/ocms/bin/ocms
sudo chmod 755 /opt/ocms/bin/ocms
sudo cp /tmp/ocms-setup/ocmsctl /opt/ocms/bin/
sudo chmod 755 /opt/ocms/bin/ocmsctl
sudo cp /tmp/ocms-setup/ocms@.service /etc/systemd/system/
for script in setup-site.sh deploy-multi.sh backup-multi.sh healthcheck-multi.sh generate-logrotate.sh helper.sh; do
    sudo cp /tmp/ocms-setup/$script /opt/ocms/
    sudo chmod 755 /opt/ocms/$script
done
sudo systemctl daemon-reload
```

**Note:** Core themes (`default`, `developer`) are embedded in the binary. No separate theme deployment is required unless you have custom themes.

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

Options:
- `--path PATH` — full path to instance root directory (default: `/var/www/vhosts/<domain>/ocms`)

Use `--path` when the default path doesn't match your setup:

```bash
sudo /opt/ocms/setup-site.sh app.example.com hosting --path /var/www/vhosts/example.com/ocms
```

### 2.1 Review generated `.env` (embed hardening)

After provisioning, review the generated env file and confirm embed variables
for your real domains/hosts:

```bash
sudo nano /var/www/vhosts/example.com/ocms/.env
```

Recommended production baseline when embed module/provider is enabled:

```bash
# Embed proxy hardening (origin match is exact: scheme + host)
OCMS_EMBED_ALLOWED_ORIGINS=https://example.com,https://www.example.com
OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS=api.dify.ai
OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS=true
OCMS_REQUIRE_EMBED_ALLOWED_UPSTREAM_HOSTS=true
OCMS_EMBED_PROXY_TOKEN=replace-with-embed-proxy-token
```

If you use self-hosted Dify, replace `api.dify.ai` with your API hostname.

### 3. Configure SSL in Plesk

1. Go to **Websites & Domains** → your domain
2. Click **SSL/TLS Certificates**
3. Click **Install** under "Let's Encrypt"
4. Check your domain name(s)
5. Click **Get it free**
6. After installation, go back to **Hosting Settings**
7. Enable **Permanent SEO-safe 301 redirect from HTTP to HTTPS**

### 4. Configure Nginx in Plesk

Go to **Websites & Domains → example.com → Apache & nginx Settings → Additional nginx directives** and paste the snippet printed by `setup-site.sh`.

### 5. Start the site

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

**Running without systemd** (not recommended for production):
```bash
# nohup (persists after logout)
cd /var/lib/ocms && nohup /opt/ocms/bin/ocms > /var/log/ocms.log 2>&1 &

# screen (can reattach with: screen -r ocms)
screen -dmS ocms /opt/ocms/bin/ocms

# tmux (can reattach with: tmux attach -t ocms)
tmux new-session -d -s ocms '/opt/ocms/bin/ocms'
```

### 6. Login

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

### Binary Only (from local machine)

Use `deploy-binary.sh` for sites that use only embedded themes (no custom content). Supports multiple instances sharing the same binary:

```bash
./scripts/deploy/deploy-binary.sh <server> <instance...> [options]

# Single instance
./scripts/deploy/deploy-binary.sh server.example.com my_site

# Multiple instances sharing the same binary
./scripts/deploy/deploy-binary.sh server.example.com site_a site_b site_c

# Skip build, dry run
./scripts/deploy/deploy-binary.sh server.example.com site_a site_b --skip-build --dry-run
```

Options:
- `-u, --user USER` — SSH user (default: `root`)
- `--skip-build` — skip `make build-linux-amd64`, use existing binary
- `--dry-run` — print commands without executing

The script:
1. Builds `bin/ocms-linux-amd64`
2. Backs up current binary on server
3. Stops all listed instances via `ocmsctl`
4. Transfers binary via `scp`
5. Starts all instances
6. Checks each instance status

### Single Instance — With Custom Content (from local machine)

Use `deploy.sh` when you also need to sync custom themes or modules:

```bash
./scripts/deploy/deploy.sh <server> <instance> [options]

# Deploy binary and sync custom themes
./scripts/deploy/deploy.sh server.example.com my_site \
  -v /var/www/vhosts/example.com -o hosting

# Deploy custom content only (skip binary)
./scripts/deploy/deploy.sh server.example.com my_site \
  --skip-binary -v /var/www/vhosts/example.com -o hosting

# Skip build, dry run
./scripts/deploy/deploy.sh server.example.com my_site --skip-build --dry-run
```

Options:
- `-v, --vhost PATH` — vhost path for custom content sync (only needed if you have custom themes)
- `-o, --owner USER` — vhost owner for chown (required if -v is provided)
- `-g, --group GROUP` — vhost group for chown (default: `psaserv`)
- `-u, --user USER` — SSH user (default: `root`)
- `--sync-custom` — force sync custom/ directory even if empty
- `--skip-build` — skip `make build-linux-amd64`, use existing binary
- `--skip-binary` — skip binary build, backup, and transfer (deploy custom content only)
- `--dry-run` — print commands without executing

Symlinked directories inside `custom/` are followed during deployment. Before syncing, all symlinks are validated: broken symlinks or links that resolve outside `custom/` abort the deploy before the instance is stopped.

The script:
1. Builds `bin/ocms-linux-amd64`
2. Backs up current binary on server
3. Stops the instance via `ocmsctl`
4. Transfers binary via `scp`
5. (If custom themes exist) Syncs `custom/` to `{vhost}/ocms/custom/` via `rsync -aL --delete`
6. (If custom themes exist) Sets ownership to `{owner}:{group}`
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

## Custom Themes

Core themes (`default`, `developer`) are embedded in the binary. To use a custom theme:

1. Create your theme in `custom/themes/mytheme/`
2. Deploy with the `-v` and `-o` options:
   ```bash
   ./scripts/deploy/deploy.sh server.example.com my_site \
     -v /var/www/vhosts/example.com -o hosting
   ```
3. Set `OCMS_ACTIVE_THEME=mytheme` in the site's `.env` file

To override an embedded theme, create a custom theme with the same name:
```
custom/themes/default/    # Overrides the embedded 'default' theme
```

Custom themes with the same name as core themes take priority.

For local development, `custom/` may contain symlinked theme or module directories. `deploy.sh` follows those symlinks and copies the resolved files to the server, but it aborts if any symlink target is missing or resolves outside `custom/`.

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
  -v /var/www/vhosts/example.com --sync-custom

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
- `--sync-custom` — Also sync custom/ directory (themes, modules)
- `--dry-run` — Print commands without executing

The script:
1. Stops local development server (port 8080)
2. Stops remote instance via `ocmsctl`
3. Checkpoints SQLite WAL on server (flushes to main file)
4. Syncs database via `rsync`
5. Syncs uploads via `rsync --delete` (mirrors production exactly)
6. Syncs logs via `rsync` (keeps local logs not on prod)
7. (If --sync-custom) Syncs custom directory
8. Restarts remote instance

**WARNING:** This overwrites your local `data/`, `uploads/`, and `logs/` directories with production data!

## Backups

```bash
# Backup all sites
sudo /opt/ocms/backup-multi.sh

# Backup one site
sudo /opt/ocms/backup-multi.sh example_com
```

Backups include:
- Database (`ocms_TIMESTAMP.db.gz`)
- Uploads (`uploads_TIMESTAMP.tar.gz`)
- Custom content (`custom_TIMESTAMP.tar.gz`) — only if custom themes exist

Backups are stored in each site's `backups/` directory with 30-day retention.

## Health Checks

```bash
# Check all sites (manual)
sudo /opt/ocms/healthcheck-multi.sh

# Check one site
sudo /opt/ocms/healthcheck-multi.sh example_com
```

Auto-restarts failed systemd-managed instances (max 3 attempts, 5-min cooldown). Supports optional Slack and email alerting — edit the script to configure.

**Limitation:** the healthcheck only acts on sites currently reporting `systemctl is-active` with a failing `/health/ready`. It **skips** `inactive`/`failed` units — so it does not recover sites that never started (e.g., after a reboot with `disabled` units). For reboot recovery, `systemctl enable` is the only safety net; see the Troubleshooting section.

## Cron Jobs

```bash
# /etc/cron.d/ocms-multi
SHELL=/bin/bash
0 3 * * * root /opt/ocms/backup-multi.sh >> /var/log/ocms-backup.log 2>&1
*/5 * * * * root /opt/ocms/healthcheck-multi.sh 2>&1 | grep -v "^$"
```

## Log Rotation

Generate logrotate configuration from `sites.conf` (re-run after adding/removing sites):

```bash
sudo /opt/ocms/generate-logrotate.sh

# Test configuration (dry-run)
sudo logrotate -d /etc/logrotate.d/ocms

# Force rotation (for testing)
sudo logrotate -f /etc/logrotate.d/ocms
```

The generated configuration:
- Per-instance entries with correct paths and ownership (`logs/*.log` glob covers both `ocms.log` and `error.log`)
- Rotates logs daily, keeps 30 days, compresses with gzip
- Uses `copytruncate` to avoid restarting the service

### Separate Error Log

Set `OCMS_ERROR_LOG_PATH=./logs/error.log` in the instance `.env` to write ERROR-level
messages to a dedicated file. Errors are **also** written to stdout (the main log),
so this is purely additive — it gives operators a focused file for quick triage.

```bash
# View error-only log
ocmsctl logs example_com --errors

# Follow error log in real time
ocmsctl logs example_com --errors --follow
```

## File Permissions

| Path | Owner | Mode |
|------|-------|------|
| `/opt/ocms/bin/ocms` | `root:root` | `755` |
| `/opt/ocms/bin/ocmsctl` | `root:root` | `755` |
| `{vhost}/ocms/` | `{user}:psaserv` | `750` |
| `{vhost}/ocms/.env` | `{user}:psaserv` | `600` |
| `{vhost}/ocms/data/` | `{user}:psaserv` | `755` |
| `{vhost}/ocms/uploads/` | `{user}:psaserv` | `755` |
| `{vhost}/ocms/custom/` | `{user}:psaserv` | `755` |

## Site Registry

All sites are tracked in `/etc/ocms/sites.conf`:

```
# oCMS Multi-Instance Site Registry
# Format: SITE_ID INSTANCE_DIR SYSTEM_USER PORT
# Managed by setup-site.sh — do not edit while services are running

# Standard setup (default path)
example_com /var/www/vhosts/example.com/ocms example_com 8081

# Custom path (--path override)
app_example_com /var/www/vhosts/example.com/ocms/app hosting 8082

# Another server vhost
blog_example_com /var/www/vhosts/blog.example.com/ocms bloguser 8083

# Disabled site (commented out, skipped by all scripts)
#old_site_com /var/www/vhosts/old-site.com/ocms olduser 8084
```

## Disabling a Site

To temporarily disable a site without removing its data, comment out its line in `sites.conf`:

```bash
sudo sed -i 's/^example_com/#example_com/' /etc/ocms/sites.conf
sudo ocmsctl stop example_com
```

All multi-instance scripts (`backup-multi.sh`, `healthcheck-multi.sh`, `deploy-multi.sh`) skip commented lines. To re-enable, uncomment the line and start the instance.

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

## Troubleshooting

### Service Won't Start

```bash
sudo systemctl status ocms@example_com
sudo journalctl -u ocms@example_com -n 50

# Common issues:
# - Missing OCMS_SESSION_SECRET in .env
# - Permission denied on data/ or uploads/
# - Port already in use
# - Missing systemd drop-in (see next section)
```

### Systemd Drop-In Missing

Symptom: the journal shows `OCMS_SESSION_SECRET is not set` on a tight restart loop, but the `.env` file exists and contains the secret. Cause: the per-instance drop-in at `/etc/systemd/system/ocms@<site_id>.service.d/instance.conf` is gone (snapshot restore, manual cleanup, etc.), so systemd runs only the template — no `EnvironmentFile=`, no `WorkingDirectory=`, no `User=`.

Diagnose:

```bash
sudo systemctl cat ocms@example_com                              # no [Service] User=/EnvironmentFile= lines?
sudo ls /etc/systemd/system/ocms@example_com.service.d/ 2>&1     # "No such file or directory"?
```

Fix — recreate the drop-in (values come from `/etc/ocms/sites.conf`; copy the format from a working site's `instance.conf`):

```bash
sudo systemctl stop ocms@example_com

sudo mkdir -p /etc/systemd/system/ocms@example_com.service.d
sudo tee /etc/systemd/system/ocms@example_com.service.d/instance.conf > /dev/null <<'EOF'
[Service]
User=<system_user>
Group=psaserv
WorkingDirectory=/var/www/vhosts/example.com/ocms
EnvironmentFile=/var/www/vhosts/example.com/ocms/.env
ReadWritePaths=/var/www/vhosts/example.com/ocms
SyslogIdentifier=ocms-example_com
EOF

sudo systemctl daemon-reload
sudo systemctl start ocms@example_com
```

### 502 Bad Gateway

```bash
# Check if oCMS is running
curl http://127.0.0.1:8081/health

# Check nginx config
sudo nginx -t

# Check nginx logs
tail -f /var/log/nginx/error.log
```

### Duplicate Location "/" Error

Plesk generates its own `location /` block. Use a regex pattern instead:

```nginx
# WRONG - causes duplicate location error
location / {
    proxy_pass http://127.0.0.1:8081;
}

# CORRECT - regex pattern avoids conflict
location ~ ^/(.*)$ {
    proxy_pass http://127.0.0.1:8081/$1$is_args$args;
}
```

Also ensure **Proxy mode** is unchecked in Apache & nginx Settings.

### Permission Errors

```bash
sudo chown -R {user}:psaserv {vhost}/ocms/data {vhost}/ocms/uploads
sudo chmod 600 {vhost}/ocms/.env
```

### Sites Don't Come Back After Reboot

Symptom: after a server restart, every domain returns 502 from nginx and `ocmsctl list` shows every site `stopped`.

Cause: the systemd units were never `systemctl enable`-d, so they don't auto-start at boot. `setup-site.sh` prints the enable command as a final step, but it's easy to skip — or to get undone by a manual `systemctl disable`.

Diagnose:

```bash
sudo /opt/ocms/bin/ocmsctl list          # all "stopped"?
sudo systemctl is-enabled 'ocms@*'       # any "disabled"?
```

Fix — enable every site registered in `sites.conf`:

```bash
sudo awk '!/^#/ && NF>0 {print $1}' /etc/ocms/sites.conf \
  | xargs -I{} sudo systemctl enable --now ocms@{}
```

After this, all sites auto-start on every future reboot.

**Note:** `healthcheck-multi.sh` does **not** revive this class of failure. It only restarts sites that are currently `active` but unhealthy; it skips `inactive`/`failed` units. `systemctl enable` is the only safety net that survives a reboot.

### Database Corruption

```bash
# Check integrity
sqlite3 {vhost}/ocms/data/ocms.db "PRAGMA integrity_check;"

# Restore from backup
sudo ocmsctl stop example_com
gunzip {vhost}/ocms/backups/ocms_TIMESTAMP.db.gz -k
mv {vhost}/ocms/data/ocms.db {vhost}/ocms/data/ocms.db.corrupted
mv {vhost}/ocms/backups/ocms_TIMESTAMP.db {vhost}/ocms/data/ocms.db
chown {user}:psaserv {vhost}/ocms/data/ocms.db
sudo ocmsctl start example_com
```

## Security Checklist

- [ ] Changed default admin password
- [ ] Set strong `OCMS_SESSION_SECRET` (32+ bytes)
- [ ] Set `OCMS_ENV=production`
- [ ] Configure `OCMS_EMBED_PROXY_TOKEN` when embed proxy is enabled
- [ ] Configure `OCMS_EMBED_ALLOWED_ORIGINS` for all public hostnames
- [ ] Set `OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS` for your API host
- [ ] SSL/TLS enabled with valid certificate
- [ ] HTTP redirects to HTTPS
- [ ] `.env` permissions set to 600
- [ ] Firewall configured (only 80/443 open)
- [ ] Backups scheduled (see Cron Jobs section)
- [ ] Health monitoring enabled (see Health Checks section)

## Migration from Separate Theme Directory

If you're upgrading from a version that used a separate `themes/` directory:

1. **Update the binary** — new versions have core themes embedded
2. **Move custom themes** — if you have custom themes:
   ```bash
   # On the server, for each site:
   mkdir -p /var/www/vhosts/example.com/ocms/custom/themes
   # Move only custom themes (not default or developer)
   mv /var/www/vhosts/example.com/ocms/themes/mytheme \
      /var/www/vhosts/example.com/ocms/custom/themes/
   # Remove old themes directory
   rm -rf /var/www/vhosts/example.com/ocms/themes
   ```
3. **Update .env** — change `OCMS_THEMES_DIR` to `OCMS_CUSTOM_DIR`:
   ```bash
   # In .env file, replace:
   # OCMS_THEMES_DIR=./themes
   # With:
   OCMS_CUSTOM_DIR=./custom
   ```
4. **Remove /opt/ocms/themes/** — no longer needed:
   ```bash
   sudo rm -rf /opt/ocms/themes
   ```

## Migration from vhost_path to instance_dir in sites.conf

> **⚠️ Breaking change** — `sites.conf` format changed in this release. Existing deployments must run the migration below before updating the deploy scripts.

The `sites.conf` format changed: column 2 is now the full instance directory
instead of the vhost path (the scripts no longer append `/ocms` automatically).

Old format:
```
example_com /var/www/vhosts/example.com example_com 8081
```

New format:
```
example_com /var/www/vhosts/example.com/ocms example_com 8081
```

### Upgrade sequence

**All steps run on the server.** Steps 3 and 4 must happen together — old scripts expect old format, new scripts expect new format. Never mix them.

```bash
# 1. Stop all instances
sudo /opt/ocms/bin/ocmsctl list
sudo /opt/ocms/bin/ocmsctl stop <site> # for each site
# Or if using systemd: sudo systemctl stop ocms@<site_id>

# 2. Backup sites.conf
sudo cp /etc/ocms/sites.conf /etc/ocms/sites.conf.backup

# 3. Migrate sites.conf (append /ocms to column 2)
sudo awk '{
    if (/^#/ || /^$/ || NF < 4) { print; next }
    if ($2 !~ /\/ocms$/) $2 = $2 "/ocms"
    print
}' /etc/ocms/sites.conf > /tmp/sites.conf.new
sudo mv /tmp/sites.conf.new /etc/ocms/sites.conf

# 4. Update deploy scripts
sudo cp /tmp/ocms-setup/ocmsctl /opt/ocms/bin/ocmsctl
sudo chmod 755 /opt/ocms/bin/ocmsctl
for script in setup-site.sh backup-multi.sh deploy-multi.sh healthcheck-multi.sh generate-logrotate.sh helper.sh; do
    sudo cp /tmp/ocms-setup/$script /opt/ocms/
    sudo chmod 755 /opt/ocms/$script
done

# 5. Update binary
sudo cp /tmp/ocms /opt/ocms/bin/ocms
sudo chmod 755 /opt/ocms/bin/ocms

# 6. Start all instances
sudo /opt/ocms/bin/ocmsctl start <site> # for each site
# Or if using systemd: sudo systemctl start ocms@<site_id>

# 7. Verify
sudo ocmsctl list
sudo /opt/ocms/healthcheck-multi.sh
```

If something goes wrong, rollback:
```bash
sudo cp /etc/ocms/sites.conf.backup /etc/ocms/sites.conf
# Restore old scripts and binary from backup
sudo /opt/ocms/bin/ocmsctl start <site> # for each site
```
