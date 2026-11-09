# MCP OAuth authorization

oCMS can act as the OAuth authorization server for its own `/api/mcp` endpoint.
It uses `github.com/go-oauth2/oauth2/v4` **v4.6.0** for the grant engine and the
official MCP Go SDK for bearer verification and protected-resource discovery.
OAuth is off by default. Existing API keys remain available with their CIDR,
expiry, anomaly-revocation and rate-limit policies.

The OAuth layer authenticates apps; [R1.5 draft tools](draft-authoring.md)
separately provide gated CMS writes. No tool publishes or calls a text model
provider. The following acceptance record covers the earlier R1.1 build. On 2026-10-31,
Claude Desktop, claude.ai and ChatGPT web passed the tested OAuth, published
context, scratch and revocation/reconnect flows on Fly staging commit
`f66bd7b4d5fa` using Pro accounts. On 2026-11-01, both CLI OAuth lifecycles and
both bearer-key modes passed on `c885da9071eb`; all three GUI clients passed
fresh published-content reads on that build. Claude Code uses the dedicated
Editor account and the shared Keychain authenticator described in
`docs/dev-authenticator.md`. Both short-lived test keys were deactivated at the
end. External Internet IPv6/TLS and real-client-IP handling also passed on
2026-11-01 from an independent probe, including forged IPv4, IPv6 and mixed
forwarding headers. R1.1 acceptance is complete against the recorded builds.
See the current matrix in `.claude/docs/PHASE5.md`; SDK tests alone do not
establish client support.

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

