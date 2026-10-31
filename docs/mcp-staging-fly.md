# Deploy MCP staging to Fly.io

Owner-run procedure for `https://mcp-staging.ocms.tech`, app
`ocms-mcp-staging`, region `fra`. The commands below create resources and
deploy when **you** run them. Preparing this guide does not execute them.
The existing `ocms-demo`, `fly.toml`, demo deployment script and the production host are
outside this procedure.

| File | Role |
|---|---|
| `fly.staging.bootstrap.toml` | Fresh app only: private first boot, seeding and MFA enrollment; no public service |
| `fly.staging.toml` | Normal staging: HTTPS, OAuth/scratch enabled, MFA required, seeding/demo/DCR off |

Both profiles use the same app, process, 512 MB Machine and 1 GB volume.
The bootstrap uses development mode solely for HTTP cookies on the private
loopback tunnel. The public profile switches to staging mode and secure
cookies. Dual-stack `[::]:8080` supports both Fly Proxy and the private 6PN
tunnel. There is no release command: oCMS migrates SQLite on startup on the
Machine that actually mounts the volume. The public service's readiness check
gates request routing; named top-level checks provide additional monitoring.

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
export STAGING_HOST=mcp-staging.ocms.tech
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
3. Enable **MCP Server** under **Modules**. OAuth/scratch remain disabled in
   the bootstrap profile even after module activation.
4. Create a dedicated editor with a strong password; sign in as that editor
   in a separate browser session and enroll MFA. Add a small published test
   page and an ordinary draft, or import a reviewed content-only export.
   Use no production user/session/token database.

Both accounts and the MFA enrollment persist on the volume through promotion.
Finish this bootstrap before the next deploy. Close the tunnel with Ctrl-C
when you have finished; it is only a local connection.

## 5. Attach the hostname and prepare DNS

Back in the first Bash session:

```bash
flyctl ips allocate-v4 --shared --app "$STAGING_APP"
flyctl ips allocate-v6 --app "$STAGING_APP"
flyctl certs add "$STAGING_HOST" --app "$STAGING_APP"
flyctl certs setup "$STAGING_HOST" --app "$STAGING_APP"
```

In your DNS GUI, apply the **actual records returned by `certs setup`** for
`mcp-staging.ocms.tech`, including any ownership/challenge records. Replace
the existing staging subdomain's conflicting A/AAAA records if choosing the
supplied CNAME. Keep the apex `ocms.tech` records unchanged. Use direct DNS
routing for this initial Fly acceptance pass. Certificate validation may
remain pending until the public service is running.

## 6. Promote the same image to public staging

The seeded credentials must already have been replaced. This changes the
runtime profile, not the tested image or database. It enables public HTTPS,
OAuth and scratch tools with MFA required, demo/DCR/seeding off.

```bash
flyctl deploy "$STAGING_CONTEXT" \
  --config "$STAGING_CONTEXT/fly.staging.toml" \
  --app "$STAGING_APP" --ha=false --strategy rolling --image "$STAGING_IMAGE"

flyctl machines list --app "$STAGING_APP"
test "$(flyctl machines list --quiet --app "$STAGING_APP" | wc -w | tr -d '[:space:]')" -eq 1
flyctl volumes list --app "$STAGING_APP"
flyctl checks list --app "$STAGING_APP"
flyctl certs check "$STAGING_HOST" --app "$STAGING_APP"
flyctl ssh console --app "$STAGING_APP" --command '/app/ocms -version'
```

Confirm the original volume remains attached, checks pass and the certificate
is ready. Re-run certificate/check commands while provisioning completes;
never bypass certificate verification. Then:

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
and scratch content; retain no secret values in the acceptance record. Keep
HTTP body/header debug logging off. This Fly check is separate from the
later nginx verification on the production host.

## 8. Run and record the client matrix

Use `https://mcp-staging.ocms.tech/api/mcp`. For Claude Desktop, claude.ai,
ChatGPT web, Claude Code and Codex, record date, client/version, account tier,
OS/browser, commit/image, settings and actual GUI availability. CLI clients
also need their bearer-key mode checked: create expiring `mcp:access` keys
with appropriate per-key source CIDRs; add `mcp:drafts` for the scratch probe.
`pages:read` still needs the MCP drafts visibility setting.

Test connect → fresh sign-in/MFA → consent → list/read → approve scratch save
→ retrieve → revoke in Account → Security → next request fails → reconnect.
Record unavailable capabilities and failures explicitly in the PHASE5 R1.1
matrix. R1.2 stays gated on recorded results and the required passes or an
explicit owner scope decision. DCR and unsupported client authentication
methods remain off while failures are diagnosed.

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

Repeat health, issuer, proxy and log verification. Redeploying clears the
in-memory scratch entries; it must preserve accounts, MFA and OAuth state
on the volume. Retain the tested commit/image references for comparison and
recovery planning. Do not blindly roll an older binary back across a database
migration.

## Validation and scope

The prepared profiles are checked with Fly's strict config validator and
configuration tests. These commands were checked against installed Fly CLI
help and the official [configuration](https://docs.fly.io/reference/configuration),
[network services](https://docs.fly.io/networking/app-services),
[proxy tunnel](https://docs.fly.io/flyctl/proxy),
[staged secrets](https://docs.fly.io/flyctl/secrets-import) and
[deploy](https://docs.fly.io/flyctl/deploy) documentation.
App-name/region capacity, remote image builds, bootstrap on Fly, actual proxy
addresses, DNS/certificate issuance and real-client acceptance are verified
when the owner runs the procedure; preparation is not evidence of those passes.
