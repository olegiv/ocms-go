# Connected apps and OAuth retention

R1.7's latest verified MCP staging build is `6c611040` (2026-11-05).
An ordinary publication save preserved the HTML and translation freshness.
On `be0e9cc3`, dedicated Editor login and normal translation review
passed at revisions EN 27/7, RU 43/6 and DE 44/5. The owner later approved
and manually published them on 2026-11-05. Final revisions 9/7/6 are
Published, with both translations Current; see the
[publication record](launch/r17-acceptance.md#human-staging-publication-and-final-demo--2026-11-05)
and earlier
[rollout record](launch/r17-acceptance.md#staging-translation-review-verification-on-be0e9cc3--2026-11-04).
Existing OAuth grants and owner connections were preserved. This focused
rollout did not repeat provider reconnect or credential lifecycle checks.

On `ba044434`, fresh Claude web and ChatGPT web own-grant
revoke/refusal/password/shared-MFA/consent reconnect and draft readbacks
passed with exactly `mcp:access mcp:drafts`.
The temporary Claude access-only grant and all new test shares were revoked;
the final two-scope connections remain active. See the
[upgrade record](launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04).
The [current acceptance record](launch/r17-acceptance.md) also retains full
EN/RU/DE draft flows and fresh web revocation/reconnect evidence on
`39d82c94`. The `16ac7161` follow-up verified repaired inline alt text through
Claude web, ChatGPT web and rendered previews of the resaved demo drafts.
Earlier bounded Claude web and ChatGPT web checks passed on `df6e5d86`
for these paths; see the
[staging record](../.claude/docs/PHASE5.md#r17-fly-staging-and-web-client-acceptance--2026-11-02).
Native Desktop authoring and Claude Code/Codex OAuth and bearer checks also
passed on that build, with a Chrome workaround for Desktop reconnect; see the
[native/CLI record](../.claude/docs/PHASE5.md#r17-native-desktop-and-cli-acceptance--2026-11-02).
The [final ba044434 follow-up](launch/r17-acceptance.md#final-native-and-cli-verification--2026-11-04)
passed native Desktop OAuth and both CLIs' OAuth/bearer modes. Native Connect
completed the normal browser confirmation, password/shared MFA, consent and
Desktop callback; the earlier workaround/pending label is superseded.
Owner approval, staging publication and the final demo walkthrough are
complete. The editor preservation rollout and launch draft are recorded
in the [release decisions](launch/release-decisions.md).

## Account and administrator controls

Account → Connected apps (`/account/connected-apps`) lists the signed-in
account's OAuth connections and API keys with `mcp:access`, including revoked,
expired and currently unavailable entries. A grant is created at consent but is
a connection only once its code has been exchanged for a token; grants that
never issued one are not listed (nor in the `/admin/mcp` overview or the older
account security list), though revoking one by ID still works. Each class has independent
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
| Unredeemed grants | Created at least 24 hours ago, no token and no preview link (the code row goes with it) | 250 |
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

A dynamically registered client expires one day after registration unless a
grant is created for it; consent extends it to 90 days. A client that is never
used therefore stops resolving after a day, is reclaimed at once when the
registry is full, and is removed by the client-metadata row above a day after
that. Unredeemed grants are removed before client metadata, so an abandoned
consent does not keep its client alive.

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

The earlier `df6e5d86` native/CLI pass confirmed revoked MCP startup requests
received 401 in both CLIs' OAuth and bearer modes. Fresh OAuth reconnects
issued new grants and restored draft reads. Two dedicated 90-minute keys with exactly
`mcp:access mcp:drafts` and the Mac's public IPv4 `/32` restriction were
deactivated; independent probes also returned 401. Native Desktop's own-grant
revocation rejected its read and invalidated its share. Chrome web settings
and shared-chat consent restored the connection, including the draft-scope
upgrade; fresh native reads passed. That run did not establish native-initiated
reconnect. Owner grants, older drafts and site settings were preserved.

The final `ba044434` pass confirmed native-initiated reconnect and both CLIs'
OAuth revoke/refusal/reconnect with new grants and fresh draft readbacks.
The temporary native access-only grant was revoked; final two-scope OAuth
connections retain their normal 30-day refresh duration. Both new 90-minute,
IPv4 `/32`-restricted bearer keys were deactivated, with direct and actual
CLI startup HTTP 401 checks. Owner connections and original content were
preserved. Full EN/RU/DE and publication/lifecycle coverage retain their
earlier build-specific evidence. The `be0e9cc3` rollout completes staging
content/translation review. The later manual publication and final demo
walkthrough are complete. The editor preservation fix passed on
`6c611040`, and the article was saved as an unscheduled production draft. See the
[phase notes](../.claude/docs/PHASE5.md), [client checklist](desktop-authoring.md),
[OAuth contract](oauth.md) and [demo script](launch/demo-script.md).