`OCMS_OAUTH_CIMD_ALLOWED_HOSTS` lists the hosts that may serve client metadata
documents, separated by commas or spaces. Unset, it means `claude.ai,chatgpt.com`.
Set to an empty value, it means no hosts: nothing is discovered live, and only
[pre-registered clients](#client-metadata-and-registration) and, when enabled,
DCR can connect. After restart, the allowlist applies to cached metadata too.
Removing a host blocks fresh authorization, pending code exchange and refresh
for its unpinned clients. Already issued access tokens still follow grant
revocation and token expiry. Every entry must be a bare lowercase DNS hostname
of at least two labels (no scheme, port, path, wildcard or IP address; text is lowercased
first), or OAuth startup refuses the configuration.

The issuer must be a configured HTTPS origin, without a path, query,
fragment or credentials. Development alone permits HTTP on an IP-literal
loopback origin, such as `http://127.0.0.1:8080`. Request Host and forwarding
headers never choose the issuer or token audience. OAuth startup does not
activate the MCP module automatically. While the module is off, discovery,
client registration, authorization and the token endpoint answer 404;
`/oauth/revoke` stays available so clients can retire their tokens.

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
| `POST /oauth/revoke` | Revoke a connection using a live access token or a refresh token and the matching `client_id` |
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

Before a consent decision, errors for dynamically registered clients stay on
oCMS as local JSON responses. This includes `access_denied` after fresh sign-in
when the account lacks the requested permissions: no callback link or automatic
navigation is returned, and the pending authorization is cleared without
signing the account out. Existing DCR clients retain this policy when new
registration is disabled. CIMD and pre-registered clients keep their validated
error callbacks. Explicit consent denial, successful authorization and errors
after an explicit allow decision still use the validated callback for all
client sources.

Both authorization and token requests must contain exactly one
`resource=https://mcp-staging.example.com/api/mcp`. Redirects must match a
registered URI exactly. Native CIMD clients may register IP-literal loopback
or literal `localhost` HTTP callbacks with a variable port at authorization.
The registered hostname, path and query must still match; `localhost` and
`127.0.0.1` are distinct registrations. The token exchange binds to the exact
redirect, including its port, used for its code. Other HTTP redirects are
refused. DCR retains its IP-literal-only HTTP registration policy. A port, where
a redirect URI has one, must be between 1 and 65535 (`:0` and `:99999` are
refused at registration and at authorization).

| Scope | Current behavior |
|---|---|
| `mcp:access` | Read published context; required to reach MCP |
| `pages:read` | Read unpublished pages only when the MCP drafts policy also permits it |
| `mcp:drafts` | Use opt-in owned AI-draft tools; no publication/deletion |

The public role has no OAuth permissions. Every MCP request resolves the
current account and grant, not a cached client session. Deleting the account,
removing its editor/admin role, changing its credential/session version or
revoking the grant invalidates access. Grant scope reductions also take effect
on existing access tokens and refreshes. API keys require a current editor/admin
owner; they otherwise retain their existing policies.

Codes last at most 60 seconds and can be consumed once. Presenting a code that
was already redeemed fails with `invalid_grant` and, as RFC 6749 section 4.1.2
recommends, revokes the grant that code issued, access and refresh tokens
alike, durably and with a warning event. Only the client the grant belongs to,
for this resource, can trigger that; any other client's attempt only fails.
The replaying request must also prove possession: a PKCE `code_verifier` whose
S256 transform is the code's stored challenge and the `redirect_uri` the code was
issued for. Authorization codes travel in redirect URLs (history, logs,
referrers), so someone who merely saw a spent code and the public `client_id`
cannot disconnect the user; their request fails with `invalid_grant` and revokes
nothing. A concurrent second redemption of one code by the real client therefore
leaves no working token.
Codes, access and
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
encryption key to rotate. R1.7 adds bounded retention locally: codes expire
from storage after a 24-hour evidence window; retired grant families after
30 days. Every token/spent-refresh row of an active family is retained.
The [retention policy](connected-apps.md#retention-policy) describes atomic
batches, preview dependencies, scheduling and rollback. This maintenance is
not deployed yet; R1.1's retained-row staging limitation remains until rollout.

Storage faults are retryable. A database error while checking an access
token, code or refresh token, or while revoking, is logged with its cause and
answered `temporarily_unavailable` (HTTP 503; the MCP endpoint answers 500),
never `invalid_token` or `invalid_grant`, so clients keep credentials that are
still valid. `POST /oauth/revoke` matches the `client_id` against the stored
grant and returns HTTP 200 after revocation. Unknown or malformed tokens,
tokens for a different client, and expired or rotated access tokens get
HTTP 200 with no effect. A spent refresh token can still revoke its family.
A storage fault that prevents revocation returns HTTP 503.

Revocation does not validate a client metadata document or read, refresh or
fetch the client registry entry. It uses the stored token and grant, so a
matching client can revoke even when its metadata has expired or is unavailable.
An unknown or non-matching `client_id` returns HTTP 200 with no effect, provided
the request passes the normal form and public-client checks: exactly one
non-empty `client_id` within the size limit, no client secret and no
`Authorization` header. This does not relax client validation for authorization
or token issuance.

When a loaded token and grant reveal a client mismatch, the server logs a
warning with the grant and user IDs. It logs neither the submitted client ID
nor credentials. The response remains HTTP 200 with no effect; unknown tokens
and expired or rotated access tokens do not produce this warning.

A client-metadata host that cannot be reached (network error, timeout, rate
limiting or 5xx) is also `temporarily_unavailable`; any other non-200 answer
or a document that fails validation is `invalid_client`.

Account → Connected apps lists the user's OAuth connections and MCP-capable API
keys with scopes, creation, last use, expiry and revoke. A grant exists from
consent, but is a connection only once its code has been exchanged for a
token: grants that never issued one (an abandoned consent, a repeated consent)
are not listed, and the retention job removes them after a day. Administrators can
review/revoke across accounts in `/admin/mcp`. Registered metadata does not
verify app identity. The earlier `/account/security` grant list/revoke path
remains compatible. Revoking a connection that does not exist or belongs to
another account (a stale form, a forged ID) redirects back with an error
message and changes nothing; repeating a revoke of your own, already revoked
connection is a harmless success, and a storage failure is a logged 500. See
[connected apps](connected-apps.md).

A dynamically registered client expires after one day unless the client
redeems an authorization code. A provisional client without a grant can
also be reclaimed at capacity. Successful code redemption extends the
registration to 90 days from consent. That covers the longest
refresh family (`OCMS_OAUTH_REFRESH_TTL_DAYS` is at most 90, `MaxRefreshTTL`), so
a used client never lapses before its grants.

## Client metadata and registration

CIMD client IDs are HTTPS document URLs on an exact hostname allowlist, with
no credentials, query, fragment or non-443 port. Fetches use a DNS-aware SSRF
guard, no environment proxy, a five-second timeout, a 64 KiB body cap and no
redirects. The document's `client_id` must equal its URL; callbacks and public
client authentication are validated. Metadata is cached in SQLite for fifteen
minutes, concurrent fetches for one URL are coalesced, and registration/cache
storage has separate capacity limits for CIMD and DCR clients.
The document must be served with a JSON `Content-Type` (`application/json` or
a `+json` type). A failed fetch is remembered for sixty seconds (at most 256
URLs), with the same error code, so unauthenticated requests naming one URL
cannot trigger an outbound request each time; a transport fault stays
`temporarily_unavailable`. A shared fetch has its own five-second deadline and
is not cancelled when the caller that started it gives up.
Public registrations cannot consume CIMD admission capacity. Unused expired
clients can be removed when a source's cap is reached; existing rows can still
refresh at capacity.

DCR applies admission and capacity limits. Source addresses follow the
configured trusted-proxy rules. Rate-limited requests return HTTP 429
with `Retry-After`.

The default hosts cover the planned Claude and ChatGPT CIMD clients. This is
an allowlist, not proof of account compatibility. Add another exact hostname
only for a client you intend to support. DCR is a separate opt-in fallback for
public clients. It creates a random client ID and no secret. A provisional
registration expires after one day and can be reclaimed earlier at capacity.
Successful code redemption gives the client at least 90 days from the
corresponding consent.
DCR accepts only code/refresh grant metadata. CIMD documents
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
With an empty allowlist the identifiers need not be on a host list: pinned
documents are never fetched. Invalid configuration refuses OAuth startup. Each exact configured identity
uses that registration instead of a live metadata fetch; unknown identities
still use normal discovery. No client gains account access from registration.

Both staging profiles pin Claude web's identity and its exact
`https://claude.ai/api/mcp/auth_callback` callback, reviewed on 2026-10-31.
The native CLI follow-up also prepares the distinct
`https://claude.ai/oauth/claude-code-client-metadata` identity with
`http://localhost/callback`. That identity and callback were observed from
installed Claude Code 2.1.316; its official documentation describes this
loopback form. Live metadata retrieval for both Claude identities returned
HTTP 403 from Fly with oCMS's Go transport. This explicit registration follows
[CIMD pre-registration](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document#section-7.2);
it preserves S256, fresh login/MFA, consent, scopes and revocation, with DCR
disabled. Update the registration only after reviewing the provider's public
identity. Registrations use the existing fifteen-minute SQLite metadata
cache; removing a pin resumes live discovery after that cache expires.
Revoke account grants separately to remove existing access immediately.

### Protected dynamic registration

Set `OCMS_OAUTH_ALLOW_DCR=true` and supply a separate
`OCMS_OAUTH_DCR_INITIAL_ACCESS_TOKEN` to restrict who can register. Generate
the token with `openssl rand -hex 32`, store it as a server secret and give
it privately to clients that support initial access tokens. They send it
in the `Authorization: Bearer <token>` header on `POST /oauth/register`.
Use HTTPS outside development loopback. Malformed configured values
prevent startup.

Requests without a valid token cannot consume registration slots or
admission budgets in protected mode. Clients with the token still share
the same limits. The token is for registration only. Previously
registered clients continue to authorize, exchange, refresh and revoke
as usual. Rotating the setting and restarting changes who can register
next. Discovery still advertises the registration endpoint when enabled.
Keep the header out of proxy logs.

Leave DCR disabled when CIMD or pre-registered clients cover the intended
integrations. If clients need DCR, configure an initial access token.

## Draft authoring and scope upgrades

When draft authoring is enabled, the first MCP OAuth challenge asks for
`mcp:access` and `mcp:drafts` together. Clients that follow the challenge can
request both permissions in one sign-in and consent flow. Existing grants
continue to use their previously approved scopes.

R1.5 removes the temporary compatibility probe and its flag. Actual draft
creation/update/translation now uses the shared writer, an admin authoring
toggle and `mcp:drafts`; see [Draft authoring](draft-authoring.md).
Missing tool scope returns HTTP 403 with a Bearer `insufficient_scope`
challenge naming `mcp:access`, `mcp:drafts` and resource metadata. Clients
request fresh consent and retry; read-only grants do not expand automatically.
API keys retain their existing tool errors and source/expiry policies.
`OCMS_AI_DISABLED=true` blocks draft operations while retaining general reads.

## Public staging and logs

Use a separate database, HTTPS host, fresh staging secrets and required MFA.
For authoring acceptance, use `OCMS_ENV=staging` and leave demo mode off. DCR
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
Independent volumes have independent SQLite state; page writes and replay
records must use the same persistent database. See [Fly configuration](https://docs.fly.io/reference/configuration),
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
OCMS_AI_DISABLED=false
OCMS_DEMO_MODE=false
OCMS_REQUIRE_MFA=true
OCMS_DO_SEED=false
```

`fly.staging.bootstrap.toml` has no public service. On this first boot only,
`OCMS_DO_SEED=true`, OAuth and AI authoring are disabled, and development mode permits
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
  call. Codes, state, PKCE verifiers, bearer/refresh credentials and article
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

The oCMS access logger strips OAuth query strings, and CSP violation reports
are logged without URL queries or fragments; events record only grant,
client, user and scope identifiers, the client address (forwarding headers
count only from a trusted proxy) and the request path, never its query or
fragment. Do not enable HTTP body/header debug
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

## Acceptance evidence and pending authoring checks

R1.1 OAuth acceptance is complete on the commits recorded in PHASE5, including
Claude Desktop/web, ChatGPT web, both CLI OAuth/bearer lifecycles and external
IPv6/TLS/forwarding verification. The retired compatibility probe established
authentication/scope/revocation behavior, not real CMS draft persistence.

R1.5 tests actual CMS draft flows with the Go SDK and isolated HTTP fixtures.
Fresh Claude Desktop/ChatGPT cloud authoring acceptance remains pending a
separately approved staging deployment. Record client/browser versions, OS,
account tier, transport, auth mode, deployed commit and module settings.
Use the dedicated development account and shared authenticator; preserve the
owner's existing connections. Test create → read → metadata/body update →
retained retry → linked translation → revocation → denied next call → reconnect.
Use two grants for the same user to continue one draft. Verify another user's
and non-opted-in human drafts remain inaccessible; publishing stays manual.
Default preview URLs require an editor session; sharing remains unavailable
until R1.6. Record unavailable client capabilities as pending, never passed.
