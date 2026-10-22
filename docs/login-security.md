# Login Security

OCMS includes built-in protection against brute force attacks and DDoS on the login form. This document describes the security measures and how to configure them.

## Login destinations and account dashboard

Successful login and authenticated visits to `/login` use the same destinations:

| Role | Destination | Access |
|------|-------------|--------|
| Administrator (`admin`) | `/admin` | Full administration |
| Editor (`editor`) | `/admin` | Content management; administrator-only routes remain forbidden |
| Public (`public`) | `/account` | Own account overview; no administration access |
| Unrecognized role | `/` | No account dashboard or administration access |

The public role is intended for users without staff privileges. Core does not
provide public self-registration; administrators or imports provision accounts.
Redirect destinations are fixed internal paths and cannot be overridden by login
query parameters or form fields.

`GET /account` requires a valid session and shows only the current user's name,
email, creation date and last-login date. Administrators and editors can also
visit it directly. It includes site branding, a website link, language selection
and the existing POST logout action. Account details are read-only: there is no
profile editor, password-change form or account deletion; the second factor is
managed on `/account/security`. The interface uses the
same language preference and localized dates as login and administration, with
English and Russian translations and a fallback when no login is recorded.

The dashboard shares the admin header, collapsible sidebar, cards and mobile
navigation. Its account variant shows only My account, Two-factor authentication
(see below), Visit website and Log out;
the same account navigation is used for administrators and editors visiting
`/account`. The profile link leads to `/account`, and language selection submits
to `/language`. Administrative settings, content and module links are omitted.
Themes do not need an account template. Responses containing account details use
`Cache-Control: private, no-store` and `noindex, nofollow`. Authentication reloads
the user and role from the database for each protected request, so demoting an
editor to public removes admin access immediately. Deleted users and sessions
invalidated by password changes must sign in again.

### Reserved `/account` route

`/account` is an unprefixed core route, alongside `/login` and `/logout`;
language selection does not change its URL. `account` is reserved as a language
prefix, and content redirect rules (including wildcard rules) cannot intercept
`/account` or paths below it. Similar names such as `/accounting` remain available
for content redirects. New pages and aliases cannot use the `account` slug,
`robots.txt` disallows `/account`, and first-party analytics does not record it.

Before upgrading, rename any page slug, alias, module route or language prefix
that already uses `/account`. The core dashboard takes precedence at `/account`;
existing conflicting content is not renamed or deleted automatically. No schema
migration or site-theme changes are required.

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
| Max Failed Attempts | 5 | Failures from one address before that email + address pair is locked |
| Account-wide Threshold | 25 | Failures across all addresses before the account is locked for everyone |
| Attempt Window | 15 minutes | Time window for counting failures |
| Base Lockout Duration | 15 minutes | Initial lockout period |
| Max Lockout Duration | 24 hours | Maximum lockout after repeated lockouts |

### How It Works

1. Failed login attempts are tracked per email address **and** client address, and separately per email address alone
2. After 5 failed attempts within 15 minutes from one address, that email + address pair is locked; the account owner can still sign in from another address. For IPv6 clients the "address" is the /64 block. After 25 failed attempts within the window across all addresses, the account is locked for everyone, which stops a distributed guessing campaign; an attacker with many addresses therefore gets at most five times the single-address budget before the account lock, and that lock doubles with each repetition. Knowing a victim's email is not enough to lock them out from their own connection.
3. Lockout duration doubles with each subsequent lockout (exponential backoff):
   - 1st lockout: 15 minutes
   - 2nd lockout: 30 minutes
   - 3rd lockout: 1 hour
   - 4th lockout: 2 hours
   - ... up to 24 hours maximum

### User Experience

| Attempt | Message |
|---------|---------|
| 1 | "Invalid email or password" |
| 2-4 | "Invalid email or password. N attempts remaining." |
| 5 | "Too many failed attempts. Account locked for 15 minutes." |
| During lockout | "Account is temporarily locked. Try again in N minutes." |

### Security Features

- **Non-existent email protection**: Failed attempts are tracked even for non-existent email addresses to prevent user enumeration attacks
- **Successful login reset**: A successful login clears the failed attempt counters in both scopes. Confirming the password for a sensitive change (a user change, a theme import, the first authenticator setup) does not: a password alone must not wipe out wrong second-factor codes
- **Automatic cleanup**: Stale tracking entries are automatically removed

