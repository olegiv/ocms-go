# CSRF Protection

oCMS implements Cross-Site Request Forgery (CSRF) protection using the `filippo.io/csrf/gorilla` library. This modern library uses Fetch metadata headers instead of traditional cookie-based tokens, providing more reliable protection.

## Overview

CSRF attacks exploit authenticated users by tricking them into submitting malicious requests. oCMS protects against this by validating Fetch metadata headers on all state-changing requests (POST, PUT, DELETE, PATCH).

## How It Works

The `filippo.io/csrf/gorilla` library uses **Fetch metadata headers** (specifically `Sec-Fetch-Site`) provided by modern browsers to determine request origin:

1. **Fetch Metadata**: Browsers automatically include `Sec-Fetch-Site` header indicating whether the request is same-origin, same-site, cross-site, or none
2. **No Form Token**: The library ignores tokens, and oCMS forms do not include a hidden token field
3. **Validation**: For every method except GET, HEAD and OPTIONS, the server checks that:
   - `Sec-Fetch-Site` is `same-origin` or `none`; any other value (`same-site`, `cross-site`) is rejected unless the request's `Origin` header exactly matches a trusted origin
   - When `Sec-Fetch-Site` is missing (legacy browsers), the host of the `Origin` header must match the request `Host` (or the `Origin` must be a trusted origin); the Referer header is not used
   - Requests carrying neither header are assumed to be same-origin or non-browser requests (curl, scripts) and are allowed

This approach is more reliable than cookie-based CSRF protection because:
- It doesn't depend on cookie settings (SameSite, Secure, etc.)
- It's immune to cookie-related attacks and browser quirks
- It's the [recommended method by browsers](https://web.dev/fetch-metadata/) for CSRF protection

## Configuration

CSRF protection is configured in `internal/middleware/csrf.go`:

```go
type CSRFConfig struct {
    AuthKey        []byte           // Passed to the library, which ignores it (no tokens)
    ErrorHandler   http.Handler     // Custom error handler
    TrustedOrigins []string         // Allowed origins for cross-origin requests
}
```

Note: Cookie-related options (Secure, Domain, Path, MaxAge, SameSite) are no longer used since the library now relies on Fetch metadata headers instead of cookies.

### TrustedOrigins Format

**IMPORTANT**: oCMS's built-in list is scheme-less (`localhost:8080`,
`127.0.0.1:8080`). The library treats a scheme-less entry as `https://host:port`
and compares it with the request's `Origin` header exactly, so on a plain-HTTP
dev server those entries trust nothing. To trust a plain-HTTP origin, list it with
its scheme (`http://localhost:8080`), which the library accepts.
`middleware.ValidateTrustedOrigins` would reject that form, but the application
never calls it.

```go
// host:port format (matched as https://localhost:8080, https://127.0.0.1:8080)
TrustedOrigins: []string{
    "localhost:8080",
    "127.0.0.1:8080",
}
```

The list comes from `DefaultCSRFConfig`; there is no environment variable for it.

### Development vs Production

In development mode (`OCMS_ENV=development`, the default):
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

## Where CSRF Applies

The CSRF middleware is attached to the browser-facing route groups in `cmd/ocms/main.go`:
`/admin/*`, the auth routes (`/login`, `/logout`, `/language`), `/account`, and the public
form routes (`/forms/{slug}`). The REST API (`/api/v2/*`) and the MCP endpoint (`/api/mcp`)
use API-key authentication instead and are not mounted behind the CSRF middleware at all.
`/csp-report`, the internal analytics beacon and the modules' public routes (such as the
Embed module's `POST /embed/dify/chat-messages`) are outside the CSRF middleware as well.

`middleware.SkipCSRF(paths...)` can exempt exact paths from a group that has the middleware,
but no application route uses it.

## Troubleshooting

### "Forbidden - CSRF validation failed"

This error occurs when CSRF validation fails. Check the server logs for the specific reason:

| Error Reason | Cause | Solution |
|--------------|-------|----------|
| `cross-origin request detected from Sec-Fetch-Site header` | `Sec-Fetch-Site` is `cross-site` or `same-site` and the `Origin` is not trusted | Ensure request is from same origin |
| `cross-origin request detected, and/or browser is out of date: Sec-Fetch-Site is missing, and Origin does not match Host` | Legacy browser (or a client that sets `Origin`) whose `Origin` host differs from `Host` | Use a modern browser, or check that the reverse proxy forwards the original `Host` |

### Debugging CSRF Issues

1. Check server logs for the `reason`, `origin`, and `sec_fetch_site` fields
2. Verify the browser supports Fetch metadata headers (all modern browsers do)
3. Check that the request originates from the same site

### Common Mistakes

1. **Leaving out the scheme of a plain-HTTP origin**: `localhost:8080` is matched as `https://localhost:8080`; write `http://localhost:8080` to trust the plain-HTTP origin
2. **Cross-origin requests without TrustedOrigins**: Add the origin to TrustedOrigins if legitimate
3. **HTTPS mismatch**: A scheme-less trusted origin is matched as `https://`, so a request from the plain-HTTP variant of that origin is not trusted
4. **Reverse proxy changing `Host`**: Without `Sec-Fetch-Site`, the `Origin` host is compared with the `Host` the application sees; see [Reverse Proxy](./reverse-proxy.md)

## Security Considerations

- oCMS passes `OCMS_SESSION_SECRET` to the CSRF middleware as `AuthKey`, but the library ignores it; the session secret itself must still be at least 32 bytes
- In production, always use HTTPS
- Modern browsers send Fetch metadata headers automatically, providing strong CSRF protection

## Testing CSRF Protection

```bash
# Test that CSRF is enforced (should return 403 for cross-site requests)
curl -X POST http://localhost:8080/login \
  -H "Sec-Fetch-Site: cross-site" \
  -d "email=test@test.com&password=test"

# Same with only an Origin header that does not match Host (also 403)
curl -X POST http://localhost:8080/login \
  -H "Origin: https://evil.example" \
  -d "email=test@test.com&password=test"

# Test with proper headers (simulating same-origin request)
curl -X POST http://localhost:8080/login \
  -H "Origin: http://localhost:8080" \
  -H "Referer: http://localhost:8080/login" \
  -d "email=admin@example.com&password=changeme"
```

Note: Real browsers automatically include `Sec-Fetch-Site` headers which the library uses for validation. curl doesn't include them, so the library compares the `Origin` header with `Host` when one is sent; a request with neither header is treated as a non-browser request and allowed, so a bare `curl -X POST` is not rejected.

## Related Documentation

- [Login Security](./login-security.md) - Rate limiting and account lockout
- [hCaptcha](./hcaptcha.md) - Bot protection on login
