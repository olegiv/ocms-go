# Connected-app draft authoring

R1.5 and R1.6 staging acceptance is recorded in PHASE5 implementation notes.
R1.6 passed the tested Claude web and ChatGPT web draft/review flows on
`ff4de3b1368c` and fresh fragment/POST sharing checks on `31c55f8c9f67`
(2026-11-02). Both clients' links opened anonymously at the pinned revision;
the browser traces confirmed token-free HTTP request URLs. Native Desktop/CLI
authoring acceptance and broader release hardening remain R1.7 work.

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
Native Desktop and CLI coverage of this build remains part of the R1.7 matrix;
earlier-build results do not establish that coverage.

## R1.7 release acceptance (pending)

The connected-app management and retention changes are local, undeployed
work. Use [Connected apps](connected-apps.md) for access management and the
[author skill](skills/ocms-author/SKILL.md) for the draft workflow. The prepared
[demo script](launch/demo-script.md) defines the multilingual recording; it
has not been recorded or published.

| Client | Authoring path to exercise after approved deployment | R1.7 result |
|---|---|---|
| Claude Desktop | OAuth; brief/context → draft → two translations → editor/review/preview | Pending |
| Claude web | OAuth; cross-client continuation, revision conflict and explicit share | Pending |
| ChatGPT web | OAuth; same full draft/translation/review flow and tracking-query share | Pending |
| Claude Code | OAuth and scoped bearer modes; same tools and denied next request after revoke | Pending |
| Codex | OAuth and scoped bearer modes; same tools and denied next request after revoke | Pending |

Record actual client/version/tier, build/tree, settings, capabilities and date
per row. Follow normal authentication with the dedicated staging account and
shared authenticator; preserve owner connections. Exercise the connected-app
page's own/admin boundaries and cleanup while active-family replay detection
still works. The final publish step needs the human editor's explicit action.
Do not mark these rows passed from local SDK tests or older staging builds.
