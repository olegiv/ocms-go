# Sentinel Module

Sentinel is an IP security module for oCMS that provides IP banning, automatic banning via path patterns and honeypot triggers, and IP whitelisting. It operates as middleware early in the request chain, blocking banned IPs before they reach any handler.

## Overview

### Features

- Manual IP banning with wildcard pattern support
- Automatic banning of IPs that access forbidden paths (e.g., `/wp-admin*`, `*/phpmyadmin*`)
- Automatic banning of IPs that trigger form honeypot fields
- IP whitelisting to bypass all security checks
- GeoIP country detection for banned IPs (requires MaxMind GeoLite2 database)
- In-memory caching of all patterns for fast lookup
- Admin UI for managing bans, paths, whitelist, and settings
- Event logging for all auto-ban actions
- Safe: admins cannot ban their own IP, authenticated editors/admins skip path auto-ban

### How It Works

Sentinel registers middleware at position 3 in the global middleware chain (after `RequestID` and `TrustedRealIP`). For every request, the middleware:

1. Checks if the module is active (skips if deactivated at runtime)
2. Checks the IP whitelist (whitelisted IPs bypass all checks)
3. Checks if the IP is banned (returns 403 Forbidden)
4. Checks auto-ban path patterns (bans IP and returns 403 if matched)

Honeypot auto-banning works differently -- it fires via the hook system when a bot fills the hidden `_website` field on public forms.

## Admin Interface

Access Sentinel at **Admin > Modules > Sentinel** or directly at `/admin/sentinel`.

### Settings

| Setting | Default | Description |
|---------|---------|-------------|
| IP Ban Check | Enabled | Block requests from banned IP addresses |
| Auto-Ban by Path | Enabled | Automatically ban IPs accessing forbidden paths |
| Auto-Ban on Honeypot | Enabled | Automatically ban IPs that fill honeypot fields in public forms |

All settings can be toggled independently from the admin UI.

### Banned IPs

Add IP bans manually with optional notes and URL context. Supports wildcard patterns:

| Pattern | Matches |
|---------|---------|
| `192.168.1.100` | Exact IP |
| `192.168.1.*` | Any IP in 192.168.1.0/24 |
| `192.168.*.*` | Any IP in 192.168.0.0/16 |
| `10*` | Partial match: 100.x.x.x, 101.x.x.x, etc. |

Bans from auto-ban (path or honeypot) are logged with the triggering path/form and country code.

### Auto-Ban Paths

Path patterns that trigger automatic IP banning when accessed. Supports wildcards:

| Pattern | Matches |
|---------|---------|
| `/wp-admin` | Exact path |
| `/wp-admin*` | Starts with `/wp-admin` |
| `*/phpmyadmin*` | Contains `/phpmyadmin` |
| `*/.env` | Ends with `/.env` |

Default paths seeded on first install:

- `/wp-admin*` -- WordPress admin
- `/wp-login*` -- WordPress login
- `*/.env` -- Environment files
- `*/xmlrpc.php` -- WordPress XML-RPC
- `/wp-includes*` -- WordPress includes
- `*/phpmyadmin*` -- phpMyAdmin
- `*/wp-content/plugins*` -- WordPress plugins

Authenticated admin/editor users are exempt from path-based auto-banning.

### Honeypot Auto-Ban

Public forms include a hidden `_website` field (invisible to real users via CSS). Bots that parse HTML and fill all fields will populate it, triggering an automatic ban.

The flow:
1. Bot submits form with `_website` field filled
2. Forms handler detects honeypot, logs a security warning event
3. Fires the `security.honeypot_triggered` hook
4. Sentinel hook handler checks settings and whitelist
5. Bans the IP via `sentinel_banned_ips` table
6. Bot receives a fake success response (silent rejection)
7. All subsequent requests from the IP are blocked with 403

The ban record includes the form URL and `honeypot:<form_slug>` as the matched pattern.

### IP Whitelist

Whitelisted IPs bypass all Sentinel checks (ban check, auto-ban, honeypot ban). Use this for trusted services, monitoring systems, or office IPs. Supports the same wildcard patterns as banning.

## GeoIP Integration

Sentinel resolves country codes for banned IPs using the MaxMind GeoLite2-Country database. To enable:

1. Download `GeoLite2-Country.mmdb` from [MaxMind](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data)
2. Set the `OCMS_GEOIP_DB_PATH` environment variable to the file path

Without GeoIP configured, country detection is disabled and the country code column remains empty.

## Database Tables

Sentinel manages its own tables via module migrations:

| Table | Description |
|-------|-------------|
| `sentinel_banned_ips` | Banned IP patterns with country code, notes, URL, timestamp |
| `sentinel_autoban_paths` | Path patterns that trigger auto-banning |
| `sentinel_whitelist` | Whitelisted IP patterns |
| `sentinel_settings` | Module settings (key-value pairs) |

## Admin Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/sentinel` | Sentinel dashboard |
| POST | `/admin/sentinel` | Create IP ban |
| POST | `/admin/sentinel/ban` | Ban IP via AJAX (from events page) |
| DELETE | `/admin/sentinel/{id}` | Remove IP ban |
| POST | `/admin/sentinel/paths` | Create auto-ban path |
| DELETE | `/admin/sentinel/paths/{id}` | Remove auto-ban path |
| POST | `/admin/sentinel/whitelist` | Create whitelist entry |
| DELETE | `/admin/sentinel/whitelist/{id}` | Remove whitelist entry |
| POST | `/admin/sentinel/settings` | Update settings |

## Template Functions

Sentinel provides template functions for use in themes:

| Function | Returns | Description |
|----------|---------|-------------|
| `sentinelVersion` | `string` | Module version |
| `sentinelIsActive` | `bool` | Whether Sentinel is active |
| `sentinelIsIPBanned(ip)` | `bool` | Check if an IP is banned |
| `sentinelIsIPWhitelisted(ip)` | `bool` | Check if an IP is whitelisted |

## Hook Integration

Sentinel listens for the `security.honeypot_triggered` hook, which is fired by the forms handler when a honeypot field is detected. The hook data is a `map[string]any` with:

| Key | Type | Description |
|-----|------|-------------|
| `ip` | `string` | Client IP address |
| `form_slug` | `string` | Form slug (e.g., `contact-us`) |
| `form_id` | `int64` | Form database ID |
| `request_url` | `string` | Full request URL |
