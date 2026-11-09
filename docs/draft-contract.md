# Draft contract and content validation

R1.3 provides `internal/ai/draft`: strict inputs, normalization, localized
feedback, Markdown rendering and link policy. It does not register MCP page
authoring tools. R1.4 now supplies local grounding and the shared visible-content resolver;
R1.5 connects this engine to the R1.2 draft-only transactional write service.

## Create and update inputs

A create requires `title`, `summary` and `body_markdown`; summary/body may be
empty strings. `page_type` defaults to `post` and accepts `post` or `page`.
Omitted or null `language_code` uses the site's configured default. Omitted or
null `slug` derives from the title using the existing transliterator, with an
ASCII fallback and at most ten bounded collision candidates. An explicit slug
is never silently replaced. Route, active-language-prefix and alias conflicts
are checked through the shared page-write rules.

```json
{
  "title": "Go release notes",
  "summary": "Changes for this release.",
  "body_markdown": "## Changes\n\n![Release image](media:42)",
  "tags": ["go"],
  "reported": {
    "model": "agent-reported model",
    "claims_to_verify": ["Check the release date before publication."]
  }
}
```

An update is a non-empty `Changes` object. Identity, author, language, page
type, lifecycle, AI access, revision and verified provenance are not editable
content fields. A later tool wrapper supplies target and base revision outside
`Changes`. Both decoders and generated schemas reject unknown properties,
including nested report/reference fields. Decoders also reject duplicate keys,
trailing JSON, invalid UTF-8 and excessive nesting.

| Input | Meaning |
|---|---|
| Omitted update field | Preserve the stored value |
| `title`, `summary`, `body_markdown`, `slug` | Non-null string replacement |
| `meta_title`, `meta_description` | String replacement; null clears |
| `category_id`, `featured_image_id` | Positive existing ID; null clears |
| `tags`, `meta_keywords`, `references` | Non-null arrays replace the collection; `[]` clears |
| `reported` | Non-null closed object; `{}` supplies an empty new report |

The Go `Field[T]` and `Nullable[T]` wrappers retain omitted/null/empty
distinctions when input is marshaled for R1.2 idempotency. Use `Set`,
`SetNullable` and `Clear` for direct Go calls. Their Huma schema providers expose
the underlying JSON types. `CreateSchema` and `UpdateSchema` use the same Huma
tags as future MCP adapters; returned schemas are independent copies.
Normalize after hashing the original typed request; normalization never mutates
that input.

## Validation and normalization

- Title: trimmed, 1–255 Unicode characters. Summary: at most 500 characters.
- Markdown **and resulting HTML**: at most 200,000 bytes. The JSON parser has a
  separate 2 MiB transport-independent ceiling, allowing escaped Unicode.
- At most 500 distinct link destinations per body; repeated links to the same
  URL count once. Link issues are reported once per kind, since they carry no
  URL; the per-link results are in the links list.
- At most ten distinct existing tag slugs, with exact language matching. No
  taxonomy creation. Category and media IDs must exist. A tag in another
  language is unavailable, not an invitation to create a new one.
- Existing featured images use the editor's shared 1200×800 minimum. Unchanged
  historical assignments and absent historical dimensions retain the existing
  compatibility behavior. JPEG, PNG, GIF and WebP are supported.
- SEO title: warn above 60 characters and retain the value. SEO description:
  shorten above 160 characters at a word boundary; a single longer word falls
  back to a Unicode character boundary. Return the saved value with its warning.
- Keywords: at most 50 input strings, each at most 200 characters. References:
  at most 50 closed `{ref, role}` objects, with `example`, `source` or `parent`
  roles. Reports cap model at 200 characters, notes at 10,000, and each of the
  two report lists at 50 entries of at most 2,048 characters.

`Validate` and `ValidateChanges` protect direct Go calls as well as JSON entry
points. `NormalizeCreate` and `NormalizeUpdate` return nil `Content` on any
blocking issue. Operational resolver failures return an error, not a misleading
"not found" issue. Warnings and information can accompany valid content.

