# Internal Analytics Module

Server-side analytics for oCMS. Tracks page views, unique visitors, referrers, browser/device stats, and geographic data with privacy-focused anonymization.

## Features

- **Page view tracking** with session detection and bounce rate
- **Privacy-first**: IP anonymization, rotating visitor hashes, no raw IP storage
- **GeoIP country detection** (optional, via MaxMind GeoLite2)
- **Background aggregation** into hourly/daily statistics
- **IP exclusion** for filtering internal/test traffic
- **Self-referral filtering** using the configured site URL
- **Configurable retention** with automatic cleanup

## Settings

Configured via Admin > Internal Analytics > Settings.

| Setting | Description |
|---------|-------------|
| **Enabled** | Toggle analytics tracking on/off |
| **Retention days** | How long to keep analytics data (default: 365) |
| **Excluded paths** | URL prefixes to skip (one per line) |
| **Excluded IPs** | IPs or CIDRs to exclude from tracking (one per line) |

### IP Exclusion

Add IPs or CIDR ranges to exclude from tracking. Useful for filtering pen testing traffic, office IPs, or monitoring services.

```
# Exact IPs
203.0.113.50
198.51.100.178

# CIDR ranges
10.0.0.0/8
192.168.0.0/16
```

Traffic from excluded IPs is silently dropped before any data is recorded.

### Self-Referral Filtering

The module automatically strips self-referrals using the site URL configured in Admin > Site Configuration (`site_url` key). When a page view's referrer domain matches the site domain (case-insensitive, with/without `www.` prefix), the referrer is cleared so it doesn't appear in referrer reports.

For example, if `site_url` is `https://www.it-digest.info`, referrers from `it-digest.info`, `www.it-digest.info`, `IT-DIGEST.INFO`, etc. are all filtered.

### Purge Self-Referral Data

A one-time purge action is available in the settings to clean up existing self-referral data from the database. This removes matching entries from both raw views and aggregated referrer statistics. Requires `site_url` to be configured.

## Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/internal-analytics` | Dashboard |
| GET | `/admin/internal-analytics/api/stats` | JSON stats (HTMX) |
| GET | `/admin/internal-analytics/api/realtime` | Real-time visitor count |
| POST | `/admin/internal-analytics/settings` | Save settings |
| POST | `/admin/internal-analytics/aggregate` | Trigger aggregation |
| POST | `/admin/internal-analytics/purge-self-referrals` | Purge self-referral data |

## Database Tables

- `page_analytics_views` - Raw page view events
- `page_analytics_hourly` - Hourly aggregates
- `page_analytics_daily` - Daily aggregates
- `page_analytics_referrers` - Daily referrer stats
- `page_analytics_tech` - Browser/OS/device stats
- `page_analytics_geo` - Geographic stats
- `page_analytics_settings` - Module configuration

## Environment Variables

| Variable | Description |
|----------|-------------|
| `OCMS_GEOIP_DB_PATH` | Path to GeoLite2-Country.mmdb for country detection |
