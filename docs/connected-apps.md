# Connected apps and OAuth retention

R1.7 is deployed to MCP staging at `df6e5d86` (2026-11-02). Bounded Claude web
and ChatGPT web checks passed for these management/maintenance paths; see the
[staging record](../.claude/docs/PHASE5.md#r17-fly-staging-and-web-client-acceptance--2026-11-02).
Native Desktop authoring and Claude Code/Codex OAuth and bearer checks also
passed on that build, with a Chrome workaround for Desktop reconnect; see the
[native/CLI record](../.claude/docs/PHASE5.md#r17-native-desktop-and-cli-acceptance--2026-11-02).
Native-only Desktop reconnect and full release acceptance remain open.

## Account and administrator controls

Account → Connected apps (`/account/connected-apps`) lists the signed-in
account's OAuth grants and API keys with `mcp:access`, including revoked,
expired and currently unavailable entries. Each class has independent
pagination, 25 rows per page. The view includes registered app/key name,
client metadata host/ID for OAuth, granted scopes, creation, last use and
expiry. API keys have no recorded client host. Registered names/hosts do not
verify a vendor; no raw credential, hash, lookup or key prefix is rendered.

The administrator's `/admin/mcp` overview adds account attribution and permits
revocation across accounts. Account management never gains that authority,
even for an administrator using their personal account page. It works with
the issuer disabled so old grants can still be revoked. A public-role account
may remove its own old access but cannot use MCP or manage another account.

Revocation accepts only a bounded form-body selector behind normal session,
current-user and CSRF protection. The service rechecks the actor's credential
version and current role inside the write transaction, then enforces target
ownership or explicit admin mode. Missing and foreign targets return 404.
Repeating revocation is safe; the grant's first revocation time is retained.
API keys are deactivated, not deleted. Security events record the actor and
non-secret selector. Demo mode blocks these credential mutations.

Revoking a grant/key rejects the next MCP request and invalidates its preview
links through the existing lifecycle triggers. It does not undo committed
content or delete drafts/revisions/review records. Already queued writes must
still pass the shared transaction-time authorization check. Reconnecting is a
separate normal login/MFA/consent flow; this page creates no credentials.

OAuth last use includes successful credential validation and token exchange,
with access-validation touches throttled to once per minute. API-key last use
retains the existing asynchronous authentication timestamp. Neither timestamp
proves a content mutation. Current policy, role, scopes, expiry, CIDR and
anomaly rules still govern access after a connection is listed.

## Retention policy

The core scheduler registers `core:oauth_cleanup` every 15 minutes,
independently of whether the OAuth issuer is enabled. Existing scheduler
overrides and manual triggering apply. No new environment variable is needed.
One run has a five-second context deadline and one atomic transaction:

| Record class | Eligibility | Maximum deletions per run |
|---|---|---|
| Authorization codes | Expired for at least 24 hours | 250 |
| Token rows | Family expired or first revoked at least 30 days ago | 250 |
| OAuth-issued preview links | Same retired-family condition | 250 |
| Grant families | Same condition and no remaining code/token/preview children | 250 |
| Client metadata | Expired for at least 24 hours and no remaining grant | 250 |

The terminal family boundary is the earlier of absolute expiry and first
revocation. A recent revocation/expiry retains evidence. Every token row of
every still-active family is preserved, including spent refresh hashes and
expired/revoked access rows. Cleanup must never turn a matching-client refresh
replay into an unknown token that cannot revoke its active family.

Child records are drained in bounded batches before a grant is deleted, so
foreign-key cascades cannot expand that deletion without limit. A large family
may need several runs. Each run is repeatable; any statement/commit failure
rolls back all phases and reports no committed deletions. Logs contain counts
and failures, not credentials or article bodies. Unused expired client-cache
entries may still be evicted earlier by the existing registration-cap guard.

The new migration adds indexes only; its rollback retains credentials and
content. Cleanup deletions themselves are not undone by rolling that migration
back. Use the site's normal database backup/recovery policy for retention data.
Other page provenance and idempotency policies remain unchanged.

## Verification and release boundary

Local tests cover ownership/admin isolation, stale actor versions/roles,
pagination, metadata projections, strict form parsing, CSRF, repeat revocation,
normal and revoked MCP access, preview invalidation, retention boundaries,
bounded child draining, concurrent refresh, replay after cleanup, cancellation,
statement/commit rollback and index down/up preservation. English/Russian
browser checks in that local suite used synthetic accounts, not staging
credentials.

On Fly staging, the dedicated Editor's English/Russian account views, language
return path, administrator denial, own-grant revocation/share invalidation and
fresh Claude/ChatGPT reconnects passed. Both restored clients read existing
drafts with exactly `mcp:access mcp:drafts`; owner connections were unchanged.
Manual cleanup removed nine expired codes, its repeat removed none, and an
automatic batch removed one more; 32 active-family token rows were unchanged.
Live aged-family draining, refresh replay and administrative cross-account or
API-key revocation were not exercised by those web checks; local coverage is
listed above. Both test shares and the temporary base grant were revoked.

The later native/CLI pass confirmed revoked MCP startup requests received 401
in both CLIs' OAuth and bearer modes. Fresh OAuth reconnects issued new grants
and restored draft reads. Two dedicated 90-minute keys with exactly
`mcp:access mcp:drafts` and the Mac's public IPv4 `/32` restriction were
deactivated; independent probes also returned 401. Native Desktop's own-grant
revocation rejected its read and invalidated its share. Chrome web settings
and shared-chat consent restored the connection, including the draft-scope
upgrade; fresh native reads passed. Its native-only reconnect remains
unverified. Owner grants, older drafts and site settings were preserved.

The full two-target multilingual demo with existing taxonomy/media, native-only
Desktop reconnect, the complete publish/schedule failure matrix, publication
by the human editor, independent security audit and release recording remain
pending. See the
[phase notes](../.claude/docs/PHASE5.md), [client checklist](desktop-authoring.md),
[OAuth contract](oauth.md) and [demo script](launch/demo-script.md).
