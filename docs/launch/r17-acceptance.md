# R1.7 release acceptance — updated 2026-11-05

The latest verified staging build is `be0e9cc3`, deployed on 2026-11-04.
Dedicated Editor password/shared-MFA login and the normal translation-review
action passed. RU 43/revision 6 and DE 44/revision 5 are both `current`
against unchanged EN 27/revision 7, confirmed in the editor and MCP.
All three remain private, unscheduled drafts. See the
[translation-review rollout](#staging-translation-review-verification-on-be0e9cc3--2026-11-04).

Earlier Claude web/ChatGPT web checks, native-initiated Desktop reconnect
and all four CLI OAuth/bearer paths passed on `ba044434`. Its dependency
revalidation found zero vulnerable imported Go packages/reachable symbols
and zero npm findings; the approved OpenPGP module-only warning remains
visible. Those results retain their build scope. The latest rollout
retained required MFA and the approved staging-only CAPTCHA exception for
zero public forms.
Klaro's interactive consent/SRI checks remain bound to `035ac1dd`, the full
EN/RU/DE workflow to `39d82c94`, and alt-text resaves to `16ac7161`. The final
native/CLI follow-up created EN/RU pairs; it did not repeat new DE creation.
**R1.7 is not release-complete:** final owner editorial approval, separately
authorized human publication, the final demo segment and the planned
production article draft remain pending. Staging content/translation
review is complete. See the
[scoped dependency dispositions](release-decisions.md#dependency-advisory-dispositions).

## Tested identities

- Fly app: `ocms-mcp-staging`, canonical origin
  `https://ocms-mcp-staging.fly.dev`.
- Latest verified deployed commit: `be0e9cc3793ed310ca3702cbce34759c99d3e659`;
  source tree `6a2a9ac1858163a40b2c219bae79e1b4d9a787ba`, binary build time
  `2026-11-04T22:22:20Z`, checked directly on the host.
  Fly readiness/liveness and both public health endpoints passed. MCP's
  generic server version `1.0.0` does not attest to a Git commit.
- Latest image digest:
  `[image digest redacted]`.
- Earlier web/native/CLI and dependency checks ran on
  `ba0444341691b567b3e128f59e7e4cb323125df8`;
  source tree `fd409ccfd133de7c8e5c0e5c4b0e8b3598f2eb20`, binary build time
  `2026-11-04T17:17:32Z`, checked directly on the host.
  Fly readiness/liveness and both public health endpoints passed. MCP's
  generic server version `1.0.0` does not attest to a Git commit.
- Earlier `ba044434` image digest:
  `[image digest redacted]`.
- Earlier Klaro checks ran on
  `035ac1ddf9b3a9abb6e4289d8545ec246cea1ac3`, built at
  `2026-11-02T22:25:49Z`, image
  `[image digest redacted]`.
- Earlier alt-text resaves/previews ran on
  `16ac71611e2e82e8a5789f0bf13145ef844301ef`, built at
  `2026-11-02T19:40:19Z`, image
  `[image digest redacted]`.
- The full multilingual client matrix and web sharing/revocation results
  below ran on `39d82c94f164452fed68157b77232815889403ba`, built at
  `2026-11-02T14:56:23Z`, image
  `[image digest redacted]`.
- Dedicated Editor user 2, `ocms-agent@example.test`; required MFA remains
  enrolled in the shared development authenticator. Only
  `mcp:access mcp:drafts`, without broad REST page-write permission.
- Draft tools enabled; global draft visibility disabled; external links
  use `nofollow`. Own opted-in drafts remain available under draft scope.
- The `ba044434` web rollout used Claude displaying Opus 5.5 Extra and
  ChatGPT in Instant mode. Its final native follow-up used Claude Desktop
  4.16336.0 on observed macOS 27.2, displaying Pro / Opus 5.5 Extra, Claude
  Code 2.1.318 and Codex CLI 0.166.0. The CLIs used isolated configurations
  with only this staging MCP server and no built-in/local tools.
  Historical native/CLI runs used Desktop 4.4212.0 and Claude Code 2.1.316.
  Displayed or agent-reported model identities remain unverified.

The `39d82c94` run added German and named synthetic EN/RU/DE categories and tags
(IDs 1/2/3 respectively), plus media 1: a 1600×900 workflow diagram with
localized alt text/captions and generated variants. No account privileges
were broadened. Source references were published page 1/revision 1 and
owned opted-in draft 23/revision 4; page 1 supplies no substantive article
claims. This avoids publishing another source fixture merely for a demo.

## Client results

On `39d82c94`, each row read context/schema, saved title/summary/body/SEO
with existing taxonomy, featured and inline images, created two linked translations using
fresh source revisions, and read authoritative results. These are successful
draft workflows with the separately recorded alt-text failure. The focused
`16ac7161` web follow-up below verifies its repair without relabelling the
earlier native/CLI results as new-build passes.

| Client / mode | EN source | RU | DE | Additional result |
|---|---:|---:|---:|---|
| Native Claude Desktop / OAuth | 27 | 43 | 44 | Four-client continuation and native final readback |
| ChatGPT web / OAuth | 28 | 40 | 41 | Continued page 27; update/replay/conflict and stale translations |
| Claude Code / OAuth | 31 | 37 | 38 | Identical create replay; localized demo pages 43/44 afterwards |
| Claude Code / bearer | 29 | 32 | 35 | Identical create replay; own scoped API-key identity |
| Codex / OAuth | 30 | 33 | 34 | Identical create replay; restored demo page 27's original summary |
| Codex / bearer | 36 | 39 | 42 | Identical create replay; own scoped API-key identity |

All 18 pages remain private, unscheduled drafts. Source translation-membership
changes advance revisions without changing source text. Both linked targets
initially report `current`; an older source revision is a valid basis while
its translated-text fingerprint still matches. Relation-only revisions have
empty current evidence; earlier authoring evidence remains in its original
revision, as documented in [draft authoring](../draft-authoring.md).

The chosen review component is [EN page 27](https://ocms-mcp-staging.fly.dev/admin/pages/27),
[RU page 43](https://ocms-mcp-staging.fly.dev/admin/pages/43) and
[DE page 44](https://ocms-mcp-staging.fly.dev/admin/pages/44).
The `16ac7161` resaves produced revisions 6, 4 and 3 respectively.
Both translations changed from `current` to `stale` after the English body
fingerprint changed. Their recorded source bases were preserved. Subsequent
content review saved revisions 7/5/4; the `be0e9cc3` editor review then
advanced only RU/DE to 6/5. Both translations are now `current` against
EN 27/revision 7. The [rollout record](#staging-translation-review-verification-on-be0e9cc3--2026-11-04)
records that completed review; final owner approval and publication remain
separate.

## Direct review and failure evidence

- ChatGPT changed only page 27's summary at base revision 3, producing
  revision 4. Exact replay returned revision 4 again; a new UUID with stale
  base 3 returned `revision_conflict`, current revision 4 and changed field
  `summary`. The stale marker was absent from readback.
- Both translations became stale. Codex restored the exact original summary
  at base 4, producing revision 5; both became current again. Claude Code
  then localized the target captions without changing source text.
- A synthetic claim acknowledgment was saved for revision 4. The editor
  showed that acknowledgment with its reviewer/time. Revision 5 showed the
  claim unchecked again. This was a mechanical acceptance check, not the
  owner's editorial sign-off or server fact verification.
- The editor separated Verified, Validated and Reported data and showed the
  actual saving clients. An old session preview of revision 4 retained its
  changed summary after current content returned to revision 5.
- Fresh 39d82c94 Claude/ChatGPT web tests also covered anonymous session
  denial, explicit pinned shares, own-grant revocation, refusal and normal
  password/MFA/consent reconnect. Shares were revoked and stayed 404.
  Browser traces put the bearer only in redemption POST bodies, never URLs
  or referrers. Four controlled Fly proxy-error probes used non-credential
  replays of observed targets; bounded logs contained no test credential.
  This does not establish universal proxy-log redaction.

Fifteen publication/lifecycle test roots were rerun on the clean
`39d82c94` commit in an isolated Linux container. Publish/unpublish and a no-op race
ran through the HTTP handler; stale publish/schedule and fresh schedule ran
through the guarded write helper. Hard publication validation, invalid/valid
published restore, scheduler webhook/no-longer-due cases, acknowledgment
reset, translation fingerprints, ownership/lifecycle denial, malformed JSON,
revocation-before-replay and cross-app CAS all passed. These automated
fixtures do not substitute for the pending human staging publication.

## Alt-text regression and verified fix

`![Workflow diagram: AI draft, editorial review, then human publication.](media:1)`
saved an inline `<img>` without `alt`, and reverse Markdown lost the
description without a warning. RU/DE punctuation reproduced it. Plain alt
text in the Codex bearer fixtures survived; featured-image/library alt also
worked. The cause is the sanitizer's inherited restrictive paragraph regex,
not image resolution or the clients' permissions.

The fix in `16ac7161` retains escaped plain-text `img.alt` attributes.
Regression tests cover supplied/library fallback text, EN/RU/DE punctuation, quotes,
angle brackets, decorative empty alt, repeated sanitization, readback and
Markdown resave. Event-handler stripping remains asserted.

The tests failed on the `39d82c94` policy before the fix. The candidate based
on `39d82c94` then passed all 68 Go test packages, golangci-lint (zero issues)
and first-party NilAway (zero diagnostics), without network or host
credentials. Only three Go files differ from that base. There was no new
deployment or Git mutation during that earlier acceptance run. A sanitizer
change cannot recover alt text already removed from stored HTML; the
authorized resaves below repaired only the selected current drafts.

## Alt-text staging verification

The owner authorized deploying `16ac7161` and resaving pages 27/43/44 without
a Git push or publication. The rollout used a clean Git archive, retained
the existing single Machine and volume, and took volume snapshot
`[snapshot ID redacted]` before deployment. Required MFA stayed enabled;
seeding stayed disabled.

| Draft | Saving client | Revision before → after | Stored HTML / derived Markdown / rendered preview |
|---|---|---|---|
| EN 27 | Claude web | 5 → 6 | PASS / PASS / PASS |
| RU 43 | ChatGPT web | 3 → 4 | PASS / PASS / PASS |
| DE 44 | ChatGPT web | 2 → 3 | PASS / PASS / PASS |

The exact inline `media:1` descriptions were:

- EN: `Workflow diagram: AI draft, editorial review, then human publication.`
- RU: `Схема: черновик ИИ, редакционная проверка, затем публикация человеком.`
- DE: `Ablauf: KI-Entwurf, redaktionelle Prüfung und Veröffentlichung durch einen Menschen.`

Each page was saved exactly once. Both web clients then freshly read all
three drafts and confirmed matching HTML/Markdown descriptions. Independent
signed-in revision previews confirmed the same rendered `alt` values.
Removing only the new inline `alt` attribute yields the prior body byte for
byte. Captions, content metadata and agent reports were preserved; saving
client provenance changed to the actual client. Issues and conversion
warnings were empty.

Both clients used dedicated Editor user 2 and existing OAuth access limited
to `mcp:access mcp:drafts`. The expired editor browser session was refreshed
with its Keychain password and shared authenticator. No owner MFA or grant
changes, new scopes, shares or bearer keys were needed.

At the end of this alt-text check, RU 43 and DE 44 reported `stale`; their
translation bases were not reset to conceal the source change. The later
`be0e9cc3` review resolves that state, as recorded below. All three pages
remain unscheduled drafts.
Native Desktop/CLI modes, web revocation/reconnect and proxy-error probes
were not rerun in this focused alt-text check; their earlier build-specific
evidence remains above.

## Klaro/minimatch staging verification — 2026-11-02

After separate deployment approval, `035ac1dd` was built from a clean Git
archive and deployed to `ocms-mcp-staging`. The exact binary/image identity
is above. The existing Machine `[machine ID redacted]` and volume
`[volume ID redacted]` were retained; recovery snapshot
`[snapshot ID redacted]` preceded the rollout. All three Machine checks
and both public health endpoints passed. OAuth discovery retained the HTTPS
issuer/resource; anonymous MCP returned 401 with resource metadata. Required
MFA stayed enabled and seeding stayed disabled.

| Check | Result on `035ac1dd` |
|---|---|
| Klaro JS/CSS/BSD license | All three public assets returned 200 and matched the committed vendor bytes |
| Script integrity | Rendered sha384 SRI matched the served JS; defer, CSP nonce, anonymous crossorigin and cache busting were present |
| Consent in Chrome | Russian notice rejection, footer reopen, required-service lock, optional opt-in/save/reload and later opt-out/save/reload passed |
| Claude web | Fresh schema/context and drafts 27/43/44 readbacks passed with OAuth Editor user 2 and exactly `mcp:access mcp:drafts` |
| ChatGPT web | The same focused read-only checks passed under its own existing grant and the same user/scopes |
| Dedicated-account login/preview | Normal password and shared-authenticator MFA passed; anonymous page 27/revision 6 returned 404, while the signed-in pinned preview rendered before and after final restart |

Asset destinations remained `/static/dist/js/klaro.min.js` and
`/static/dist/css/klaro.css`; the new license was delivered at
`/static/dist/licenses/klaro-LICENSE.txt`. Browser rendering confirmed that
the consent UI loaded with the served script and stylesheet.

Privacy was originally disabled with no configured services. The test used
two synthetic local preference services and a separate cookie name; no real
analytics/ad service was added. The original privacy row was restored
exactly and is disabled again. Pre/post-fixture database fingerprints
matched for all 44 page rows, the published count of one, ten owner grants
and module settings; SQLite quick_check returned ok. The temporary SQLite
verifier was removed. The helper's private MFA code files were removed;
owner credentials and MFA were unchanged.

Both clients freshly read EN 27/revision 6, RU 43/revision 4 and DE 44/revision
3. Localized alt text and captions survived in HTML and Markdown, with empty
issues/warnings. RU/DE remained stale and their source bases were unchanged.
After restoration/restart, each client freshly called site info and page
27 once more. The final server log buffer independently corroborated these
two successful OAuth user-2 calls per client under the existing grants.
Initial schema/context/readback evidence is in the actual web UI captures;
that final buffer does not corroborate the entire earlier call set.

A controlled normal/spoof request pair kept the same resolved client and
did not trust the spoofed address. A bounded 100-record log slice contained
none of the checked credential patterns; raw logs were not saved. No new
upstream-error/share probe ran, and universal log redaction is not claimed.

Go 1.27.3 / govulncheck 1.9.0 package analysis for Linux/amd64, CGO disabled,
returned zero imported-package vulnerabilities and retained the module-only
OpenPGP warning. This rechecks source packages for the staging target; it
does not scan the deployed binary or suppress the approved scoped finding.

This was a focused web/read-only follow-up. Native Desktop/CLI authoring,
OAuth revocation/reconnect, new draft saves and sharing were not rerun;
their earlier results remain tied to their recorded builds. The full Go
suite was not repeated during rollout; the unchanged runtime source had
already passed 68 test packages and Docker builds locally. No CMS content
edit, publication, scheduling, new commit or Git push occurred in this run.

## Staging upgrade to ba044434 — 2026-11-04

After explicit approval, the merged release commit was built from a clean
Git archive and deployed to `ocms-mcp-staging`. The binary/image identity
is above. Machine `[machine ID redacted]`, its 2 shared vCPU / 1 GB configuration
and volume `[volume ID redacted]` were retained. All three Fly checks and
both public health endpoints passed. Canonical HTTPS OAuth discovery and
anonymous MCP 401/resource-metadata refusal passed.
Klaro JS/CSS and the BSD license returned 200 and matched reviewed vendor
bytes; interactive consent and rendered SRI were not rechecked.

The approved prerequisites were applied before startup:

- Recovery snapshot `[snapshot ID redacted]` preceded configuration
  changes and deployment.
- `OCMS_API_ALLOWED_CIDRS` was set privately to the current Mac's public
  IPv4 `/32`. Existing session, MFA and trusted-proxy secret digests matched.
- Developer, Embed and Migrator modules were disabled; the Developer theme
  remained active. No other module setting was changed by this preparation.
- The owner explicitly approved `OCMS_REQUIRE_FORM_CAPTCHA=false` only
  for this staging app, which had zero public forms. No CAPTCHA keys were
  configured. Required MFA stayed enabled and seeding/DCR stayed disabled.
  The tracked `fly.staging.toml` still requires CAPTCHA: this rollout used
  an explicit deployment override, not a changed default. A future redeploy
  must retain that approved exception while there are no public forms, or
  configure a real verifier before requiring CAPTCHA.
- Startup applied core migration `20261023120000_create_page_search` and
  MCP module migration 3. The new `agent_share_links` setting was enabled.

Both web clients used dedicated Editor user 2 and exactly
`mcp:access mcp:drafts`, with a 30-day refresh lifetime. Password and MFA
were supplied privately from Keychain and the shared development
authenticator; no owner credentials or authenticator enrollment were changed.

| Check | Claude web | ChatGPT web |
|---|---|---|
| Fresh schema/context and demo readback | PASS; references page 1/revision 1 and own page 27/revision 6 | PASS; same bounded source/context checks |
| Existing demo revisions | EN 27/6, RU 43/4, DE 44/3; localized alt/captions intact, no issues/warnings, RU/DE stale | PASS; same authoritative readback |
| Create/update and exact replay | Page 45: create/replay at revision 1, update/replay at revision 2 | Page 46: same revision progression |
| New request with stale base revision | `revision_conflict`, no write or retry | `revision_conflict`, no write or retry |
| Disabled share policy | `share_links_disabled`, no URL issued | Same refusal; original policy restored to enabled |
| Anonymous revision-2 share | Issued and rendered; unchanged after current draft advanced to revision 3 | Issued and rendered; landing also accepts ChatGPT's tracking query |
| Revocation | Explicit link revoke denied fresh-token and existing-cookie access with 404 | Parent-grant revoke denied both with 404; both stayed 404 after reconnect |
| OAuth revoke/refusal/reconnect | PASS; normal password/MFA/consent, draft-scope upgrade, then fresh readback | PASS; normal password/MFA/two-scope consent, then fresh readback |

Claude's revision-2 share response stayed byte-identical after page 45
advanced to revision 3. ChatGPT's separate post-update pin-invariance check
was not repeated; its revision-2 rendering and parent-grant invalidation
were directly checked. The temporary access-only Claude grant was revoked,
and both final two-scope connections passed fresh site-info/draft calls,
independently corroborated by successful user-2 OAuth events in server logs.
Claude's English listing included its fixture while the Russian filter
excluded it. All three disposable share rows are revoked.

The ChatGPT conversation initially retained the old R1.5 tool description.
Refresh tools and a fresh chat exposed the current sharing contract without
changing global permissions; the old-context share was revoked. Temporary
response-load placeholders were resolved with Reload response, without
replaying non-idempotent calls. ChatGPT preserved the exact supplied inline
alt punctuation and final period. Claude preserved what it submitted but
omitted the requested final period; exact period preservation is not claimed
for that client. Stored HTML and derived Markdown retained the submitted
descriptions and quotes.

All original 44 pages, 101 version rows and 99 provenance rows matched
pre-upgrade hashes. Demo drafts 27/43/44 stayed at revisions 6/4/3,
unscheduled, with stale translation bases intact. New test pages 45/46 ended
at revision 3 and remain private, unscheduled drafts: the final totals are
45 drafts and one existing published page. Owner user/TOTP/recovery records
and API-key tables matched baseline hashes. OAuth runtime fingerprints
changed alongside normal refresh activity; byte-identical preservation is
not claimed for those tables. SQLite quick_check returned ok and
foreign_key_check was empty.

Anonymous session-preview access returned 404 while the dedicated signed-in
Editor preview rendered. Share redemption used the fixed POST endpoint,
with bearer tokens in form bodies and no token in request targets or
response URLs. Preview responses enforced private/no-store and no-referrer.
Neither known share token appeared in the captured 100-record Fly log
buffer. No fresh controlled proxy-error fault injection was performed;
universal log redaction is not claimed. This initial web rollout did not
rerun native/CLI modes or new multilingual creation. The final follow-up
below adds native/CLI EN/RU and dependency results on the same SHA; it does
not repeat Klaro interactive consent/SRI.

Temporary remote maintenance probes, local preview-token/cookie files and
private MFA code files were removed. Shared development enrollment remains
in Keychain. The rollout made no commit, push, publication or scheduling
change; wiki and `.claude/shared` were untouched. Evidence is under ignored
`output/r17-validation/ba044434-staging-20261104/`, including
`acceptance-summary.json`, `verification.txt`, deployment and preservation
receipts, sanitized client/log captures and final readback screenshots.

## Final native and CLI verification — 2026-11-04

The final tests used the same `ba044434` binary/image and existing Machine,
volume and configuration, without another deployment. All Fly checks and
both public health endpoints still passed. Dependency scans used a clean
archive whose 1,425 tracked blobs matched the tested Git tree.

Every row used dedicated Editor user 2 and exactly `mcp:access mcp:drafts`.
Each created one synthetic English draft with existing taxonomy/media,
replayed identical create/update arguments, refused a stale write, retained
body/metadata and exact quoted inline alt text, and created/read a linked
current Russian translation. Source membership advanced the English draft
to revision 3; the Russian target was revision 1. CLI assertions checked
saved tool requests/results, including structured/text parity and
published-page draft-read refusal. Native UI and server dispatch records
corroborated its authoring sequence.

| Client / mode | EN / RU fixture | Authoring result | Credential lifecycle |
|---|---|---|---|
| Native Claude Desktop 4.16336.0 / OAuth | 47 / 48 | PASS | Native Connect, password/shared MFA, base and draft consent, fresh native readback; temporary base grant revoked |
| Claude Code 2.1.318 / OAuth | 51 / 52 | PASS | Revoked startup refused; fresh login/MFA/consent and new-grant readback passed |
| Claude Code 2.1.318 / bearer | 53 / 54 | PASS | Scoped key accepted; deactivated key refused with HTTP 401 during CLI startup |
| Codex CLI 0.166.0 / OAuth | 49 / 50 | PASS | Revoked startup refused; fresh login/MFA/consent and new-grant readback passed |
| Codex CLI 0.166.0 / bearer | 55 / 56 | PASS | Scoped key accepted; deactivated key refused with HTTP 401 during CLI startup |

Desktop reconnect had already passed on `035ac1dd` on 2026-11-03; the old
pending label was stale. On `ba044434`, both Connect actions originated in
the native app. Chrome's normal **Finish connecting a connector?** page
identified `claude-desktop-chat` and a Desktop callback; **Continue
connecting** led to normal oCMS password/MFA/consent. Fresh native identity
and draft reads after the callback and temporary base-grant cleanup
confirmed the final two scopes. No web-settings workaround or oCMS runtime
fix was needed. The historical failed attempt's cause remains unestablished;
it is not a current reconnect blocker.

All three OAuth revocation tests produced real backend refusal. New native
and CLI grants retained the 30-day refresh duration. Two new 90-minute
bearer keys (7/8), each restricted to this Mac's public IPv4 `/32`, were
retained only in Keychain and deactivated after testing. Direct probes and
actual CLI initialization with the revoked keys returned 401; no protected
tool was dispatched. Missing tools alone were not counted as refusal.

The first bearer creates correctly hit the existing 50-AI-draft limit and
returned `draft_limit_reached` without creating pages. Normal Editor saves
disabled AI editing on four completed fixtures from this run to free slots;
both full bearer sequences then passed. After testing, all ten new pages
47–56 had AI editing disabled, restoring the original count of 44 active AI
drafts. Their content/history remain saved as private, unscheduled drafts.
These cleanup saves produced source/target revisions 4/2; the authoring
matrix above records the tested pre-cleanup revisions 3/1.

Go 1.27.3 / govulncheck 1.9.0 source/symbol analysis for Linux/amd64,
CGO disabled, returned zero vulnerable imported packages and zero reachable
symbols. GO-2026-5932 remains module-only for `x/crypto v0.58.0`; the prior
approved not-applicable disposition was revalidated for this build/target,
without suppression or a claim that OpenPGP was fixed. The Go database's
last update was `2026-11-02T22:01:43Z`. npm audit, including dev dependencies,
reported zero findings. Vendored Klaro JS/CSS/license bytes matched the
previously reviewed `035ac1dd` build. This was not a new repository audit,
deployed-binary scan or container OS scan.

All original 46 pages, 107 version rows and 105 provenance rows matched
hashes. Demo pages 27/43/44 stayed at revisions 6/4/3, unscheduled, with
RU/DE still stale. Original API-key restrictions, owner TOTP/recovery data
and grant fields were preserved. All 32 untargeted original OAuth grants
retained their compared identity/scope/lifecycle fields; routine last-use
timestamps were excluded. Whole users-row equality is not claimed after
the dedicated logins updated `last_login_at`. SQLite quick/FK checks passed.

The remote test utility, temporary local CLI registration/workspace and
private MFA delivery files were removed. Final normal OAuth connections
remain active, and both test keys are inactive. Bounded Fly buffers exposed
neither known bearer key nor OAuth token prefixes; no fresh proxy-error
fault injection or universal log-redaction pass is claimed. The full Go
suite and an independent security audit were not repeated in this follow-up.

Corrected setup/input attempts are retained in the evidence: the scanner's
read-only Go-cache setup, the expected quota refusal, Codex's unsupported
`draft:27` context reference, and the temporary Claude Code workspace's
pending project MCP approval. They were resolved without an oCMS code
change. Native dispatch records show ten authoring calls; its model-reported
total of twelve was not used as verification.

Evidence: ignored
`output/r17-validation/ba044434-final-clients-20261104/`, especially
`acceptance-summary.json`, `verification.txt`, `final-assertions.json`,
dependency reports, final native screenshots, sanitized CLI/log records,
source-integrity and preservation/cleanup receipts. The earlier native
reconnect pass is retained under `output/r17-validation/desktop-reconnect-20261103/`.
No commit, push, deployment, publication or original-draft edit occurred in
this test run; wiki and `.claude/shared` were untouched.

## Staging translation-review verification on be0e9cc3 — 2026-11-04

After separate authorization, `be0e9cc3` was deployed from its exact clean
Git archive, with 1,429 source blobs verified, including symlink targets.
The full uncached Go test gate and Fly configuration validation passed
before deployment. The existing Machine `[machine ID redacted]`, volume
`[volume ID redacted]`, 2 shared vCPU / 1 GB sizing and environment were
preserved. Volume snapshot `[snapshot ID redacted]` preceded rollout.
The binary/image identity is recorded above. All three Fly checks passed;
public `/health/live` and `/health/ready` returned 200.

Required MFA stayed enabled, seeding stayed disabled, and the existing
owner-approved staging-only `OCMS_REQUIRE_FORM_CAPTCHA=false` exception
was retained with zero public forms. No tracked configuration or secret
was changed by the rollout.

Fresh normal password login and the shared development authenticator
restored dedicated Editor user 2, `ocms-agent@example.test`. The preceding
content review had saved EN 27/7, RU 43/5 and DE 44/4, clarifying reference
limits, identical-input retries and text/SEO freshness triggers. Comparing
the localized text with English revision 7 required no further article or
metadata edits; localized images and taxonomy were preserved.

The editor's **Mark translation reviewed** action was used once per target,
with its explicit meaning acknowledgment and source snapshot 27/revision 7.
The editor and independent MCP draft/translation-status reads agreed:

| Draft | Revision before → after | Final translation freshness / basis |
|---|---|---|
| EN 27 | 7 → 7 | Source unchanged; `not_tracked` |
| RU 43 | 5 → 6 | `current` against page 27/revision 7 |
| DE 44 | 4 → 5 | `current` against page 27/revision 7 |

Each new target revision records user 2, source `human`, transport
`translation_review`, the reviewed target revision (RU 5 / DE 4), time and
source 27/revision 7. This is the normal editor-session review provenance;
the MCP reader separately retained OAuth user 2 and exactly
`mcp:access mcp:drafts`. It is not the owner's final publication approval.
Article/SEO fields, localized image descriptions and translation links
were unchanged. Earlier claim acknowledgments and provenance stayed in
their original revisions; no old agent assertions were copied into the
new editor-review revisions.

Read-only database comparisons preserved all 56 page identities and all
140 existing versions, 138 provenance rows and five claim checks. Only
pages 43/44 advanced, adding exactly two versions and two provenance rows.
English 27 and every other page were unchanged. The page totals stayed at
55 drafts and one pre-existing published page; all three demo pages stayed
unscheduled drafts. Owner/dedicated credential identity, owner MFA/recovery,
OAuth grant identity/scope/lifecycle, API-key restrictions, AI access,
preview links, configuration and module settings matched. Routine OAuth
last-use and dedicated-login/MFA counters were excluded from preservation
comparisons. SQLite quick-check passed with no foreign-key errors.

Nine anonymous checks returned 404: the three demo public URLs, their
current session-preview URLs and their REST page endpoints. No share,
bearer key, grant revocation, publication or schedule was created. Private
MFA delivery files and the temporary remote read-only verifier were removed.
No additional commit or Git push occurred; wiki and `.claude/shared` were
untouched.

This focused rollout verifies the deployed translation-review action and
editor/MCP readbacks. It did not rerun Claude/ChatGPT GUI acceptance,
native/CLI OAuth reconnect, dependency scans, sharing or Fly proxy-error
probes. Their earlier results retain the build identities above.

Evidence is under ignored
`output/r17-validation/be0e9cc3-staging-20261104/`: deployment/source/snapshot
receipts, `acceptance-summary.json`, `verification.txt`, editor captures and
`both-translations-current.png`, MCP before/after readbacks, anonymous
checks, preservation fingerprints and cleanup receipts.

## Evidence, cleanup and remaining decisions

Local evidence is under ignored `output/r17-validation/release-closure/`:
sanitized CLI events/matrix, catalog fixtures, GUI snapshots/screenshots,
publication checks, failing/passing regression logs and tested file hashes.
Earlier 39d82c94 deployment/share evidence is under
`output/r17-validation/hardening-staging/`. Credentials are not part of the
release notes or recording.

The `16ac7161` rollout/readback evidence is under ignored
`output/r17-validation/alt-text-staging/`: deployment/image and snapshot
identity, before/after drafts, exact body comparisons, both web clients'
final readbacks/screenshots and all three rendered previews. Read-only
database comparison confirmed 44 pages, an unchanged published-page count,
all other page identity/revision/lifecycle fingerprints and ten owner
grants preserved, unchanged preview links and SQLite quick-check success.
Temporary inspection files were removed; both prior test keys stayed
inactive. No new Git commit, Git push, publication or scheduling occurred
in this rollout. Existing wiki work was untouched.

The `035ac1dd` follow-up evidence is under ignored
`output/r17-validation/klaro-staging/`: archive/deployment/image and snapshot
receipts, asset/SRI checks, consent and client AX captures/screenshots,
pre/post-fixture fingerprints, exact restoration, the Linux/amd64 package
scan and sanitized final tool/log records. `acceptance-summary.json` and
`verification.md` record the scope and limits. Core HEAD stayed `035ac1dd`,
the core working tree stayed clean and wiki metadata was unchanged.

`r17-draft-workflow-partial.mp4` is a 40-second H.264 screenshot walkthrough
of the actual captured states. Its captions explicitly retain the alt-text
and human-publication limitations. The original 26 page identity/revision/
lifecycle fingerprints and ten owner grants match the pre-run record.

The two new 90-minute bearer keys (5/6), restricted to the Mac's public
IPv4 `/32` and retained privately in Keychain, were deactivated after the
runs. Both subsequent MCP initialization probes returned HTTP 401.
Owner connections and pre-existing drafts are preserved; no new share was
issued for the multilingual demo.

The screenshot walkthrough is partial evidence, not a continuous recording
of publication. See [the demo script](demo-script.md) for its remaining shot.
The exact release decisions are in [release decisions](release-decisions.md):
the alt-fix and Klaro/minimatch staging checks are complete; the approved
OpenPGP disposition retains its module-only warning and staging-target
package revalidation.
The final `ba044434` follow-up completes native-initiated Desktop reconnect,
the four CLI OAuth/bearer rows and the scoped dependency revalidation.
Earlier full EN/RU/DE and interactive Klaro results retain their build
scope. The `be0e9cc3` rollout completes staging content/translation review:
RU 43/6 and DE 44/5 are current against EN 27/7. The remaining work is final
owner editorial approval, separately approved human publication, the final
recording segment and the planned launch article draft. Production
authoring/publication remains a separate request under the
[article outline](article-outline.md).
