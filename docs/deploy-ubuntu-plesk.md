# Deploying oCMS on Ubuntu with Plesk and Nginx

This guide covers deploying oCMS on an Ubuntu server managed by Plesk, using Nginx as the reverse proxy (without Apache).

## Prerequisites

- Ubuntu 20.04 LTS or newer
- Plesk Obsidian 18.0+ installed
- Domain configured in Plesk
- SSH access with sudo privileges

---

## Part 1: Build and Deploy Binary

### 1.1 Build on Your Mac

```bash
# Build Linux binary
make build-linux-amd64

# Verify
ls -lh bin/ocms-linux-amd64
```

### 1.2 Copy to Server

```bash
# Copy binary
scp bin/ocms-linux-amd64 user@your-server:/tmp/ocms

# Copy deployment scripts (first time only)
scp -r scripts/deploy/* user@your-server:/tmp/ocms-setup/
```

---

## Part 2: Server Setup

### 2.1 Create System User and Directories

```bash
# Create ocms user (no login shell)
sudo useradd -r -s /bin/false -d /var/lib/ocms ocms

# Create directories
sudo mkdir -p /opt/ocms/bin
sudo mkdir -p /var/lib/ocms/{data,uploads,themes}
sudo mkdir -p /etc/ocms
sudo mkdir -p /var/backups/ocms

# Set ownership
sudo chown -R ocms:ocms /var/lib/ocms
sudo chown -R ocms:ocms /opt/ocms
sudo chown -R ocms:ocms /var/backups/ocms
```

### 2.2 Deploy the Binary

```bash
# Binary was copied to /tmp/ocms in step 1.2
sudo cp /tmp/ocms /opt/ocms/bin/ocms
sudo chmod 755 /opt/ocms/bin/ocms
sudo chown ocms:ocms /opt/ocms/bin/ocms
```

### 2.3 Configure Environment

Create the environment file:

```bash
sudo nano /etc/ocms/ocms.env
```

Add the following (generate secret with `openssl rand -base64 32`):

```bash
# Required - session encryption key (min 32 bytes)
OCMS_SESSION_SECRET=your-secure-secret-key-at-least-32-bytes-long

# Server configuration
OCMS_SERVER_HOST=127.0.0.1
OCMS_SERVER_PORT=8081
OCMS_ENV=production
OCMS_LOG_LEVEL=warn

# Paths
OCMS_DB_PATH=/var/lib/ocms/data/ocms.db
OCMS_THEMES_DIR=/var/lib/ocms/themes
OCMS_ACTIVE_THEME=default

# Optional: Redis for distributed caching
# OCMS_REDIS_URL=redis://localhost:6379/0

# Optional: hCaptcha for login protection
# OCMS_HCAPTCHA_SITE_KEY=your-site-key
# OCMS_HCAPTCHA_SECRET_KEY=your-secret-key
```

Secure the file:

```bash
sudo chmod 600 /etc/ocms/ocms.env
sudo chown ocms:ocms /etc/ocms/ocms.env
```

### 2.4 Create Systemd Service

```bash
sudo nano /etc/systemd/system/ocms.service
```

Add:

```ini
[Unit]
Description=oCMS Content Management System
After=network.target

[Service]
Type=simple
User=ocms
Group=ocms
WorkingDirectory=/var/lib/ocms
EnvironmentFile=/etc/ocms/ocms.env
ExecStart=/opt/ocms/bin/ocms
Restart=always
RestartSec=5

# Security
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/ocms

# Graceful shutdown
TimeoutStopSec=30
KillMode=mixed

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=ocms

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable ocms
sudo systemctl start ocms

# Verify it's running
sudo systemctl status ocms
curl http://127.0.0.1:8081/health
```

---

## Part 3: Plesk Configuration (Nginx Only)

### 3.1 Disable Apache for Your Domain

Plesk can run Nginx alone without Apache. This is more efficient for reverse proxy setups.

