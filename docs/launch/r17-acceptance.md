# R1.7 release acceptance — 2026-11-02

The full EN/RU/DE draft workflow now runs in native Claude Desktop, ChatGPT,
Claude Code and Codex. **R1.7 is not release-complete:** this run found a
punctuation-dependent inline-image alt-text bug. Its local fix passes tests,
lint and NilAway but has not been deployed. Human publication
and the final publication segment of the demo remain pending.

## Tested identities

- Fly app: `ocms-mcp-staging`, canonical origin
  `https://ocms-mcp-staging.fly.dev`.
- Deployed commit: `39d82c94f164452fed68157b77232815889403ba`; binary build
  time `2026-11-02T14:56:23Z`. The host checked the binary directly. MCP's
  generic server version `1.0.0` does not attest to a Git commit.
- Image digest:
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

The run added German and named synthetic EN/RU/DE categories and tags
(IDs 1/2/3 respectively), plus media 1: a 1600×900 workflow diagram with
localized alt text/captions and generated variants. No account privileges
were broadened. Source references were published page 1/revision 1 and
owned opted-in draft 23/revision 4; page 1 supplies no substantive article
claims. This avoids publishing another source fixture merely for a demo.

## Client results

Each row read context/schema, saved title/summary/body/SEO with existing
taxonomy, featured and inline images, created two linked translations using
fresh source revisions, and read authoritative results. These are successful
draft workflows with the separately recorded alt-text failure, not complete
release passes.

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
At handoff their revisions are 5, 3 and 2 respectively. Both targets are
current after source text was restored. They still need the alt fix applied
to their stored bodies and a fresh human review before publication.

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

Fifteen publication/lifecycle test roots were rerun on the clean deployed
commit in an isolated Linux container. Publish/unpublish and a no-op race
ran through the HTTP handler; stale publish/schedule and fresh schedule ran
through the guarded write helper. Hard publication validation, invalid/valid
published restore, scheduler webhook/no-longer-due cases, acknowledgment
reset, translation fingerprints, ownership/lifecycle denial, malformed JSON,
revocation-before-replay and cross-app CAS all passed. These automated
fixtures do not substitute for the pending human staging publication.

## Alt-text regression and local fix

`![Workflow diagram: AI draft, editorial review, then human publication.](media:1)`
saved an inline `<img>` without `alt`, and reverse Markdown lost the
description without a warning. RU/DE punctuation reproduced it. Plain alt
text in the Codex bearer fixtures survived; featured-image/library alt also
worked. The cause is the sanitizer's inherited restrictive paragraph regex,
not image resolution or the clients' permissions.

The local fix retains escaped plain-text `img.alt` attributes. Regression
tests cover supplied/library fallback text, EN/RU/DE punctuation, quotes,
angle brackets, decorative empty alt, repeated sanitization, readback and
Markdown resave. Event-handler stripping remains asserted.

The tests failed on the deployed policy before the fix. The candidate based
on `39d82c94` then passed all 68 Go test packages, golangci-lint (zero issues)
and first-party NilAway (zero diagnostics), without network or host
credentials. Only three Go files differ from that base. There is no new
deployment or Git mutation during the acceptance run. Existing saved snapshots need
an explicit body resave after an authorized rollout; a sanitizer change
cannot recover alt text already removed from stored HTML.

## Evidence, cleanup and remaining decisions

Local evidence is under ignored `output/r17-validation/release-closure/`:
sanitized CLI events/matrix, catalog fixtures, GUI snapshots/screenshots,
publication checks, failing/passing regression logs and tested file hashes.
Earlier 39d82c94 deployment/share evidence is under
`output/r17-validation/hardening-staging/`. Credentials are not part of the
release notes or recording.

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
authorize the alt-fix commit/rollout and fresh resave/verification, decide
the scoped dependency dispositions and Desktop reconnect caveat, then have
the human review/publish and finish the recording. Production
authoring/publication remains a separate request under the
[article outline](article-outline.md).