## Two-Factor Authentication

oCMS supports a second factor at sign-in: a six-digit code from an
authenticator app (TOTP, RFC 6238: SHA-1, 30-second period, as 1Password,
Bitwarden, Google Authenticator and Microsoft Authenticator use), with ten
single-use recovery codes for a lost device. It is built in; no external
service is involved.

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `OCMS_MFA_KEY` | - | Seals the TOTP secrets at rest (AES-256-GCM under a key HKDF derives from it, bound to the user ID). Minimum 32 bytes, distinct from `OCMS_SESSION_SECRET`, with the same placeholder and entropy checks. Generate with `openssl rand -base64 32` |
| `OCMS_REQUIRE_MFA` | `false` (`true` in production when unset) | Every account, admin, editor and public alike, must pass a second factor. Startup fails while it is on and `OCMS_MFA_KEY` is unset |

`ocms init` and `scripts/deploy/setup-site.sh` write a random `OCMS_MFA_KEY`.
Back the key up with the database: it is the only way to read the enrolled
secrets. If it is lost or changed, authenticator codes stop working (the
sign-in page says so) and enrolled users sign in with a recovery code, then
set up their app again; nothing else about the account changes. Without
`OCMS_MFA_KEY` (development, or `OCMS_REQUIRE_MFA=false`), users cannot
enroll an authenticator app.

Demo mode turns two-factor authentication off: the seeded demo accounts are
shared, so one visitor's authenticator would lock the others out. The Fly demo
sets `OCMS_REQUIRE_MFA=false`.

### Sign-in

1. The password is checked as before, with the same rate limit and lockout.
2. An account with an authenticator goes to `/login/mfa` for its code. An
   account without one, where `OCMS_REQUIRE_MFA` is on, goes to
   `/login/mfa/setup`: it scans a QR code (or types the key), confirms one
   code, and is shown its recovery codes once. Accounts without an
   authenticator on a site that does not require one sign in as before.
3. Only then is the session signed in: until the second factor passes, the
   session holds no user, so `/admin`, `/account` and every module route treat
   it as anonymous. The session token is rotated at both steps.

The pending sign-in lasts 10 minutes and ends after 5 wrong codes, or when
the account's password or second factor changes in the meantime. Wrong codes
also count toward the account lockout described above. Each code is accepted
once: the server records the last accepted time step, so a code read over
someone's shoulder cannot be replayed, and codes within one period either
side of the server's clock are accepted. **Sign in with a different
account** abandons the pending sign-in.

When `OCMS_REQUIRE_MFA` is switched on, sessions that signed in without a
second factor are signed out at their next request.

### Account security page

`/account/security` (linked from the user menu and the account sidebar) shows
whether two-factor authentication is on and how many recovery codes are
left. From there a user can:

- **Set up** an authenticator app, which also issues recovery codes.
- **Replace** the authenticator (new phone): it asks for a code from the old
  one, or a recovery code, and issues new recovery codes.
- **Generate new recovery codes**, which voids the old ones.
- **Turn it off**, unless the site requires it.

The first setup asks for the account password and every later change for a
current code, so a stolen session cannot add or change the second factor;
wrong passwords and codes count toward the lockout.
Setting up, replacing and turning off sign out the user's other sessions.

### Recovery codes

Each code is 16 characters (`xxxx-xxxx-xxxx-xxxx`, 80 random bits) from
Crockford's base32 alphabet, stored as a SHA-256 hash and accepted once.
Upper or lower case and the dashes do not matter, and `o`, `i` and `l` read
as `0`, `1` and `1`. A sign-in with a recovery code is logged at warning level
and tells the user how many codes are left; the security page suggests new
ones at three or fewer.

### Lost authenticator and recovery codes

- **Administrator reset**: on `/admin/users/{id}`, **Reset two-factor
  authentication** removes the user's authenticator and recovery codes and
  signs them out everywhere. It asks for the acting administrator's current
  password. Where a second factor is required they set up a
  new one at their next sign-in. Administrators cannot reset their own here;
  they use the account security page, which asks for a current code.
