# Authoring context and editorial guidance

R1.4 gives connected AI apps bounded, database-only site context. It adds
four read-only tools, three versioned prompts, eight resource registrations
and `/admin/editorial`. R1.5 now adds [gated draft-writing tools](draft-authoring.md)
that consume this context. There is no server-side text generation or outbound content retrieval.

## Tools

| Tool | Purpose |
|---|---|
| `get_authoring_context` | Brief, selected page references, editorial preferences and existing taxonomy/media/link choices |
| `get_page_draft_schema` | Distinct R1.3 create/update schemas and the closed English/Russian issue catalog |
| `get_style_guide` | Language override or explicitly identified site-default fallback |
| `list_presets` | Blog post, page, FAQ, how-to, release notes and bot-blog instructions |

The existing `list_pages` and `search_pages` accept `language_code`.
REST `GET /api/v2/pages` has the same optional filter. Omission keeps the
existing all-language behavior; an explicit filter requires an active,
routable language. Published FTS and draft-visible literal LIKE searches
both apply it. Page list/detail responses include the current revision.

```json
{
  "task": "write_page",
  "brief": "Explain our approach to Go modules for new contributors.",
  "language_code": "en",
  "preset": "how-to",
  "references": [
    {"ref": "12", "role": "example"},
    {"ref": "/about", "role": "source"}
  ],
  "media_search": "contributors"
}
```

Tasks are `write_page`, `translate_page` and `seo_review`. Briefs accept
multiple words and line breaks. Reference roles are `example`, `source` and
`parent`. A reference accepts a positive ID, `page:ID`, slug, path/alias or a
same-origin HTTP(S) URL. IDs are unambiguous; use a leading slash for a numeric
slug. A configured HTTP(S) site origin is required, even for ID-based context,
so returned public links never depend on the request Host.

## Reference resolution and visibility

`internal/contentpath` owns the pure routing primitives shared with language
middleware, redirects, frontend page/alias/ID/taxonomy/form handlers and
importer wildcard checks. `internal/ai/grounding` calls those same primitives:

- Active, lowercase, routable language prefixes include the configured default.
  `/en/topic` may resolve, while its canonical default-language URL is `/topic`.
  A slug URL belongs to its explicit language or the active site default.
- Redirects match the raw decoded request path before language stripping,
  in database `source_path` order. Wildcards can therefore shadow exact rules.
  Captures and query propagation use the middleware's shared implementation.
- Grounding follows at most three redirects/aliases/page-ID redirects, with a
  visited set. A host-bearing redirect target is rejected as in the frontend;
  non-HTTP schemes, foreign origins and credential-bearing references are also
  refused. Nothing fetches or resolves DNS for a submitted URL.
- Router matching retains escaped paths where chi does; alias fallback uses
  the decoded path. Direct dot-segment references are rejected. Regression
  fixtures compare actual frontend HTTP routing against grounding, including
  encoded prefixes, slugs, IDs and aliases.
- Existing taxonomy/form routes take precedence; only their normal 404 path
  may resolve a page alias. `/page/ID` follows the published target's canonical
  language route. A direct ID reference selects the stored page identity.
- Published means `status=published`, without a new date check. General draft
  reads retain the existing `AllowDrafts` plus `pages:read` policy.
- Independently, `mcp:drafts` permits authoring context for the caller's own
  AI-editable, unscheduled plain drafts. Human drafts without opt-in, other
  users' drafts and scheduled drafts do not gain access through that scope.
  Generic `get_page`/search/list reads keep their existing broader-read policy;
  full own-draft reads use R1.5's `get_my_draft` when authoring is enabled.

Missing and inaccessible references return the same `reference_unresolved`
issue, without private IDs, titles or body data. Operational database failures
remain errors. Every requested reference stays in the response with its role,
resolution issue or recorded page/revision identity. Duplicate page references
retain every role, point to `duplicate_of`, and reuse the first snapshot rather
than duplicating its body. No later revision is silently substituted.

Stored HTML remains authoritative. Context uses R1.3's explicit derived
Markdown, retaining verified existing `media:ID` images and conversion warnings.
It never rewrites a page. Follow-up page reads report their current revision;
refresh context if that differs from the recorded reference.

## Bounded offered sets and data envelope

Defaults are fixed, offline limits:

| Section | Limit |
|---|---|
| Selected references | 8; each input reference at most 2,048 bytes |
| Brief | 4,000 Unicode characters |
| Reference Markdown | 2,500 characters per reference before total budgeting |
| Tags, categories, languages | 20 items per section |
| Existing images | 8 items |
| Link candidates | 10 items |
| Serialized context data | 46,000 characters; complete prompt/tool text below 50,000 |

Offered taxonomy matches the selected language and uses existing IDs/slugs.
Existing image search matches filenames, alt text, captions and translations;
`%`, `_` and `!` are literal search characters, not wildcards. Images include
available variants, dimensions, localized alt/caption fallback and a
`media:ID` destination. Featured eligibility uses existing image-size rules,
including compatibility with missing historical dimensions. No media is
uploaded, generated or modified.

Sections return `next_offset` when more storage rows are available. Supply
`taxonomy_offset`, `media_offset`, `link_offset` or `language_offset` to
continue; ordering is deterministic. Unsafe legacy media rows are skipped
without losing the follow-up position. Truncated sections/excerpts are marked.
If budgeting is needed, text excerpts and guidance are shortened with flags;
selected reference identities/roles are never dropped. A request whose
metadata alone cannot fit is rejected instead of silently losing references.
Use the corresponding get/list tool for full guidance or an offered item ID.