**Option A: Disable Apache globally (if you don't need it for any site)**

1. Log in to Plesk
2. Go to **Tools & Settings** → **Updates and Upgrades**
3. Click **Add/Remove Components**
4. Find **Web hosting** → **Apache web server**
5. Set to "Remove" and apply

**Option B: Disable Apache per-domain (recommended)**

1. Go to **Websites & Domains**
2. Click on your domain (e.g., `cms.example.com`)
3. Click **Hosting Settings**
4. Uncheck **Apache support** (or set to "Nginx only")
5. Click **OK**

### 3.2 Configure SSL Certificate

1. Go to **Websites & Domains** → your domain
2. Click **SSL/TLS Certificates**
3. Click **Install** under "Let's Encrypt"
4. Check your domain name(s)
5. Click **Get it free**
6. After installation, go back to **Hosting Settings**
7. Enable **Permanent SEO-safe 301 redirect from HTTP to HTTPS**

### 3.3 Configure Nginx Reverse Proxy

1. Go to **Websites & Domains** → your domain
2. Click **Apache & nginx Settings**

   > Note: Even with Apache disabled, Plesk still shows this panel for nginx configuration.

3. Ensure **Proxy mode** is **unchecked** (we don't want nginx proxying to Apache)

4. Scroll down to **Additional nginx directives**

5. Paste the following configuration:

```nginx
# oCMS Reverse Proxy to Go app on port 8081
# IMPORTANT: Use regex location to avoid "duplicate location /" error
# Plesk generates its own "location /" block, so we use a regex pattern
location ~ ^/(.*)$ {
    proxy_pass http://127.0.0.1:8081/$1$is_args$args;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_connect_timeout 60s;
    proxy_send_timeout 60s;
    proxy_read_timeout 60s;
    client_max_body_size 100M;
}

# Block access to hidden files
location ~ /\. {
    deny all;
}
```

> **Note:** Do NOT use `location /` in Plesk's additional nginx directives. Plesk generates its own `location /` block, which causes a "duplicate location" error. The regex pattern `~ ^/(.*)$` captures all paths and has higher priority than Plesk's prefix location.

6. Click **OK** to apply

### 3.4 Verify Configuration

```bash
# Test nginx configuration
sudo nginx -t

# If there are errors, check Plesk logs
tail -f /var/log/nginx/error.log

# Test the site
curl -I https://cms.example.com/health
```

---

## Part 4: Initial Login and Configuration

1. Open `https://cms.example.com/admin/` in your browser

2. Login with default credentials:
   - Email: `admin@example.com`
   - Password: `changeme1234`

3. **Immediately change the password:**
   - Go to **Settings** → **Users**
   - Click **Edit** on the admin user
   - Set a strong password

4. Configure your site:
   - **Settings** → **General** - Site name, description
   - **Settings** → **Languages** - Enable languages if needed
   - **Themes** - Select and configure your theme

---

## Part 5: Continuous Deployment

### 5.1 Update Process

When you have code changes:

```bash
# On your Mac: build and copy
make build-linux-amd64
scp bin/ocms-linux-amd64 user@your-server:/tmp/ocms

# On server: deploy
ssh user@your-server
sudo /opt/ocms/deploy.sh /tmp/ocms
```

### 5.2 Rollback

If something goes wrong:

```bash
sudo systemctl stop ocms
sudo cp /opt/ocms/bin/ocms.backup /opt/ocms/bin/ocms
sudo systemctl start ocms
```

---

## Part 6: Backup and Maintenance

### 6.1 Backup

Backup scripts are installed by `setup.sh`. Daily backups run automatically at 3 AM.

```bash
# Manual backup
sudo /opt/ocms/backup.sh

# Restore from backup
sudo /opt/ocms/restore.sh /var/backups/ocms/ocms_YYYYMMDD_HHMMSS.db.gz
```

### 6.2 View Logs

```bash
# Real-time logs
sudo journalctl -u ocms -f

# Last 100 lines
sudo journalctl -u ocms -n 100

# Since specific time
sudo journalctl -u ocms --since "1 hour ago"
```

### 6.3 Health Monitoring

Health checks run automatically every 5 minutes (configured by `setup.sh`).

```bash
# Manual health check
curl http://127.0.0.1:8081/health
```

---

## Troubleshooting

### Service Won't Start

```bash
# Check status and logs
sudo systemctl status ocms
sudo journalctl -u ocms -n 50

# Common issues:
# - Missing OCMS_SESSION_SECRET in /etc/ocms/ocms.env
# - Permission denied on /var/lib/ocms
# - Port 8081 already in use
```

### 502 Bad Gateway

```bash
# Check if oCMS is running
curl http://127.0.0.1:8081/health

# Check nginx configuration
sudo nginx -t

# Check nginx logs
tail -f /var/log/nginx/error.log
```

### Duplicate Location "/" Error

If you see this error when saving nginx settings:
```
nginx: [emerg] duplicate location "/" in .../vhost_nginx.conf
```

**Cause:** Plesk generates its own `location /` block, and your configuration also contains `location /`.

**Solution:** Use regex location instead of prefix location:
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
# Reset ownership
sudo chown -R ocms:ocms /var/lib/ocms
sudo chown ocms:ocms /opt/ocms/bin/ocms
sudo chmod 600 /etc/ocms/ocms.env
```

### Database Corruption

```bash
# Check integrity
sqlite3 /var/lib/ocms/data/ocms.db "PRAGMA integrity_check;"

# Restore from backup
sudo systemctl stop ocms
cp /var/backups/ocms/ocms_YYYYMMDD_HHMMSS.db.gz /tmp/
gunzip /tmp/ocms_*.db.gz
mv /var/lib/ocms/data/ocms.db /var/lib/ocms/data/ocms.db.corrupted
mv /tmp/ocms_*.db /var/lib/ocms/data/ocms.db
chown ocms:ocms /var/lib/ocms/data/ocms.db
sudo systemctl start ocms
```

---

## Quick Reference

| Action | Command |
|--------|---------|
| Start service | `sudo systemctl start ocms` |
| Stop service | `sudo systemctl stop ocms` |
| Restart service | `sudo systemctl restart ocms` |
| View status | `sudo systemctl status ocms` |
| View logs | `sudo journalctl -u ocms -f` |
| Health check | `curl http://127.0.0.1:8081/health` |
| Backup database | `sudo /opt/ocms/backup.sh` |
| Deploy update | `sudo /opt/ocms/deploy.sh /path/to/new/binary` |

---

## Security Checklist

- [ ] Changed default admin password
- [ ] Set strong `OCMS_SESSION_SECRET` (32+ bytes)
- [ ] Set `OCMS_ENV=production`
- [ ] SSL/TLS enabled with valid certificate
- [ ] HTTP redirects to HTTPS
- [ ] `/etc/ocms/ocms.env` permissions set to 600
- [ ] Firewall configured (only 80/443 open)
- [ ] Regular backups scheduled
- [ ] Health monitoring enabled

---

## Related Documentation

- [Reverse Proxy Configuration](reverse-proxy.md) - Additional proxy options
- [Login Security](login-security.md) - Rate limiting and lockout
- [Webhooks](webhooks.md) - Event notifications
- [hCaptcha Setup](hcaptcha.md) - Bot protection for admin login
