# MCP OAuth authorization

oCMS can act as the OAuth authorization server for its own `/api/mcp` endpoint.
It uses `github.com/go-oauth2/oauth2/v4` **v4.6.0** for the grant engine and the
official MCP Go SDK for bearer verification and protected-resource discovery.
OAuth is off by default. Existing API keys remain available with their CIDR,
expiry, anomaly-revocation and rate-limit policies.

This is the R1.1 compatibility implementation. It does not write CMS drafts,
publish pages, generate content or call a model provider. Real Claude Desktop
and ChatGPT account acceptance is still pending; SDK tests do not establish
support for a particular account tier or GUI version.

## Enable

Enable **MCP Server** in **Admin → Modules**, then configure and restart:

```dotenv
OCMS_SITE_URL=https://mcp-staging.example.com
OCMS_OAUTH_ENABLED=true
OCMS_OAUTH_CIMD_ALLOWED_HOSTS=claude.ai,chatgpt.com
OCMS_OAUTH_ALLOW_DCR=false
OCMS_OAUTH_ACCESS_TTL=3600
OCMS_OAUTH_REFRESH_TTL_DAYS=30
```

The issuer must be a configured HTTPS origin, without a path, query,
fragment or credentials. Development alone permits HTTP on an IP-literal
loopback origin, such as `http://127.0.0.1:8080`. Request Host and forwarding
headers never choose the issuer or token audience. OAuth startup does not
activate the MCP module automatically.

Use an editor or admin account. Authorization starts a fresh password sign-in
and follows the site's existing MFA policy, even if the browser already has
a signed-in session. Consent shows the account, client ID, client metadata
host, callback host and requested permissions. Client names are unverified
registration data. The decision has a session-bound, single-use nonce,
origin checks and a ten-minute deadline. Closing or denying it grants nothing.
Revoke an app in **Account → Security → Connected AI apps**.

## Protocol and permissions

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/oauth-authorization-server` | OAuth server metadata |
| `GET /.well-known/oauth-protected-resource/api/mcp` | MCP resource and issuer metadata |
| `GET/POST /oauth/authorize` | Browser sign-in and explicit consent |
| `POST /oauth/token` | Form-encoded code exchange or rotating refresh |
| `POST /oauth/revoke` | Revoke a token's connection, bound to the requesting client |
| `POST /oauth/register` | Optional public-client registration; otherwise 404 |

Only authorization code with **S256 PKCE** and refresh grants are supported.
Public clients use `token_endpoint_auth_method=none`. Implicit, password,
client credentials, JWT assertions, client secrets and OIDC scopes are not
supported or advertised. The authorization response includes the configured
`iss` and echoes `state`; clients must validate both.

The browser callback uses a non-cacheable HTML navigation page after consent.
Its nonce-authorized script performs the GET to the validated callback, with
a normal Continue link as a fallback. This keeps `form-action 'self'` intact;
a cross-origin HTTP redirect directly from the consent POST is blocked by
Chrome. Success and denial both use this path. Consent remains single-use,
with fresh login/MFA and the existing ten-minute deadline.

Both authorization and token requests must contain exactly one
`resource=https://mcp-staging.example.com/api/mcp`. Redirects must match a
registered URI exactly. IP-literal loopback HTTP redirects permit a variable
port at authorization, as RFC 8252 requires; the token exchange still binds to
the exact redirect used for its code. Other HTTP redirects are refused.

| Scope | Current behavior |
|---|---|
| `mcp:access` | Read published context; required to reach MCP |
| `pages:read` | Read unpublished pages only when the MCP drafts policy also permits it |
| `mcp:drafts` | Use the temporary scratch tools when explicitly enabled |

The public role has no OAuth permissions. Every MCP request resolves the
current account and grant, not a cached client session. Deleting the account,
removing its editor/admin role, changing its credential/session version or
revoking the grant invalidates access. Grant scope reductions also take effect
on existing access tokens and refreshes. API keys require a current editor/admin
owner; they otherwise retain their existing policies.

