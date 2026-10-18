# Internal Analytics Module

The Internal Analytics module is a first-party, privacy-focused analytics backend. Unlike the External Analytics module (which injects GA4 / GTM / Matomo into visitors' browsers), all tracking happens server-side in oCMS and the raw data never leaves your database. It records page views, unique visitors, referrers, browser/device stats, geographic data, and Medium.com-style "reads" (scroll-depth + time-on-page engagement).

For the module's own detailed notes and JS snippets, see `modules/analytics_int/README.md`.

## Overview

### Features

- Page-view tracking via chi middleware (no client-side beacon for views)
- Read tracking — scroll-depth ≥ 60 % and a content-aware time-on-page threshold (5–20 s based on word count, with a 5 s server-side floor) — via the `/analytics/read` beacon endpoint
- Rotating salted visitor hash (default 24 h rotation) — no raw IP or user agent is stored
- Background aggregation into hourly, daily, referrer, tech, and geo tables
- Configurable retention with automatic cleanup
- Self-referral filtering based on the configured `site_url`
- Per-request IP exclusion (global `excluded_ips` config key)
- Optional GeoIP country detection via MaxMind GeoLite2 — hot-reloaded every hour
- Per-post view/read counts rendered by themes through template functions

### How It Works

1. `TrackingMiddleware` records each non-excluded request into `page_analytics_views` with an anonymized visitor hash and session hash (both derived from the rotating salt).
2. Themes optionally inject `analyticsIntReadTracker` — an inline `<script>` that posts `{path, scroll_depth, time_on_page}` to `POST /analytics/read` once both thresholds are met. The endpoint is CSRF-exempt (anonymous engagement data, no session mutation) but rate-limited to 2 req/s per IP with burst 5.
3. Cron jobs aggregate the raw `page_analytics_views` table into the hourly/daily/referrer/tech/geo tables, delete raw views after 7 days, and delete aggregates older than the retention period. `page_analytics_reads` is neither aggregated nor pruned: read counts are queried from it directly.
4. A separate cron job (`geoip_reload`, default `0 * * * *`) re-opens the GeoLite2 file so database updates take effect without a server restart.

## Admin Interface

Access at **Admin > Modules > Internal Analytics** or `/admin/internal-analytics`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/internal-analytics` | Dashboard (overview, charts) |
| GET | `/admin/internal-analytics/report` | Views & Reads report (per-page counts, read rate) |
| GET | `/admin/internal-analytics/api/stats` | JSON stats (overview, top pages, time series) |
| GET | `/admin/internal-analytics/api/realtime` | Real-time visitor count |
| POST | `/admin/internal-analytics/settings` | Save settings |
| POST | `/admin/internal-analytics/aggregate` | Trigger aggregation manually |
| POST | `/analytics/read` | Public read-tracking beacon (rate-limited, CSRF-exempt) |

Editors and administrators can open the dashboard, the report and the JSON
endpoints. Saving settings and triggering aggregation are admin-only: editors
do not see those forms, and their requests get `403 Forbidden`.

### Settings

| Setting | Default | Description |
|---------|---------|-------------|
| Enabled | on | Toggle tracking on/off |
| Show post statistics | on | Display view/read counts on public post pages |
| Retention days | 365 | How long aggregated statistics are kept (raw views are pruned after 7 days) |
| Excluded paths | — | URL prefixes to skip (one per line) |

Excluded IPs are not a module setting. The module reads the global `excluded_ips` config key (IPs or CIDRs, one per line), which is edited in Admin > Site Configuration, on every request. Requests from a matching IP are silently dropped before any data is written — useful for internal pen-testing and monitoring traffic.

The visitor-hash salt rotates every 24 hours by default (`salt_rotation_hours` in `page_analytics_settings`); there is no admin setting for it.

If the stored settings cannot be read at startup (a database error), the module logs an error and runs on fallback values with tracking off. It does not act on those values: expiry cleanup is skipped (logged as a failed run in the Event Log; a manual trigger reports it), salt saves are skipped, and the dashboard shows a warning and disables saving; a form rendered from the fallback is refused even after the settings recover. Aggregation and the 7-day raw-data cleanup keep running. The hourly aggregation job and the dashboard retry the load at most once a minute, the weekly expiry cleanup retries it at once, and tracking follows the stored settings again once it succeeds; saving stays disabled until then.

### Self-referral filtering

If `site_url` is set in Admin > Site Configuration, the module strips referrers whose domain matches the site domain (case-insensitive, with or without `www.`). Migration 9 runs once on first install of v1.0.1+ (when `site_url` is already set): it blanks `referrer_domain` on matching rows in `page_analytics_views` and deletes matching rows from `page_analytics_referrers`.

## Template Functions

| Function | Returns | Use |
|----------|---------|-----|
| `analyticsPostStats(slug)` | `PageStats{Views, Reads}` | Render per-post counters |
| `analyticsShowPostStats()` | `bool` | Guard for the block above |
| `analyticsIntReadTracker(nonce)` | `template.HTML` | Inline `<script>` for read tracking (pass the CSP nonce) |

The core default and developer themes (`internal/themes/`) and the starter theme (`custom/themes/starter/`) display view/read counts on post pages via these helpers.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `OCMS_GEOIP_DB_PATH` | Path to `GeoLite2-Country.mmdb`; enables country detection |

No other env vars are module-specific.

## Database Tables

Managed via module migrations (11 versions):

| Table | Purpose |
|-------|---------|
| `page_analytics_views` | Raw page view events |
| `page_analytics_reads` | Raw read events (unique per `session_hash + path`) |
| `page_analytics_hourly` | Hourly aggregate: views + unique visitors per path |
| `page_analytics_daily` | Daily aggregate: views, unique visitors, bounces |
| `page_analytics_referrers` | Daily referrer-domain aggregate |
| `page_analytics_tech` | Daily browser/OS/device aggregate |
| `page_analytics_geo` | Daily per-country aggregate |
| `page_analytics_settings` | Single-row settings table |

## Privacy Notes

- No cookies, no local storage.
- Visitor and session identifiers are hashes that change with a rotating salt (every 24 hours by default) and with the date.
- IP addresses are never persisted — only derived hashes.
- Raw page views are deleted after 7 days and aggregates after the retention window. Raw read events (`page_analytics_reads`) are not pruned.

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/analytics_int/...
```

Covers the aggregator, tracking middleware, self-referral filtering, and IP exclusion logic.
