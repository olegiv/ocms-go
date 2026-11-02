# Deploy MCP staging to Fly.io

Owner-run procedure for `https://ocms-mcp-staging.fly.dev`, app
`ocms-mcp-staging`, region `fra`. The commands below create resources and
deploy when **you** run them. Preparing this guide does not execute them.
The existing `ocms-demo`, `fly.toml`, demo deployment script and the production host are
outside this procedure.

| File | Role |
|---|---|
| `fly.staging.bootstrap.toml` | Fresh app only: private first boot, seeding and MFA enrollment; no public service |
| `fly.staging.toml` | Normal staging: HTTPS, OAuth enabled, draft authoring default-off, MFA required, seeding/demo/DCR off |

Both profiles use the same app, process, 2 shared vCPU / 1 GB Machine and
1 GB volume. Go's memory limit is 800 MiB, leaving room for the runtime OS.
The bootstrap uses development mode solely for HTTP cookies on the private
loopback tunnel. The public profile switches to staging mode and secure
cookies. Dual-stack `[::]:8080` supports both Fly Proxy and the private 6PN
tunnel. There is no release command: oCMS migrates SQLite on startup on the
Machine that actually mounts the volume. The public service's readiness check
gates request routing; named top-level checks provide additional monitoring.
Use the app's Fly-managed HTTPS hostname for R1.1. A custom domain is optional;
this procedure requires no changes to the `ocms.tech` DNS zone.