References remain unverified caller inputs in this iteration. R1.4 resolves
their existence and visibility. Reported model, sources, claims and notes remain
separate from server evidence; normalization does not certify that an agent used
the references or that an article's claims are true. Omitted report data starts
empty for a new revision rather than inheriting another agent's report.

## Markdown and existing images

Goldmark parses Markdown with raw HTML disabled. Raw HTML nodes are rejected;
HTML inside code blocks or escaped as ordinary text remains harmless text.
Images accept canonical `media:ID` destinations only. Remote, relative-file,
`data:`, `file:` and other image destinations are rejected. Nothing downloads a
URL, generates an image or changes media records.

Existing images resolve to their stored original/available uncropped variants.
Standalone images use a figure and escaped caption, with explicit Markdown alt
text or the library alt fallback. Generated markup includes the media ID,
available responsive sources, dimensions and lazy/async loading. The shared
sanitizer permits this bounded local-upload markup; it still removes event
handlers, scripts and arbitrary remote srcsets.

Media resolution and link rewriting run before the shared HTML sanitizer.
Its output is the final HTML returned for a body replacement. Expanded HTML
is size-checked before link processing and again after final sanitization.

Stored sanitized HTML is authoritative. A metadata-only update never converts,
sanitizes or re-renders its existing body. `DeriveMarkdown` is an explicit read
representation and returns warnings for unsupported rich layout/attributes or
images it cannot map. It verifies both an image's current media identity and its
actual local URL; a forged `data-media-id` or a foreign host with a matching path
is not trusted. Existing canonical library URLs can also be resolved without
that attribute. Supported figures retain their caption as the Markdown image
title. A later body replacement always requires explicit input and validation.

## Link policy

The trusted site options choose `keep` (default), `nofollow` or `strip` for
external text links. HTTP, HTTPS, mailto, relative site links and fragments are
supported. Embedded credentials, unsupported schemes, malformed URLs and unsafe
control/backslash spellings are rejected. Origin comparison normalizes IDNA,
host casing and default/numeric ports. It performs no DNS or HTTP request.

| Policy | External link output |
|---|---|
| `keep` | Keep the anchor with `rel="noopener noreferrer"` |
| `nofollow` | Keep it with `rel="noopener noreferrer nofollow"` |
| `strip` | Keep readable child content and remove the anchor |

All external URLs are listed for review, with `not_checked` or `removed` status;
the server does not claim to verify those destinations. Internal links are
checked through the injected resolver and get a warning if unavailable under
its visibility rules. Fragments remain `not_checked`. `StoreResolver` refuses
internal-link checks if the R1.4-compatible visibility callback is missing.
It does not implement a separate approximation of frontend redirects/routing.

The global page sanitizer now preserves explicit allowed link relations instead
of forcing `nofollow` on every link. This allows keep/nofollow choices to survive
both v2 persistence and frontend sanitization. Existing stored `nofollow`
relations remain when valid. Draft policy supplies `noopener noreferrer` on
external links; the sanitizer does not independently classify site origins.

## Issue catalog

`issues.go` is the closed catalog of codes, default severities and English/
Russian messages and hints. Locale entries must match it. The table includes
codes reserved for grounding, transactional writes and editorial review in
later iterations; their presence does not mean those checks run in R1.3.