Codes last at most 60 seconds and can be consumed once. Codes, access and
refresh credentials contain 32 random bytes; SQLite stores SHA-256 hashes,
not their plaintext. An immediate SQLite transaction covers code consumption,
PKCE validation and token creation. Credentials are returned only after commit.
An invalid verifier or failed commit cannot consume the code or expose tokens.

Refresh rotates both credentials, invalidates the previous access token and
retains the spent refresh hash. The grant has an absolute refresh-family
expiry; rotation does not extend it. Reuse by the matching client and resource
revokes the entire family durably, including when the response is
`invalid_grant`. A different client cannot revoke a family by presenting its
refresh token. Keep SQLite and its normal backup policy; there is no token
encryption key to rotate. Expired/revoked rows currently remain as evidence;
automatic retention/maintenance is not implemented in R1.1.

## Client metadata and registration

CIMD client IDs are HTTPS document URLs on an exact hostname allowlist, with
no credentials, query, fragment or non-443 port. Fetches use a DNS-aware SSRF
guard, no environment proxy, a five-second timeout, a 64 KiB body cap and no
redirects. The document's `client_id` must equal its URL; callbacks and public
client authentication are validated. Metadata is cached in SQLite for fifteen
minutes, concurrent fetches for one URL are coalesced, and registration/cache
storage is capped at 1,000 clients. Unused expired clients can be removed when
the cap is reached. A cache refresh cannot exceed that cap.

The default hosts cover the planned Claude and ChatGPT CIMD clients. This is
an allowlist, not proof of account compatibility. Add another exact hostname
only for a client you intend to support. DCR is a separate opt-in fallback for
public clients; it creates a random client ID, no secret, and a ninety-day
registration. DCR accepts only code/refresh grant metadata. CIMD documents
must advertise authorization code support when `grant_types` is present;
additional advertised grant types do not enable those flows in oCMS. Both
paths still require public-client authentication and code response types.
For CIMD, an advertised `token_endpoint_auth_methods_supported` array is
authoritative and must include `none`; oCMS selects that method even when
the legacy singular preference names `private_key_jwt`. Without the array,
the singular method must be `none`. JWT assertions remain rejected. DCR
continues to require the singular `none` method.
Public protocol routes use the application's IP rate limiter; MCP adds its
existing request/body/concurrency guards and a per-user OAuth limit.

`OCMS_OAUTH_PRE_REGISTERED_CLIENTS` optionally supplies a JSON array of
owner-reviewed public client metadata. It defaults to empty, accepts at most
20 documents within 64 KiB, and applies the same client-ID host allowlist,
HTTPS identifier, callback, code-flow and `none` authentication checks.
Invalid configuration refuses OAuth startup. Each exact configured identity
uses that registration instead of a live metadata fetch; unknown identities
still use normal discovery. No client gains account access from registration.

