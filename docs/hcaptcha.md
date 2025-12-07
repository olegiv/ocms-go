# hCaptcha Integration

OCMS includes an hCaptcha module to protect the login form from automated attacks and bots. This document describes how to configure and use hCaptcha protection.

## Overview

[hCaptcha](https://www.hcaptcha.com/) is a privacy-focused CAPTCHA service that helps protect your login form from:

- Automated brute force attacks
- Credential stuffing
- Bot registrations
- Spam submissions

The hCaptcha module integrates seamlessly with the login form and can be configured entirely through the admin interface or environment variables.

## Getting Started

### 1. Create an hCaptcha Account

1. Visit [hCaptcha Dashboard](https://dashboard.hcaptcha.com/)
2. Create a free account
3. Add your site and obtain:
   - **Site Key** (public key for the widget)
   - **Secret Key** (private key for verification)

### 2. Configure hCaptcha

There are two ways to configure hCaptcha:

#### Option A: Admin Interface

1. Log in to your OCMS admin panel
2. Navigate to **Modules > hCaptcha**
3. Enter your Site Key and Secret Key
4. Choose your preferred theme and size
5. Enable hCaptcha
6. Click Save

#### Option B: Environment Variables

Set the following environment variables:

```bash
OCMS_HCAPTCHA_SITE_KEY=your-site-key
OCMS_HCAPTCHA_SECRET_KEY=your-secret-key
```

Then enable hCaptcha in the admin interface or database:

```bash
sqlite3 ./data/ocms.db "UPDATE hcaptcha_settings SET enabled = 1"
```

Note: Environment variables take precedence over database settings for the keys.

## Configuration Options

| Setting | Options | Description |
|---------|---------|-------------|
| Enabled | On/Off | Enable or disable hCaptcha protection |
| Site Key | String | Your hCaptcha public site key |
| Secret Key | String | Your hCaptcha secret key (stored securely) |
| Theme | `light` / `dark` | Widget color theme |
| Size | `normal` / `compact` | Widget size |

## How It Works

1. When enabled, the hCaptcha widget appears on the login form
2. Users must complete the CAPTCHA challenge before submitting
3. The server verifies the response with hCaptcha's API
4. Login proceeds only if verification succeeds

### Verification Flow

```
User → Login Form → Complete CAPTCHA → Submit Form
                                           ↓
Server ← hCaptcha API ← Verify Token ← Extract Token
   ↓
Success: Continue login
Failure: Show error, stay on login page
```

## Testing

### Test Keys

hCaptcha provides test keys for development and testing:

| Key Type | Value |
|----------|-------|
| Site Key | `10000000-ffff-ffff-ffff-000000000001` |
| Secret Key | `0x0000000000000000000000000000000000000000` |

With test keys:
- The widget always passes automatically
- No actual challenge is shown
- Verification always succeeds

### Using Test Keys

```bash
OCMS_HCAPTCHA_SITE_KEY="10000000-ffff-ffff-ffff-000000000001" \
OCMS_HCAPTCHA_SECRET_KEY="0x0000000000000000000000000000000000000000" \
OCMS_SESSION_SECRET=your-secret-key \
./ocms
```

## Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| "Please complete the captcha" | User didn't complete the challenge | Complete the hCaptcha widget |
| "Captcha verification failed" | Network or API error | Check internet connection, try again |
| "Invalid captcha response" | Token rejected by hCaptcha | Complete the challenge again |

## Troubleshooting

### Locked Out After Enabling hCaptcha

If you enabled hCaptcha but can't complete the challenge:

```bash
# Disable hCaptcha via database
sqlite3 ./data/ocms.db "UPDATE hcaptcha_settings SET enabled = 0"

# Restart the application
```

### Widget Not Appearing

1. Check that hCaptcha is enabled in the database:
   ```bash
   sqlite3 ./data/ocms.db "SELECT * FROM hcaptcha_settings"
   ```

2. Verify both Site Key and Secret Key are set

3. Check browser console for JavaScript errors

4. Ensure the hCaptcha script can load (check firewall/ad blockers)

### Verification Always Fails

1. Verify your Secret Key is correct
2. Check server logs for specific error codes:
   - `missing-input-secret` - Secret key not provided
   - `invalid-input-secret` - Secret key is incorrect
   - `missing-input-response` - Token not submitted
   - `invalid-input-response` - Token is invalid or expired

3. Ensure your server can reach `https://api.hcaptcha.com/siteverify`

### Behind a Reverse Proxy

Ensure your proxy passes the real client IP:

```nginx
# Nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

The module checks headers in this order:
1. `X-Forwarded-For`
2. `X-Real-IP`
3. `RemoteAddr`

## Integration with Login Security

hCaptcha works alongside the existing login protection:

| Layer | Protection |
|-------|------------|
| 1. hCaptcha | Blocks bots before login attempt |
| 2. IP Rate Limiting | Limits requests per IP |
| 3. Account Lockout | Locks account after failed attempts |

For maximum security, enable all three layers.

## Privacy Considerations

hCaptcha is designed with privacy in mind:

- Does not sell personal data
- GDPR compliant
- Can operate without cookies
- Rewards website owners for privacy-preserving challenges

For more information, see [hCaptcha Privacy Policy](https://www.hcaptcha.com/privacy).

## Environment Variables Reference

| Variable | Required | Description |
|----------|----------|-------------|
| `OCMS_HCAPTCHA_SITE_KEY` | No | Override site key from env |
| `OCMS_HCAPTCHA_SECRET_KEY` | No | Override secret key from env |

## Admin Interface

Access hCaptcha settings at: **Admin > Modules > hCaptcha** (`/admin/hcaptcha`)

The settings page allows you to:
- View current configuration status
- Update Site Key and Secret Key
- Change theme (light/dark)
- Change size (normal/compact)
- Enable/disable protection

## Module Information

| Property | Value |
|----------|-------|
| Module Name | `hcaptcha` |
| Version | 1.0.0 |
| Admin URL | `/admin/hcaptcha` |
| Database Table | `hcaptcha_settings` |

## See Also

- [Login Security](login-security.md) - Rate limiting and account lockout
- [Reverse Proxy Configuration](reverse-proxy.md) - Proxy setup for correct IP detection
