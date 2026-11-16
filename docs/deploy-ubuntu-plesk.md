# Deploying oCMS with ocmsctl

`ocmsctl` deploys and operates oCMS sites on a Linux server: one shared binary,
one systemd unit per site (`ocms@<site>`), Nginx (Plesk) in front.

It is one Go program (`cmd/ocmsctl`) used in two places:

- **on your machine** it builds, uploads and calls the server over `ssh`;
- **on the server** it installs releases, restarts sites behind a health check,
  provisions sites, backs them up and monitors them.

Deployment decisions run in Go on both ends. Initial bootstrap uses one fixed
installer line over SSH to install the server tool. Your `~/.ssh/config`, agent and jump hosts apply, because the system
`ssh` and `rsync` are used.

## What a deploy does

`ocmsctl deploy <host> <site>...`

1. Builds the Linux binary (`make build-linux-amd64`). From a site repository
   that is the site Makefile, so the binary carries the site's custom modules.
2. Computes its SHA-256 and streams it to the server over ssh.
3. The server verifies the digest and that the file is a Linux executable.
   A bad upload stops here: nothing was installed, nothing was stopped.
4. Installs it as a new release, `/opt/ocms/releases/<time>-<digest>/ocms`, and
   switches the `/opt/ocms/bin/ocms` symlink to it by rename. Running sites keep
   the binary they started from.
5. Stops the named sites, starts them (systemd gives each up to 60 seconds to
   drain requests), and waits for each to answer `/health` (30 seconds; change
   with `--health-timeout 1m`).
6. Asks every healthy site once more after 3 seconds. systemd restarts a binary
   that exits, so a site that dies right after its first answer would otherwise
   pass.
7. Reports each site `OK` or `FAILED` with the last lines of its log, and exits 1
   if any failed. A site that did not stop is not started (its old process may
   still hold the port); the other sites carry on.

There is no automatic rollback: the new binary may already have migrated a
database. The failed release stays installed and the output names the command
that goes back (`ocmsctl rollback`).

Sites you do not name keep running the old binary until they restart.
After a deployment passes its health checks, the last 3 releases are kept.
Failed deployments and installs without restart targets do not prune releases,
so retries preserve the last working binary.

## Layout on the server

```
/opt/ocms/bin/ocms -> ../releases/<release>/ocms    current release (symlink)
/opt/ocms/bin/ocmsctl                               this tool
/opt/ocms/releases/<release>/ocms                   retained releases
/etc/ocms/sites.conf                                site registry: ID DIR USER PORT
/etc/ocms/alert                                     optional alert hook
/etc/systemd/system/ocms@.service                   unit template
/etc/systemd/system/ocms@<site>.service.d/instance.conf   per-site user, directory, env file
/etc/cron.d/ocms                                    nightly backup, health check every 5 min
/etc/logrotate.d/ocms                               generated from the registry

/var/www/vhosts/<domain>/ocms/                      one site
├── .env                 configuration and secrets (0600)
├── data/ocms.db         SQLite database
├── uploads/             media
├── custom/themes/       custom themes (core themes are in the binary)
├── logs/ocms.log        application log; logs/error.log errors only
└── backups/             backups (0700)
```

Each site listens on its own loopback port (8081, 8082, ...).

## First-time server setup

From a checkout of oCMS, with root ssh access to the server:

```bash
make server-setup HOST=root@server.example.com
```

This builds `ocmsctl`, installs it as `/opt/ocms/bin/ocmsctl` and runs
`ocmsctl init` there: directories, the systemd unit template, the cron file
(only when there is none) and the logrotate configuration. Run it again to
update `ocmsctl` on the server after updating oCMS.

For an ARM server add `SERVER_ARCH=arm64` to this and the other `make` commands.

## Adding a site

On the server, as root. The domain must exist as a Plesk vhost.

```bash
ocmsctl site add example.com example_com
```

Arguments are the domain and the vhost's system user. Options: `--port N`
(default: first free port from 8081), `--group G` (default `psaserv`),
`--dir PATH` (default `/var/www/vhosts/<domain>/ocms`; must stay under the
vhost).

It creates the directories, a `.env` with four generated secrets, the systemd
drop-in and the registry entry, regenerates logrotate, and prints the Nginx
snippet. The site ID is the domain with `.` and `-` replaced by `_`.

Then:

