# oCMS Deployment Scripts

This directory contains scripts and configuration files for deploying oCMS on Ubuntu servers with Plesk and Nginx.

## Quick Start

1. **Build on your Mac:**
   ```bash
   make build-linux-amd64
   ```

2. **Copy to server:**
   ```bash
   scp bin/ocms-linux-amd64 user@server:/tmp/ocms
   scp -r scripts/deploy/* user@server:/tmp/ocms-setup/
   ```

3. **Run setup (first time, on server):**
   ```bash
   ssh user@server
   cd /tmp/ocms-setup && sudo ./setup.sh
   ```

4. **Deploy (on server):**
   ```bash
   sudo /opt/ocms/deploy.sh /tmp/ocms
   ```

## Files

| File               | Description                                                                  |
|--------------------|------------------------------------------------------------------------------|
| `setup.sh`         | Automated initial server setup (creates user, directories, installs service) |
| `deploy.sh`        | Deploy new binary with automatic rollback on failure                         |
| `backup.sh`        | Backup database and uploads with retention policy                            |
| `restore.sh`       | Restore from backup files                                                    |
| `healthcheck.sh`   | Health monitoring with optional alerting and auto-restart                    |
| `ocms.service`     | Systemd service file template                                                |
| `ocms.env.example` | Environment configuration template                                           |

## Directory Structure (After Setup)

```
/opt/ocms/
├── bin/
│   └── ocms              # Application binary
├── deploy.sh             # Deployment script
├── backup.sh             # Backup script
├── restore.sh            # Restore script
└── healthcheck.sh        # Health check script

/var/lib/ocms/
├── data/
│   └── ocms.db           # SQLite database
├── uploads/              # User-uploaded media
└── themes/               # Custom themes

/etc/ocms/
└── ocms.env              # Environment configuration

/var/backups/ocms/        # Backup files
```

## Usage

### Initial Setup

```bash
# Copy setup script to server and run
sudo ./setup.sh
```

### Deployment

```bash
# On your Mac
make build-linux-amd64
scp bin/ocms-linux-amd64 user@server:/tmp/ocms

# On server
sudo /opt/ocms/deploy.sh /tmp/ocms
```

### Backup

```bash
# Manual backup
sudo /opt/ocms/backup.sh

# Backup to custom directory
sudo /opt/ocms/backup.sh /path/to/backups
```

### Restore

```bash
# Restore database only
sudo /opt/ocms/restore.sh /var/backups/ocms/ocms_20240115_030000.db.gz

# Restore database and uploads
sudo /opt/ocms/restore.sh /var/backups/ocms/ocms_20240115_030000.db.gz /var/backups/ocms/uploads_20240115_030000.tar.gz
```

### Health Check

```bash
# Manual health check
/opt/ocms/healthcheck.sh

# Configured to run via cron every 5 minutes
```

## Automated Tasks (Cron)

The setup script configures these cron jobs in `/etc/cron.d/ocms`:

- **Daily backup** at 3 AM
- **Health check** every 5 minutes

## Documentation

For detailed deployment instructions, see:
- [Ubuntu/Plesk Deployment Guide](../../docs/deploy-ubuntu-plesk.md)
- [Reverse Proxy Configuration](../../docs/reverse-proxy.md)
