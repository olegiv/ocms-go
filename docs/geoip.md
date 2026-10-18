# GeoIP Country Detection

OCMS Internal Analytics module supports GeoIP-based country detection using the MaxMind GeoLite2-Country database. The Sentinel module reads the same database to record the country of banned IPs.

## Overview

When enabled, the analytics module detects and records the country of each visitor based on their IP address. This data appears in:

- Country breakdown statistics on the analytics dashboard
- Geographic distribution charts

## Setup

### 1. Download the GeoLite2-Country Database

MaxMind provides a free GeoLite2-Country database:

1. Create a free account at [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data)
2. Download the GeoLite2-Country database in MMDB format
3. Extract `GeoLite2-Country.mmdb` to a location on your server

### 2. Configure the Database Path

Set the `OCMS_GEOIP_DB_PATH` environment variable:

```bash
export OCMS_GEOIP_DB_PATH=/path/to/GeoLite2-Country.mmdb
```

Or in your systemd service file:

```ini
[Service]
Environment="OCMS_GEOIP_DB_PATH=/opt/geoip/GeoLite2-Country.mmdb"
```

### 3. Restart OCMS

The database is loaded at startup for optimal performance.

## Database Updates

MaxMind updates the GeoLite2 databases weekly (Tuesdays). To update:

1. Download the new database from MaxMind
2. Replace the existing `.mmdb` file
3. Internal Analytics automatically detects file changes and reloads the database within an hour (the hourly `geoip_reload` job listed at `/admin/scheduler`, scheduled only if the database loaded at startup)

Alternatively, restart OCMS to immediately load the new database. The Sentinel module loads the database only at startup, so it needs a restart to pick up a replaced file.

## Graceful Degradation

If the GeoIP database is not configured or cannot be loaded:

- The analytics module continues to function normally
- Country detection is silently disabled
- The "Countries" section shows "Unknown" for public addresses (private and loopback addresses still show as "Local Network")
- A startup message is logged: info when `OCMS_GEOIP_DB_PATH` is unset, warning when the file cannot be loaded

## Performance

- Database is loaded at startup (memory-mapped) and reloaded when the file changes
- Lookups are thread-safe and extremely fast (<1ms)
- Memory usage depends on database size (~5MB for GeoLite2-Country)

## Troubleshooting

### Country Data Not Appearing

1. Check if `OCMS_GEOIP_DB_PATH` is set:
   ```bash
   echo $OCMS_GEOIP_DB_PATH
   ```

2. Verify the file exists and is readable:
   ```bash
   ls -la /path/to/GeoLite2-Country.mmdb
   ```

3. Check startup logs for GeoIP-related messages:
   ```
   INFO GeoIP database loaded path=/path/to/GeoLite2-Country.mmdb
   ```
   or
   ```
   INFO GeoIP not configured, country detection disabled. Set OCMS_GEOIP_DB_PATH to enable.
   ```

### All Visitors Showing as "Unknown"

- Ensure you're using the **Country** database, not City or ASN
- Check that visitors are not all from local/private IP ranges
- Verify the database file is not corrupted

### All Visitors Showing as "Local Network"

- This means all traffic is coming from private or loopback IP ranges (10.x.x.x, 192.168.x.x, 172.16.x.x to 172.31.x.x, 127.x.x.x)
- If behind a reverse proxy, ensure it sends X-Forwarded-For or X-Real-IP and that its address is listed in `OCMS_TRUSTED_PROXIES`; forwarding headers are ignored from any other peer
- See [Reverse Proxy documentation](reverse-proxy.md) for header configuration

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `OCMS_GEOIP_DB_PATH` | No | Path to GeoLite2-Country.mmdb file |

## See Also

- [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) - Database downloads
- [Reverse Proxy](reverse-proxy.md) - Nginx/Apache header configuration