| Code | Severity | Meaning |
|---|---|---|
| `invalid_input` | error | Input does not match the draft contract. |
| `title_length` | error | The title must contain 1–255 characters. |
| `summary_length` | error | The summary exceeds 500 characters. |
| `meta_title_length` | warning | The SEO title exceeds 60 characters. |
| `meta_description_trimmed` | warning | The SEO description was shortened to 160 characters or fewer. |
| `slug_invalid` | error | The slug has an invalid format. |
| `slug_taken` | error | The slug conflicts with an existing page, alias or route. |
| `tags_limit` | error | More than ten distinct tags were supplied. |
| `unknown_tag` | error | A tag is unavailable in the selected language. |
| `tag_creation_not_allowed` | error | Creating tags through draft authoring is not allowed. |
| `category_not_found` | error | The category is unavailable in the selected language. |
| `image_not_found` | error | The featured image is unavailable or is not a supported image. |
| `image_too_small` | error | The featured image is smaller than 1200×800 pixels. |
| `media_ref_not_found` | error | An inline image does not resolve to supported existing media. |
| `language_inactive` | error | The language is unavailable, inactive or cannot be routed. |
| `reference_unresolved` | error | A site reference could not be resolved. |
| `reference_not_visible` | error | A site reference is unavailable to this account. |
| `external_link` | info | The draft includes an external link for editorial review. |
| `internal_link_broken` | warning | An internal link does not resolve to visible site content. |
| `link_not_allowed` | error | A link uses an unsupported or unsafe URL. |
| `raw_html_not_allowed` | error | Raw HTML is not accepted in Markdown drafts. |
| `overlap_with_example` | warning | The draft overlaps substantially with an example page. |
| `forbidden_field` | error | The request contains a field this operation cannot change. |
| `revision_required` | error | A base revision is required. |
| `revision_conflict` | error | The page changed after it was read. |
| `idempotency_key_reused` | error | This request ID was already used for different input. |
| `request_expired` | error | The original result is expired or its page was deleted. |
| `not_ai_editable` | error | The draft is not available for AI edits. |
| `source_revision_conflict` | error | The translation source changed after it was read. |
| `translation_exists` | error | This translation group already contains the target language. |
| `body_too_large` | error | The Markdown or resulting HTML exceeds 200,000 bytes. |
| `too_many_links` | error | The body links to more than 500 distinct destinations. |
| `html_conversion_lossy` | warning | The stored rich HTML cannot be represented completely as Markdown. |
| `media_conversion_lossy` | warning | An existing image could not be converted to a media:ID reference. |

## Verification

Tests cover strict schemas and direct Go validation, retry hash distinctions,
normalization without input mutation, XSS/unsafe URL and remote-image cases,
all link policies, media/featured-image parity, loss-aware reverse conversion,
metadata-only body preservation and the full save path through R1.2. Catalog,
locale, documentation and outbound/write-boundary drift checks supplement
these behavioral tests. Parser and Markdown fuzz targets and validation/render
benchmarks run offline.

Current results and source identity are recorded in the R1.3 implementation
notes in `.claude/docs/PHASE5.md` and local ignored `output/r13-validation/`.
No new MCP CMS write endpoint, production/staging migration or deployment is
part of this iteration.

Local verification on 2026-11-01 passed the full 60-package test gate, focused
race checks, both 15-second fuzz targets and eight negative/restored drift
probes. The rebuilt disposable app passed 32 HTTP checks covering all link
policies through REST persistence and frontend rendering, unchanged body HTML
after metadata updates, draft privacy, complete revisions and responsive image
assets. Browser inspection confirmed the caption and responsive source choice.
No staging account or production content was used; the fixture key was
deactivated and removed afterward.

Vet, Staticcheck, Errcheck and scoped GolangCI passed. Full GolangCI still has
five pre-existing findings outside these changes; Govulncheck found no reachable
vulnerability and one uncalled module advisory. No independent repository audit
or real-client CMS-authoring test is implied by those results.

Tested base: `fc25f0d29fbc56be9fd8e5dd09d6b7f407e9fde9`. Runtime/test source
fingerprint: `f4f48db10a7a5a206879ecf6cbb0d97ffd8feb215a28207dfd5978de5c926d8b`.
The per-file manifest, direct behavior evidence, logs and browser screenshot are
in ignored `output/r13-validation/`. R1.4 is implemented separately; see [Authoring context](authoring-context.md). R1.5 now consumes these types through [gated draft tools](draft-authoring.md); fresh cloud-client acceptance remains pending.