The envelope labels reference/library content as untrusted data. Style guides
and presets are administrator editorial preferences, never permission to
publish or override the user. JSON serialization escapes HTML delimiters
before embedding data in a prompt. This is a prompt boundary, not a claim that
oCMS controls an external model's other knowledge or tools.

`CheckOverlap` provides a bounded warning for substantial five-word-shingle
reuse from an example excerpt (at least 40 shingles and 60% coverage). Source
and parent references and short quotations do not trigger it. It is neither
plagiarism detection nor factual verification. R1.5 consumes the warning while saving; the context read does not write drafts or certify article truth.

## Editorial administration

Administrators use **Admin → Editorial guidance** (`/admin/editorial`).
The core tables are `editorial_style_guides` and `editorial_presets`.

- One site-default guide, with optional replacements per active language.
- Six seeded presets: `blog-post`, `page`, `faq`, `how-to`, `release-notes`,
  `bot-blog`. Each can have a language override. English defaults and a Russian
  guide are seeded without overwriting later settings on repeated startup.
- Guides: 1–8,000 characters for the default; an empty override restores
  fallback. Presets need a name (1–100) and instructions (1–4,000); clearing
  both override fields restores the default preset.
- Changes use the current administrator identity, the existing Fetch Metadata
  CSRF protection, and demo-mode refusal. Editors/public users cannot manage
  these settings. Invalid submissions retain their text and do not change
  stored settings. The service rechecks the administrator role before writes.
- The page reuses installed templUI cards, labels, inputs, textareas and
  buttons, with English/Russian UI strings. It adds no frontend framework.

These are trusted configuration writes, separate from MCP's read-only context
surface. Back up a real site's database before any eventual deployment; this
iteration's migration checks use disposable local databases/copies only.

## Prompts and resource/tool parity

All three prompts take optional single tokens: `preset`, `language`, `refs`.
The compact reference syntax is comma-joined, for example
`12:example,/about:source`. The topic comes from the conversation; if missing,
the prompt asks the agent to clarify. Use `get_authoring_context` for a full
brief or references that cannot fit the token syntax.

| Prompt | Tool equivalent |
|---|---|
| `write_page` | `get_authoring_context(task="write_page")` |
| `translate_page` | `get_authoring_context(task="translate_page")` |
| `seo_review` | `get_authoring_context(task="seo_review")` |

`internal/ai/prompts` supplies the same versioned instructions to both paths.
Golden fixtures bind their content to version `1.0.0`. Required missing source
or topic information is requested conversationally; optional arguments do not
silently become fabricated content.

| Resource URI | Full equivalent tool result |
|---|---|
| `ocms://style-guide`, `ocms://style-guide/{lang}` | `get_style_guide` |
| `ocms://presets`, `ocms://presets/{lang}` | `list_presets` |
| `ocms://taxonomy/{lang}` | `get_authoring_context` for that language, including bounded choices |
| `ocms://languages` | `get_site_info`, including languages and caller access metadata |
| `ocms://draft-schema`, `ocms://issue-catalog` | `get_page_draft_schema`, including schemas and localized issues |

Template languages must be non-empty, active and routable. Unknown prompt
arguments, malformed roles/URIs and unknown languages fail with curated
JSON-RPC errors. Prompts/resources have the same current HTTP authentication,
revocation checks, 25-second cancellation bound and one scrubbed log record;
arguments, full resource URIs, content and exception text are not recorded in
those read logs. Transport authentication failures retain HTTP 401/403.

Initialize explicitly advertises empty tools/prompts/resources capabilities,
with no subscriptions or list-change notifications. The server card matches
those capabilities. Administrator-wide MCP instructions now cap at 2,400
characters so the complete instructions stay within 4,096, including the authoring orientation. Older stored text is clipped only for rendering;
its database value is not silently rewritten. Longer writing guidance belongs
in the editorial guide/preset resources.

## Verification and scope

Executed test evidence and source identity are recorded under ignored
`output/r14-validation/` and in the R1.4 implementation notes in
`.claude/docs/PHASE5.md`. Checks cover real frontend routing parity, visibility,
reference snapshots, bounded/paged data, media conversion, overlap warnings,
seed/override/rollback behavior, CSRF and roles, prompt goldens, protocol parity,
revoked credentials, capabilities, cancellation and log scrubbing.

On 2026-11-01 the uncached full suite passed 64 test packages, race checks
passed all eight affected packages, and resolver fuzzing passed 131,040
executions. Ten deliberate safeguard removals failed their targeted tests;
restored source passed. The rebuilt loopback app passed SDK/resource/prompt
parity checks, raw curl tool discovery, 27 HTTP checks and the actual guide
override/fallback browser flow. All disposable fixture keys were deactivated.
Whole-tree GolangCI retains five pre-existing findings; Vet, Staticcheck and
Errcheck pass. Govulncheck finds no reachable/imported-package vulnerability
and one uncalled module advisory. Full details and limits are in the evidence
record; these checks are not an independent repository security audit.

This local implementation does not claim a new staging deployment or fresh
real-client R1.4 compatibility. CMS creation/update/translation tools are implemented locally in R1.5;
review previews and connected-app release work remain R1.6/R1.7.