Both profiles include an explicit, owner-reviewed public Claude registration
in `OCMS_OAUTH_PRE_REGISTERED_CLIENTS`. The Fly Machine received HTTP 403
when fetching Claude's metadata URL. The pin supplies only the verified
client identity and exact callback; login, MFA, S256 PKCE, consent and grant
revocation remain required. DCR stays disabled. Review the public identity
before changing the pin; see [OAuth client registration](oauth.md#client-metadata-and-registration).

## 1. Prepare the reviewed source

Use a Bash terminal in the repository root. First finish review and your
separately approved R1.1 commit, including these files. These commands
deliberately reject `master`/`main` and uncommitted root changes. They contain
no commit or push operation. Pending wiki submodule edits do not enter the
build; the image context is an archive of tracked files from one commit,
excluding ignored local modules, credentials and working files.

```bash
bash
```

Run the following blocks in that Bash session. Keep it open through the
deployment; the exported variables are reused.

```bash
set -euo pipefail
command -v flyctl
command -v git
command -v openssl
command -v curl
command -v jq
command -v rg

export STAGING_APP=ocms-mcp-staging
export STAGING_HOST=ocms-mcp-staging.fly.dev
export STAGING_ORIGIN="https://$STAGING_HOST"
STAGING_BRANCH="$(git branch --show-current)"
export STAGING_BRANCH
case "$STAGING_BRANCH" in
  ''|master|main) printf 'Use the reviewed R1.1 branch first.\n' >&2; exit 1 ;;
esac
test -z "$(git status --porcelain --untracked-files=all --ignore-submodules=all)"
scripts/test-gate.sh

STAGING_REV="$(git rev-parse HEAD)"
export STAGING_REV
STAGING_CONTEXT="$(mktemp -d "${TMPDIR:-/tmp}/ocms-mcp-staging.XXXXXX")"
export STAGING_CONTEXT
git archive --format=tar "$STAGING_REV" | tar -xf - -C "$STAGING_CONTEXT"
test -f "$STAGING_CONTEXT/fly.staging.toml"
test -f "$STAGING_CONTEXT/fly.staging.bootstrap.toml"
flyctl config validate --strict --config "$STAGING_CONTEXT/fly.staging.toml"
flyctl config validate --strict --config "$STAGING_CONTEXT/fly.staging.bootstrap.toml"
printf 'Branch: %s\nCommit: %s\nBuild context: %s\n' \
  "$STAGING_BRANCH" "$STAGING_REV" "$STAGING_CONTEXT"
```

Keep this commit/context for every step of the first deployment. The current
uncommitted implementation is not an exception to the source gate.

## 2. Create the separate app and volume

These are first-install commands. A name collision or existing staging
resources need inspection; do not substitute `ocms-demo` or rerun creation
blindly. Pick your organization slug from the list.

```bash
flyctl auth login
flyctl orgs list
printf 'Fly organization slug: '
IFS= read -r STAGING_ORG
test -n "$STAGING_ORG"
flyctl apps create "$STAGING_APP" --org "$STAGING_ORG"
flyctl volumes create ocms_staging_data --app "$STAGING_APP" \
  --region fra --size 1 --count 1
flyctl volumes list --app "$STAGING_APP"
flyctl ips list --app "$STAGING_APP"
```

There must be one new volume, not a production/demo volume or snapshot.
Public IPs are not needed for bootstrap. Absence of public service/port
configuration is the bootstrap ingress boundary.

## 3. Set and retain the two secrets

Generate and save **two distinct random 64-character values** in your password
manager: a session secret and an MFA sealing key. Retain the MFA key with
your staging recovery information; losing or replacing it breaks enrolled
authenticators. Keep both values out of repository files and transcripts.

This block reads hidden input directly from your terminal and sends it to
Fly on stdin. Staging the secrets does not deploy. Run it once for this new
app; reuse the same secrets across bootstrap, public staging and updates.

```bash
(
  set +x
  printf 'Session secret from password manager: ' >&2
  IFS= read -r -s STAGING_SESSION_SECRET </dev/tty
  printf '\nMFA sealing key from password manager: ' >&2
  IFS= read -r -s STAGING_MFA_KEY </dev/tty
  printf '\n' >&2
  test "${#STAGING_SESSION_SECRET}" -ge 32
  test "${#STAGING_MFA_KEY}" -ge 32
  test "$STAGING_SESSION_SECRET" != "$STAGING_MFA_KEY"
  printf 'OCMS_SESSION_SECRET=%s\nOCMS_MFA_KEY=%s\n' \
    "$STAGING_SESSION_SECRET" "$STAGING_MFA_KEY" |
    flyctl secrets import --stage --app "$STAGING_APP"
)
flyctl secrets list --app "$STAGING_APP"
```

At this point only those two secret names are expected. Later,
`OCMS_TRUSTED_PROXIES` is added after observing ingress. Do not place env-mode,
seeding, demo or MFA switches in secrets: they would override the TOML.

## 4. Bootstrap privately

This is the **only** deploy that uses the bootstrap profile. It creates the
initial admin on the attached volume with public routing absent.

```bash
flyctl deploy "$STAGING_CONTEXT" \
  --config "$STAGING_CONTEXT/fly.staging.bootstrap.toml" \
  --app "$STAGING_APP" --ha=false --strategy rolling --remote-only \
  --build-arg "VERSION=mcp-staging-${STAGING_REV:0:12}" \
  --build-arg "GIT_COMMIT=$STAGING_REV" \
  --build-arg "BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

flyctl machines list --app "$STAGING_APP"
test "$(flyctl machines list --quiet --app "$STAGING_APP" | wc -w | tr -d '[:space:]')" -eq 1
flyctl volumes list --app "$STAGING_APP"
flyctl config show --app "$STAGING_APP" |
  jq -e '(.http_service == null) and ((.services // []) | length == 0)'
flyctl ssh console --app "$STAGING_APP" --command '/app/ocms -version'
flyctl image show --app "$STAGING_APP"
```

Confirm one Machine, one attached volume, no public services and the expected
commit in `-version`. Keep the exact image reference from the successful
deployment/image details for promotion; prefer its digest when available.
Enter the full reference, not an image from another app:

```bash
printf 'Bootstrap image reference (registry.fly.io/ocms-mcp-staging:... or @sha256:...): '
IFS= read -r STAGING_IMAGE
case "$STAGING_IMAGE" in
  "registry.fly.io/$STAGING_APP:"*|"registry.fly.io/$STAGING_APP@sha256:"*) ;;
  *) printf 'Expected an image in this staging app registry.\n' >&2; exit 1 ;;
esac
export STAGING_IMAGE
```

Open a **second terminal** and keep this tunnel running:

```bash
flyctl proxy 18081:8080 --app ocms-mcp-staging --bind-addr 127.0.0.1
```

Open `http://127.0.0.1:18081/login` in your browser. Complete these private
steps before enabling public routing:

1. Sign in as `admin@example.com` / `changeme1234`. Enroll the required
   authenticator and retain its recovery codes privately.
2. In **Users**, change the seeded admin's email and password to your own
   non-default credentials. Enter the acting admin's current password when
   requested. Sign out and verify the new login plus MFA; the default login
   must fail.
3. Enable **MCP Server** under **Modules**. OAuth remains disabled and draft authoring stays off in
   the bootstrap profile even after module activation.
4. Create a dedicated editor with a strong password; sign in as that editor
   in a separate browser session and enroll MFA. Add a small published test
   page and an ordinary draft, or import a reviewed content-only export.
   Use no production user/session/token database.

Both accounts and the MFA enrollment persist on the volume through promotion.
Finish this bootstrap before the next deploy. Close the tunnel with Ctrl-C
when you have finished; it is only a local connection.

If the owner defers the dedicated editor for the bounded R1.1 probe, record
that decision and the actual admin role in the acceptance matrix. The admin
must still have non-default credentials and MFA. Leave editor-role manual
coverage unverified; this exception does not authorize broader MCP scopes or
CMS writes.

## 5. Prepare Fly public ingress

Back in the first Bash session:

```bash
flyctl ips allocate-v4 --shared --app "$STAGING_APP"
flyctl ips allocate-v6 --app "$STAGING_APP"
flyctl ips list --app "$STAGING_APP"
```

Fly manages DNS and TLS for `ocms-mcp-staging.fly.dev`. Do not add a custom
certificate for that hostname or change the `ocms.tech` zone. These address
allocation commands are for a new app; retain the existing addresses on later
deployments. Public routing begins when the public profile is deployed.

## 6. Promote the same image to public staging

The seeded credentials must already have been replaced. This changes the
runtime profile, not the tested image or database. It enables public HTTPS,
OAuth with MFA required and draft authoring off by default, demo/DCR/seeding off.

```bash
flyctl deploy "$STAGING_CONTEXT" \
  --config "$STAGING_CONTEXT/fly.staging.toml" \
  --app "$STAGING_APP" --ha=false --strategy rolling --image "$STAGING_IMAGE"

flyctl machines list --app "$STAGING_APP"
test "$(flyctl machines list --quiet --app "$STAGING_APP" | wc -w | tr -d '[:space:]')" -eq 1
flyctl volumes list --app "$STAGING_APP"
flyctl checks list --app "$STAGING_APP"
flyctl ssh console --app "$STAGING_APP" --command '/app/ocms -version'
```

Confirm the original volume remains attached, checks pass and the certificate
is trusted at the Fly hostname. Re-run health checks while provisioning
completes; never bypass certificate verification. Then:

```bash
curl --fail --silent --show-error "$STAGING_ORIGIN/health/live"
curl --fail --silent --show-error "$STAGING_ORIGIN/health/ready"
curl --fail --silent --show-error \
  "$STAGING_ORIGIN/.well-known/oauth-authorization-server" |
  jq -e --arg issuer "$STAGING_ORIGIN" \
    '.issuer == $issuer and .authorization_response_iss_parameter_supported == true'
curl --fail --silent --show-error \
  "$STAGING_ORIGIN/.well-known/oauth-protected-resource/api/mcp" |
  jq -e --arg resource "$STAGING_ORIGIN/api/mcp" --arg issuer "$STAGING_ORIGIN" \
    '.resource == $resource and .authorization_servers == [$issuer]'
curl --silent --show-error --dump-header - --output /dev/null \
  --request POST "$STAGING_ORIGIN/api/mcp" \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' --data '{}'
```

The last request must return 401 with `WWW-Authenticate` containing the HTTPS
`resource_metadata` URL. A 404 means MCP is inactive; stop and correct it.

## 7. Verify the Fly proxy and logs

Do this before connecting the real clients. The first probe intentionally has
no cookie, bearer token or OAuth query parameters. With proxy trust initially
unset, its access-log `from` field identifies the original peer:

```bash
STAGING_PROBE="peer-$(openssl rand -hex 8)"
export STAGING_PROBE
curl --fail --silent --show-error \
  "$STAGING_ORIGIN/health/live?staging_probe=$STAGING_PROBE"
flyctl logs --no-tail --app "$STAGING_APP" | rg --fixed-strings "$STAGING_PROBE"
flyctl ips list --app "$STAGING_APP"
```

Normal access logs show the resolved peer, not the raw forwarding headers.
Use that peer and Fly's documented forwarded chain to determine narrow
trusted peer/proxy addresses. The rightmost forwarded hop can be an app
public address; trusting only one observed peer is insufficient evidence.
oCMS currently reads `X-Forwarded-For`, not `Fly-Client-IP`. Do not guess a
global trust range. If the actual chain needs further inspection, collect only
the peer and IP headers on a controlled non-secret request before continuing;
do not enable general request/header logging. Enter the verified
addresses/CIDRs, comma separated:

```bash
printf 'Verified Fly peer/proxy IPs or CIDRs, comma separated: '
IFS= read -r STAGING_PROXY_CIDRS
test -n "$STAGING_PROXY_CIDRS"
printf 'OCMS_TRUSTED_PROXIES=%s\n' "$STAGING_PROXY_CIDRS" |
  flyctl secrets import --stage --app "$STAGING_APP"
flyctl deploy "$STAGING_CONTEXT" \
  --config "$STAGING_CONTEXT/fly.staging.toml" \
  --app "$STAGING_APP" --ha=false --strategy rolling --image "$STAGING_IMAGE"

STAGING_PROBE="client-$(openssl rand -hex 8)"
export STAGING_PROBE
curl --fail --silent --show-error \
  "$STAGING_ORIGIN/health/live?staging_probe=$STAGING_PROBE"
flyctl logs --no-tail --app "$STAGING_APP" | rg --fixed-strings "$STAGING_PROBE"

STAGING_PROBE="spoof-$(openssl rand -hex 8)"
export STAGING_PROBE
curl --fail --silent --show-error \
  --header 'X-Forwarded-For: 203.0.113.77' \
  --header 'X-Real-IP: 203.0.113.77' \
  "$STAGING_ORIGIN/health/live?staging_probe=$STAGING_PROBE"
flyctl logs --no-tail --app "$STAGING_APP" | rg --fixed-strings "$STAGING_PROBE"
```

The access-log **resolved `from` field** must be your actual external client
IP, including for the spoofed request, and stay correct after redeployment.
It must not be the Fly peer or `203.0.113.77`. Sign in with MFA on the public
hostname and confirm the auth event has the same expected client IP. If the
observed Fly chain cannot be handled safely or the peer changes outside the
verified trust set, stop client acceptance and resolve that configuration or
middleware gap; do not widen trust to all addresses.

After a complete OAuth sign-in/token exchange and MCP call, inspect locally:

```bash
flyctl logs --no-tail --app "$STAGING_APP"
```

Record whether logs exclude raw code, state, verifier, access/refresh tokens
and article content; retain no secret values in the acceptance record. Keep
HTTP body/header debug logging off. This Fly check is separate from the
later nginx verification on the production host.

## 8. Run and record the client matrix

Use `https://ocms-mcp-staging.fly.dev/api/mcp`. For Claude Desktop, claude.ai,
ChatGPT web, Claude Code and Codex, record date, client/version, account tier,
OS/browser, commit/image, settings and actual GUI availability. CLI clients
also need their bearer-key mode checked: create expiring `mcp:access` keys
with appropriate per-key source CIDRs; add `mcp:drafts` for authorized draft authoring.
`pages:read` still needs the MCP drafts visibility setting.

R1.1 compatibility acceptance is complete, including its retired temporary
probe. For separately authorized R1.5 deployments, enable draft authoring in
Admin → MCP Server, grant only required scopes and follow
[desktop authoring acceptance](desktop-authoring.md). Test connect → fresh
sign-in/MFA → consent → context → owned draft save/read/update/translation
→ revoke → next request fails → reconnect. Use stable request IDs and current
revisions. Check anonymous preview denial; explicit sharing is available from
R1.6. Staging now runs `31c55f8c9f67` (2026-11-02), including the fragment/POST
transport fix and startup legacy-link revocation migration. Earlier web-client
draft/review results on `ff4de3b1368c` and fresh sharing results on the current
build are recorded in PHASE5. The earlier error-canary check showed Fly Proxy
recording a full legacy preview path before oCMS redaction; the new transport
keeps bearer credentials out of that request target.

Fresh Claude web and ChatGPT web links rendered their pinned snapshots in an
anonymous browser, including ChatGPT's tracking-query link. Browser requests
contained no bearer in URLs or referrers; redemption used a fixed-endpoint
POST body. Session previews stayed private. Revocation and controlled expiry
returned 404, and both new links are revoked. Eight real proxy errors and
all 247 captured app/proxy records contained neither test bearer. The proxy
records had empty URL fields; this does not prove proxy redaction. The error
probes replayed the observed targets with non-credential content because the
browser controller could not inject the Fly test header. See the
[rollout record](../.claude/docs/PHASE5.md#r16-preview-transport-fly-rollout--2026-11-02)
for exact image/snapshot identity, evidence limits and cleanup.

For subsequent approved deployments, exclude concurrent old URL-issuing
writers, let startup migrate first, and repeat the fixed-path upstream-error
and real-client sharing checks. Keep HTTP body dumps disabled. Record actual
client/version/build/results in PHASE5 and mark unavailable capabilities
pending. Native Desktop/CLI authoring acceptance and the broader release
matrix remain R1.7 work. DCR and unsupported authentication methods remain
off. Do not reuse the historical R1.1 probe result as a CMS-write pass.

## Subsequent source updates

Do not rerun first-install creation, seed/bootstrap or secret generation.
After the next separately approved commit, repeat section 1 to archive the
new reviewed source, then build/deploy the **public** profile:

```bash
flyctl deploy "$STAGING_CONTEXT" \
  --config "$STAGING_CONTEXT/fly.staging.toml" \
  --app "$STAGING_APP" --ha=false --strategy rolling --remote-only \
  --build-arg "VERSION=mcp-staging-${STAGING_REV:0:12}" \
  --build-arg "GIT_COMMIT=$STAGING_REV" \
  --build-arg "BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
flyctl machines list --app "$STAGING_APP"
flyctl volumes list --app "$STAGING_APP"
flyctl checks list --app "$STAGING_APP"
flyctl ssh console --app "$STAGING_APP" --command '/app/ocms -version'
```

Repeat health, issuer, proxy and log verification. Redeploying must preserve
accounts, MFA, OAuth state, drafts, revisions, provenance and idempotency
records on the volume. The retired in-memory probe no longer exists. Retain the tested commit/image references for comparison and
recovery planning. Do not blindly roll an older binary back across a database
migration.

## Optional custom hostname later

Moving to `mcp-staging.ocms.tech` is a separate canonical-issuer change.
Review and test the new `OCMS_SITE_URL` in both profiles, attach the custom
hostname to this app, and use the actual DNS/verification records returned by
`flyctl certs setup`. Deploy only the public profile and reconnect clients to
the new issuer. Retain the existing secrets and volume. The original custom
hostname is deferred for R1.1; its DNS is outside the current deployment.

A configuration-only change may reuse an already reviewed image after
validating and recording the exact profile delta. It does not rebuild source
or authorize a Git commit/push. Source updates still use the clean commit
archive procedure above.

## Validation and scope

The prepared profiles are checked with Fly's strict config validator and
configuration tests. These commands were checked against installed Fly CLI
help and the official [configuration](https://docs.fly.io/reference/configuration),
[network services](https://docs.fly.io/networking/app-services),
[proxy tunnel](https://docs.fly.io/flyctl/proxy),
[staged secrets](https://docs.fly.io/flyctl/secrets-import) and
[deploy](https://docs.fly.io/flyctl/deploy) documentation.
App-name/region capacity, remote image builds, bootstrap on Fly, actual proxy
addresses, public TLS and real-client acceptance are verified
when the owner runs the procedure; preparation is not evidence of those passes.
