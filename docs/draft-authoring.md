# Draft authoring over MCP

R1.5 lets connected agents save drafts for review through the same atomic page
writer as the editor and REST API. oCMS does no text generation. No MCP tool
can publish, schedule, delete, change ownership or enable AI access to a page.
R1.5 staging acceptance is recorded in PHASE5 implementation notes. R1.6 adds
editorial review and secure previews. Claude web and ChatGPT web draft/review
checks passed on `ff4de3b1368c`; fresh fragment/POST sharing passed on
`31c55f8c9f67` (2026-11-02). Broader release acceptance remains R1.7 work.

## Enable and authorize

Enable **MCP Server** in Admin → Modules, then open Admin → MCP Server:

1. Turn on **Enable draft authoring** (off by default).
2. Choose the external-link policy: keep, add nofollow, or strip hyperlinks.
3. Authorize an editor/admin account's app for `mcp:access` and `mcp:drafts`.

The `mcp:drafts` scope does not require `pages:write` or `taxonomy:write`.
New tags/categories are not created. `OCMS_AI_DISABLED=true`, an inactive MCP
module or demo mode blocks draft tools. General published/context reads remain
available under their existing rules. The retired compatibility probe and its
environment flag no longer exist. Upgrades keep draft authoring disabled.

OAuth scope upgrades use normal fresh sign-in/MFA and consent. API keys keep
expiry, global/per-key CIDR, lifetime and anomaly policies. Current account,
role, module/toggle, credential/scopes and source policy are checked again in
the write transaction. A changed link/read policy makes a new write retryable
with its original request ID. Storage faults while checking module state,
draft settings, the account or its editor role, credentials or API-key source
policy return `write_retryable` and produce a server-side diagnostic. This
applies to initial authorization and transaction-time rechecks, including
idempotent draft replays and preview link issuance. For `write_retryable`,
wait at least the indicated `retry_after_seconds` before each retry, keep the
same `request_id` when the call takes one, and stop after two retries. If
both fail, report the persistent failure to the editor or administrator.
A missing account or a removed editor role still denies access. These checks
still refuse a disabled module or draft toggle; only a credential that is gone, no longer the
caller's or refused by policy is the 401 "Credential unavailable", so an
agent never drops a working key over a database hiccup.
Likewise the HTTP layer answers a failed lookup of an API key's owner with a
500 instead of a 401. Credentials and consent never come from draft
input. Turning a feature on does not expand an app's granted scopes.

## Tools and annotations

The catalog has 21 registrations: 14 existing reads, six draft operations
and explicit share-link issuance.

| Tool | Input | Behavior |
|---|---|---|
| `create_draft_page` | `request_id`, `draft` | Create an owned, AI-editable, unscheduled draft. |
| `update_draft_page` | `request_id`, `page_id`, `base_revision`, `changes` | Apply selected changes to an owned AI-editable plain draft. |
| `create_translation_draft` | `request_id`, `source_page_id`, `source_revision`, `language_code`, `draft` | Create and link a target-language draft atomically. |
| `get_my_draft` | `page_id` | Current HTML/derived Markdown, revision, warnings and provenance. |
| `list_my_drafts` | Optional `language_code`, `page`, `per_page` | Own AI-editable plain drafts only; at most 100 per page. |
| `get_translation_status` | `page_id` | Visibility-filtered active-language membership and freshness. |
| `create_preview_link` | `page_id`, `revision` | Explicitly issues one expiring, revision-pinned bearer link; warns about disclosure. |

Reads are read-only, non-destructive and idempotent. Creates/translations are
additive, non-read-only and idempotent under their retained request key.
Updates are replacing/destructive and idempotent. Share issuance is
additive/non-idempotent. All declare a closed world; none grants publication.
`get_site_info.server.read_only` reports the current draft-authoring setting.

## Save contract

Read `get_page_draft_schema` and `get_authoring_context` first. The distinct
[create/update contracts](draft-contract.md) retain omission and null:

```json
{
  "request_id": "b52d8972-9e36-4e7c-8a26-b0d890d62d3e",
  "draft": {
    "title": "An article for review",
    "summary": "A concise summary.",
    "body_markdown": "## Introduction\n\nDraft text grounded in selected pages.",
    "references": [{"ref": "12", "role": "source"}],
    "reported": {"claims_to_verify": ["Check the stated software version."]}
  }
}
```

Use a new, non-zero UUID once for each logical create/update/translation.
Reuse it on network failures, timeouts and retryable errors. Do not generate
a replacement UUID merely because the response was lost.

A save returns `page_id`, `revision`, `admin_url`, `preview_url`, `issues`
and `translation_status`. Warnings/info do not block; error-level issues do.
The preview URL is language-qualified and revision-pinned. Opening it requires
an editor session. Saves, reads and retries never mint or persist a bearer
preview token. Sharing requires the separate explicit `create_preview_link`
action. See [editorial review](editorial-review.md) for expiry, revocation,
private rendering and proxy-log configuration.

