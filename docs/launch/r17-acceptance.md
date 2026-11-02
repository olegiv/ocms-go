# R1.7 release acceptance — updated 2026-11-03

The latest verified staging build is `035ac1dd`: Klaro consent, asset/SRI and
license delivery, plus fresh Claude/ChatGPT read-only checks passed. The
full EN/RU/DE draft workflow ran on `39d82c94` in native Claude Desktop,
ChatGPT, Claude Code and Codex; alt-text resaves were verified on `16ac7161`.
Those earlier results retain their build-specific scope.
**R1.7 is not release-complete:** the Desktop reconnect decision, fresh
editorial/translation review, human publication, the final demo segment and
the planned production article draft remain pending. The approved
OpenPGP disposition has a fresh Linux/amd64 package recheck; the module-only
warning remains visible. See the
[scoped dependency dispositions](release-decisions.md#dependency-advisory-dispositions).

## Tested identities

- Fly app: `ocms-mcp-staging`, canonical origin
  `https://ocms-mcp-staging.fly.dev`.
- Latest verified deployed commit: `035ac1ddf9b3a9abb6e4289d8545ec246cea1ac3`;
  binary build time `2026-11-02T22:25:49Z`, checked directly on the host.
  Fly readiness/liveness and both public health endpoints passed. MCP's
  generic server version `1.0.0` does not attest to a Git commit.
- Image digest:
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
- Native Claude Desktop 4.4212.0, Pro, Opus 5.5 Extra as displayed; ChatGPT
  web in Instant mode (tier/model not independently established). Agent
  model reports remain unverified. Claude Code 2.1.316 and Codex 0.166.0
  used isolated CLI configurations with only this staging MCP server.

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
After the `16ac7161` resaves their revisions are 6, 4 and 3 respectively.
Both translations changed from `current` to `stale` after the English body
fingerprint changed. Their recorded source bases were preserved. These
current revisions need fresh human claim/content review and translation
review before any separately authorized publication.

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

RU 43 and DE 44 now report `stale`; their translation bases were not reset
to conceal the source change. All three pages remain unscheduled drafts.
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
Decide the Desktop reconnect caveat, then have the human review the
current drafts/translations and, after separate publication approval,
publish and finish the recording. Production
authoring/publication remains a separate request under the
[article outline](article-outline.md).