1. **Finish `.env`.** In production oCMS refuses to start until these are set
   (or the matching `OCMS_REQUIRE_*` is explicitly `false`):

   ```bash
   OCMS_TRUSTED_PROXIES=127.0.0.1          # the nginx on this host
   OCMS_API_ALLOWED_CIDRS=203.0.113.0/24   # who may use API keys
   OCMS_HCAPTCHA_SITE_KEY=...              # or OCMS_REQUIRE_FORM_CAPTCHA=false
   OCMS_HCAPTCHA_SECRET_KEY=...
   ```

   Check `OCMS_EMBED_ALLOWED_ORIGINS` (every public hostname) and
   `OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS`. Back up `OCMS_MFA_KEY` with the database:
   without it, enrolled users can sign in only with recovery codes.

2. **Nginx.** Paste the printed snippet into Plesk → Websites & Domains →
   the domain → Apache & nginx Settings → Additional nginx directives, and leave
   "Proxy mode" unchecked. Install a Let's Encrypt certificate and enable the
   HTTP → HTTPS redirect in Plesk.

3. **First admin account.** Keep `OCMS_DO_SEED=false` in `.env`; seed once in the
   foreground:

   ```bash
   cd /var/www/vhosts/example.com/ocms
   sudo -u example_com env OCMS_ENV=development OCMS_DO_SEED=true /opt/ocms/bin/ocms
   ```

   Sign in at `https://example.com/admin/` as `admin@example.com` with
   `OCMS_SEED_ADMIN_PASSWORD` from `.env`, change the email and password, stop
   the process with Ctrl+C and delete `OCMS_SEED_ADMIN_PASSWORD` from `.env`.

4. **Start it for good.**

   ```bash
   systemctl enable --now ocms@example_com
   ocmsctl status
   ```

   `enable` is what brings the site back after a reboot.

`ocmsctl site remove <site>` disables the unit and removes the drop-in and the
registry entry. The site's directory is left alone. To disable a site without
removing it, comment out its line in `/etc/ocms/sites.conf` and stop it.

## Deploying

From the oCMS checkout:

```bash
make deploy HOST=root@server.example.com SITES="example_com blog_example_com"
```

From a site repository (`include core/site.mk`), set the target once in the
site Makefile, before the include:

```make
DEPLOY_HOST  ?= root@server.example.com
DEPLOY_SITES ?= example_com
include core/site.mk
```

```bash
make deploy                        # binary
make deploy DEPLOY_OPTS=--themes   # binary and custom/themes
```

`--dry-run` is supported by the local `bootstrap`, `deploy` and `pull` commands.
Server commands reject it before changing anything. Deploy requires named sites
or `--all`.

Or call the tool directly (`make build-ctl` builds `bin/ocmsctl`):

```bash
ocmsctl deploy root@server example_com blog_example_com
ocmsctl deploy root@server --all                       # every registered site
ocmsctl deploy root@server example_com --themes        # also replace its themes
ocmsctl deploy root@server example_com --themes-only   # themes and restart, no binary
ocmsctl deploy root@server example_com --no-build --binary ocms   # a release download
ocmsctl deploy root@server example_com --dry-run       # print the commands only
```

To deploy a published release instead of a local build, download and verify it
(`gh attestation verify <archive> --repo olegiv/ocms-go`), then use
`--no-build --binary <file>`.

### Themes

`--themes` replaces the site's `custom/themes` with your local `custom/themes`.
Symlinked theme directories are followed and arrive as real files when their
targets stay inside `custom/`, outside `custom/modules/`. Links to developer
configuration or other files outside that tree abort deployment before upload.
The upload is
unpacked beside the old themes and swapped in, so a broken upload leaves the old
set in place; themes you deleted locally disappear from the server. It refuses
to run when `custom/themes` holds no theme (a directory with `theme.json`).

Custom **modules** are Go code compiled into the binary; they travel with the
binary, never as files. All sites share one binary, so it must contain every
site's modules: see [docs/custom-modules.md](custom-modules.md).

### Rolling back

On the server:

```bash
ocmsctl releases                           # * marks the current one
ocmsctl rollback --all                     # the release before the current one
ocmsctl rollback example_com --to 20261113T101500Z-3728df6f
```

Rollback switches the symlink and restarts the named sites behind the same
health check. An older binary is not guaranteed to run on a database a newer one
migrated; restore the database from a backup if it does not.

