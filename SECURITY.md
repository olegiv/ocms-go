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
- **Confirmation for Sensitive Changes**: The acting administrator's current password is required for changes that could hand an account over (a new password or email for any user, giving or taking away the admin role, resetting another user's second factor), for theme imports and for switching on the DB Manager module; wrong attempts count toward the login lockout, and a content import that overwrites an existing user never grants or removes the admin role (users it creates take the archive's role, with a random password nobody knows). Once DB Manager is active, its raw SQL needs no password, so switch it off when done
- **Two-Factor Authentication**: Authenticator apps (TOTP) with single-use recovery codes; required for every account in production (`OCMS_REQUIRE_MFA`), with secrets sealed under `OCMS_MFA_KEY`. A session is signed in only after the second factor, codes cannot be replayed, adding a second factor outside sign-in needs the password and changing it a current code, and `/health` and Sentinel treat a stale session as signed out

### Login Protection
- **IP Rate Limiting**: Prevents brute-force attacks
- **Account Lockout**: Exponential backoff after failed attempts
- **hCaptcha Integration**: Optional bot protection

### API Security
- **API Key Authentication**: Bearer token with Argon2id hashing
- **Granular Permissions**: Fine-grained access control per key
- **Rate Limiting**: Token bucket per client IP on every API request, plus per-key limits on write requests
- **Per-Key Source CIDR Allowlists**: Restrict keys to specific IP ranges
- **API Key Maximum Lifetime**: Configurable expiry enforcement (default 90 days in production)
- **Source IP Anomaly Detection**: Auto-revoke keys on unexpected source IP changes
- **Global API CIDR Policy**: Restrict all API access to trusted networks
- **Fail-Closed Forwarding**: Unparseable X-Forwarded-For entries are skipped, and the connecting peer's address is used when no entry parses

### MCP Server Security
- **Opt-in Endpoint**: The MCP Server module starts inactive; `/api/mcp` returns 404 until an admin enables it
- **Explicit Permission**: API keys need `mcp:access`; REST integration keys never become usable by AI agents implicitly
- **Shared API Key Policies**: The same authentication middleware as REST v2 (CIDR allowlists, expiry, maximum lifetime, IP-change revocation, Argon2 verification)
- **Read-Only Tools**: No tool writes data; a drift test fails if a tool without the read-only annotation is registered
- **Draft Confidentiality**: Drafts are exposed only when an admin enables it and the key holds `pages:read`; hidden drafts answer "not found" (tag and category page counts still include them, as in REST v2)
- **No Personal Data**: Author email addresses, users, form submissions, settings and keys are never returned
- **Origin Validation**: Cross-origin browser requests are rejected (`Sec-Fetch-Site` / `Origin`), countering DNS rebinding
- **Resource Limits**: 256 KiB request body, 32 concurrent requests, per-IP and per-key rate limits, JSON-RPC batches rejected (one call per request), 25 s per tool call
- **Cancellation**: Client disconnects and timeouts cancel a tool's database work on every protocol version
- **Panic Isolation**: Panics in the endpoint, the protocol layer and tools become internal errors instead of crashing the process
- **Prompt-Injection Guidance**: Server instructions tell agents that returned content is website data, not instructions
- **Private Responses**: `Cache-Control: no-store` and MCP `cacheScope: private`; internal error details are never sent to agents
- **Audit Logging**: One structured log line per dispatched tool call (key id and prefix, client, outcome; never keys or arguments; the cause of an internal failure is logged as-is and can quote returned data); settings changes go to the event log

### Request Security
- **CSRF Protection**: Fetch Metadata headers with Origin/Referer fallback
- **SQL Injection Prevention**: SQLC-generated parameterized queries for core data access
- **XSS Prevention**: Go html/template auto-escaping + bluemonday sanitization
- **CSP Nonce Support**: Per-request nonces across the render pipeline
- **Frame Ancestors**: CSP `frame-ancestors` directive to prevent clickjacking
- **Open Redirect Protection**: Validates redirect targets to prevent open redirects
- **Page HTML Sanitization**: Configurable sanitization before rendering (`OCMS_SANITIZE_PAGE_HTML`)
- **Suspicious Markup Blocking**: Reject page writes containing dangerous HTML patterns (`OCMS_BLOCK_SUSPICIOUS_PAGE_HTML`)

### HTTP Security Headers
- Content-Security-Policy (CSP), first-party for scripts, styles, connections
  and frames apart from Google Fonts and YouTube frames; a module adds the
  third-party hosts it loads only while it is active
- Strict-Transport-Security (HSTS), whenever `OCMS_ENV` is not `development`; `OCMS_HSTS_PRELOAD=true` adds `preload`
- X-Frame-Options: SAMEORIGIN
- X-Content-Type-Options: nosniff
- X-XSS-Protection: 0 (legacy XSS auditor off; CSP is the protection)
- Referrer-Policy: strict-origin-when-cross-origin
- Cross-Origin-Opener-Policy: same-origin
- Cross-Origin-Resource-Policy: same-origin, and cross-origin for `/uploads/`,
  `/static/` and `/themes/` so feed readers and other sites can show media
- CSP `report-uri /csp-report`: browsers send violation reports there, and
  they are logged with bounded fields and a per-IP rate limit

The headers are set before any middleware that can answer on its own, so bans,
timeouts and redirects carry them too.

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
- **Fail-Closed Forwarding**: Forwarding headers from untrusted sources are ignored, and an unusable X-Forwarded-For chain falls back to the peer address
- **Production Requirement**: Configurable startup failure without trusted proxy configuration

### Content Security
- **Page HTML Sanitization**: Sanitize page content before rendering to visitors
- **Suspicious Markup Blocking**: Reject page writes with dangerous patterns (script injection, event handlers)
- **Form Captcha Requirement**: Require hCaptcha on public form submissions in production (`OCMS_REQUIRE_FORM_CAPTCHA`)

### File Upload Security
- Size limit (20MB per file; the admin UI disables media uploads in demo mode; the REST API does not check demo mode)
- MIME type validation
- Magic number checking
- UUID-based storage

### Migrator Filesystem Security
- **Trusted source roots**: `OCMS_MIGRATOR_ALLOWED_FILE_ROOTS` limits local-media reads to configured, existing absolute directories; `DRUPAL_FILES` and `ELEFANT_FILES` are trusted roots for their sources. Safe root symlinks are canonicalized, and roots resolving to the filesystem root are rejected
- **Fail-closed media import**: Without a trusted root, database/content migration remains available but local-media import is refused
- **Root-relative access**: Lexical and symlink containment are verified before scanning, and files are opened through `os.Root` with relative paths
- **Durable cleanup**: Failed UUID directory cleanup remains queued across restarts instead of being reported as complete

## Security Documentation

For detailed information, see:

- [Login Security](docs/login-security.md) - Rate limiting, account lockout and two-factor authentication
- [CSRF Protection](docs/csrf.md) - CSRF configuration and trusted origins
- [hCaptcha Integration](docs/hcaptcha.md) - Bot protection setup
- [MCP Server](docs/mcp-module.md) - AI agent access, visibility rules, limits and threat model

## Security Updates

Security patches are announced via:

- **GitHub Releases**: Tagged releases with security notes; archives published after v0.28.0 carry signed build provenance (`gh attestation verify <archive> --repo olegiv/ocms-go`)
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