Both staging profiles pin Claude's public identity and its exact
`https://claude.ai/api/mcp/auth_callback` callback, reviewed on 2026-10-31.
Live metadata retrieval returned HTTP 403 from the Fly Machine with oCMS's
Go transport. This explicit registration follows
[CIMD pre-registration](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document#section-7.2);
it preserves S256, fresh login/MFA, consent, scopes and revocation, with DCR
disabled. Update the registration only after reviewing the provider's public
identity. Registrations use the existing fifteen-minute SQLite metadata
cache; removing a pin resumes live discovery after that cache expires.
Revoke account grants separately to remove existing access immediately.

## Temporary client-write probe

On an isolated development or staging instance only:

```dotenv
OCMS_ENV=staging
OCMS_MCP_SPIKE_TOOLS=true
OCMS_AI_DISABLED=false
```

`spike_save_draft` replaces the user's in-memory scratch entry;
`spike_get_draft` reads it by its returned ID. Both require `mcp:drafts`.
Entries have an 8 KiB combined title/body limit, one per user, 100 total,
and one-hour expiry. A process restart drops all entries. They have no CMS,
translation, media or preview-link write path and return no admin page URL.
The write is advertised as non-read-only, destructive and non-idempotent.
Missing OAuth tool scope returns HTTP 403 with a Bearer `insufficient_scope`
challenge naming `mcp:access`, the tool's required scope and resource metadata.
Clients can request fresh consent and retry; an existing read-only grant is
not expanded automatically. The tool wrapper also retains its scope check
and result-metadata challenge. API keys retain their existing tool errors.

Production and demo mode refuse the scratch flag. `OCMS_AI_DISABLED=true`
removes these tools while keeping normal reads. Remove the probe tools and
flag in R1.5; they cannot establish that real CMS persistence is correct.

## Public staging and logs

Use a separate database, HTTPS host, fresh staging secrets and required MFA.
For the scratch probe, use `OCMS_ENV=staging` and leave demo mode off. DCR
defaults off; investigate a client registration/authentication failure before
changing that policy. The deployment plan does not provision a host or alter
connected-client settings.

### Fly.io staging preparation

The R1.1 target is `https://ocms-mcp-staging.fly.dev`, with a separate Fly app
`ocms-mcp-staging` in `fra`. The dedicated `fly.staging.toml` and private
`fly.staging.bootstrap.toml` are accompanied by the
[exact owner-run deployment procedure](mcp-staging-fly.md). The existing
`fly.toml`, `ocms-demo` and demo deploy/reset script remain unchanged.
Deploy this compatibility branch to the separate Fly app.

Use two shared vCPUs, 1 GB RAM, and one fresh 1 GB volume mounted at
`/app/data`, with the database and uploads under that mount. Keep
`kill_timeout = 60` and the existing health endpoints. Set
`min_machines_running = 1` during acceptance and keep the only Machine in the
primary region. Deploy with `--ha=false` and verify that exactly one Machine
and its volume exist: the flag does not remove any existing extra Machines.
Independent volumes have independent SQLite state, and the scratch store is
process-local. See [Fly configuration](https://docs.fly.io/reference/configuration),
[deploy options](https://docs.fly.io/flyctl/deploy) and
[volume behavior](https://docs.fly.io/volumes/overview).

The staging environment must include:

```dotenv
OCMS_ENV=staging
OCMS_SITE_URL=https://ocms-mcp-staging.fly.dev
OCMS_OAUTH_ENABLED=true
OCMS_OAUTH_CIMD_ALLOWED_HOSTS=claude.ai,chatgpt.com
OCMS_OAUTH_ALLOW_DCR=false
OCMS_OAUTH_ACCESS_TTL=3600
OCMS_OAUTH_REFRESH_TTL_DAYS=30
OCMS_MCP_SPIKE_TOOLS=true
OCMS_AI_DISABLED=false
OCMS_DEMO_MODE=false
OCMS_REQUIRE_MFA=true
OCMS_DO_SEED=false
```

`fly.staging.bootstrap.toml` has no public service. On this first boot only,
`OCMS_DO_SEED=true`, OAuth/scratch are disabled, and development mode permits
HTTP cookies through an owner-bound loopback `fly proxy` tunnel. MFA remains
required. Replace seeded credentials, enable the MCP module and enroll a
dedicated editor privately. Then deploy the same image and volume with
`fly.staging.toml`: staging mode, secure cookies and seeding off. Use fresh
seed data and a reviewed content-only export; keep production
user/session/token databases out of staging.

The owner has deferred the editor account for this bounded R1.1 probe. Record
the actual admin role in client results and leave manual editor-role coverage
unverified. Required MFA, non-default credentials and the scope/CMS-write
restrictions still apply.

The owner sets `OCMS_SESSION_SECRET` and `OCMS_MFA_KEY` through Fly secrets;
they must be distinct random values of at least 32 bytes. Keep them out of
TOML, repository files, shared command transcripts and logs.

Fly Proxy provides HTTPS at the app's `fly.dev` hostname, so R1.1 requires no
custom-domain DNS or certificate setup. Keep `OCMS_SITE_URL`, authorization
server issuer, protected resource and client endpoint on that same origin.
The original `mcp-staging.ocms.tech` hostname is deferred. If adopting it
later, review the issuer change and use the actual DNS/verification records
returned by `fly certs setup`; reconnect clients after the switch. See
[custom-domain setup](https://docs.fly.io/networking/custom-domain).

Before client acceptance, verify both deployment-specific controls:

- **Client IP:** oCMS currently consumes `X-Forwarded-For` only from a trusted
  immediate peer and scans the chain from the right. It does not consume
  `Fly-Client-IP`. Observe only the raw peer and relevant IP headers on a
  controlled request, configure the verified Fly peer/proxy hops in
  `OCMS_TRUSTED_PROXIES`, and confirm the resolved client reaches rate limits
  and auth events. Include a request with a spoofed forwarded address and a
  check after restart/redeploy. Avoid guessed global trust ranges. If the
  existing resolver cannot safely handle the observed chain, make a scoped,
  tested fix before proceeding. [Fly request headers](https://docs.fly.io/networking/request-headers)
- **Logs:** inspect owner-visible `fly logs` after a complete sign-in and MCP
  call. Codes, state, PKCE verifiers, bearer/refresh credentials and scratch
  content must be absent. Keep HTTP body/header debug logging disabled.
  Record the result without copying secrets into the evidence.

The runbook contains exact owner-run bootstrap, secrets, ingress, deploy and
verification commands. It builds from a clean commit archive to exclude
ignored local modules and working files. Branch/commit handling follows
`AGENTS.md`; preparing files is not commit, push or deployment approval.
Build images only from a clean, tested commit of the R1.1 branch, with the
explicit staging config/app and `--ha=false` supplied to `fly deploy`.
For configuration-only changes, validate and record the exact profile delta
and retain the reviewed image; this does not authorize a new source build or
Git mutation. Provisioning, secret handling and deployment require owner
authorization; preparation alone supplies none.

### nginx deployments

For nginx-hosted installations, use the normal
[reverse-proxy setup](reverse-proxy.md). Verify the production proxy and
OAuth log redaction during the later rollout to the production host as well.
Verify nginx separately from Fly acceptance.

The oCMS access logger strips OAuth query strings; events record only grant,
client, user and scope identifiers. Do not enable HTTP body/header debug
logging. Reverse-proxy logs need the same protection. For nginx, define a
path-only format in the `http` block and use it for OAuth routes:

```nginx
log_format oauth_path '$remote_addr [$time_local] "$request_method $uri $server_protocol" $status';

# Inside the existing HTTPS server block:
location ^~ /oauth/ {
    access_log /var/log/nginx/ocms-oauth.log oauth_path;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $remote_addr;
}
```

Use `$uri`, never `$request`, `$request_uri`, `$args`, `$http_referer`,
Authorization or request bodies for these routes. Apply equivalent redaction
to upstream load balancers and observability tools.

## Acceptance evidence still needed

The local suite covers code exchange, retry/rollback, concurrent redemption,
refresh rotation/replay/commit failure, audience/client/scope binding, CIMD,
fresh login/MFA/consent, grant management and authenticated MCP tool calls.
It also checks scratch ownership/bounds and legacy key behavior. The running
application must additionally be checked over HTTP after a build.

For each real client, record date, app/browser version, OS, account tier,
transport and auth mode. Test connect → consent → list → read context →
approve scratch write → retrieve → revoke → next call fails → reconnect.
Record unavailable account/UI capabilities as unverified. The R1.1 matrix in
`.claude/docs/PHASE5.md` remains pending until those account checks are run;
public HTTPS is the common baseline and a tunnel is optional.
Record every matrix row against the staged commit and configuration before
starting R1.2. Missing required GUI passes remain pending until the owner
resolves the blocker or explicitly changes scope.
