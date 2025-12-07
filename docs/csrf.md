# CSRF Protection

oCMS implements Cross-Site Request Forgery (CSRF) protection using the `filippo.io/csrf/gorilla` library. This modern library uses Fetch metadata headers instead of traditional cookie-based tokens, providing more reliable protection.

## Overview

CSRF attacks exploit authenticated users by tricking them into submitting malicious requests. oCMS protects against this by validating Fetch metadata headers on all state-changing requests (POST, PUT, DELETE, PATCH).

## How It Works

The `filippo.io/csrf/gorilla` library uses **Fetch metadata headers** (specifically `Sec-Fetch-Site`) provided by modern browsers to determine request origin:

1. **Fetch Metadata**: Browsers automatically include `Sec-Fetch-Site` header indicating whether the request is same-origin, same-site, cross-site, or none
2. **Form Token**: Forms include a hidden `gorilla.csrf.Token` field for compatibility
3. **Validation**: On form submission, the server validates that:
   - The `Sec-Fetch-Site` header indicates a same-origin or trusted request
   - The request's Origin/Referer header matches trusted origins (for legacy browser fallback)

This approach is more reliable than cookie-based CSRF protection because:
- It doesn't depend on cookie settings (SameSite, Secure, etc.)
- It's immune to cookie-related attacks and browser quirks
- It's the [recommended method by browsers](https://web.dev/fetch-metadata/) for CSRF protection

## Configuration

CSRF protection is configured in `internal/middleware/csrf.go`:

```go
type CSRFConfig struct {
    AuthKey        []byte           // 32-byte key for token encryption
    ErrorHandler   http.Handler     // Custom error handler
    TrustedOrigins []string         // Allowed origins for cross-origin requests
}
```

Note: Cookie-related options (Secure, Domain, Path, MaxAge, SameSite) are no longer used since the library now relies on Fetch metadata headers instead of cookies.

### TrustedOrigins Format

**IMPORTANT**: The `TrustedOrigins` must be in `host:port` format, NOT full URLs.

```go
// CORRECT - host:port format
TrustedOrigins: []string{
    "localhost:8080",
    "127.0.0.1:8080",
}

// WRONG - full URLs cause "origin invalid" errors
TrustedOrigins: []string{
    "http://localhost:8080",  // DO NOT USE
}
```

### Development vs Production

In development mode (`OCMS_ENV=development`):
- `localhost:8080` and `127.0.0.1:8080` are trusted origins

In production mode (`OCMS_ENV=production`):
- No default trusted origins (same-origin requests only)

## Template Fields (Legacy)

The `CSRFField` and `CSRFToken` template variables are kept for backward compatibility but now output empty strings. Since the library uses Fetch metadata headers for protection, hidden form fields are no longer required.

Existing templates with `{{ .CSRFField }}` will continue to work - they simply output nothing:

```html
<form method="POST" action="/login">
    {{.CSRFField}}  <!-- No longer needed, outputs empty string -->
    <input type="email" name="email" required>
    <input type="password" name="password" required>
    <button type="submit">Login</button>
</form>
```

## AJAX Requests

AJAX requests from the same origin work automatically without needing to include any CSRF tokens. The browser's Fetch metadata headers provide the necessary protection.

```javascript
// No CSRF token needed - Fetch metadata headers handle protection
fetch('/api/endpoint', {
    method: 'POST',
    headers: {
        'Content-Type': 'application/json'
    },
    body: JSON.stringify(data)
});
```

## Skipping CSRF for API Routes

REST API endpoints use token-based authentication instead of CSRF. These routes are excluded from CSRF protection:

```go
// API routes skip CSRF validation
r.Route("/api", func(r chi.Router) {
    r.Use(middleware.SkipCSRF("/api/v1/..."))
    // ... API routes
})
```

## Troubleshooting

### "Forbidden - CSRF validation failed"

This error occurs when CSRF validation fails. Check the server logs for the specific reason:

| Error Reason | Cause | Solution |
|--------------|-------|----------|
| `Sec-Fetch-Site cross-site` | Request from different site | Ensure request is from same origin |
| `origin invalid` | Origin header doesn't match trusted origins | Check TrustedOrigins uses host:port format |
| `referer not supplied` | No Referer header (legacy browser) | Use a modern browser |

### Debugging CSRF Issues

1. Check server logs for the `reason`, `origin`, and `sec_fetch_site` fields
2. Verify the browser supports Fetch metadata headers (all modern browsers do)
3. Check that the request originates from the same site

### Common Mistakes

1. **Using full URLs in TrustedOrigins**: Use `localhost:8080`, not `http://localhost:8080`
2. **Cross-origin requests without TrustedOrigins**: Add the origin to TrustedOrigins if legitimate
3. **HTTPS mismatch**: In production, ensure both site and requests use HTTPS

## Security Considerations

- The CSRF auth key should be the same as the session secret (`OCMS_SESSION_SECRET`)
- Minimum 32-byte key length is required
- In production, always use HTTPS
- Modern browsers send Fetch metadata headers automatically, providing strong CSRF protection

## Testing CSRF Protection

```bash
# Test that CSRF is enforced (should return 403 for cross-site requests)
curl -X POST http://localhost:8080/login \
  -d "email=test@test.com&password=test"

# Test with proper headers (simulating same-origin request)
# Note: curl doesn't send Sec-Fetch-Site, so use Referer/Origin for testing
curl -X POST http://localhost:8080/login \
  -H "Origin: http://localhost:8080" \
  -H "Referer: http://localhost:8080/login" \
  -d "email=admin@example.com&password=changeme"
```

Note: Real browsers automatically include `Sec-Fetch-Site` headers which the library uses for validation. curl doesn't include these headers, so the library falls back to Origin/Referer validation.

## Related Documentation

- [Login Security](./login-security.md) - Rate limiting and account lockout
- [hCaptcha](./hcaptcha.md) - Bot protection on login
