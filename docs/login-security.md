# Login Security

OCMS includes built-in protection against brute force attacks and DDoS on the login form. This document describes the security measures and how to configure them.

## Overview

The login protection system provides two layers of defense:

1. **IP-based Rate Limiting** - Limits the number of login attempts per IP address
2. **Account Lockout** - Temporarily locks accounts after multiple failed login attempts

## IP-based Rate Limiting

Rate limiting prevents rapid-fire login attempts from a single IP address.

### Default Configuration

| Setting | Value | Description |
|---------|-------|-------------|
| Rate | 0.5 req/s | One request per 2 seconds (sustained) |
| Burst | 5 | Initial burst allowance before rate limiting kicks in |

### How It Works

1. Each IP address has a token bucket with 5 tokens
2. Tokens regenerate at 0.5 per second
3. Each login attempt consumes 1 token
4. When tokens are exhausted, requests receive HTTP 429 (Too Many Requests)

### Response When Rate Limited

```
HTTP/1.1 429 Too Many Requests
Content-Type: text/plain

Too many requests. Please try again later.
```

## Account Lockout

Account lockout prevents password guessing attacks by temporarily blocking login attempts after multiple failures.

### Default Configuration

| Setting | Value | Description |
|---------|-------|-------------|
| Max Failed Attempts | 5 | Number of failures before lockout |
| Attempt Window | 15 minutes | Time window for counting failures |
| Base Lockout Duration | 15 minutes | Initial lockout period |
| Max Lockout Duration | 24 hours | Maximum lockout after repeated lockouts |

### How It Works

1. Failed login attempts are tracked per email address
2. After 5 failed attempts within 15 minutes, the account is locked
3. Lockout duration doubles with each subsequent lockout (exponential backoff):
   - 1st lockout: 15 minutes
   - 2nd lockout: 30 minutes
   - 3rd lockout: 1 hour
   - 4th lockout: 2 hours
   - ... up to 24 hours maximum

### User Experience

| Attempt | Message |
|---------|---------|
| 1-2 | "Invalid email or password" |
| 3-4 | "Invalid email or password. N attempts remaining." |
| 5 | "Too many failed attempts. Account locked for 15 minutes." |
| During lockout | "Account is temporarily locked. Try again in N minutes." |

### Security Features

- **Non-existent email protection**: Failed attempts are tracked even for non-existent email addresses to prevent user enumeration attacks
- **Successful login reset**: A successful login clears the failed attempt counter
- **Automatic cleanup**: Stale tracking entries are automatically removed

## Event Logging

All login-related events are logged to the events system:

| Event | Level | Description |
|-------|-------|-------------|
| Login failed: user not found | Warning | Attempt with non-existent email |
| Login failed: invalid password | Warning | Wrong password for existing user |
| Login attempt on locked account | Warning | Attempt during lockout period |
| Account locked due to failed attempts | Warning | Account just got locked |
| User logged in | Info | Successful login |

View these events in the admin panel under **Admin > Events**.

## Configuration

Login protection settings are currently defined in code. To customize:

```go
// In cmd/ocms/main.go
loginProtection := middleware.NewLoginProtection(middleware.LoginProtectionConfig{
    IPRateLimit:       0.5,              // Requests per second per IP
    IPBurst:           5,                // Burst allowance
    MaxFailedAttempts: 5,                // Lock after N failures
    LockoutDuration:   15 * time.Minute, // Base lockout time
    AttemptWindow:     15 * time.Minute, // Window for counting failures
})
```

## Behind a Reverse Proxy

When running behind a reverse proxy (nginx, Apache, etc.), ensure the proxy is configured to pass the real client IP:

### Nginx

```nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

### Apache

```apache
RemoteIPHeader X-Forwarded-For
```

The login protection middleware checks headers in this order:
1. `X-Real-IP`
2. `X-Forwarded-For`
3. `RemoteAddr` (direct connection)

## Best Practices

1. **Use strong passwords**: Enforce minimum password requirements
2. **Monitor events**: Regularly check the events log for suspicious activity
3. **Use HTTPS**: Always run behind HTTPS in production
4. **Consider additional measures**: For high-security deployments, consider:
   - CAPTCHA after N failed attempts
   - Two-factor authentication
   - IP allowlisting for admin access

## Troubleshooting

### Legitimate user locked out

If a legitimate user is locked out:

1. Wait for the lockout period to expire, OR
2. Restart the application (clears in-memory lockout data)

Note: In a future version, admin users will be able to unlock accounts from the admin panel.

### Rate limiting too aggressive

If legitimate users are being rate limited:

1. Increase `IPBurst` to allow more initial requests
2. Increase `IPRateLimit` for faster token regeneration
3. Check if users are behind a shared NAT (many users with same IP)

### False positives in logs

High volumes of "user not found" warnings may indicate:

1. Legitimate users mistyping email addresses
2. Automated scanning/attacks (expected and blocked)
3. Misconfigured client applications
