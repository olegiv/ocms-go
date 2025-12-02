# Reverse Proxy Configuration

This guide covers configuring oCMS behind popular reverse proxy servers. Running oCMS behind a reverse proxy enables SSL termination, load balancing, and improved security.

## Prerequisites

Before configuring a reverse proxy:

1. oCMS running on your server (default: `localhost:8080`)
2. Domain name pointing to your server
3. SSL certificate (Let's Encrypt recommended)

## Environment Configuration

When running behind a reverse proxy, configure oCMS:

```bash
# Server binds to localhost only (proxy handles external traffic)
export OCMS_SERVER_HOST=127.0.0.1
export OCMS_SERVER_PORT=8080
export OCMS_ENV=production
```

---

## Nginx

Nginx is a high-performance web server commonly used as a reverse proxy.

### Basic Configuration

Create `/etc/nginx/sites-available/ocms`:

```nginx
server {
    listen 80;
    server_name example.com www.example.com;

    # Redirect HTTP to HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name example.com www.example.com;

    # SSL Configuration
    ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;
    ssl_session_timeout 1d;
    ssl_session_cache shared:SSL:50m;
    ssl_session_tickets off;

    # Modern SSL configuration
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;

    # HSTS (optional, uncomment if you're sure)
    # add_header Strict-Transport-Security "max-age=63072000" always;

    # Client body size for file uploads
    client_max_body_size 100M;

    # Proxy headers
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # Timeouts
    proxy_connect_timeout 60s;
    proxy_send_timeout 60s;
    proxy_read_timeout 60s;

    # Main application
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    # Static assets caching
    location /static/ {
        proxy_pass http://127.0.0.1:8080;
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    # Theme static assets
    location /themes/ {
        proxy_pass http://127.0.0.1:8080;
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    # Uploads with longer cache
    location /uploads/ {
        proxy_pass http://127.0.0.1:8080;
        expires 30d;
        add_header Cache-Control "public";
    }

    # Health check (no logging)
    location /health {
        proxy_pass http://127.0.0.1:8080;
        access_log off;
    }

    # Deny access to sensitive files
    location ~ /\. {
        deny all;
    }
}
```

### Enable the Site

```bash
# Create symlink
sudo ln -s /etc/nginx/sites-available/ocms /etc/nginx/sites-enabled/

# Test configuration
sudo nginx -t

# Reload Nginx
sudo systemctl reload nginx
```

### With Load Balancing (Multiple Instances)

For high availability with multiple oCMS instances:

```nginx
upstream ocms_backend {
    least_conn;
    server 127.0.0.1:8080 weight=1;
    server 127.0.0.1:8081 weight=1;
    server 127.0.0.1:8082 weight=1 backup;

    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name example.com;

    # ... SSL configuration same as above ...

    location / {
        proxy_pass http://ocms_backend;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

**Note**: When running multiple instances, configure Redis for distributed caching:

```bash
export OCMS_REDIS_URL=redis://localhost:6379/0
```

---

## Apache

Apache HTTP Server with mod_proxy for reverse proxying.

### Enable Required Modules

```bash
sudo a2enmod proxy
sudo a2enmod proxy_http
sudo a2enmod ssl
sudo a2enmod headers
sudo a2enmod rewrite
```

### Basic Configuration

Create `/etc/apache2/sites-available/ocms.conf`:

```apache
<VirtualHost *:80>
    ServerName example.com
    ServerAlias www.example.com

    # Redirect to HTTPS
    RewriteEngine On
    RewriteCond %{HTTPS} off
    RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [L,R=301]
</VirtualHost>

<VirtualHost *:443>
    ServerName example.com
    ServerAlias www.example.com

    # SSL Configuration
    SSLEngine on
    SSLCertificateFile /etc/letsencrypt/live/example.com/fullchain.pem
    SSLCertificateKeyFile /etc/letsencrypt/live/example.com/privkey.pem

    # Modern SSL settings
    SSLProtocol all -SSLv3 -TLSv1 -TLSv1.1
    SSLCipherSuite ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384
    SSLHonorCipherOrder off
    SSLSessionTickets off

    # Proxy settings
    ProxyPreserveHost On
    ProxyRequests Off

    # Pass real IP to backend
    RequestHeader set X-Real-IP "%{REMOTE_ADDR}s"
    RequestHeader set X-Forwarded-Proto "https"

    # Main application proxy
    ProxyPass / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/

    # Timeouts
    ProxyTimeout 60

    # Static asset caching
    <LocationMatch "^/(static|themes)/">
        Header set Cache-Control "public, max-age=31536000, immutable"
    </LocationMatch>

    <Location /uploads/>
        Header set Cache-Control "public, max-age=2592000"
    </Location>

    # Health check (no logging)
    <Location /health>
        SetEnv nolog
    </Location>

    # Logging
    ErrorLog ${APACHE_LOG_DIR}/ocms_error.log
    CustomLog ${APACHE_LOG_DIR}/ocms_access.log combined env=!nolog
</VirtualHost>
```

### Enable the Site

```bash
# Enable site
sudo a2ensite ocms.conf

# Disable default site (optional)
sudo a2dissite 000-default.conf

# Test configuration
sudo apache2ctl configtest

# Restart Apache
sudo systemctl restart apache2
```

### With Load Balancing

For multiple backend servers:

```apache
<Proxy "balancer://ocms_cluster">
    BalancerMember http://127.0.0.1:8080
    BalancerMember http://127.0.0.1:8081
    BalancerMember http://127.0.0.1:8082 status=+H

    ProxySet lbmethod=bybusyness
</Proxy>

<VirtualHost *:443>
    ServerName example.com

    # ... SSL configuration same as above ...

    ProxyPreserveHost On
    ProxyPass / balancer://ocms_cluster/
    ProxyPassReverse / balancer://ocms_cluster/
</VirtualHost>
```

Enable load balancing modules:

```bash
sudo a2enmod proxy_balancer
sudo a2enmod lbmethod_bybusyness
```

---

## Nginx Proxy Manager

Nginx Proxy Manager provides a web-based GUI for managing Nginx reverse proxies.

### Docker Installation

```yaml
# docker-compose.yml
version: '3.8'
services:
  nginx-proxy-manager:
    image: 'jc21/nginx-proxy-manager:latest'
    restart: unless-stopped
    ports:
      - '80:80'
      - '443:443'
      - '81:81'  # Admin panel
    volumes:
      - ./data:/data
      - ./letsencrypt:/etc/letsencrypt
```

```bash
docker-compose up -d
```

Default credentials: `admin@example.com` / `changeme`

### Configuration Steps

1. **Access Admin Panel**
   - Navigate to `http://your-server:81`
   - Log in with default credentials
   - Change password when prompted

2. **Add Proxy Host**
   - Click **Hosts > Proxy Hosts > Add Proxy Host**

3. **Details Tab**
   | Field | Value |
   |-------|-------|
   | Domain Names | `example.com`, `www.example.com` |
   | Scheme | `http` |
   | Forward Hostname/IP | `127.0.0.1` (or container name if using Docker) |
   | Forward Port | `8080` |
   | Block Common Exploits | Enabled |
   | Websockets Support | Enabled |

4. **SSL Tab**
   | Field | Value |
   |-------|-------|
   | SSL Certificate | Request a new SSL Certificate |
   | Force SSL | Enabled |
   | HTTP/2 Support | Enabled |
   | HSTS Enabled | Enabled (optional) |

5. **Advanced Tab** (Custom Nginx Configuration)

   Add the following for optimal performance:

   ```nginx
   # Client body size for file uploads
   client_max_body_size 100M;

   # Static asset caching
   location /static/ {
       proxy_pass http://127.0.0.1:8080;
       expires 1y;
       add_header Cache-Control "public, immutable";
   }

   location /themes/ {
       proxy_pass http://127.0.0.1:8080;
       expires 1y;
       add_header Cache-Control "public, immutable";
   }

   location /uploads/ {
       proxy_pass http://127.0.0.1:8080;
       expires 30d;
       add_header Cache-Control "public";
   }

   # Health check without logging
   location /health {
       proxy_pass http://127.0.0.1:8080;
       access_log off;
   }
   ```

6. Click **Save**

### Docker Network Configuration

If running oCMS in Docker alongside Nginx Proxy Manager:

```yaml
# docker-compose.yml
version: '3.8'

services:
  ocms:
    build: .
    environment:
      - OCMS_SESSION_SECRET=your-secret-key-at-least-32-bytes
      - OCMS_SERVER_HOST=0.0.0.0
      - OCMS_ENV=production
    volumes:
      - ./data:/app/data
      - ./uploads:/app/uploads
    networks:
      - proxy_network

  nginx-proxy-manager:
    image: 'jc21/nginx-proxy-manager:latest'
    ports:
      - '80:80'
      - '443:443'
      - '81:81'
    volumes:
      - ./npm-data:/data
      - ./letsencrypt:/etc/letsencrypt
    networks:
      - proxy_network

networks:
  proxy_network:
    driver: bridge
```

In Nginx Proxy Manager, use `ocms` as the Forward Hostname (container name).

---

## SSL Certificates with Let's Encrypt

### Certbot for Nginx

```bash
# Install Certbot
sudo apt install certbot python3-certbot-nginx

# Obtain certificate
sudo certbot --nginx -d example.com -d www.example.com

# Auto-renewal (usually configured automatically)
sudo systemctl enable certbot.timer
```

### Certbot for Apache

```bash
# Install Certbot
sudo apt install certbot python3-certbot-apache

# Obtain certificate
sudo certbot --apache -d example.com -d www.example.com
```

---

## Security Headers

Add these security headers to your reverse proxy for improved security:

### Nginx

```nginx
# Security headers
add_header X-Frame-Options "SAMEORIGIN" always;
add_header X-Content-Type-Options "nosniff" always;
add_header X-XSS-Protection "1; mode=block" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;

# Content Security Policy (adjust as needed)
add_header Content-Security-Policy "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self';" always;
```

### Apache

```apache
Header always set X-Frame-Options "SAMEORIGIN"
Header always set X-Content-Type-Options "nosniff"
Header always set X-XSS-Protection "1; mode=block"
Header always set Referrer-Policy "strict-origin-when-cross-origin"
Header always set Permissions-Policy "camera=(), microphone=(), geolocation=()"
```

---

## Troubleshooting

### Common Issues

**502 Bad Gateway**
- oCMS is not running or crashed
- Wrong backend port configured
- Check: `curl http://127.0.0.1:8080/health`

**504 Gateway Timeout**
- oCMS taking too long to respond
- Increase proxy timeout values
- Check server resources (CPU, memory)

**413 Request Entity Too Large**
- File upload exceeds `client_max_body_size`
- Increase the limit in proxy configuration

**Mixed Content Warnings**
- `X-Forwarded-Proto` header not set
- Ensure proxy passes `https` scheme

### Verify Proxy Headers

Check that oCMS receives correct headers:

```bash
# Check logs or add debug endpoint
curl -v https://example.com/health
```

The `X-Real-IP` and `X-Forwarded-For` headers should contain the client's real IP.

### Health Check Monitoring

Set up monitoring with the `/health` endpoint:

```bash
# Simple health check
curl -sf http://127.0.0.1:8080/health || echo "oCMS is down!"

# With timeout
curl -sf --max-time 5 https://example.com/health
```

---

## Performance Tips

1. **Enable HTTP/2**: Modern browsers benefit from HTTP/2 multiplexing
2. **Gzip Compression**: oCMS handles compression; avoid double-compression at proxy
3. **Connection Keepalive**: Reduces connection overhead for repeated requests
4. **Static Asset Caching**: Long cache times for versioned assets
5. **Health Check Frequency**: Don't check too frequently (every 30s is usually sufficient)

## Next Steps

- [Webhooks Configuration](webhooks.md) - Set up event notifications
- [Multi-Language Setup](multi-language.md) - Configure multiple languages
- [Import/Export Guide](import-export.md) - Backup and migrate content
