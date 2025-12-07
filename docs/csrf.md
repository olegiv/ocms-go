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

## Using CSRF Tokens in Templates

The CSRF token is automatically available in templates via the `CSRFField` function:

```html
<form method="POST" action="/login">
    {{ CSRFField }}
    <input type="email" name="email" required>
    <input type="password" name="password" required>
    <button type="submit">Login</button>
</form>
```

This outputs:
```html
<input type="hidden" name="gorilla.csrf.Token" value="...">
```

## AJAX Requests

For AJAX requests, include the CSRF token in the `X-CSRF-Token` header:

```javascript
// Get token from meta tag or form field
const token = document.querySelector('input[name="gorilla.csrf.Token"]').value;

fetch('/api/endpoint', {
    method: 'POST',
    headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': token
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

### "Forbidden - CSRF token invalid or missing"

This error occurs when CSRF validation fails. Check the server logs for the specific reason:

| Error Reason | Cause | Solution |
|--------------|-------|----------|
| `referer not supplied` | No Referer header on POST | Browser should send this automatically |
| `origin invalid` | Origin header doesn't match trusted origins | Check TrustedOrigins uses host:port format |
| `token invalid` | Token doesn't match or is expired | Ensure form includes `{{ CSRFField }}` |
| `token missing` | No token in form or header | Add CSRF token to form/request |

### Debugging CSRF Issues

1. Check server logs for the `reason` field in CSRF errors
2. Verify the `_gorilla_csrf` cookie is being set
3. Ensure the form includes the hidden token field
4. For AJAX, check the `X-CSRF-Token` header is present

### Common Mistakes

1. **Using full URLs in TrustedOrigins**: Use `localhost:8080`, not `http://localhost:8080`
2. **Missing form token**: Ensure `{{ CSRFField }}` is in every POST form
3. **Cookie not set**: Check if cookies are being blocked by browser settings
4. **HTTPS mismatch**: In production, ensure both site and requests use HTTPS

## Security Considerations

- The CSRF auth key should be the same as the session secret (`OCMS_SESSION_SECRET`)
- Minimum 32-byte key length is required
- In production, always use HTTPS
- Modern browsers send Fetch metadata headers automatically, providing strong CSRF protection

## Testing CSRF Protection

```bash
# Test that CSRF is enforced (should return 403)
curl -X POST http://localhost:8080/login \
  -d "email=test@test.com&password=test"

# Test with valid CSRF token (should succeed)
# First get the login page and extract token
curl -c cookies.txt http://localhost:8080/login > form.html
TOKEN=$(grep -oP 'name="gorilla.csrf.Token" value="\K[^"]+' form.html)

# Then submit with token and cookie
curl -b cookies.txt \
  -H "Referer: http://localhost:8080/login" \
  -d "email=admin@example.com&password=changeme&gorilla.csrf.Token=$TOKEN" \
  http://localhost:8080/login
```

## Related Documentation

- [Login Security](./login-security.md) - Rate limiting and account lockout
- [hCaptcha](./hcaptcha.md) - Bot protection on login
