# Connected-app draft authoring

R1.5 and R1.6 staging acceptance is recorded in PHASE5 implementation notes.
R1.6 passed the tested Claude web and ChatGPT web draft/review flows on
`ff4de3b1368c` and fresh fragment/POST sharing checks on `31c55f8c9f67`
(2026-11-02). Both clients' links opened anonymously at the pinned revision;
the browser traces confirmed token-free HTTP request URLs. R1.7's current
client coverage and remaining release gates are recorded below.

R1.7 is now deployed at `ba044434` (2026-11-04). Fresh Claude web and ChatGPT
web checks passed OAuth/MFA revoke/reconnect, draft replay/conflict,
share-policy refusal and anonymous sharing/revocation; see the
[upgrade record](launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
for the separate pinning checks and limits. The
[final native/CLI follow-up](launch/r17-acceptance.md#final-native-and-cli-verification--2026-11-04)
passed native Desktop OAuth and both CLIs' OAuth/bearer authoring and
credential lifecycle checks on the same SHA. Native-initiated Desktop
reconnect passed through normal browser confirmation, password/shared MFA,
consent and Desktop callback. Earlier web OAuth/sharing checks and full
EN/RU/DE draft authoring with existing taxonomy/media ran on
`39d82c94` in native Desktop, ChatGPT and both CLIs' OAuth/bearer modes.
The inline alt-text repair passed Claude web/ChatGPT web resaves and
readbacks on `16ac7161`, with matching rendered previews. That focused alt-text
follow-up did not rerun native/CLI modes. See the
[current acceptance record](launch/r17-acceptance.md)
and [release decisions](launch/release-decisions.md) for dependency
revalidation and the remaining human editorial/publication/demo gates.

## Workflow

1. An administrator enables MCP Server and **Enable draft authoring**. Connect
   the app to the site's `/api/mcp` endpoint using fresh login, MFA when required
   and consent for `mcp:access` plus `mcp:drafts` only. Existing API-key policy
   also applies; broad page-write permission is unnecessary.
2. Ask for `get_site_info`, `get_page_draft_schema` and `get_authoring_context`
   with selected source/example references. The three prompts also have tool
   equivalents for clients that do not support MCP prompts/resources.
3. Review the proposed text, references, existing taxonomy and media. Approve
   saving a draft. The agent chooses one UUID `request_id`, retaining it for
   retries, and calls `create_draft_page`.
4. Inspect `issues`, `revision`, `admin_url` and `preview_url`. Open the URLs in
   a signed-in editor session. Anonymous visitors cannot open draft content.
   Previews are pinned to the saved revision. Sharing requires a separate,
   user-requested `create_preview_link`; anyone holding that URL can read the
   snapshot until it expires or is revoked.
   Copy the complete share URL including `#...`. A JavaScript-enabled browser
   opens it through the credential-free landing and body-only redemption;
   never move that fragment into a path or query parameter.
5. To continue in another app, connect with the same oCMS user and scopes,
   use `list_my_drafts`/`get_my_draft`, then update with a new request UUID and
   the last-read `base_revision`. Foreign-user drafts remain inaccessible,
   including with an admin account. Re-read on a revision conflict.
6. For translation, read the source revision, author the target text, then call
   `create_translation_draft`. A changed source text or existing target-language
   member prevents creation. Review source freshness with
   `get_translation_status`.

The agent cannot publish, schedule, delete, switch ownership or opt another
page into AI editing. Publishing remains a deliberate human editor action.
See [draft authoring](draft-authoring.md) for retries, quotas and provenance,
and [editorial review](editorial-review.md) for checks, revision comparisons and
secure sharing.

## Acceptance record after authorized deployment

For each available client record date/version, tested commit/image, account
role and non-secret settings. Verify normal save/read/update/translation, the
same lost-response retry, two apps on one user's draft, a stale editor revision,
foreign and published-page denial, disabled-toggle denial, anonymous preview
denial and revoke → refusal → reconnect. Check the complete structured error
and text fallback, annotations, prompt/tool parity and log redaction. Deactivate
all temporary test keys afterwards. Use the dedicated agent account and
[shared development authenticator](dev-authenticator.md); preserve the owner's
connections. Record unavailable capabilities as pending. Do not mark R1.5
external acceptance complete from SDK tests or from R1.1 probe results.

For R1.6 additionally verify the saved revision after another edit, anonymous
session-preview denial, explicit sharing only, anonymous share rendering,
revocation/expiry and publication invalidation. Check theme/privacy headers,
private sibling isolation, claim acknowledgment, revision comparison,
translation freshness and stale publish/schedule rejection. Confirm upstream
request URLs and referrers contain no share credential, inspect application
and proxy error logs, and keep HTTP body dumps disabled.

The earlier `ff4de3b1368c` Fly check failed upstream redaction: Fly Proxy
recorded a non-credential canary path in `meta.URL.Full`. The later
`31c55f8c9f67` rollout removed bearer credentials from request targets and
passed fresh dedicated-account Claude/ChatGPT sharing, session-preview denial,
revocation and controlled expiry checks. Eight induced proxy errors and all
247 captured app/proxy records contained neither real test bearer; those
proxy records had empty URL fields, so they do not establish proxy redaction.
The proxy probes replayed the browser-observed targets with non-credential
content; the browser traces establish the URL-safety property directly.
Both new links were revoked and existing drafts, reviews and OAuth grants
were preserved. See the [rollout record](../.claude/docs/PHASE5.md#r16-preview-transport-fly-rollout--2026-11-02).
Later native Desktop and CLI results on `df6e5d86` appear in the R1.7 matrix
below; earlier-build results do not establish that coverage.

## R1.7 release acceptance (partial)

The [39d82c94 acceptance matrix](launch/r17-acceptance.md#client-results)
includes complete EN/RU/DE draft fields, taxonomy/media, retries and linked
translations. The [16ac7161 alt-text verification](launch/r17-acceptance.md#alt-text-staging-verification)
records web checks of resaved pages 27/43/44 at revisions 6/4/3. Fresh
editorial/stale-translation review, human publication and its final
recording segment are pending.
The [ba044434 web rollout](launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
and [final native/CLI follow-up](launch/r17-acceptance.md#final-native-and-cli-verification--2026-11-04)
add fresh web checks, native-initiated Desktop reconnect, all four CLI
OAuth/bearer paths and scoped dependency revalidation. The final native/CLI
authoring checks created EN/RU pairs. The table below retains the earlier
bounded `df6e5d86` evidence.
Use [Connected apps](connected-apps.md) for access management and
the [author skill](skills/ocms-author/SKILL.md) for the draft workflow. Follow
the [demo script](launch/demo-script.md) for the remaining human-publication shot.

| Client | Authoring path | R1.7 result (2026-11-02) |
|---|---|---|
| Claude Desktop 4.4212.0 | OAuth; context/schema → EN draft/replay/update/conflict → RU translation → editor/review/pinned preview; cross-client readback | Bounded authoring passed on `df6e5d86`; reconnect completed through Chrome; native initiation was not established in that run and passed in later follow-ups |
| Claude web | Context/schema → EN draft/replay → RU translation; cross-client readback, pinned share, own-grant revoke/reconnect | Passed, bounded web flow on `df6e5d86` |
| ChatGPT web | Context/schema → EN draft/replay → RU translation; cross-client update/replay/conflict, stale translation, tracking-query share, revoke/reconnect | Passed, bounded web flow on `df6e5d86` |
| Claude Code 2.1.316 | OAuth and scoped bearer; context/schema → draft/replay/update/conflict → RU translation; revoked startup denial and fresh OAuth reconnect/read | Passed, both bounded modes on `df6e5d86` |
| Codex 0.166.0 | OAuth and scoped bearer; context/schema → draft/replay/update/conflict → RU translation; cross-client update, revoked startup denial and fresh OAuth reconnect/read | Passed, both bounded modes on `df6e5d86` |

Both web clients used dedicated Editor user 2 and the shared authenticator,
with exactly `mcp:access mcp:drafts`. Fresh caller identities matched new
database grants and successful MCP events; draft save provenance retained its
historical identity. Test shares and the temporary base grant were revoked;
owner connections and older fixtures were preserved.

The native/CLI follow-up used the same dedicated Editor and shared
authenticator. Desktop created pages 13/14; Codex continued page 13 to revision
4, and Desktop/Claude Code read it back. A stale edit was rejected, its Russian
translation became stale, and the explicit share stayed pinned to revision 2
until grant revocation made it return 404. Both CLI modes verified identical
create/update retries, preserved bodies/reports and linked translations.
Revoked CLI credentials received 401 during MCP startup; missing tools alone
were not treated as refusal evidence. Both OAuth reconnects produced new
grants and fresh reads. The two 90-minute bearer keys, each restricted to the
Mac's public IPv4 `/32`, were deactivated after testing.

**Historical Desktop reconnect caveat (`df6e5d86`):** native Connect/Reconnect
did not open an observable sign-in in the controlled native/Chrome surfaces.
Chrome web settings and Connect in the shared chat completed normal password/MFA/consent,
including the `mcp:drafts` upgrade. Fresh native site-info/draft calls then
confirmed user 2, exactly both scopes and revision 4. The original cause
remains unestablished. Native-initiated reconnect subsequently passed on
`035ac1dd` (2026-11-03) and `ba044434` (2026-11-04), through normal external
browser confirmation and MFA/consent; the pending/workaround label is stale.

Those earlier runs enabled only English/Russian without taxonomy/media.
The [new run](launch/r17-acceptance.md) adds German, full catalog fixtures,
all six client/mode flows and 15 publication/lifecycle test roots. Audit
findings and first-party NilAway diagnostics were fixed before deployment.
Native reconnect and dependency revalidation are complete. The remaining
[release decisions](launch/release-decisions.md) are fresh human
editorial/stale-translation review, publication, the final recording segment
and the planned launch article draft. See the earlier
[web staging record](../.claude/docs/PHASE5.md#r17-fly-staging-and-web-client-acceptance--2026-11-02)
and [native/CLI record](../.claude/docs/PHASE5.md#r17-native-desktop-and-cli-acceptance--2026-11-02)
for build identity, evidence limits, cleanup and remaining gates.

Record actual client/version/tier, build/tree, settings, capabilities and date
per row. Follow normal authentication with the dedicated staging account and
shared authenticator; preserve owner connections. Exercise the connected-app
page's own/admin boundaries and cleanup while active-family replay detection
still works. The final publish step needs the human editor's explicit action.
Do not mark these rows passed from local SDK tests or older staging builds.
