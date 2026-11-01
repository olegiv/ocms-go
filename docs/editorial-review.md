# Editorial review and revision previews

R1.6 is implemented for local verification. It has not been deployed or
accepted in connected cloud clients. Publishing remains an explicit human
action; MCP cannot publish, schedule, delete or transfer pages.

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

Translations show their lifecycle and source freshness. Freshness uses the
same content fingerprint as draft authoring: source text/SEO changes make a
translation stale; publication, AI-access changes and sibling membership do
not. Missing or historical bases are labelled explicitly.

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
the issuance limit. Revoke links from the editor's preview panel.

Each read rechecks current account/role, owner, AI opt-in, draft/schedule and
issuing key/grant state. Explicit revocation, publication, scheduling, deletion,
owner change, disabling page AI access, role loss, credential revocation and
session-version rotation invalidate links. Durable invalidation prevents those
links reappearing when a setting is reversed. Missing, expired, malformed or
revoked links all return 404 on redemption. The fixed GET landing itself has
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

**Fly staging check, 2026-11-02 (`ff4de3b1368c`):** application logs redacted
real test bearer paths, query and referrer markers. A controlled proxy-error
request using a non-credential canary recorded the full path in
`meta.URL.Full` with provider `proxy`. No real bearer was used for that error
probe, and all synthetic test links were revoked afterward. Fly also
[documents request-path logging on proxy errors](https://fly.io/legal/security-questionnaire/)
under COMPLIANCE-12. An application logger or nginx inside the machine cannot
redact an earlier Fly Proxy record. Secure public sharing on this ingress is
therefore blocked until the credential is removed from the proxy-visible URL
or the ingress is changed and verified. Use session previews for private
content in the meantime. Claude web and ChatGPT web acceptance of the tested
draft/review flows passed; the full R1.7 client matrix remains separate.

The fragment/POST transport above is a **local, undeployed fix**. Local browser
checks observed 24 requests through a simulated upstream, including an error
before application handling: none contained either test bearer in URL or
referrer fields. Pinned content, reopening, tracking queries, revocation and
expiry also passed. Fly staging still runs the earlier URL-token build; its
error-path check and fresh client-link acceptance must be repeated after a
separately authorized deployment. Keep request/response body dumps and external
telemetry capturing credentials disabled: the token necessarily travels in
the HTTPS POST body, rather than in a proxy-visible URL.

See [draft authoring](draft-authoring.md), [page writes](page-writes.md) and
[connected-app acceptance](desktop-authoring.md).
