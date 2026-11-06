# Editorial review and revision previews

R1.6 is deployed on Fly staging. Claude web and ChatGPT web passed the tested
draft/review flows on `ff4de3b1368c` and fresh fragment/POST sharing checks on
`31c55f8c9f67` (2026-11-02). The broader client matrix and release hardening
are recorded in the [R1.7 acceptance record](launch/r17-acceptance.md),
including the inline alt-text repair on `16ac7161`, translation review on
`be0e9cc3` and owner-confirmed staging publication on 2026-11-05. Final demo
revisions EN 27/9, RU 43/7 and DE 44/6 are Published; both translations are
Current. The editor HTML preservation fix passed on staging `6c611040`.
Publication, scheduling, deletion and transfer remain human CMS operations.

## Review a draft

Open the saved `admin_url`. The editor sidebar separates:

- **Verified:** recorded account, credential/grant and registered client ID,
  transport, time and revision. Registered display names do not establish
  vendor identity. Historical migration actors remain explicitly unknown.
- **Validated by oCMS:** validation issues, resolved reference revisions,
  inspected links and the authoring contract version. These checks do not
  establish the factual truth of the article.
- **Reported by the agent:** model, claimed sources, notes and claims needing
  editorial attention. Checkboxes record who acknowledged each claim and when,
  without changing its provenance or converting it into a verified fact.

Checks belong to one provenance/revision pair. New revisions start unchecked;
old acknowledgments remain visible in revision review. A stale checkbox form
is rejected. Deleting a reviewer retains the acknowledgment with an unavailable
reviewer label. The page list has AI-draft badges, unchecked counts and an
**AI drafts awaiting review** filter.

A missing review target or unavailable claim/source remains a conflict.
Missing required evidence or revision records is a server error, logged with
record IDs; failed operations leave the review unchanged.

Translations show their lifecycle and source freshness. Freshness uses the
same content fingerprint as draft authoring: source text/SEO changes make a
translation stale; publication, AI-access changes and sibling membership do
not. Missing or historical bases are labelled explicitly.

AI-access storage faults are reported as server errors. A missing AI-access
record is valid and leaves a draft labelled as an ordinary draft.

First save any text edits. For an existing translated draft, compare its
saved text with the linked **Source to review** snapshot, acknowledge that
it preserves the source's meaning,
then choose **Mark translation reviewed**. This editor action was deployed
and verified on staging `be0e9cc3`. It binds both displayed
revisions and rechecks the source relationship and editor role in one
transaction. A changed source or translation, an unavailable source, or a
published/scheduled target refuses the review; reload and compare again.

A successful review creates a new translation revision with the current
source fingerprint and records the reviewer, time and reviewed revisions.
It changes no article text, metadata, taxonomy, AI access, publication state,
source or sibling revision. The new revision carries forward validation
evidence and agent-reported claims. Claim acknowledgments stay with the
revision where they were recorded.
Ordinary draft updates retain the reviewed basis. A subsequent source
text/SEO change makes the translation stale again. This action is available
through the normal editor session and CSRF protection; MCP cannot approve
translation reviews or publish content.

Revision history identifies the recorded principal/app and links to a
side-by-side comparison of title, summary, SEO metadata and body text.
Incomplete legacy revisions show unrecorded fields rather than borrowing
current values. The existing restore action creates a new revision and checks
the displayed revision before changing content.

Publish, schedule and restore require the displayed revision. Restoring content
into a published or scheduled AI page also runs the publication gate. Shared hard validation
rechecks AI-authored content, including current links, taxonomy and media,
even after AI access is disabled. Unchecked claims and warning/info issues
are visible notices and do not block otherwise valid publication.

A refused publication says why: the editor form comes back with the reason and
the unsaved changes, and the publish toggle names the reason instead of a
generic error. A missing or invalid site URL also refuses publication, but as
a configuration problem. When a scheduled AI page fails the publication gate,
the scheduler clears its schedule, keeps it a draft and records one warning
event; a missing site URL leaves the schedule in place.

## Preserving unchanged editor HTML