## Operating sites

On the server, as root:

| Command | Does |
|---|---|
| `ocmsctl status [site...]` | unit state and health of the sites (all by default) |
| `ocmsctl start\|stop <site>...\|--all` | `systemctl start\|stop ocms@<site>` |
| `ocmsctl restart <site>...\|--all` | stop, start, require healthy (as a deploy does) |
| `ocmsctl health <site>` | exit 0 when `/health` answers 200 |
| `ocmsctl logs <site> [-f] [--errors]` | the site's log file, or the journal when it has none |

Sites run only under systemd. To debug one in the foreground, stop its unit and
run `/opt/ocms/bin/ocms` as the site user from the site directory.

## Backups

```bash
ocmsctl backup                # all sites
ocmsctl backup example_com
```

Per site, into its `backups/` (mode 0700, files 0600): a consistent snapshot of
the database taken while the site runs (`ocms_<time>.db.gz`, integrity-checked),
`uploads_<time>.tar.gz` and `custom_<time>.tar.gz`. Backups older than 30 days
are removed only after the current backup succeeds. Everything is done as the
site's own user, never as root, so a directory a site user swapped for a symlink cannot redirect a root write.

The cron file runs this nightly at 03:00 and logs to `/var/log/ocms-backup.log`.

Restore a database:

```bash
ocmsctl stop example_com
cd /var/www/vhosts/example.com/ocms
gunzip -k backups/ocms_<time>.db.gz
mv data/ocms.db data/ocms.db.old && rm -f data/ocms.db-wal data/ocms.db-shm
mv backups/ocms_<time>.db data/ocms.db && chown example_com:psaserv data/ocms.db
ocmsctl start example_com
```

## Monitoring

`ocmsctl check` runs from cron every 5 minutes:

| Unit state | Meaning | What the check does |
|---|---|---|
| `active`, `/health/ready` fails | running but unhealthy | reports it, restarts it at most 3 times per outage |
| `activating` | exits at startup; systemd keeps restarting it | reports it; a restart adds nothing |
| `failed` | systemd gave up, or a stop did not end cleanly | reports it; not restarted, it may have been stopped on purpose |
| `inactive` | stopped or never started | skipped |

It prints only when something changes: a site goes down, a restart is tried,
the restarts run out, the site recovers. Set `MAILTO=you@example.com` in
`/etc/cron.d/ocms` to get those by mail; a long outage does not mail every five
minutes. It exits 1 while any site is down.

For other channels create an executable `/etc/ocms/alert`; it is called with
the site ID, the status (`DOWN`, `NEEDS ATTENTION`, `recovered`) and a message:

```bash
#!/bin/sh
# /etc/ocms/alert <site> <status> <message>
curl -fsS "https://api.telegram.org/bot<your-token>/sendMessage" \
  --data-urlencode "chat_id=<chat-id>" --data-urlencode "text=$2 $3" >/dev/null
```

State is kept in `/run/ocms` (root only, cleared on reboot). A site whose unit
is not enabled does not come back after a reboot, and the check skips it:
`systemctl enable ocms@<site>` is the safety net.

## Copying production to development

```bash
make pull HOST=root@server.example.com SITE=example_com
# or: ocmsctl pull root@server example_com [--no-db] [--no-uploads] [--no-logs] [--themes] [--yes]
```

Database snapshots and backups read `OCMS_DB_PATH` from the site's `.env`.
Site-level `make pull` also uses its configured local database path.

Production keeps running. The database arrives as an online snapshot and is
scrubbed before it replaces `data/ocms.db`; `uploads/` is mirrored (local files
the server lacks are deleted), `logs/` is copied, and `--themes` copies
`custom/themes` without deleting local themes. Uploads are copied live, so a file
uploaded during the pull may be missing from the database copy or the reverse.

Stop the local server first: the command refuses to replace the database while
something listens on port 8080 (`--port N` names another port, `--port 0` skips
the check).

The scrub ([sync-db-policy.sql](../cmd/ocmsctl/templates/sync-db-policy.sql)) deletes sessions, OAuth
tokens and codes and preview links; clears webhook URLs, secrets and headers and
scheduled-task URLs and disables both; clears the hCaptcha secret; resets embed
providers. **The result is still production data**: it keeps password and API
key hashes, sealed MFA secrets, recovery-code hashes, content, form submissions
and logs.