- **Command line**, for an administrator with no other administrator to
  reset them:
  ```bash
  ocms mfa-reset admin@example.com            # reads .env / OCMS_ENV_FILE like ocms migrate
  ocms mfa-reset -db ./data/ocms.db admin@example.com
  ```
  It needs no `OCMS_MFA_KEY` and records the reset in the Event Log.

### What it does not cover

API keys (REST API, MCP server, `/health`) authenticate with a bearer token,
not a session, and are not affected; restrict them with their own expiry and
source-CIDR policies. Passkeys (WebAuthn) as a second factor are planned.

## Session Lifetime

Sessions are stored server-side; the session row holds the data and the cookie only carries its token. A session ends after **24 hours** regardless of activity, and after **2 hours** without any request; each request renews the idle window. This applies to every session, admin and public alike.

## Event Logging

All login-related events are logged to the events system:

| Event | Level | Description |
|-------|-------|-------------|
| Login failed: user not found | Warning | Attempt with non-existent email |
| Login failed: invalid password | Warning | Wrong password for existing user |
| Login attempt on locked account | Warning | Attempt during lockout period |
| Account locked due to failed attempts | Warning | Account just got locked |
| Login failed: invalid two-factor code | Warning | Wrong, expired or reused code at `/login/mfa` |
| Recovery code used to sign in | Warning | Sign-in with a recovery code |
| Two-factor authentication enabled | Info | Authenticator set up or replaced |
| Two-factor authentication disabled | Warning | Turned off on the account security page |
| Two-factor change refused: invalid code | Warning | Wrong current code on the account security page |
| Two-factor change refused: invalid password | Warning | Wrong password for the first authenticator setup on the account security page |
| Two-factor change refused: no password | Warning | First setup sent without a password (a form from before the account's status changed); not counted toward the lockout |
| Two-factor change refused: password could not be verified | Error | The stored password hash could not be checked |
| Recovery codes regenerated | Info | New recovery codes issued |
| Two-factor authentication reset by an administrator | Warning | Reset from the user page (User category) |
| User change refused: wrong current password | Warning | Wrong acting-administrator password on the user form or a second-factor reset (User category) |
| User change refused: account locked | Warning | The same, while the administrator's account is locked (User category) |
| User change refused: no current password | Warning | The same change sent without the password; not counted toward the lockout (User category) |
| User change refused: password could not be verified | Error | The administrator's stored password hash could not be checked (User category) |
| Theme import refused: password confirmation failed / no password / too many failed password confirmations | Warning | Theme import without the administrator's current password (Security category; only a wrong one is counted) |
| Theme import refused: password could not be verified | Error | The stored password hash could not be checked (Security category) |
| Module activation refused: wrong current password / account locked / no password | Warning | Activating DB Manager without the administrator's password (Security category; only a wrong one is counted) |
| Module activation refused: password could not be verified | Error | The stored password hash could not be checked (Security category) |
| Module activated with password confirmation | Info | DB Manager switched on after the password check (Security category) |
| Two-factor authentication reset from the command line | Warning | `ocms mfa-reset` |
| User logged in | Info | Successful login; metadata `second_factor` names `totp` or `recovery_code` when one was used |

View these events in the admin panel under **Admin > Events**.

## Configuration

Login protection settings are currently defined in code. To customize:

```go
// In cmd/ocms/main.go
loginProtection := middleware.NewLoginProtection(db, middleware.LoginProtectionConfig{
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
# mod_proxy_http adds X-Forwarded-For itself; also pass the connecting address
RequestHeader set X-Real-IP "%{REMOTE_ADDR}s"
```

The login protection middleware uses the client IP resolved by oCMS (`middleware.GetClientIP`). Forwarding headers are honored only when the direct peer is listed in `OCMS_TRUSTED_PROXIES` (see the checklist below); any other peer is identified by its connection address. For a trusted peer the order is:
1. `X-Forwarded-For` (the first address from the right that is not a trusted proxy)
2. `X-Real-IP`, only when `X-Forwarded-For` is absent
3. `RemoteAddr` (direct connection, or no usable header)

## Session Invalidation on Password Change

Sessions are bound to a per-user `session_version` counter that bumps on every password change, and on every change of the second factor (setup, replacement, turning it off, an administrator or command-line reset) — admin reset, self-service edit, and the demo's automatic admin rotation all go through the same SQL UPDATE that increments it. The login flow snapshots the live value into the session; the auth middleware (`LoadUser`) compares the snapshot against the live row on every protected request and destroys any session whose snapshot is older. The actor's own password change re-snapshots the new value into their current session so they remain logged in; sibling devices/browsers for the same account become unauthenticated on their next request. User changes that could hand an account to whoever holds an administrator's session ask for that administrator's current password: a new password or email for any user (the email keys the login lockout, so a new one would bring fresh password guesses), giving or taking away the admin role, and resetting another user's second factor. Wrong attempts count toward the login lockout. A content import that overwrites users never grants or removes the admin role. A stolen administrator session therefore cannot set a password of its own and pass the check that guards adding a second factor, reset an account's lockout by changing its email, create an administrator it controls to take the others over, or demote the real ones. Activating the DB Manager module asks for the administrator's current password too, since its raw SQL reaches every table, users and sessions included; once active, its queries need none, so switch it off when it is not needed.

The Argon2 parameter-upgrade rehash that runs on a successful login uses a separate query that does **not** bump `session_version`, because the credential is unchanged — only its hash representation. Without this distinction, every login that triggered a rehash would also log the user out.

## Best Practices

1. **Use strong passwords**: Enforce minimum password requirements
2. **Monitor events**: Regularly check the events log for suspicious activity
3. **Use HTTPS**: Always run behind HTTPS in production
4. **Keep two-factor authentication required**: production requires it by default; keep `OCMS_MFA_KEY` backed up with the database
5. **Consider additional measures**: For high-security deployments, consider:
   - CAPTCHA after N failed attempts
   - IP allowlisting for admin access

## Production Deployment Checklist

Before exposing an oCMS instance to the public internet, work through this list:

1. **Rotate the seed admin password.** When `OCMS_DO_SEED=true` is used (typically on first boot), oCMS provisions `admin@example.com` with the well-known password `changeme1234`. Log in and change both the email and password on `/admin/users` before anything else. The seed password is public knowledge — anyone who reaches the login page can try it. Production startup blocks `OCMS_DO_SEED=true` unless demo mode is also enabled, so the exposure window is limited to initial-setup runs, but the first admin session still has to replace these credentials. Once any administrator account exists, seeding no longer recreates `admin@example.com`, so renaming or replacing it is permanent even if the flag stays on.
2. Set `OCMS_SESSION_SECRET` to a freshly generated 32-byte value (e.g. `openssl rand -base64 32`). Startup rejects known defaults, placeholder values copied from the example files or docs, and (in production) the public test-suite secret, but rotate on each environment.
3. Set `OCMS_ENV=production` so security defaults apply (stricter CSRF, HSTS, seed-disabled, two-factor authentication for every account, etc.).
4. Set `OCMS_MFA_KEY` to another freshly generated 32-byte value and back it up with the database. Production requires a second factor for every account and refuses to start without the key unless `OCMS_REQUIRE_MFA=false`; each user sets up an authenticator app at their next sign-in.
5. Configure `OCMS_TRUSTED_PROXIES` if running behind a reverse proxy — otherwise client IPs used by rate limiting and account lockout will be the proxy's address, collapsing every attacker into one bucket. In production, startup fails while it is unset unless `OCMS_REQUIRE_TRUSTED_PROXIES=false`. See `docs/reverse-proxy.md`.
6. Enable HSTS preload (`OCMS_HSTS_PRELOAD=true`) once you're certain every subdomain has a valid TLS certificate; submit the domain at <https://hstspreload.org>. Details in `docs/reverse-proxy.md`.

## Troubleshooting

### Legitimate user locked out

If a legitimate user is locked out:

1. Wait for the lockout period to expire, OR
2. Delete the account's rows from the `login_protection` table. Lockout state is stored in SQLite, so restarting the application does not clear it:
   ```bash
   sqlite3 ./data/ocms.db "DELETE FROM login_protection WHERE email IN ('user@example.com', 'account' || char(31) || 'user@example.com') OR email GLOB 'pair' || char(31) || 'user@example.com' || char(31) || '*'"
   ```

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