The local follow-up retains original body bytes when a TinyMCE save changes
only status or metadata. Actual body edits still serialize normally. The
server applies the existing body policy and then preserves stored line
endings when they are the only remaining difference. Source text/SEO edits
still make translations stale, and revision conflicts still refuse writes.
The `be0e9cc3` publication run recovered the approved English HTML through
Version History. On `6c611040`, a normal status-only save preserved the
fixture HTML byte for byte and both translations stayed Current. See the
[staging verification](launch/r17-acceptance.md#editor-preservation-staging-verification--2026-11-05).

## Session previews

Save/read results return `/LANG/preview/pages/ID/revisions/REVISION`.
This URL grants no access: the viewer needs a current editor/admin session.
It renders the recorded snapshot, including its language and image assignment,
without following subsequent edits. The ordinary staff-only draft slug route
also uses the isolated recorded renderer. Missing/incomplete snapshots return
404; theme-rendering failures return 503. Neither falls back to live content.

Previews use the active content layout and local theme assets, with a visible
revision banner. They bypass page caches and suppress scripts, tracking,
widgets, forms and external loads. Embeds become placeholders. Menus expose
only public content, including when an HTML theme calls menu helpers.
Private translation links and structured/canonical/OG URLs are omitted.
Headers enforce private/no-store, no-referrer, noindex/nofollow and a restrictive
CSP. Interactive integrations and historical asset bytes are outside the
preview fidelity promise. Taxonomy/image assignments are pinned; referenced
names and local media files use their current stored values.

Preview routes are `/preview/...` and one language-prefix segment followed by
`/preview/...`, such as `/ru/preview/pages/12/revisions/3`. Only those paths get
the preview headers and request context, and only legacy
`/preview/share/<bearer>` paths under them are redacted. A path with
`/preview/` deeper in it is ordinary public content and is served unchanged.
The middleware runs before the active languages are known, so it judges the
first segment by the language-code grammar (2–10 lowercase letters, digits or
hyphens, not a name an application route owns) and cannot tell `docs/preview/x`
from `ru/preview/x`. Page slugs and aliases therefore cannot start with
`preview`, or have it as the second segment after a language-code-shaped
first one: every page write path refuses or skips them (see
[multi-language](multi-language.md)). An alias stored before this rule, with a
`preview` segment after a first segment that fits the language-code grammar,
is still answered as a preview (no-store, noindex, no scripts), and one below
`.../preview/share` is rewritten to a 404; rename it. Aliases such as
`blog/preview/x` are not affected, because `blog` is never a language prefix.

## Explicit secure sharing

Sharing is a separate action. The owner of a plain, unscheduled, AI-editable
draft may choose a recorded revision, acknowledge the disclosure and create a
link in the editor. MCP `create_preview_link(page_id, revision)` requires
`mcp:access` and `mcp:drafts`, the current draft-authoring policy, and ownership.
Its description warns that anyone holding the link can read that snapshot.
Saving, reading, retrying or listing never creates a share link.

Links contain 32 random bytes; only SHA-256 hashes are stored. Their URL is
returned once and is absent from later reads, metadata and idempotency receipts.
New links use `/preview/share#<bearer>`. The browser handles the fragment
outside the HTTP request target ([RFC 3986 §3.5](https://www.rfc-editor.org/rfc/rfc3986#section-3.5)).
A standalone page, with no theme, integrations or external resources, clears
the fragment and any client-added tracking query from the current URL before
submitting the bearer as a form body to fixed `/preview/share/open`. That
response uses the ordinary isolated renderer and its script-blocking CSP.
The two paths differ so reopening another share causes a fresh request rather
than changing only the fragment of an already-rendered preview.

The landing and its POST are rate limited per client address (two requests a
second, burst of twenty). A limited request gets a plain-text 429 page, not the
API's JSON error, because people open these URLs in a browser.

An anonymous share shows the author box only when the recorded revision's
author is the user who issued the link. If authorship was reassigned after the
revision was recorded, the share omits the box, so a link never publishes
another user's name, avatar, bio or links. Signed-in editor previews still show
the recorded author.

Opening share links requires JavaScript; the landing explains this when it is
disabled. Copy the entire issued URL, including its fragment. Non-browser
consumers must POST exactly one `token` form field with content type
`application/x-www-form-urlencoded` to `/preview/share/open`. A GET of
`/preview/share` returns only the credential-free landing, never draft content.
Query credentials,
encoded endpoint aliases, duplicate fields, unsupported formats and bodies
larger than 256 bytes cannot redeem a link. The landing ignores tracking
queries, including ChatGPT's `utm_source`, and never treats them as credentials.

The transport migration revokes every previously unrevoked preview link,
including expired legacy records, without changing accounts, sessions, grants,
keys or content. Rollback does not undo those revocations. This prevents an
already-logged old bearer from being repackaged into the new POST transport.
Legacy `/preview/share/<bearer>` requests return 404 without redirection.
Run the normal startup migration before accepting requests, and do not run
an old URL-issuing binary concurrently against the upgraded database.

The default lifetime is 24 hours, bounded by the issuing credential's expiry.
Set `OCMS_PREVIEW_LINK_TTL_HOURS` to 1–24 to shorten it. Limits are ten active
links and sixty issuances per user per rolling hour; revocation does not reset
the issuance limit, and a request past it is answered 429 with that reason.
Revoke links from the editor's preview panel. Issuing and revoking a link are
both recorded in the event log; the token never is.

Each read rechecks current account/role, owner, AI opt-in, draft/schedule and
issuing key/grant state. Explicit revocation, publication, scheduling, deletion,
owner change, disabling page AI access, role loss, credential revocation and
session-version rotation invalidate links. Durable invalidation prevents those
links reappearing when a setting is reversed. Missing, expired, malformed or
revoked links all return 404 on redemption; a server-side fault returns 503, so
a valid link never looks expired. The fixed GET landing itself has
no credential or draft content. A link never grants editor or MCP permissions.

## Proxy and telemetry logging

New share URLs contain no credential in the request target. The application
also redacts rejected legacy paths before its loggers and integrations; keep
equivalent upstream redaction for historical/invalid requests. Do not capture
request bodies, response bodies, query arguments, referers or redirect headers
for preview traffic. A minimal nginx access-log pattern for legacy paths is:

```nginx
# http context; use this format on the oCMS server block.
map $uri $ocms_log_path {
    default $uri;
    ~^(/[^/]+)?/preview/share/ /preview/share/[redacted];
}
log_format ocms_safe '$remote_addr $request_method $ocms_log_path '
                     '$server_protocol $status $body_bytes_sent';
# server context:
access_log /var/log/nginx/ocms-access.log ocms_safe;
```

Apply equivalent redaction/exclusion to error logs, CDN/WAF tracing, APM and
hosting request logs; do not enable request dumps. The nginx example is
configuration guidance, not proof about an existing host's logs.

**Earlier Fly staging check, 2026-11-02 (`ff4de3b1368c`):** application logs redacted
real test bearer paths, query and referrer markers. A controlled proxy-error
request using a non-credential canary recorded the full path in
`meta.URL.Full` with provider `proxy`. No real bearer was used for that error
probe, and all synthetic test links were revoked afterward. Fly also
[documents request-path logging on proxy errors](https://fly.io/legal/security-questionnaire/)
under COMPLIANCE-12. An application logger or nginx inside the machine cannot
redact an earlier Fly Proxy record. This blocked public sharing on that
URL-token build until its credential transport was replaced.

**Fragment/POST rollout, 2026-11-02 (`31c55f8c9f67`):** the exact commit is
deployed and the transport migration applied. Fresh Claude web and ChatGPT
web calls through the dedicated Editor connectors issued fragment links.
Both opened anonymously at the pinned revision, including ChatGPT's actual
tracking-query link. Session-preview denial, explicit revocation and
controlled expiry returned 404. All 13 captured browser requests excluded
bearers from URLs and referrers; response Location headers also excluded
them. The bearer appeared only in the fixed-endpoint POST body.

Eight induced Fly proxy errors and 247 complete application/proxy log records
contained neither real test bearer; the non-credential POST-body canary was
also absent. These proxy records had empty URL fields, so this is not proof
of proxy redaction. Error probes replayed the observed HTTP targets with
non-credential content because browser header injection was unsupported.
Browser network traces provide the direct evidence that the new transport
keeps credentials out of request targets. Both disposable links are revoked;
content, review data and existing OAuth grants were preserved. See the
[rollout record](../.claude/docs/PHASE5.md#r16-preview-transport-fly-rollout--2026-11-02)
for build identity, direct checks and remaining R1.7 coverage.

Local browser checks additionally covered 24 requests through a simulated
upstream, including a pre-application 503, with no bearer in URL or referrer
fields. No Fly logging policy was changed. Keep request/response body dumps
and external telemetry capturing credentials disabled: the token necessarily
travels in the HTTPS POST body.

See [draft authoring](draft-authoring.md), [page writes](page-writes.md) and
[connected-app acceptance](desktop-authoring.md).