## Troubleshooting

**A deploy reports a site `FAILED`.** The last lines of its log are in the
output; `ocmsctl logs <site>` shows more. Usual causes: a setting the new
version requires is missing from `.env`, the port is taken, or `data/` is not
writable by the site user. Fix it and `ocmsctl restart <site>`, or
`ocmsctl rollback`.

**`OCMS_SESSION_SECRET is not set` in a restart loop, but `.env` has it.** The
drop-in `/etc/systemd/system/ocms@<site>.service.d/instance.conf` is gone, so
the unit has no `EnvironmentFile`, `User` or `WorkingDirectory`. Copy one from
another site, adjust user and paths, then `systemctl daemon-reload` and
`ocmsctl restart <site>`.

**502 Bad Gateway.** `ocmsctl status` shows whether the site answers on its
port; if it does, check `nginx -t` and the Plesk snippet.

**Nginx: duplicate location "/".** Plesk generates its own `location /`. Use
the printed snippet (`location ~ ^/(.*)$`) and leave "Proxy mode" unchecked.

**URLs with spaces or other encoded characters return 400.** The `proxy_pass`
line has a URI after the port (`.../$1$is_args$args`). Remove it:
`proxy_pass http://127.0.0.1:8081;`.

**Sites are down after a reboot.** Their units were never enabled:
`systemctl enable --now ocms@<site>` for each.

**Database corruption.** `sqlite3 data/ocms.db "PRAGMA integrity_check;"`, then
restore a backup as shown under [Backups](#backups).

## Security checklist

- [ ] Default admin password changed, `OCMS_SEED_ADMIN_PASSWORD` removed from `.env`
- [ ] `.env` is mode 600 and `OCMS_MFA_KEY` is backed up with the database
- [ ] `OCMS_TRUSTED_PROXIES` and `OCMS_API_ALLOWED_CIDRS` set
- [ ] `OCMS_EMBED_ALLOWED_ORIGINS` lists every public hostname
- [ ] TLS certificate installed, HTTP redirects to HTTPS
- [ ] Firewall leaves only 80 and 443 open; site ports listen on 127.0.0.1
- [ ] Units enabled (`systemctl is-enabled 'ocms@*'`)
- [ ] `/etc/cron.d/ocms` present, with `MAILTO` or an alert hook

## Moving from the shell scripts

The registry format and site directories from the previous implementation
remain compatible. Switching an existing server requires these operator steps;
updating the source checkout alone does not perform them.

1. Before replacing the installed management tool, stop any sites running in
   its former PID-file mode with that installed tool. Enabled systemd sites
   can keep running during the tool update.
2. From the core checkout, run `make server-setup HOST=root@server`. It replaces
   `/opt/ocms/bin/ocmsctl` with the Go tool and creates `/etc/cron.d/ocms` if
   missing. Review an existing cron file: initialization preserves it.
   Retire the old `/etc/cron.d/ocms-multi` and any other old deployment cron
   entries after checking the new backup and monitor jobs, so they do not run
   twice. Remove former deployment scripts in `/opt/ocms` once nothing refers
   to them; preserve unrelated scripts and any alert integrations.
3. The first `ocmsctl deploy` moves the plain file `/opt/ocms/bin/ocms` into
   `/opt/ocms/releases/<time>-legacy/` and makes `bin/ocms` a symlink, so
   `ocmsctl rollback` can return to it. `ocms.backup` is no longer written.
4. For each site moved from PID-file mode, use
   `systemctl enable --now ocms@<site>`. Check `ocmsctl status` and
   `ocmsctl health <site>` after the move.

The source tree now contains only the Go deployment implementation. Update
site Makefiles to include `core/site.mk` and set `DEPLOY_HOST` and
`DEPLOY_SITES`; replace old deployment wrappers with `make deploy` or
`make pull`. The former deployment Make target and shell-specific flags are
no longer supported. The server registry supplies site directories and owners.

Build, CI and Fly.io scripts are independent of this systemd/Plesk workflow.
They remain available. The release checker under `scripts/deploy` is also
retained.

## For other projects

The logic is in `internal/sitectl`, which imports nothing from oCMS
(`TestSitectlIsApplicationAgnostic`); `cmd/ocmsctl/main.go` supplies the name,
paths, health endpoints, unit file and templates. A tool for another Go service
is a `main.go` of that size.
