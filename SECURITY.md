# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.14.x  | :white_check_mark: |
| 0.12.x  | :white_check_mark: |
| < 0.12  | :x:                |

## Reporting a Vulnerability

We take security vulnerabilities seriously. If you discover a security issue, please report it responsibly.

### Preferred Method: GitHub Security Advisories

1. Go to the repository's **Security** tab
2. Click **Report a vulnerability**
3. Provide a detailed description of the issue

This method ensures private communication and allows coordinated disclosure.

### Alternative: Email

If you cannot use GitHub Security Advisories, contact the maintainer directly via the email listed in the repository.

### What to Include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### Response Timeline

- **Acknowledgment**: Within 48-72 hours
- **Initial Assessment**: Within 1 week
- **Fix Timeline**: Depends on severity (critical issues prioritized)

## Security Features

oCMS implements defense-in-depth security measures:

### Authentication & Authorization
- **Password Hashing**: Argon2id with secure parameters
- **Session Management**: SCS with SQLite persistence, 24-hour lifetime
- **Role-Based Access Control**: Admin, Editor, and Public roles

### Login Protection
- **IP Rate Limiting**: Prevents brute-force attacks
- **Account Lockout**: Exponential backoff after failed attempts
- **hCaptcha Integration**: Optional bot protection

### API Security
- **API Key Authentication**: Bearer token with Argon2id hashing
- **Granular Permissions**: Fine-grained access control per key
- **Per-Key Rate Limiting**: Token bucket algorithm
- **Per-Key Source CIDR Allowlists**: Restrict keys to specific IP ranges
- **API Key Maximum Lifetime**: Configurable expiry enforcement (default 90 days in production)
- **Source IP Anomaly Detection**: Auto-revoke keys on unexpected source IP changes
- **Global API CIDR Policy**: Restrict all API access to trusted networks
- **Fail-Closed Forwarding**: Reject requests with malformed X-Forwarded-For chains

### Request Security
- **CSRF Protection**: Fetch Metadata headers with Origin/Referer fallback
- **SQL Injection Prevention**: 100% SQLC-generated parameterized queries
- **XSS Prevention**: Go html/template auto-escaping + bluemonday sanitization
- **CSP Nonce Support**: Per-request nonces across the render pipeline
- **Frame Ancestors**: CSP `frame-ancestors` directive to prevent clickjacking
- **Open Redirect Protection**: Validates redirect targets to prevent open redirects
- **Page HTML Sanitization**: Configurable sanitization before rendering (`OCMS_SANITIZE_PAGE_HTML`)
- **Suspicious Markup Blocking**: Reject page writes containing dangerous HTML patterns (`OCMS_BLOCK_SUSPICIOUS_PAGE_HTML`)

### HTTP Security Headers
- Content-Security-Policy (CSP)
- Strict-Transport-Security (HSTS)
- X-Frame-Options: SAMEORIGIN
- X-Content-Type-Options: nosniff
- Referrer-Policy: strict-origin-when-cross-origin

### Webhook Security
- **HMAC-SHA256 Signatures**: Verify webhook payload integrity
- **Destination Host Allowlisting**: Restrict webhook targets (`OCMS_WEBHOOK_ALLOWED_HOSTS`)
- **HTTPS Enforcement**: Require HTTPS for outbound integration URLs (`OCMS_REQUIRE_HTTPS_OUTBOUND`)
- **Form Data Minimization**: Control form data exposure in webhook payloads (`OCMS_WEBHOOK_FORM_DATA_MODE`)

### Embed Proxy Security
- **Browser Origin Allowlisting**: Restrict which origins can use the embed proxy (`OCMS_EMBED_ALLOWED_ORIGINS`)
- **Upstream Host Allowlisting**: Restrict which API hosts the proxy can reach (`OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS`)
- **Signed Proxy Tokens**: Short-lived tokens for embed proxy requests (`OCMS_EMBED_PROXY_TOKEN`)

### Trusted Proxy Hardening
- **Trusted Proxy Configuration**: `OCMS_TRUSTED_PROXIES` for correct client IP detection
- **No Chi RealIP**: Replaced chi's `RealIP` middleware (which blindly trusts `True-Client-IP`, `X-Real-IP`, and `X-Forwarded-For` from any source) with a trusted-proxy-aware middleware that only reads forwarding headers from configured proxies
- **Fail-Closed Forwarding**: Reject malformed forwarding headers from untrusted sources
- **Production Requirement**: Configurable startup failure without trusted proxy configuration

### Content Security
- **Page HTML Sanitization**: Sanitize page content before rendering to visitors
- **Suspicious Markup Blocking**: Reject page writes with dangerous patterns (script injection, event handlers)
- **Form Captcha Requirement**: Require hCaptcha on public form submissions in production (`OCMS_REQUIRE_FORM_CAPTCHA`)

### File Upload Security
- Size limits (20MB, 2MB in demo mode)
- MIME type validation
- Magic number checking
- UUID-based storage

## Security Documentation

For detailed information, see:

- [Login Security](docs/login-security.md) - Rate limiting and account lockout
- [CSRF Protection](docs/csrf.md) - CSRF configuration and trusted origins
- [hCaptcha Integration](docs/hcaptcha.md) - Bot protection setup

## Security Updates

Security patches are announced via:

- **GitHub Releases**: Tagged releases with security notes
- **CHANGELOG.md**: Detailed change history

Subscribe to repository releases to receive security update notifications.

## Dependency Security

We regularly scan dependencies for vulnerabilities:

```bash
# Go dependencies
govulncheck ./...

# JavaScript dependencies
npm audit
```