Share issuance distinguishes configuration and quota refusals.
`site_url_required` asks the administrator to configure a valid HTTPS site
origin (HTTP is allowed only for localhost or loopback).
`preview_limit_reached` means the active-link cap: ask the editor to revoke
a link or wait for expiry. Both have `retryable=false`.
`preview_rate_limited` means the rolling hourly issuance cap, including
revoked links, and has `retryable=true` with `retry_after_seconds`.
Revoking links cannot clear that hourly cap. A retry still rechecks all
eligibility and quota rules; another issuance can consume the available slot.
Storage faults while checking sharing policy or reserving the SQLite writer,
including SQLite lock contention, return `write_retryable` and produce a
server-side diagnostic. A failed issuance creates no link and consumes no
quota. An explicit `share_links_disabled` refusal stays non-retryable.

Updates require the last-read `base_revision`. Omission preserves fields;
arrays replace collections and empty arrays clear them. Explicit null clears
nullable SEO/image/category fields. Author, language, page type, lifecycle,
AI access and verified provenance cannot be supplied in `changes`.
Metadata-only changes preserve stored rich HTML byte-for-byte. Existing links
are inspected for review without applying a new link policy to old HTML.
Oversized legacy-body link review is explicitly marked truncated. Body
replacement uses Markdown with existing `media:ID` images and the selected
external-link policy. No remote image or content URL is fetched.

## Ownership, retries and quotas

Ownership is the authenticated oCMS user, not a key or OAuth connection. A
second app for that user can continue the same draft. Admin credentials have
no implicit right to other users' AI drafts. Private reads and updates require
an owned page with AI access enabled, `status=draft` and no schedule.
Human drafts without AI opt-in, foreign drafts, published/scheduled pages and
missing pages share the generic inaccessible/not-found response.

Typed input is hashed before normalization, defaults, media and slug lookup.
Fresh authorization and retained-request lookup precede current validation
and CAS. The write transaction repeats both checks and prepares the receipt
before committing. A lost committed response replays its original receipt
even after the page or mutable defaults changed. Different input under the
same ID returns `idempotency_key_reused`. A storage fault while reading
the committed receipt returns `write_retryable`. Retry with the same
`request_id` to recover the original receipt without creating a second draft.

The original non-secret receipt is retained for seven days, then compacted to
a permanent tombstone; deletion also preserves a tombstone. `request_expired`
never re-executes the write. Replays recheck current owned-draft access and
add no version, provenance, webhook or quota charge. Retained agent receipts
exclude full body HTML, so maximum-size articles cannot overflow replay storage.

At most 60 new successful content operations per user per rolling hour are
allowed across all grants/keys. The transaction counts retained operation
records; rejected/rolled-back writes spend no quota. `mutation_rate_limited`
has `retryable=true` and `retry_after_seconds`. Existing transport/app limits
also apply. Requests have a 512 KiB wire cap inside a 2 MiB SDK hard ceiling;
Markdown and expanded HTML remain limited to 200,000 bytes each.

### Draft limit

A user may hold at most 50 unpublished AI drafts. "AI draft" has one
definition, the one behind `list_my_drafts` and `get_my_draft`: a page the user
owns, with AI access enabled, status `draft` and no schedule. Pages that are
published, scheduled, without AI access, or owned by someone else do not count;
publishing a draft, scheduling it or ending its AI access frees its slot.

`create_draft_page` and `create_translation_draft`, the only paths that insert
a page, are refused with `draft_limit_reached` when the user already holds 50.
The code has `retryable=false`: waiting does not help, and the message tells the
agent to continue in an existing draft or ask the editor to publish or delete
one. The count runs in the write transaction ahead of the insert, so concurrent
creates cannot pass 50; a refused create writes nothing and spends no quota.
It is reported before `mutation_rate_limited`, whose retry would only run into
it. Updates to existing drafts are not affected, and a replay of a create that
already committed returns its receipt. The administrator's AI-access toggle in
the page editor is a human action on an existing page and is not limited.

### Revision retention

Every save records a complete revision. The core scheduler job
`core:ai_revision_cleanup` (every 15 minutes by default; see
[Scheduler](scheduler.md)) keeps the newest 20 revisions of each AI draft, as
defined above and for every owner, and removes older ones in atomic batches of
at most 100. Published pages, scheduled pages, pages without AI access and
legacy title/body history are never touched, and neither is the current revision.
Only revisions an agent or API key wrote are removed; an editor's saves and
system revisions stay, also on a page whose AI access was switched on later.

Beyond the newest 20, a revision is kept while something still reads it:

- an unrevoked, unexpired share link pins it;
- it is the latest revision whose evidence carries the prompt contract version
  or a translation basis (publication validation and translation status read
  "the latest such revision at or before current");
- the current evidence of an AI draft records it as a reference, because a
  later update that inherits references re-reads that revision.

A removed revision loses its version row, provenance row and claim
acknowledgments together. Retained replay receipts of a removed revision become
tombstones, as after their seven days: replaying that request returns
`request_expired`, never re-executes the write, and the request still counts
toward the hourly quota. A recorded reference whose revision is nonetheless
missing is reported as `reference_unresolved` rather than an internal error.

## References, translation and provenance

Selected references are resolved through shared site routing and current
visibility. Every role and recorded page/revision is retained, including
duplicate roles. Omitted reference changes retain those snapshots; explicit
empty references clear them. Follow-up source edits do not silently replace
the recorded reference revision. Substantial example copying yields a warning.
Reference validity establishes existence/visibility, not truth or model usage.

Translations require an active target language matching any explicit draft
language. The recorded source snapshot's title, body, summary and SEO text
fingerprint must still match current source content inside the transaction.
Publication, AI-access and sibling membership revisions do not cause staleness
when these text fields are unchanged. A changed source yields
`source_revision_conflict`; a duplicate target language anywhere in the full
component yields generic `translation_exists`. Concurrent attempts leave no
orphan page. Source content/lifecycle is never replaced by the translation.
Linking does add a revision to the source and each existing sibling, because
their translation membership changes: an editor with one of them open must
reload before saving. Those revisions are recorded as `system` changes with
transport `translation_link` and the acting account, never as an edit by the
agent's credential.

The source page/revision/fingerprint is stored as the translation basis.
Ordinary updates retain it; a later text change marks it stale rather than
claiming it was refreshed. Legacy/unlinked basis is `not_tracked`. Hidden
sources/siblings return no unreadable page IDs, revisions, titles or bodies.

An editor can explicitly review an existing translated draft against the
current source with **Mark translation reviewed**. That action creates a
revision with a fresh source basis and the reviewer identity, preserving
earlier history. It checks both displayed revisions and never changes text
or publishes. The action was deployed and verified on `be0e9cc3`:
RU 43/6 and DE 44/5 became Current against EN 27/7 without text changes.
The owner later published that component; final revisions are 9/7/6,
with both translations Current. Ordinary MCP updates continue to retain
the recorded basis. See
[editorial review](editorial-review.md#review-a-draft).

`get_my_draft` separates three objects:

- `verified`: authenticated user, grant/key, registered client ID, transport,
  revision timestamp; a public client registration is not vendor identity.
  The user and credential identifiers appear only when the current revision
  was saved by the caller's own account; otherwise only its source,
  transport and time are shown.
- `validated`: server issues, reference snapshots, link review, translation
  basis and contract version; this does not attest factual correctness.
- `reported`: untrusted model, sources, claims, notes and MCP client name/version.

Explicit reference/report-only edits can create a CAS-protected revision;
identical evidence is a no-op. Metadata-only edits never reconstruct the body.
Omitted reports retain the previous revision's agent report, including the
legacy bare-report format; `reported: {}` clears it. MCP client name/version
always describe the current call and remain untrusted. Inherited agent claims
are not proof of the current app's sources or model usage. Human revisions have
fresh empty agent reports. Translation membership
revisions on source/sibling pages do not inherit the target draft's claims.
The [shared writer](page-writes.md) commits content, assignments, AI access,
translation links, snapshots/provenance and retained request atomically.

## Errors, logging and acceptance

Tool failures preserve the REST error envelope. `structuredContent` adds
issues and retryability; text content carries the complete same JSON fallback.
Authorization failures at the HTTP layer remain 401/403 with normal challenges.
Arguments, bodies, result URLs and raw panic values are not logged. Writes
retain the 25-second cancellation bound; retry a timeout with the same UUID.

Local tests cover SDK flows, lost responses, escaped large bodies, scope/
ownership/lifecycle matrices, queued policy changes, CAS/translation races,
rollback, quotas, translation fingerprints and provenance classes. The local
HTTP/browser evidence and exact tested source are recorded under ignored
`output/r15-validation/` and in PHASE5 implementation notes.
R1.5 cloud authoring acceptance is recorded in PHASE5 implementation notes.
R1.6 local tests and browser evidence are under `output/r16-validation/`.
The [R1.6 transport rollout record](../.claude/docs/PHASE5.md#r16-preview-transport-fly-rollout--2026-11-02)
records fresh Claude/ChatGPT sharing, anonymous pinned rendering, revocation,
expiry and Fly proxy-log checks on `31c55f8c9f67`, including the empty proxy URL
fields and the browser evidence for token-free request targets. Native
Desktop/CLI authoring and broader release checks remain R1.7 work. No further
deployment or Git publication is authorized by these instructions.
