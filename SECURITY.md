# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.2.x   | :white_check_mark: |
| < 0.2   | :x:                |

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

### Request Security
- **CSRF Protection**: Fetch Metadata headers with Origin/Referer fallback
- **SQL Injection Prevention**: 100% SQLC-generated parameterized queries
- **XSS Prevention**: Go html/template auto-escaping + bluemonday sanitization

### HTTP Security Headers
- Content-Security-Policy (CSP)
- Strict-Transport-Security (HSTS)
- X-Frame-Options: SAMEORIGIN
- X-Content-Type-Options: nosniff
- Referrer-Policy: strict-origin-when-cross-origin

### File Upload Security
- Size limits (20MB)
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
