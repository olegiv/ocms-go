# Opossum CMS (oCMS) — Phase 5 Implementation Prompt: Write with Your AI, Review in oCMS

## Product Promise

> **Write with your preferred AI, using your website's context, then review and publish in oCMS.**

The AI apps people already use do the writing: Claude (Desktop, web), ChatGPT (web), Claude Code and
Codex, using the user's chosen account and plan. They connect to oCMS over MCP; this does not imply
local model execution or unlimited subscription usage.

oCMS does no text generation. Its job:
- give the agent the site's context: style guide, presets, taxonomy, languages, existing images, and
  the pages the user chose as references;
- accept drafts **safely**: drafts only, idempotent, conflict-checked, saved atomically, owned by the
  oCMS user;
- make **editorial review** excellent: accurate previews, revision history, references, translation
  status, clear validation feedback, and a strict split between what oCMS verified and what the agent
  only reported.

**Release 1 is one complete, dependable flow.** A brief plus selected existing pages becomes a draft
with:
- title, summary, body and SEO metadata;
- existing taxonomy;
- an existing image;
- linked translation drafts;
- an editor link and a preview link.

The flow is proven in Claude Desktop and ChatGPT with real accounts.

**Release 2 adds reach:**
- the MCP Apps preview card;
- image generation and upload;
- the local Claude Desktop bridge;
- a public demo endpoint;
- the MCP Registry listing.

**Later:** usage dashboards and built-in assist (inline, bulk).

### What We May Claim (README, Docs, Demo)
- **Claim:**
  - "oCMS retrieves article context from your site and checks submitted site references for existence
    and visibility";
  - "agents can only create and edit drafts; publishing is always a human action";
  - "every revision records who saved it and with which app";
  - "no server-side model API charges for text authoring" (hosting and the user's AI plan still cost).
- **Never claim** that the agent used *only* site content. oCMS can't know what an external model or its
  other tools contributed. Agent-reported information is always labelled as reported.
- Do not name competitors.

---

## How to Use This File

- One Claude Code session per iteration. Reset context between iterations.
- Prompt format: `@.claude/docs/PHASE5.md — Implement iteration R1.N: <title>`
- This file is the spec **and** the persistent memory. At the end of every iteration, append what you
  decided, what you deviated from and why, and any follow-ups to **Implementation Notes**.
- **Iteration 0 notes (§0.1–0.9) are binding** where this spec doesn't explicitly supersede them. They
  use the **old iteration numbers**; translate them with the mapping table at the top of Implementation
  Notes.
- **Release 1 ends at R1.7.** STOP for the release before Release 2 starts.

### Running in Claude Code on the Web (Cloud Sessions)
1. Run `git submodule update --init` first (`.claude/shared` hooks are symlinks into it).
2. `../llm-wiki-go` is not available. Use `docs/` and `wiki/` for the Wiki-first lookup.
3. The pre-push test gate is not installed. Run `scripts/test-gate.sh` yourself before every push.
4. Do not edit `wiki/` (separate repo). List pending wiki changes in Implementation Notes.
5. At the STOP point, present the diff, verification results and pending manual checks. Follow
   `AGENTS.md` Git Safety in cloud sessions too: a separate explicit approval is required for the
   exact commit and another for the exact push. This file grants neither; preserve the session until
   the human chooses how to retain its changes.
6. Steps needing my accounts or my Mac (GUI client checks, Keychain, staging deploys) are listed as
   **Manual check** in the STOP summary.

---

## Critical Development Rules

### 1. Iterative Development
- **DO NOT implement everything at once.** Work through iterations in order.
- Each iteration MUST be independently testable and committable.
- **STOP after each iteration** with a summary: files changed, tests added, how to try it, manual checks,
  open questions. **Do not commit** (local sessions). I run `/commit-prepare` myself.

### 2. Follow CLAUDE.md Exactly
- `AGENTS.md` Git Safety applies to every session. `CLAUDE.md` overrides this file where they conflict,
  especially these sections: Testing Requirements,
  Test Gate, Fixes Need Drift Tests, Code Quality Requirements, License Headers, Wiki Sync, Wiki-first
  Lookup, Admin Dashboard UX Policy (templUI first) and Frontend JS Policy.
- Run `go vet`, `staticcheck`, `errcheck -ignoregenerated` and `govulncheck` before you STOP.

### 3. Dependencies
- **OAuth:** an established library for the standard OAuth mechanics is allowed **after** the R1.1
  comparison and my approval. MCP-specific parts are our own code (see Decisions #13).
- Release 2 may add `github.com/openai/openai-go/v3` (images). Later work may add
  `github.com/anthropics/anthropic-sdk-go/v2`.
- Any other new dependency needs my approval: **ask first**.
- Always use the latest stable versions (`go list -m -versions`).
- Reuse what is already in `go.mod`:
  - `modelcontextprotocol/go-sdk`
  - `google/jsonschema-go`
  - `yuin/goldmark`
  - `JohannesKaufmann/html-to-markdown/v2`
  - `microcosm-cc/bluemonday` (through `internal/security`)
  - `mozillazg/go-unidecode`
  - `golang.org/x/net/idna`
  - `golang.org/x/time/rate`
  - `golang.org/x/crypto`
- No Node in runtime pieces or in the test gate.

### 4. Tests Never Call Real Services
- `go test ./...` passes offline. MCP is tested with the go-sdk client in-process; OAuth with `httptest`
  (including a TLS server standing in for a CIMD host).
- Real-client checks are **manual**: listed per iteration and recorded in Notes.

---

## Read First (Every Iteration)

1. `CLAUDE.md`, `AGENTS.md`, **Implementation Notes** (mapping table + §0.1–0.9)
2. `../llm-wiki-go/wiki/index.md` (local sessions; it predates the MCP module)
3. `docs/mcp-module.md`, `docs/login-security.md`, `docs/multi-language.md`, `docs/media.md`, `docs/csrf.md`,
   `docs/i18n.md`, `docs/custom-modules.md`
4. Code:
   - `modules/mcpserver/` (catalog, drift tests, settings, identity, logging, transport, `requestGuard`)
   - `internal/module/module.go`
   - `internal/api/v2/{api.go,pages,media,taxonomy}`
   - `internal/handler/pages.go`
   - `internal/security/page_html.go`, `internal/util/network.go`, `internal/config/config.go`
   - `internal/auth` and the login, MFA and session flow
   - `internal/seo/wellknown.go`
   - htmx 4.0.0 and TinyMCE 8 conventions; `sri_coverage_test.go`; the CSP nonce rules

---

## Verified External Facts — Re-verify in R1.1 with Real Accounts

**Subscription policy (Anthropic).** Third-party apps may not offer Claude.ai login, may not route
requests through consumer plan credentials, and may not "collect, store, or intermediate Claude.ai
credentials or session tokens". oCMS OAuth issues **oCMS** tokens for **oCMS** accounts only.
Source: https://code.claude.com/docs/en/legal-and-compliance

**Claude custom connectors** (support article, checked 2026-10-30)
- Source:
  https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp
- Plans: Free (one connector), Pro, Max, Team (owners), Enterprise (by role). They work in Claude,
  Cowork and Claude Desktop.
- **The server must be reachable over the public internet from Anthropic's IP ranges**; no localhost.
- Authentication options:
  - "Sign in now" / "Sign in when needed" / "No sign in";
  - optional "Use your own OAuth client";
  - **fixed credentials (request headers)**, now described for Pro/Max as well as Team/Enterprise. On
    2026-10-28 my own dialog had no headers section (§0.5/§0.7), so **re-check in R1.1**.
- Tool approval prompts apply; "Allow always" is per tool.
- More details (OAuth callback `https://claude.ai/api/mcp/auth_callback`, CIMD conditions, annotations,
  240 s timeout, result size): §0.5.

**ChatGPT custom MCP** (OpenAI docs, checked 2026-10-30)
- Sources:
  - https://developers.openai.com/api/docs/guides/custom-mcp-server
  - https://developers.openai.com/apps-sdk/build/auth
- Read **and write** tools are supported. Tools without `readOnlyHint` count as writes, and writes need
  confirmation by default (per conversation).
- It runs on ChatGPT **web**; transport is SSE or streamable HTTP.
- **Secure MCP Tunnel** can be selected instead of a URL, so a public URL is not strictly required. Read
  the tunnel guide in R1.1.
- Auth: OAuth, no auth, or mixed. For OAuth: a predefined client ("static credentials"), **CIMD**
  (`none` or `private_key_jwt`), or DCR. **No API keys or custom headers.**
- Requirements:
  - PKCE `S256` advertised;
  - RFC 9207 `iss` for the stable redirect `https://chatgpt.com/connector_platform_oauth_redirect`
    (otherwise `https://chatgpt.com/connector/oauth/{callback_id}`);
  - `resource` sent on authorize and token, so bind the token audience to it;
  - advertised OIDC scopes get requested, so don't advertise them;
  - per-tool `securitySchemes`;
  - `_meta["mcp/www_authenticate"]` (with `error` and `error_description`) for tool-level auth errors.
- Plans: the docs page names none. Do not turn third-party rollout reports into a product restriction
  or support guarantee. **Test with my own account in R1.1** and record the actual availability.

**Claude Code / Codex:** both support static bearer headers and OAuth (§0.5). Codex has no MCP prompts,
so every prompt needs a tool equivalent.

**Tool annotations:** declare reads, additive creates and replacing updates separately. In the go-sdk,
`destructiveHint: false` means additive-only, so `update_draft_page` is destructive even though versions
make it reversible. `idempotentHint` describes repeated-call effects, not whether a tool is read-only.
Source: https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/

**MCP Apps** (Release 2): official extension `io.modelcontextprotocol/ui`, stable spec 2026-01-26. Key
points:
- `ui://` resources with `text/html;profile=mcp-app`;
- tools link them with `_meta.ui.resourceUri`;
- rendered in a sandboxed iframe with a restrictive default CSP;
- text fallback in hosts without support.

Source: https://github.com/modelcontextprotocol/ext-apps

**MCP Registry** (Release 2): `server.json` with templated remote URLs (`https://{host}/api/mcp`);
namespace via GitHub or DNS (`ocms.tech`).

**Go OAuth libraries** (R1.1A completed; v4.6.0 approved and added 2026-10-30)
- Recommended: `github.com/go-oauth2/oauth2/v4` **v4.6.0** (Go 1.21, MIT), with an oCMS SQLite/sqlc
  transaction adapter and explicitly constrained server configuration.
- Compared: `github.com/zitadel/oidc/v3` **v3.51.16** (Go 1.26, Apache-2.0). Both can run embedded;
  neither requires a separate identity-provider process. ZITADEL's provider adds OIDC/key/session work.
- Neither is a complete MCP authorization server. ZITADEL has a CIMD discovery field but no provider
  resolver in the inspected release; declaring the field does not implement CIMD. Both need MCP glue.
- Pinned versions, conformance matrix, dependency measurements, source links and remaining validation
  are recorded under **Iteration R1.1A — OAuth Library Comparison** in Implementation Notes.
  The earlier `fosite` exclusion remains outside this two-candidate comparison.

---

## Decisions (Fixed — Do Not Re-litigate Without Asking)

1. **Agent-native.** External agents generate; oCMS supplies context, validates, stores drafts, and runs
   review. No server-side text generation in Releases 1–2.
2. **Drafts only.**
   - Every AI write forces `status=draft` and clears `published_at` and `scheduled_at` (§0.6 #11).
   - No publish, no delete, no un-publish over MCP.
   - Requests that try to set those fields are rejected by the schema or with `forbidden_field`, never
     silently stripped. The domain service also rejects them when called without MCP schema validation.
3. **The oCMS user owns drafts.** Connected apps (OAuth grants, API keys) act **on behalf of a user**
   with explicit scopes.
   - An app may read and update that user's drafts that are **AI-editable**:
     - drafts created through MCP are AI-editable;
     - drafts a person wrote are editable by agents only after the author switches on "Allow AI
       edits".
   - So you can start in Claude, continue in Codex, and rotate credentials without losing access.
   - The app identity (grant or key, client) is kept per revision in provenance and the event log.
4. **Dependable writes.**
   - **Idempotent content writes:** create, update and translation tools require `request_id`. A retry
     with the same ID returns the original result within the replay window; an expired ID never creates
     another write (retention below).
   - **Optimistic concurrency:** updates require `base_revision` and get `revision_conflict` when the
     page changed. The **admin editor gets the same protection** against agent writes.
   - **Atomic save:** page content, taxonomy links, translation link, ownership flags, revision row,
     provenance and the idempotency record are committed in **one transaction**.
5. **What "site-only" means (enforceable scope).**
   - oCMS retrieves context only from its own database.
   - The content resolver never fetches page URLs. OAuth client-metadata retrieval is a separate,
     outbound operation with its own SSRF controls.
   - It resolves every submitted site reference for existence and visibility; that is not factual
     verification of the article or evidence that the model actually used the reference.
   - It does not claim anything about the agent's other knowledge.
6. **Links.**
   - External links are **kept** by default:
     - sanitized (http, https and mailto only);
     - `rel="noopener noreferrer"`, with `nofollow` as an admin option;
     - **listed in review**.
   - Internal links are checked against existing pages; broken ones are flagged.
   - An admin option strips external links.
7. **Markdown in, sanitized HTML out.** goldmark (raw HTML off) → `security.SanitizePageHTML` (always)
   → suspicious check → link policy. Images in Markdown reference existing media as `![alt](media:123)`
   and are resolved to proper variant URLs.
8. **Single write path:** `internal/pagewrite` + v2 services (§0.2). No raw writes from AI or MCP code.
9. **Verified vs validated vs reported.** These are three distinct classes in storage, API results and
   UI, and must never be mixed.
   - **Verified** (authenticated by oCMS): user, app grant or key, registered OAuth client ID,
     transport, timestamps, revision. A public OAuth client ID identifies the authorized registration;
     it does not prove that the caller is a genuine vendor application.
   - **Validated by oCMS:** references resolved to existing pages; links checked; taxonomy, image and
     language checked; issue list.
   - **Reported:** model name, MCP client name/version, sources the agent says it used, claims to
     verify, notes. Client display names from metadata remain labelled as registered display names,
     never verified vendor identities.
10. **Cross-cutting data is core:** provenance, revisions, AI-edit flags, idempotency records, preview
    links, OAuth storage, editorial guidance.
11. **Engine in `internal/ai`; MCP surface in `modules/mcpserver`;** no `modules/ai` in Releases 1–2.
12. **Release scope:**
    - **R1** = OAuth + client compatibility, authoring context, safe writes, translations, review,
      connected-app management.
    - **R2** = MCP Apps card, images (generate / attach), local bridge, public demo, Registry.
    - **Later** = usage dashboard, built-in assist.
13. **OAuth = library core + our MCP glue.**
    - The library handles authorization-code/PKCE checks, grant processing, token generation hooks and
      error formats. oCMS must enforce atomic code consumption, refresh rotation/reuse detection,
      client/scope/resource binding and grant revocation in its transaction/storage adapter; neither
      candidate supplies those storage guarantees automatically.
    - Our code handles PRM (RFC 9728), AS metadata extras, CIMD, RFC 8707 resource binding, RFC 9207
      `iss`, consent, grants and revocation.
    - The approved R1.1A choice is `go-oauth2/oauth2/v4` v4.6.0, added on 2026-10-30. The
      comparison with `zitadel/oidc` is recorded below. If integration fails the mandatory gates,
      present the concrete gaps and a revised integration
      proposal; do not silently fall back to writing an authorization-code/token core from scratch.
14. **No CLI shell-out to `claude -p` or `codex exec`.** Subscription use happens inside the native
    apps.
15. **Every tool's text result stands alone.** Rich UI (R2) is progressive enhancement only.

---

## Architecture

### Package Layout

```
internal/
├── pagewrite/        # §0.2 shared invariants + revision CAS + atomic save (InTx) + ForceDraft + AI-edit checks
├── provenance/       # Per-revision provenance (verified / validated / reported), event hooks
├── idempotency/      # request_id records: lookup, store-in-tx, compact replay payloads
├── preview/          # Expiring preview links (hashed tokens), theme-accurate draft rendering
├── editorial/        # Style guides & presets; admin UI /admin/editorial
├── oauth/            # Authorization server (library-backed core + MCP glue), CIMD fetcher, grants, revocation
├── store/            # + migrations & sqlc (tables below)
└── ai/
    ├── draft/        # PageDraft contract, JSON Schema, Validate/Normalize, issue catalog, md→safe HTML, link policy, media: refs
    ├── grounding/    # Reference resolver (§0.3 extraction), offered sets, envelope, duplicate guard, overlap check
    └── prompts/      # Versioned templates + golden tests
modules/mcpserver/    # resources, prompts, tool equivalents, authoring tools, spike tools (R1.1 only)
```

### Data Flow (Release 1)

```
Claude / ChatGPT / Claude Code / Codex  ── connect: OAuth (consent → grant for user U, app A) or API key of U
   │ get_authoring_context(brief, refs, preset, language)   ◄── DB only: refs resolved, style guide, taxonomy, images, languages
   │ (the connected AI application generates the content)
   │ create_draft_page(request_id, PageDraft)
   ▼
draft.Validate → issues ─┐
pagewrite.InTx: page(draft, author=U, ai_editable) + taxonomy + revision r1 + provenance(r1: verified U/A, validated, reported) + idempotency(request_id)
   ◄ {page_id, revision, admin_url, preview_url (session-authenticated), issues, translation_status}
   │ create_translation_draft(request_id, source_page_id, source_revision, lang, PageDraft) ×N
   │ update_draft_page(request_id, page_id, base_revision, changes) ── revision_conflict if changed
   ▼
Editor: preview link → revision diff → AI panel (Verified | Validated | Reported) → translation status → publish (human)
```

### Ownership and Access
- `pages.author_id` is the owner. Pages created through MCP get `author_id = acting user`.
- `page_ai_access` (new, core): `page_id` PK, `ai_editable` bool, `set_by`, `set_at`. Rows are created
  `true` for MCP-created drafts. The editor toggle "Allow AI edits" (owner or admin) flips it.
- An agent acting for user U may update page P only if all of these hold:
  - P is a plain draft (`status='draft' AND scheduled_at IS NULL`);
  - `P.author_id = U`;
  - `ai_editable = true`;
  - the app holds `mcp:drafts`.
- `get_my_draft` / `list_my_drafts` require `mcp:drafts` and return only U's plain AI-editable drafts.
  General page/context reads retain the existing published/drafts policy; ownership alone does not
  expose a human draft whose author has not enabled AI access. A translation source is readable only
  under one of these explicit rules.
- Admins have no implicit agent access to other users' drafts. They can reassign authorship in the UI.
- API keys act for `APIKey.CreatedBy`. OAuth grants act for the consenting user. Both are "apps" in
  `/account/connected-apps`.
- Recheck active account, current role, module settings and grant/key permissions on every request,
  including replayed requests. Keys retain their existing expiry, CIDR and anomaly rules. For a new
  write, recheck ownership, AI access, lifecycle and authorization inside the write transaction.
- Revocation or role removal rejects the next request, including on an existing MCP session. A write
  already committed cannot be undone by revocation; queued writes must pass the transaction check.

### Revisions, Idempotency, Atomicity
- `pages.revision INTEGER NOT NULL DEFAULT 1` (core migration).
- **Every** page-changing write increments it once per affected page inside `pagewrite.InTx`, including
  taxonomy, translation membership, featured image, authorship, lifecycle and AI-access changes:
  `UPDATE … SET …, revision = revision + 1 WHERE id = ? AND revision = ?`.
  Zero rows means `revision_conflict`; after permission checks, return the current revision and changed
  field names, not another user's content. Create starts at revision 1; no-op saves do not add revisions.
- The admin editor form carries the revision as a hidden field. A mismatch shows a conflict notice with
  "view latest" / "overwrite" (overwrite = a new CAS against the latest revision after confirmation).
- Writer compatibility is explicit:
  - MCP updates and admin edit/restore/publish/schedule/AI-access actions require the revision shown to
    the caller. Permission and lifecycle checks use the same transaction as CAS.
  - REST v2 adds `revision` to reads and optional `base_revision` to updates. Updates to AI-editable
    pages require it (`revision_required`); ordinary pages retain legacy unconditional updates when it
    is absent. A supplied revision is always enforced. Document this opt-in compatibility restriction.
  - Scheduler, media cleanup and imports also increment revisions and snapshot affected pages.
    System actions operate on fresh transactional state and narrowly update their intended fields;
    import overwrites of AI-editable pages require a captured base revision or report a conflict.
  - Trace every writer, including restore, bulk operations, `UpdatePageCreatedAt`, featured-image
    cleanup, translation/language changes, importers and developer generators. Existing allowlists do
    not exempt writes to existing pages from revision tracking.
- `page_versions` stores a complete immutable content snapshot per new revision: title, slug, language,
  body, summary, SEO fields, page type, taxonomy and media assignments, plus actor/provenance links.
  A migration seeds revision-1 snapshots of current pages; older partial versions remain marked legacy
  with no fabricated complete snapshot. Restoring creates a new revision and retains current ownership,
  AI-access and publish/schedule state. Diff/preview identify legacy fields that were not recorded.
- Every new revision has provenance, including human/API/system writes. Missing agent-report fields
  remain empty; human changes do not inherit the preceding revision's model, claims or validation stamp.
- `idempotency_keys`: `user_id`, `tool`, `request_id`, `request_hash`, nullable `result_json`,
  `entity_type`, `entity_id`, `entity_revision`, `created_at`.
  - Unique on (`user_id`, `tool`, `request_id`); written in the same transaction.
  - Require a bounded UUID `request_id`, created once per logical mutation and preserved on retries.
    Hash the typed input canonically, preserving omitted/null distinctions and including target,
    base/source revision and reported fields, excluding `request_id`. Do this before mutable defaults
    or slug/media resolution; JSON property order does not change the hash.
  - After schema parsing and fresh authorization, check for a matching committed request **before**
    current-content validation, mutable default resolution or the update's CAS: a lost commit response
    must replay with its original base revision. New requests validate and recheck the key in the write
    transaction before mutation.
  - Same key with different input → `idempotency_key_reused`. A concurrent duplicate rolls back the
    whole losing transaction before a bounded lookup of the winner; SQLite busy/timeout returns a
    retryable error with the same request ID. Failed validation creates no committed request record.
  - Keep successful non-secret results for 7 days. Then compact to a tombstone retaining the key,
    hash and entity/revision identifiers; return `request_expired`, never re-execute. Entity deletion
    also retains the tombstone. Replays recheck entity access and never store or return preview tokens.
  - A replay creates no version, webhook or mutation-quota charge. Events and cache invalidation occur
    only after the original commit; use existing delivery guarantees, without claiming exactly-once
    external webhook delivery.

### Translation Consistency
- Require `source_page_id`, the `source_revision` the agent read and an active target language.
  Resolve the stored source snapshot and compare its content fingerprint with the current source inside
  the transaction. If changed, return `source_revision_conflict`; the agent must reread and regenerate.
- Fingerprint the translated fields (title, body, summary and SEO text). Publication, AI-access toggles
  and sibling-link changes advance CAS revisions but do not make unchanged translations stale.
- In that same transaction, enforce one page per target language in the entire translation component.
  Concurrent creates with different request IDs return `translation_exists` for the loser without an
  orphan page. Source content and lifecycle are never changed by translation creation.
- Persist source page, source revision and fingerprint per translated revision. Translation status is
  `not_tracked` for legacy/unlinked provenance, rather than guessing freshness from timestamps. Refreshes
  supply a new checked source revision; otherwise the preceding translation basis remains in effect.

### PageDraft Contract

```go
type PageDraft struct {
	Title           string      `json:"title"`
	Slug            *string     `json:"slug"`
	PageType        string      `json:"page_type"` // post | page
	LanguageCode    *string     `json:"language_code"`
	Summary         string      `json:"summary"`
	MetaTitle       *string     `json:"meta_title"`
	MetaDescription *string     `json:"meta_description"`
	MetaKeywords    []string    `json:"meta_keywords"`
	BodyMarkdown    string      `json:"body_markdown"` // images: ![alt](media:123)
	Tags            []string    `json:"tags"`          // exact existing slugs offered for the chosen language
	CategoryID      *int64      `json:"category_id"`   // existing only
	FeaturedImageID *int64      `json:"featured_image_id"` // existing media only
	References      []Reference `json:"references"`    // {ref, role: example|source|parent}
	Reported        AgentReport `json:"reported"`      // model, sources_used, claims_to_verify, notes
}
```

- **Schema rules:** use separate create and update schemas; reject unknown properties. The MCP schema
  requires actual mandatory fields, rather than applying provider-specific strict-output rules to every
  tool. Any later API-provider adapter owns its stricter schema conversion; Go validation is authoritative.
- **Updates:** `changes` is a non-empty object of editable fields. Omitted means unchanged; explicit
  null clears only nullable fields; arrays replace their complete collection (empty clears it). Title,
  summary and body cannot be null. Author, language, page type, lifecycle, AI access and verified fields
  are not editable through this tool. Taxonomy uses existing, language-valid entries only in R1; no
  automatic tag/category creation or tag-creation toggle.
- Stored sanitized HTML is authoritative. `get_my_draft` returns current HTML, derived Markdown,
  revision and conversion warnings. Metadata-only updates never rewrite the body. Preserve supported
  `media:ID` references when converting back to Markdown; warn on lossy rich HTML conversion before an
  agent replaces a human-edited body.
- **Go limits:** title 1–255 runes; summary ≤ 500; body ≤ 200,000 bytes; at most 10 deduplicated tags.
  Meta title warns above 60 runes; meta description above 160 is shortened at a word boundary with an
  issue showing the normalization. Slugs obey existing route/alias rules and bounded collision retries.
  Retain the existing featured-image minimum and active/routable language checks.
- Parse Markdown and resolve only existing `media:ID` images before HTML sanitization. Remote, `data:`,
  `file:` and other inline-image sources are rejected; external text hyperlinks follow Decision 6.
  The safe allowlist must preserve the supported image/figure markup. Return normalization issues and
  the saved revision so an agent can see what was actually stored.
- **Issues:** `draft.Validate` returns `[]Issue{Code, Field, Severity (error|warning|info), Message,
  Hint}`. Codes are a closed, documented catalog in one Go file:
  - `slug_taken`, `title_length`, `meta_description_trimmed`;
  - `unknown_tag`, `tag_creation_not_allowed`, `category_not_found`;
  - `image_not_found`, `image_too_small`, `media_ref_not_found`;
  - `language_inactive`;
  - `reference_unresolved`, `reference_not_visible`;
  - `external_link`, `internal_link_broken`;
  - `overlap_with_example`;
  - `forbidden_field`, `revision_required`, `revision_conflict`, `idempotency_key_reused`,
    `request_expired`, `not_ai_editable`, `source_revision_conflict`, `translation_exists`;
  - `body_too_large`.
- Errors block the write and come back as an `isError` tool result with structured issues. Warnings and
  info are returned with a successful write and stored for review.
- Preserve the existing REST-style error envelope; add issues and retryability in `structuredContent`
  and a complete short text fallback. Transport auth failures remain HTTP 401/403 with appropriate
  challenges, rather than being disguised as successful tool results. Non-visible references return a
  generic inaccessible/not-found issue, without revealing another user's draft.

### Preview Access
- Draft tools return an ordinary **session-authenticated** preview URL by default, alongside the
  editor URL. Opening it uses existing editor authorization; saving or reading a draft never silently
  issues a public bearer link.
- `create_preview_link(page_id, revision)` is an explicit write for a user-requested share link. It
  requires current own-draft access and `mcp:drafts`, pins one recorded revision (no `latest` tokens),
  records the issuing grant/key, and explains that anyone holding the link can read that snapshot.
  Tokens use 32 random bytes, are stored only as hashes, expire after 24 h, and have per-user caps.
- Revoke share links on explicit revocation, issuer-grant/key revocation, page deletion/publication,
  author reassignment or disabling AI access. Check current account/page/grant state on every request.
  Expired, unavailable and mismatched revisions all return 404. Never include token URLs in logs,
  persisted MCP results, canonical/OG URLs or subsequent read-tool output; configure proxy log redaction.
- Reuse the active theme's content layout and assets with a preview context: skip analytics, external
  widgets, forms and third-party network loads; render embeds as placeholders. Add the draft/revision
  banner, `Cache-Control: private, no-store`, `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex,
  nofollow`. Bypass page caches and visibility-filter menus, related content and translation links.
  Theme layout fidelity does not promise live tracking, interactive integrations or historical assets.

### Database (New, Core Unless Noted)
- `pages.revision`
- `page_ai_access`
- `idempotency_keys`
- `content_provenance`: per entity **and revision**.
  - Verified: user, app type (oauth or key), grant or key ID, OAuth client ID, client metadata host,
    client display name, transport, created_at.
  - Validated: issues JSON, resolved references JSON, external links JSON.
  - Reported: model, sources_used JSON, claims_to_verify JSON, notes.
  - Plus prompt template/version, reference page/revision identities, and source page/revision/content
    fingerprint for translations. Legacy baseline provenance explicitly records unknown historical actor
    and validation state; migration time is not presented as the original authoring time.
- `claims_checks`: provenance ID, claim index, checked_by, checked_at.
- `preview_links`: `token_hash`, `page_id`, `revision` (required), `created_by`, issuing grant/key,
  `expires_at`, `revoked_at`.
- `editorial_style_guides`, `editorial_presets`.
- Existing bounded media search in R1; `media_fts` is deferred unless measured results justify it.
- `oauth_clients`, `oauth_codes`, `oauth_grants` (user + client + scopes + created and last-used),
  `oauth_tokens` (hashed, family, grant, resource, expiry, revocation).
- mcpserver `mcp_settings` additions:
  - `draft_tools_enabled` (0)
  - `external_links` (`keep` | `nofollow` | `strip`, default `keep`)

### Scopes and Permissions
- `mcp:access`: read published context (existing).
- `mcp:drafts` (new): create drafts, and read and update the acting user's AI-editable drafts. It
  doesn't require `pages:write`; the draft-actor helper grants write only with ForceDraft (§0.6 #8).
- `pages:read`: drafts visibility per `AllowDrafts` (existing).
- Effective permissions = granted scopes ∩ the user's role permissions. The public role gets nothing.
- `scopes_supported` advertises only these. No OIDC scopes.

---

## Security Requirements (Each Control Needs a Test)

| Threat | Control | Test |
|---|---|---|
| Publishing through the back door | ForceDraft; reject status, `published_at`, `scheduled_at`; updates only on plain drafts | Inject each field on create/update; untouched agent drafts never auto-publish; explicit human scheduling still works |
| Unauthorized drafts | Ownership + `ai_editable` + plain-draft rule + scope | Matrix: other user's draft, human draft without the toggle, published page, scheduled draft, missing scope |
| Lost or duplicate writes | `request_id` for all content writes; replay before CAS; compact tombstones; admin form CAS | Lost create/update responses, parallel duplicates, changed payload, expired replay, deleted entity, and agent/editor races |
| Partial writes | Single `InTx` for page, taxonomy, link, flags, revision, provenance, idempotency | Fault injection at each step → no partial rows |
| Revoked or stolen access | Short access tokens (1 h), rotating refresh with reuse detection, immediate grant revocation, current account/role checks, existing API-key policies | Revocation/disabled user → next call fails, including replay and existing sessions; removed scope → 403; refresh reuse revokes the family |
| OAuth flow attacks | PKCE S256; exact redirects (+ loopback per RFC 8252); single-use codes ≤ 60 s; `state`; RFC 8707 `resource` = MCP URL; RFC 9207 `iss`; hashed tokens; consent shows client name, client_id host, redirect host and scopes; fresh login + MFA policy; CSRF on consent | `httptest` flow suite |
| SSRF via CIMD | HTTPS only; `SSRFSafeDialContext`; host allowlist; 5 s; 64 KiB; `client_id` = document URL; cache | Private IP, other host, oversized, slow, mismatched |
| Prompt injection via site content | Envelope; data-not-instructions server text; drafts only; review | Golden prompt test; injected text cannot escalate (tools have no publish path) |
| XSS | Pipeline per Decision 7; link policy | XSS corpus through create, update and preview rendering |
| Provenance spoofing | Agent input lands only in Reported fields; Verified fields come from auth context only; UI renders the classes in separate, labelled components | Report fields containing "Verified" markers render inside the Reported component; verified fields can't be set from input |
| Preview-link leakage | Session preview by default; explicit revision-pinned share tokens; lifecycle/grant revocation; no-referrer; no external loads; log redaction | No share token on save/read; invalid links → 404; no other drafts or tokens in menus, metadata, telemetry or proxy logs |
| Secret leakage | No tokens, codes, verifiers or keys in logs or events | slog capture test |

---

## Performance Requirements
- `get_authoring_context` returns ≤ 50 000 chars by default using existing bounded search/list queries
  with deterministic ordering. Mark truncated sections and provide follow-up IDs/pagination; never
  silently drop the user's chosen reference list. Include revision and role for each resolved reference;
  provenance retains that snapshot identity rather than silently substituting its latest revision.
- `create_draft_page` / `update_draft_page` take < 100 ms server time (excluding the network), with no
  transaction around slow work.
- Measure warm OAuth processing separately from human login/consent and bounded cold CIMD fetches;
  target < 1 s for warm server processing, with cached metadata and explicit cold-fetch timeouts.
- Preview rendering costs no more than a normal page render. Previews are never written to the page
  cache.
- Benchmarks (`b.Loop()`): context build, `draft.Validate`, the md→HTML pipeline, the token lookup,
  `InTx` save.

---

## Quality Requirements
- Unit, table, fuzz (resolver, PageDraft parser, link policy, OAuth parameters), go-sdk client
  integration, `httptest` OAuth tests; `testing/synctest` for rate limits and expiry.
- Prompt golden tests (snapshot + version bump).
- **Required drift tests** (each MUST fail on revert; verify that and note it):
  1. All MCP/AI page writes go through `pagewrite.InTx`. Maintain an inventory of existing page and
     relationship writers; behavioral fixtures prove revision/snapshot handling for each. AST/query
     scans supplement these checks, including raw SQL and import/generator paths.
  2. Only the allowlisted package (`internal/oauth` CIMD fetcher) uses `net/http` client APIs among the
     new packages.
  3. MCP create/update schemas match their distinct presence/null rules and reject unknown fields;
     schema and service validation both reject forbidden lifecycle/ownership inputs.
  4. Tool classification:
     - read-only tools: `readOnlyHint: true`, `destructiveHint: false`;
     - content creates: `readOnlyHint: false`, `destructiveHint: false`, `idempotentHint: true`;
     - replacing draft updates: `readOnlyHint: false`, `destructiveHint: true`, `idempotentHint: true`;
     - explicit share-link issuance: non-read-only, additive, `idempotentHint: false`;
     - all content writes require `request_id`; updates also require `base_revision`;
     - no tool can publish or delete.
  5. Every prompt and resource has a tool equivalent.
  6. The existing MCP drift tests pass (server card now compares capabilities).
  7. Every issue code has en and ru messages and a docs entry.
  8. Every OAuth scope maps to a permission; no OIDC scope is advertised; no secret in logs.
  9. Verified, validated and reported fields render only in their own components.
  10. No spike code or flag remains after R1.5.
  11. i18n completeness; one provenance row per revision.
- **Docs:**
  - `docs/mcp-module.md`;
  - `docs/oauth.md`;
  - `docs/desktop-authoring.md` (per client);
  - `docs/editorial-review.md`;
  - README promise section;
  - README and `CLAUDE.md` env tables;
  - wiki;
  - CHANGELOG.

---

## Configuration (Env)

| Variable | Release | Default | Description |
|---|---|---|---|
| `OCMS_AI_DISABLED` | R1 | `false` | Kill switch: disables draft tools (read tools stay) |
| `OCMS_OAUTH_ENABLED` | R1 | `false` | OAuth 2.1 authorization server for `/api/mcp`; requires `OCMS_SITE_URL` (the issuer) |
| `OCMS_OAUTH_CIMD_ALLOWED_HOSTS` | R1 | seeded in R1.1 with the verified client hosts (`claude.ai`, ChatGPT's host) | Hosts allowed to serve Client ID Metadata Documents |
| `OCMS_OAUTH_PRE_REGISTERED_CLIENTS` | R1.1 | empty | Owner-reviewed public CIMD metadata JSON array; at most 20 documents / 64 KiB; no account authorization |
| `OCMS_OAUTH_ALLOW_DCR` | R1 | `false` | Allow Dynamic Client Registration (rate-limited) |
| `OCMS_OAUTH_ACCESS_TTL` | R1 | `3600` | Access token lifetime (s) |
| `OCMS_OAUTH_REFRESH_TTL_DAYS` | R1 | `30` | Refresh lifetime (rotating) |
| `OCMS_PREVIEW_LINK_TTL_HOURS` | R1 | `24` | Draft preview link lifetime |
| Retired compatibility probe | Removed in R1.5 | – | No scratch tools or environment flag remain; use gated draft authoring |
| `OCMS_AI_OPENAI_API_KEY`, `OCMS_AI_IMAGE_MODEL`, `OCMS_AI_IMAGE_DAILY_LIMIT`, `OCMS_AI_MONTHLY_BUDGET_USD` | R2 | – / `gpt-image-2` / `20` / – | Image generation |
| `OCMS_DEMO_MCP` | R2 | `false` | Public demo endpoint (demo mode only) |

---

## Release 1 — Iteration Plan

### Iteration 0: Recon and Design Notes — DONE (2026-10-28)
See Implementation Notes §0.1–0.9.

### Iteration R1.1: Client Compatibility Spike and OAuth Core

**Status: complete (2026-11-01).** The real-client matrix and external public
IPv6/TLS/forwarding acceptance are recorded in R1.1M–R1.1N. The deployed runtime
remains `c885da9071eb`; the shared development authenticator and acceptance
records accompany this closeout. R1.2 is implemented locally; see its status and
verification notes below.

Goal: prove with **my real accounts** that Claude Desktop and ChatGPT (plus Claude Code and Codex) can
connect, read context and invoke an approved write **before** the full authoring flow is built. The
spike writes isolated scratch data; actual CMS draft persistence is first proven after R1.2–R1.5.

- **Library comparison**:
  - **First checkpoint completed:** see Iteration R1.1A in Notes. Recommendation: `go-oauth2/oauth2/v4`
    v4.6.0; explicitly approved and added on 2026-10-30. The OAuth/scratch implementation, automated/HTTP checks and real-account acceptance are complete; see the current matrix and R1.1N closeout.
  - compare `zitadel/oidc` and `go-oauth2/oauth2` against a conformance checklist:
    - PKCE S256;
    - single-use codes;
    - refresh rotation + reuse detection;
    - opaque hashed tokens or JWT;
    - RFC 8414 metadata incl. `code_challenge_methods_supported`,
      `client_id_metadata_document_supported`, `authorization_response_iss_parameter_supported`,
      `token_endpoint_auth_methods_supported` incl. `none`;
    - RFC 9728 PRM;
    - RFC 8707 resource binding;
    - RFC 9207 `iss`;
    - CIMD;
    - optional DCR;
    - no forced OIDC scopes;
    - storage interface fit with SQLite/sqlc;
    - dependency weight;
    - maintenance activity;
    - license;
  - first checkpoint: record pinned versions, source links, the comparison and recommended library,
    then **STOP for my approval** before adding a dependency; the second checkpoint implements and
    tests the approved choice. An unmet checklist produces an explicit gap, not a homemade core.
- **Authorization server** (`internal/oauth`):
  - metadata;
  - `/oauth/authorize` (existing login + MFA → templUI consent → code);
  - `/oauth/token`;
  - `/oauth/revoke`;
  - optional `/oauth/register`;
  - CIMD fetcher (Security table);
  - grants and tokens storage;
  - the `/api/mcp` resource-server integration (go-sdk `auth.RequireBearerToken` + our verifier;
    401 with `resource_metadata`);
  - the ChatGPT requirements (Verified Facts);
  - metadata and issuer/resource URLs come from validated `OCMS_SITE_URL`, not request Host or untrusted
    forwarded headers; the server echoes OAuth client `state`, while clients validate it;
  - consent has its own session-bound CSRF protection, reauthentication and MFA policy;
  - CIMD fetches reject redirects and recheck allowed hosts/IPs at connection time; metadata client
    names are display data. Store user-approved scopes on each grant;
  - demo write exemptions remain R2 work; the R1 spike is disabled in demo mode.
- **Principal model**:
  - MCP requests carry a verified principal (user + app: OAuth grant/client or API key);
  - `get_site_info` returns it (who am I, which app, which scopes);
  - effective permissions = scopes ∩ role.
- **Minimal revocation UI** in `/account/security` (list and revoke grants); polished in R1.7.
- **Spike tools** `spike_save_draft` and read-only `spike_get_draft` (flagged, dev/staging only):
  - save/read a user-scoped title and short body in a bounded in-memory scratch store: 8 KiB per entry,
    one entry per user, 100 entries total, 1 h expiry; data disappears on restart;
  - the write requires `mcp:drafts`, returns a scratch ID, and is clearly labelled non-publishable;
  - these tools have no access to page, translation, media or preview-link writes and return no admin
    page URL. They test client write discovery, approval, principal propagation and revocation;
  - remove both tools and the flag in R1.5. Their results are not evidence that CMS persistence works.
- **Staging:** the target is `https://ocms-mcp-staging.fly.dev` on a separate Fly app,
  `ocms-mcp-staging` in `fra`, separate from `ocms-demo` and the production host.
  Prepared `fly.staging.toml`, a private `fly.staging.bootstrap.toml` and exact owner-run commands
  in `docs/mcp-staging-fly.md`, without changing `fly.toml` or the demo deploy script.
  Use one Machine and one persistent volume, `--ha=false`, a running minimum of one during tests,
  `OCMS_REQUIRE_MFA=true`, DCR off and demo mode off. Verify Fly proxy/client IP handling and
  sanitized Fly logs before the client matrix. R1.1C in Notes records the aligned plan and R1.1D
  records the prepared files/local evidence. `docs/oauth.md` separates Fly and nginx instructions.
  The owner authorized provisioning/deployment and retained fresh secrets in macOS Keychain.
  The Fly hostname replaces the original custom domain for R1.1, with no DNS change. The owner
  initially deferred editor creation. GUI acceptance uses the admin account;
  the dedicated Editor account now covers Claude Code OAuth and both CLI bearer
  modes (R1.1M). Additional GUI/editor flows are not claimed.
- **Manual checks, recorded as a matrix in Notes:** for Claude Desktop (custom connector), claude.ai
  web, ChatGPT web (URL and/or tunnel), Claude Code (OAuth and bearer) and Codex (OAuth and bearer):
  - connect;
  - consent;
  - list tools;
  - read context (`get_site_info`, `search_pages`, `get_page`);
  - approve scratch write, then retrieve it;
  - revoke → the next call fails;
  - reconnect.

  Record date, client/version, OS/browser, actual account tier, transport/auth mode and results. Also
  re-check Claude's headers option and ChatGPT writes. An unavailable GUI capability is **unverified**,
  not a pass or a silent Codex substitution; record the blocker and obtain a scope decision before
  claiming that GUI client is supported. Public HTTPS is the common R1 baseline; tunnel testing is optional.
- Tests: the full OAuth suite (Security table rows), principal propagation, spike flag gating.
- **Exit gate:** record every real-client matrix row, including explicit blockers, against the staged
  commit/configuration before starting R1.2. Missing required GUI passes need my scope decision;
  local SDK passes and a successful staging deployment do not satisfy client acceptance.

### Iteration R1.2: Write Foundations — Shared Path, Ownership, Revisions, Idempotency, Provenance

**Status: complete locally (2026-11-01), committed as `fc25f0d2`.** The shared boundary,
legacy writers, admin conflict flow and storage contracts are covered by local
checks. See `docs/page-writes.md` and the R1.2 implementation notes. No R1.2
push/deployment has been authorized; MCP page-authoring tools remain R1.5.

- §0.2 in full:
  - `internal/pagewrite`;
  - admin `Translate` fixed;
  - v2 `WriteOptions` (ForceDraft, TranslationOf);
  - in-service validation;
  - version rows;
  - suspicious event;
  - `v2.ActorForUser`;
  - `module.Context.Webhooks`;
  - featured-image size check shared.
- `pages.revision` + CAS in **all** write paths, including the admin editor form, with a conflict UI.
- Implement the writer inventory, REST compatibility rule, revision-1 backfill and legacy-version
  handling from Revisions above. Apply normal schema migration/startup checks; take a backup before
  migrating a real site and verify on a disposable copy first.
- `page_ai_access` + the "Allow AI edits" toggle in the editor (owner or admin).
- `internal/idempotency`.
- `content_provenance` per revision with the verified, validated and reported columns. Verified fields
  are filled only from the principal.
- Atomic save: page + taxonomy + translation link + flags + revision + version row + provenance +
  idempotency in one `InTx`.
- Tests:
  - CAS races (agent vs agent, agent vs editor);
  - idempotency (lost create/update responses, canonical payload order, parallel duplicates, expired
    replay/tombstone, deletion and changed permissions);
  - fault injection for atomicity;
  - ForceDraft fields;
  - ownership matrix;
  - drift tests #1, #9 (storage side), #11;
  - normal REST updates without a revision on ordinary pages; `revision_required` on AI-editable pages;
    scheduler, restore, media cleanup, imports and translation membership revision handling;
  - migration of existing pages/partial version rows; existing handler tests green.

### Iteration R1.3: Draft Contract, Validation Feedback, Safe HTML, Link Policy

**Status: complete locally; committed as `67fb3980` (2026-11-01).** Work was
verified on `codex/phase5-r1.3-draft-contract`, based on `fc25f0d2`. Full tests,
focused race/fuzz/drift checks and live local HTTP/browser verification passed.
No push or deployment was performed. See `docs/draft-contract.md` and the R1.3
implementation notes below. R1.4 is the active iteration.

- `internal/ai/draft`: types, schema, `Validate` / `Normalize`, the **issue catalog** (codes, severities,
  en/ru messages, hints), distinct create/update presence rules, slug derivation, md → safe HTML,
  `media:` image references (variant URLs,
  alt, figure), the link policy (keep/nofollow/strip; internal link check).
- Tests: XSS corpus, PageDraft fuzz test, issue catalog drift test (#7), schema drift test (#3), link
  policy table, remote inline-image rejection and metadata-only updates preserving human HTML.

### Iteration R1.4: Authoring Context — Grounding, Editorial Guidance, MCP Resources and Prompts

**Status: complete locally; committed as `1c8f71d4` (2026-11-01).**
Work is on `codex/phase5-r1.4-authoring-context`, based on R1.3 `67fb3980`.
No commit, push or deployment is authorized. See `docs/authoring-context.md`.
Local SDK, HTTP and browser checks passed; fresh cloud-client compatibility
is not claimed. Implementation notes and remaining R1.5 work are below.

- Grounding per §0.3:
  - extract the pure resolvers, with frontend and middleware switched over and their tests green;
  - bounded redirect walk;
  - IDNA and port normalization;
  - visibility (published / drafts policy / own drafts);
  - Markdown with caps;
  - envelope;
  - bounded existing media list/search (defer FTS; supersedes §0.6 #14 for R1);
  - offered sets (categories, tags, languages, link candidates, **existing images** with dimensions,
    alt and variants);
  - duplicate guard;
  - overlap check.
- Editorial (minimal): `/admin/editorial` with one default style guide, per-language overrides and
  presets (blog post, page, FAQ, how-to, release notes, **bot blog**). Seeded.
- MCP:
  - prompts: `write_page`, `translate_page`, `seo_review`;
  - resources: style guide, presets, taxonomy, languages, draft schema, issue catalog;
  - **tool equivalents**: `get_authoring_context`, `get_page_draft_schema`, `get_style_guide`,
    `list_presets`;
  - language filter for list and search;
  - §0.6 #1–#6 (single-token prompt args, parity, annotations, explicit capabilities and server card,
    instructions ≤ 4 096 chars, result size).
- Tests: go-sdk client integration, parity drift test (#5), resolver fuzz test, visibility matrix,
  unchanged frontend behaviour.

### Iteration R1.5: Authoring Tools — Create, Update, Translate, Links

**Status: committed, deployed; core GUI acceptance passed (2026-11-01).**
Commit `0afcbf618e3b` on `codex/phase5-r1.5-authoring-tools`, base `1c8f71d4`,
is deployed to `ocms-mcp-staging` with explicit approval. Claude web/Desktop and
ChatGPT passed the dedicated-account authoring and revoke/reconnect flow.
The OAuth consent description is corrected locally in English/Russian; these
catalog changes are not deployed. See the staging notes below. No Git push
occurred. The owner approved keeping
`create_preview_link` explicitly unavailable until R1.6; saves return existing
session-only draft URLs. See `docs/draft-authoring.md`.

- Tools (use the per-operation annotations in Quality #4):
  - `create_draft_page(request_id, draft)` returns `page_id`, `revision`, `admin_url`, `preview_url`,
    `issues` and `translation_status`; `preview_url` requires an editor session;
  - `update_draft_page(request_id, page_id, base_revision, changes)`;
  - `create_translation_draft(request_id, source_page_id, source_revision, language_code, draft)`, linked
    atomically with its source fingerprint and one-language-per-component check;
  - `get_my_draft(page_id)`: current HTML/derived Markdown, revision, issues, translation basis/status,
    reported info and conversion warnings;
  - `list_my_drafts(filter)`;
  - `get_translation_status(page_id)`;
  - `create_preview_link(page_id, revision)`: explicit share-link issuance only; not called automatically
    by write or read tools.
- Translation status and duplicate-language errors follow the caller's visibility rules: no unreadable
  sibling IDs, titles or content are returned, including when a hidden sibling prevents creation.
- Policy:
  - admin toggle `draft_tools_enabled`;
  - `mcp:drafts`;
  - the draft-actor helper (§0.6 #8);
  - aggregate per-user mutation limit (60/h across grants and keys), plus existing per-app/transport
    limits; successful idempotent replays do not spend another mutation;
  - the route-level body cap (§0.6 #9);
  - the external-link setting; existing taxonomy only;
  - catalog decisions (§0.6 #7);
  - the classification drift test (#4).
  - Content create/update tools declare `idempotentHint: true` under the retained-key contract; share-link
    issuance stays non-idempotent (supersedes §0.6 #3).
- Delete both scratch tools, their state and flag (#10). Update "read-only" wording everywhere (§0.6 #4).
- Tests:
  - **retries** (timeout simulation → same draft);
  - **concurrent edits** (two clients; client vs editor);
  - **revoked access** mid-session;
  - **unauthorized drafts** (matrix);
  - **publishing through status, `scheduled_at` or `published_at`**;
  - concurrent translation creation (different request IDs), changed source and non-text source changes;
  - session preview URL returned; no bearer token generated by content writes or read calls;
  - issue results;
  - "start in Claude, continue in Codex" (two grants, same user, same draft).
- **Manual check:** the full flow in Claude Desktop and ChatGPT on staging.

### Iteration R1.6: Editorial Review

**Status, 2026-11-02:** implemented and deployed on Fly staging. Claude web
and ChatGPT web draft/review checks passed on `ff4de3b1368c`; fresh
fragment/POST sharing checks passed on `31c55f8c9f67`, closing the tested
preview-URL leakage gate. See the [transport rollout record](#r16-preview-transport-fly-rollout--2026-11-02)
for direct evidence and the proxy-log limitation. The broader client matrix,
release hardening and independent security audit remain R1.7 work.

- **Preview:**
  - implement Preview Access above: editor-session previews first, explicit revision-pinned share links;
  - active-theme layout, language prefix and featured image; preview mode filters other private content
    and suppresses tracking, external embeds and form side effects;
  - draft/revision banner, noindex/no-store/no-referrer, cache bypass and token-safe proxy logging;
  - a theme failure or missing snapshot returns an explicit error, never the currently published page.
- **Revision history:**
  - list with the verified principal and app per revision;
  - side-by-side or inline diff of title, summary, meta and body text;
  - restore uses the existing admin action with CAS, a new snapshot/provenance row, and the restore
    lifecycle/ownership rules above; old partial snapshots are visibly marked.
- **AI panel** (editor sidebar), with three labelled sections:
  - **Verified:** account, grant/key, registered client ID, transport, time, revision; metadata display
    names are labelled as registered values, not proof of vendor identity;
  - **Validated by oCMS:** resolved references with links, issue list, external links list, broken
    internal links;
  - **Reported by the agent:** model, sources used, notes, and **claims to verify** as a checklist that
    the editor ticks off (stored in `claims_checks`, scoped to that revision). Checking a claim records
    an editorial acknowledgment, not server fact verification; new revisions need their own review.
- **Translation status:** per active language (missing / AI draft / draft / published / **stale** when
  the translated source fields differ from the recorded fingerprint), with links; legacy basis is
  `not_tracked`. Adding a sibling, publishing or toggling AI access must not mark translations stale.
- Page list: AI-draft badge; filter "AI drafts awaiting review"; unchecked-claims count.
- Before publishing: shared hard validation must pass. Unchecked claims and warning/info issues produce
  a non-blocking notice. Error-level draft validation cannot have been saved and cannot be overridden
  by this notice. Publish and schedule require the displayed revision so reviewed content cannot change
  between review and the action.
- templUI first; en/ru.
- Tests: default session-only access; explicit share-token lifecycle, snapshot pinning, headers and
  no external loads/private sibling leaks; legacy/current diffs and restore; panel separation (#9);
  translation fingerprints; publish/schedule after an intervening edit.

### Iteration R1.7: Connected Apps, Hardening and Release

**Status updated 2026-11-04:** the latest verified Fly staging build is
`ba044434`. Claude web and ChatGPT web passed fresh OAuth/MFA
revoke/reconnect, draft create/update/replay/conflict, share-policy and
anonymous preview/revocation checks. Original content and demo revisions
6/4/3 were preserved. An owner-approved staging-only CAPTCHA exception is
active with zero public forms; required MFA remains enabled. See the
[upgrade record](../../docs/launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04).
Klaro interactive consent/SRI checks retain their `035ac1dd` scope; fresh
JS/CSS/license delivery matched reviewed bytes during this upgrade.

The earlier Claude/ChatGPT web OAuth/share/revocation checks and the full
EN/RU/DE draft workflow in native Desktop, ChatGPT and both CLI modes are
recorded for `39d82c94` in
[release acceptance](../../docs/launch/r17-acceptance.md).
The inline alt-text repair passes all 68 test packages, lint and first-party
NilAway and is verified on `16ac7161`: both web clients read the repaired
demo pages 27/43/44 at revisions 6/4/3, with matching rendered previews.
The owner accepted the unimported OpenPGP advisory for the reviewed local
build. The Klaro/minimatch rollout is complete, and a Linux/amd64 package
recheck retained zero imported-package vulnerabilities and the module-only
warning. Native-only Desktop reconnect, fresh editorial/stale-translation
review, human publication, the recording's final segment and the planned
production article draft remain pending. See the
[release decisions](../../docs/launch/release-decisions.md).
R1.7 is not complete. Earlier bounded results remain in the
[staging record](#r17-fly-staging-and-web-client-acceptance--2026-11-02).
The earlier [native/CLI record](#r17-native-desktop-and-cli-acceptance--2026-11-02)
adds bounded native Desktop authoring and both CLIs' OAuth/bearer passes on
`df6e5d86`. Desktop reconnect required Chrome; native-only reconnect is
unverified. The [implementation notes](#r17-implementation-notes--2026-11-02)
retain local checks and benchmarks; the
[local quality follow-up](#r17-local-quality-follow-up--2026-11-02) records
the completed findings repairs, passing analyzer checks and earlier
dependency findings. The newer acceptance record supersedes the earlier
EN/RU-only fixture limitation and records publication-check coverage precisely.

- `/account/connected-apps`: the user's OAuth grants and MCP-capable API keys, with app name, client
  host, scopes, created, last used, revoke. Admin overview and revoke in `/admin/mcp`.
- **OAuth retention:** add bounded, repeatable sqlc cleanup of expired codes and eligible expired or
  revoked grant families before release. Specify the retention policy and preserve spent-refresh
  evidence for every still-active family; cleanup must not disable replay detection. Test cleanup
  boundaries, active-family reuse after cleanup, repeated runs and rollback on failure. R1.1's
  retained rows are an explicit staging limitation, not the final maintenance policy.
- **End-to-end acceptance (manual, my accounts, staging):**
  - in Claude Desktop **and** ChatGPT: brief + selected pages → draft (title, summary, body, SEO,
    existing taxonomy, existing image) → two linked translation drafts → editor and preview links →
    review → publish by hand;
  - Claude Code and Codex: the same flow through tools.
  - Record the results in Notes against the tested build/tree, settings, client versions, account tier
    and date. Both target GUI clients need a real pass; a blocked account is a pending acceptance item.
- The failure suite from R1.5 runs green; `/security-audit` and `/code-quality` findings fixed with
  drift tests; benchmark baselines.
- **Docs:**
  - `docs/desktop-authoring.md` (per client: connect, consent, prompts or tool equivalents, the review
    loop);
  - `docs/oauth.md`;
  - `docs/editorial-review.md`;
  - `docs/skills/ocms-author/SKILL.md`;
  - README promise section (the claims list above);
  - env tables, wiki (local), CHANGELOG.
- **Launch kit:**
  - `docs/launch/demo-script.md`: a **multilingual article from conversation to reviewed
    publication**, recorded; no public write endpoint needed;
  - `docs/launch/article-outline.md`: an article for a production site drafted through oCMS itself.
- Final test gate. **STOP: Release 1.**

---

## Release 2 — Reach (After the Release 1 STOP)

- **R2.1 MCP Apps preview card.**
  - `ui://ocms/draft-preview` (`text/html;profile=mcp-app`, one embedded vanilla HTML file, no external
    loads, no `innerHTML`, thumbnails as `data:` URIs), linked from the draft tools.
  - It shows the draft and the Verified / Validated / Reported sections.
  - Actions: open editor, open preview, ask for changes (`ui/message`), full screen. No write actions.
  - Text fallback drift test.
  - Recon first: go-sdk extension-capability support; which clients render it.
- **R2.2 Images.**
  - `generate_image` (OpenAI `gpt-image-2`, budget cap and ledger, timeouts per §0.6 #10) and
    `attach_media` (upload constructor per §0.6 #13, body cap per §0.6 #9).
  - Default prompt suffix: no text, no watermarks, no logos; left third calm.
- **R2.3 Local bridge** `ocms mcp-bridge`, per §0.4 "Bridge": Claude Desktop with an API key against
  `localhost` dev servers; Keychain support.
- **R2.4 Public demo endpoint.**
  - `OCMS_DEMO_MCP` (demo mode only): OAuth with the demo account, image and attach tools off, per-user
    and global caps, a "Connect your AI" page, and a daily reset that wipes drafts, grants and tokens.
- **R2.5 MCP Registry and directories.**
  - `server.json` (`tech.ocms/ocms` via DNS; remote `https://{host}/api/mcp`, defaulting to the demo
    host once R2.4 is live), schema validation and version drift tests, a publish script, a directories
    checklist.

## Later (Not Planned in Detail)
- **Usage dashboard:** image spend, MCP activity per app and user.
- **Built-in assist** (the old Part B): `modules/ai` with Anthropic, OpenAI and OpenAI-compatible
  providers (Ollama free and local); inline TinyMCE assist; bulk SEO, alt-text and tag passes; bulk
  translation; job queue. Reuses `draft`, `grounding`, `prompts`, provenance and revisions.
- **Server-side Studio** (the old Part C).
- Roadmap ideas:
  - embeddings (related posts, duplicates);
  - outline-first prompts;
  - content calendar;
  - content refresh;
  - promo pack (Telegram, X);
  - newsletter;
  - eval harness;
  - accessibility checks;
  - TTS;
  - log analysis;
  - form spam scoring;
  - external authorization servers (PRM pointing at Keycloak or Zitadel, JWT validation);
  - more MCP Apps views.

---

## Per-Iteration Checklist
- [ ] Code follows CLAUDE.md (headers, error wrapping, slog, naming, no `//nolint:dupl`)
- [ ] `sqlc generate` / `templ generate` re-run if needed
- [ ] `make assets`, server restarted, curl checks pass, new endpoints exercised; MCP via the go-sdk
      client test and a curl `tools/list`
- [ ] `scripts/test-gate.sh` green; `go vet`, `staticcheck`, `errcheck`, `govulncheck` clean
- [ ] New invariants have drift tests that fail on revert (verified)
- [ ] en and ru complete; docs updated (wiki: local only, else listed as pending)
- [ ] Manual checks listed in the STOP summary; Implementation Notes appended
- [ ] Evidence distinguishes normal/failure cases and affected existing consumers; label static/manual
      traces, executed offline fixtures and real-account checks separately. Bind results to the tested
      tree/configuration. Pending client checks cannot be described as verified compatibility
- [ ] **STOP and wait for my confirmation**

---

## Success Criteria — Release 1
- [ ] In **Claude Desktop** and **ChatGPT**, with my accounts and recorded passing results: connect to
      an already deployed/configured site (target under a minute), give a brief and pick existing pages,
      get a draft with title, summary, body, SEO, existing taxonomy, an existing image and linked
      translation drafts, open the
      editor and preview links, review, publish by hand
- [ ] Claude Code and Codex complete the same flow through tools
- [ ] Create/update retries never repeat a write, including expired request IDs; concurrent edits conflict
      cleanly (agent↔agent, agent↔editor); revoked access fails on the next request, including replays;
      unauthorized drafts are refused; agent input cannot publish or schedule
- [ ] Existing REST callers follow the documented AI-editable-page precondition; scheduler, restore,
      imports and media/translation changes preserve revision tracking; legacy migrations preserve data
- [ ] Translations cannot duplicate a component language or silently use changed source content;
      non-text changes do not create false stale flags
- [ ] Saves return session-authenticated previews; only explicit share requests mint revision-pinned
      bearer links, and preview rendering leaks no other private content or third-party requests
- [ ] Verified, validated and reported information is visibly separate in the editor
- [ ] Start in one app and continue in another, as the same user
- [ ] Recorded demo: a multilingual article from conversation to reviewed publication

---

## Implementation Notes

_Claude Code appends here after each iteration: decisions, deviations and why, client capability
findings, benchmark baselines, follow-ups._

### Iteration Number Mapping (Spec Restructured 2026-10-30)

§0.1–0.9 below were written against the earlier plan. Translate their iteration references with this
table.

| Old iteration (as referenced in §0) | New iteration |
|---|---|
| 1: Foundations | R1.2 (write path, ownership, revisions, idempotency, provenance) + R1.3 (draft contract) |
| 2: Grounding | R1.4 |
| 3: Editorial guidance | R1.4 |
| 4: MCP authoring context | R1.4 |
| 5: Draft-write tools | R1.5 |
| 6: Bridge | R2.3 |
| 7: Images | R1.5 (existing images only) + R2.2 (generation, `attach_media`, timeouts, upload constructor) |
| 8: MCP Apps | R2.1 |
| 9: OAuth | R1.1 |
| 10: Public demo | R2.4 |
| 11: Registry | R2.5 |
| 12: Release | R1.7 |
| 13–21 (Part B), Part C | Later |

### Iteration 0 — Recon and Design Notes (2026-10-28)

Read-only; no code changed. Sources: code at `master` 0b40be56, go-sdk v1.8.0 in the module cache, and
official client docs fetched 2026-10-28. `file:line` anchors are as of that commit.

**Wiki-first lookup.** `../llm-wiki-go` (compiled 2026-04-15) predates the MCP module. It has no MCP,
page-write or grounding page; that is a wiki gap. Pages consulted: multi-language, module-system, media.
Relevant contradiction: the media variant count. `docs/media.md` lists seven variants including `og`;
the project wiki lists four. Use `docs/media.md` in iteration 7.

#### 0.1 Page-write paths — invariants

Legend: V2 = `internal/api/v2/pages/service.go`, PH = `internal/handler/pages.go`.

| Invariant | v2 service | Admin handler |
|---|---|---|
| Route / language-prefix guard (`handler.PageRouteConflict`, PH:1786) | Before the tx only (`ensureSlugAvailable` V2:204-233, uses `s.queries`): **racy** | Before the tx (PH:549, 832) **and inside it** (`writePageGuarded` PH:1732-1765) |
| Slug unique vs `page_aliases` | **No**, pages only (V2:219) | Yes (`SlugOrAliasExists[Excluding]`, PH:1674, 1690) |
| Slug derived from title | No (slug required) | `util.Slugify` (unidecode) (PH:544, 819) |
| Aliases | **No** | User-supplied, after commit (PH:1996-2037) |
| Alias/redirect on slug change | No | No |
| `page_versions` row | **No** | Create always (PH:667); update if title/body changed (PH:968); outside tx, errors swallowed |
| Sanitize on write (`OCMS_SANITIZE_PAGE_HTML`) | Yes (`normalizeBody` V2:133) | Yes (PH:1967); also on render (`trustedPageBody`, frontend.go:628) |
| Suspicious block (`DetectSuspiciousHTMLTokens`) | Yes, 422 lists tokens (V2:119-130) | Yes (PH:1956) |
| Security event for suspicious HTML when not blocking | **No** | Yes (`logSuspiciousPageContentEvent` PH:1922) |
| Webhooks | created/updated/deleted after commit, no author email (V2:477, 594). **MCP's own `pages.Service` has no dispatcher** (`modules/mcpserver/module.go:200-208`; only main.go:2581 wires one) | created/updated (PH:689, 1024); published/unpublished only via TogglePublish |
| Cache | `InvalidatePage` (page + sitemap) | Same; Translate only invalidates the translation cache |
| Audit | "API: Page …", user = `APIKey.CreatedBy`, no IP/URL (V2:347) | Session user, IP, URL; extra `published_at` change event |
| Translation link | **None** | `Translate` (PH:1361-1471): no tx, **no route guard**, pages-only slug check, no webhook |
| Force draft | **No.** Any `pages:write` key can publish; `scheduled_at` on a draft is auto-published by the scheduler (`GetScheduledPagesForPublishing`, pages.sql:230) | n/a (editor chooses) |
| Title/status/page_type validation | **huma tags only** (types.go:71-76), so direct callers such as MCP skip them | Title ≥ 2 chars (PH:1657) |
| Tags | Names → `Slugify` → `GetTagBySlug` (global); creates missing tags if the actor has `taxonomy:write`, else 403; inside the tx (`pages/tags.go:26-100`) | IDs only, after commit, errors swallowed |
| Categories | Validated, inside the tx | Not validated, after commit |
| Featured image min size | **No** | Yes (`validateFeaturedImageSize` PH:1892) |
| Actor | API key only: `requireWritePerm` needs `APIKey != nil` (V2:69); **no session-user constructor** (`v2.ActorFromContext`, api.go:197) | Session user |
| Demo mode | Only blocks unpublish (V2:636) | `demoGuard` blocks all writes |

Other facts:
- The tx in V2 Create/Update covers tag resolve/create, the page row, and category and tag links.
  Cache, audit and webhook run after commit.
- `pages.Service` imports `internal/handler` for `PageRouteConflict` and `ValidateSlugFormat`.
- `translations` still uses `language_id` (migration 00020).
- The FTS5 triggers index published rows only.
- Neither path touches menus.
- The existing `internal/handler/page_route_drift_test.go` only checks that the guard's *name* appears,
  not that every write path calls it.

#### 0.2 Decision — how the v2 service will carry the invariants (iteration 1 shape)

1. **A neutral shared package.** Proposed name `internal/pagewrite`, owned by neither `handler` nor `v2`.
   It removes the `v2 → internal/handler` import.
   - `RouteConflict` (moved from `handler.PageRouteConflict`).
   - `SlugAvailable(ctx, q, slug, exceptID)`: route conflict + slug-or-alias uniqueness.
   - `InTx(ctx, db, q, slugChanged, fn func(qtx) error)`: re-runs `SlugAvailable` on the tx queries, then
     runs `fn`, then commits.
   - `RecordVersion(ctx, qtx, page, changedBy)`: inside the tx.
   - `LinkTranslation(ctx, qtx, sourceID, langCode, targetID)`: the translation-component check plus
   `CreateTranslation`, inside the tx.
   - Callers: admin `createPageGuarded` / `writePageGuarded` / `Translate`, and v2 `Create` / `Update`.
     `Translate` thereby gains the route guard, the alias-aware slug check and a transaction.
2. **v2 per-call options, not site policy.** For example `CreateWith(ctx, actor, body, WriteOptions)`.
   - `ForceDraft`: status=draft, `published_at`=NULL, `scheduled_at`=NULL.
   - Update under `ForceDraft` refuses unless the stored page is `status='draft' AND scheduled_at IS NULL`
     (a human-scheduled draft counts as "not a plain draft"). It also refuses `status`, `scheduled_at` and
     `published_at` input.
   - `TranslationOf` links through `LinkTranslation` in the same tx.
3. **In-service validation** of title (trimmed, 1–255 runes), the status enum and the page_type enum.
   Version rows on create and on a title/body change. The suspicious-content security event when not
   blocking.
4. **`v2.Actor` gains a user identity.** Proposed: `UserID int64` plus `v2.ActorForUser(user, perms)`,
   with role→permissions mapping. One `actor.userID()` helper feeds the author, the audit entry and the
   version `changed_by`. `requireWritePerm` accepts a key actor or a user actor. Drift test: nothing in
   `internal/api/v2/**` reads `APIKey.CreatedBy` outside that helper.
5. **Webhooks from module writes.** Add `Webhooks webhook.EventDispatcher` to `module.Context`
   (nil-guarded, wired in `cmd/ocms/main.go` from the existing dispatcher). `mcpserver` then calls
   `SetWebhookDispatcher`.
6. **Slug derivation lives in `internal/ai/draft`.** `util.Slugify` plus `-2`, `-3`… on collision; REST
   keeps the slug required. A collision found in the tx gets one retry with the next suffix.
7. **Parity for AI featured images.** Move `validateFeaturedImageSize` into the shared package so AI
   featured images pass the same check. A 1536×1024 image passes.
8. **Drift tests.**
   - AST: every `store.CreatePage` / `UpdatePage` call outside the importer and developer allowlists runs
     inside `pagewrite.InTx`. This replaces the name-only check.
   - The actor-helper test from point 4.
   - Revert verification for each.

#### 0.3 Frontend resolver — what grounding can reuse (iteration 2)

**Pure already.**
- `seo.CanonicalLanguagePath` (`internal/seo/sitemap.go:125`)
- `handler.PageRouteConflict`
- `util.IsValidSlug`, `IsValidAlias`, `IsRoutableLanguageCode`, `ReservedRoutePath`
- sqlc `GetPublishedPageBySlugAndLanguage`, `GetPublishedPageByAlias`, `ListEnabledRedirects`
- migrator `shared.WildcardRedirectMatchesPath` / `RedirectPathOccupied` (a duplicate of the middleware
  matcher)

**Bound to a request or receiver; extract them as pure `(ctx, q, path)` functions and make the
frontend/middleware call the extracted versions.** That keeps "reuse the frontend's resolver" literal,
and existing frontend tests prove the behaviour is unchanged.
- `middleware.matchLanguagePrefix` / `activeLanguageMap` (`internal/middleware/language.go:161-196`,
  unexported)
- `FrontendHandler.canonicalPageLanguagePrefix` (frontend.go:3335), `canonicalAliasLanguagePrefix`
  (:3360), `pageAliasLookupPath` (:3229), `explicitPageLanguage` (:3250)
- `RedirectsMiddleware.matchPathWithCaptures` / `buildTargetURL` (`internal/middleware/redirects.go:153-186,
  257`)

**Behaviour to replicate.**
- **Language prefix.** The first segment, lowercase only, must be an active and routable language,
  including the default. `/en/x` with `en` as default is served, not redirected; the canonical form drops
  the prefix.
- **Slug lookup.** Per language, `status='published'`. Slugs are globally unique.
- **Aliases.** Always a 301, and only to a page in the same language.
- **Redirects.**
  - They run before routing, on the raw path including the language prefix, and skip
    `admin|api|account`.
  - The **first match in alphabetical `source_path` order** wins, so a wildcard can shadow an exact rule.
  - A target with a host gets a 400. The query string is appended.
  - **There is no hop or loop handling anywhere.** The spec's "max 3 hops" has nothing to reuse, so
    grounding implements a bounded walk (≤ 3 hops, visited set, same-site targets only).
- **Visibility.** `status='published'`, with no `published_at` check. A scheduled page is a draft.
- **Site host.** No normalization exists: `applySiteURLOverride` (main.go:1462) doesn't lowercase the host,
  convert IDNA or strip the default port. Grounding normalizes both sides itself. `golang.org/x/net`
  v0.61.0 is already a direct dependency (used by `internal/security`), so `x/net/idna` adds no module.
- **Search.**
  - `pages_fts(title, body, meta_*)`, porter unicode61, published rows only.
  - `service.SearchPublishedPages` already takes an optional language. MCP passes none.
  - `SearchAllPages` is a LIKE search over all statuses and languages.
- **Media search.** There is no media FTS. It is LIKE on `filename` and `alt` only: no caption, no
  translations, and `%`/`_` are not escaped (`media.sql:24-28`, `media/service.go:177`).

#### 0.4 go-sdk v1.8.0 (still the latest release)

**Prompts.**
- `(*Server).AddPrompt(*Prompt, PromptHandler)`, `Prompt{Name, Title, Description, Arguments}`,
  `PromptArgument{Name, Title, Description, Required}`.
- Every argument value is a **string**, and required arguments are **not enforced** by the SDK.
- Completion goes through the single `ServerOptions.CompletionHandler`, dispatched on `Ref`.

**Resources.**
- `AddResource` / `AddResourceTemplate` (RFC 6570 via `yosida95/uritemplate/v3`).
- Exact URIs are checked first, then templates in **lexicographic** order.
- **Every `{var}` is optional** (`ocms://style-guide/` matches `ocms://style-guide/{lang}`). `{var}` does
  not match `/`. The handler receives only the URI, so it extracts variables with `uritemplate.Match`.
- `Contents` must be non-nil.
- Not found returns `-32602`.
- Do not enable subscriptions.

**Tools.**
- `ToolAnnotations{ReadOnlyHint bool, IdempotentHint bool, DestructiveHint *bool (default true),
  OpenWorldHint *bool (default true)}`.
- The current read-only tools leave `DestructiveHint` nil, so it is omitted.

**Capabilities (iteration 4 must do this).**
- `buildServer` sets only `Tools`. The SDK fills a nil `Prompts` or `Resources` with `listChanged:true`.
- A 2026-07-28 client may then open `subscriptions/listen`. That request is forced onto SSE and blocks
  into the 30 s global timeout.
- Fix: declare `Prompts: &mcp.PromptCapabilities{}` and `Resources: &mcp.ResourceCapabilities{}`
  explicitly.

**Errors and logging.**
- A plain handler error goes out as JSON-RPC code 0 with the raw `err.Error()`.
- `requestGuard`, `finishToolCall`, the 25 s bound and `identityFromRequest` are typed to
  `*mcp.CallToolRequest`. `prompts/get` and `resources/read` currently get no log line, no time bound and
  no error scrubbing.
- `identityFromTokenInfo` works for any `ServerRequest[P]`.

**2026-07-28 header checks.**
- `Mcp-Method` / `Mcp-Name` must match the body for `prompts/get` and `resources/read`.
- `Accept` must list `application/json` and `text/event-stream`.

**Bridge (iteration 6).**
- There is no built-in bridge. `StdioTransport` + `StreamableClientTransport{Endpoint, HTTPClient,
  DisableStandaloneSSE:true}` and `jsonrpc.EncodeMessage` / `DecodeMessage` (exported) suffice for raw
  forwarding to our stateless JSON server.
- The client transport has no header field, so the bearer goes in through an `http.RoundTripper`.
- Pitfalls:
  - `Write` blocks for the whole call, so use one goroutine per request.
  - A failed `Write` produces no JSON-RPC response; synthesize an error response for the request ID.
  - Non-JSON-RPC 400/401/403 bodies permanently fail the connection. Treat a bad key as fatal and report
    it on stderr.
  - The `Mcp-Protocol-Version` header is not set for legacy requests; the server defaults to 2025-03-26.
  - Split batches.
  - Exit on stdin EOF.
- `SDK/examples/server/custom-transport` is the reference `Connection`.

**Auth (iteration 9).**
- `auth.RequireBearerToken`, `TokenVerifier`, `ProtectedResourceMetadataHandler`.
- `oauthex` has client-side DCR and metadata helpers, but **no authorization-server implementation**:
  oCMS would have to build the AS endpoints itself.

**Protocol versions.**
- 2026-07-28 (stateless only), 2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05.

#### 0.5 MCP clients (verified 2026-10-28)

| | Claude Code | Claude Desktop / claude.ai (remote connector) | Claude Desktop (local stdio) | Codex |
|---|---|---|---|---|
| Static bearer | Yes: `claude mcp add --transport http ocms <url> --header "Authorization: Bearer …"`. `.mcp.json` headers expand `${VAR}`, and `headersHelper` can run a command (e.g. Keychain lookup) | **Beta, limited orgs only** ("Request headers" section; up to 4 headers; enter `Bearer <key>`). Otherwise OAuth (CIMD preferred, DCR, own client) or no auth | n/a: the bridge holds the key | `[mcp_servers.ocms] url = "…"`, `bearer_token_env_var = "OCMS_MCP_API_KEY"` (or `env_http_headers`) |
| Reaches localhost | Yes | **No** (requests come from `160.79.104.0/21`, re-confirmed) | Yes, through the bridge | Yes |
| OAuth | Yes (own CIMD, loopback redirect on any port) | Specs 2025-03-26 / 06-18 / 11-25; callback `https://claude.ai/api/mcp/auth_callback`; PKCE S256; CIMD only if AS metadata has `client_id_metadata_document_supported:true` **and** `none` in `token_endpoint_auth_methods_supported`; 401 + `resource_metadata`; `resource` must equal the URL; rotate refresh tokens; endpoints answer in < 10 s | n/a | `codex mcp login` (CIMD/DCR) |
| Prompts | Yes: `/ocms:write_page (MCP)` or `/mcp__ocms__write_page`. **Arguments are split on whitespace, one token each** | Supported per Anthropic's build docs; UI placement (the "+" menu) is **unverified** (third-party sources only) | Same as left | **No** (openai/codex#5059 open; #8342 closed as duplicate) |
| Resources | Yes: `@ocms:ocms://style-guide`; auto list/read tools | Supported (text and blob); no subscriptions, no sampling | Same as left | Yes, through the model tools `list_mcp_resources`, `list_mcp_resource_templates`, `read_mcp_resource` |
| Annotations | `readOnlyHint` → runs in parallel; `idempotentHint` → **retried once on reconnect**; `_meta["anthropic/requiresUserInteraction"]` → prompt on every call | **Every tool must declare both `readOnlyHint` and `destructiveHint`** | Same | `default_tools_approval_mode = "writes"` prompts for tools not marked read-only; per-tool `tools.<name>.approval_mode` |
| Tool timeout | `MCP_TOOL_TIMEOUT` (default ≈ 28 h); idle abort after 5 min over HTTP | **240 s** per call | Same | `tool_timeout_sec` **default 60**; `startup_timeout_sec` 10 |
| Result size | Warning above 10k tokens; limit 25k tokens (`MAX_MCP_OUTPUT_TOKENS`); text over **50,000 chars is saved to a file** unless `_meta["anthropic/maxResultSizeChars"]` (≤ 500,000) | ≈ 150,000 chars | Same | n/s |
| Instructions | Server instructions and upfront tool descriptions are **cut at 4,096 chars** (16,384 for tool-search-loaded descriptions) | n/s | n/s | n/s |

**Local stdio config.**
- `claude_desktop_config.json` is still documented on modelcontextprotocol.io: Settings → Developer → Edit
  Config; `~/Library/Application Support/Claude/claude_desktop_config.json`; `command` / `args` / `env`.
- stderr goes to `~/Library/Logs/Claude/mcp-server-<name>.log`.
- claude.com steers users to **MCPB** bundles instead: `.mcpb` with `manifest.json`, `user_config` fields
  marked `sensitive` are stored in the OS keychain, and Node is "strongly recommended" (a binary server
  type exists in the MCPB spec). An MCPB wrapper around `ocms mcp-bridge` is an optional later step; it
  would avoid a plaintext key in `env`.

**Subscription policy re-verified** (code.claude.com legal-and-compliance). It now also says developers
"may not collect, store, or intermediate Claude.ai credentials or session tokens". Part A complies.

#### 0.6 Spec corrections (apply in the iteration named)

1. **Prompt arguments (it 4).** Claude Code splits slash-command arguments on whitespace, so
   `write_page(topic, preset, language, references…)` cannot take a multi-word topic or a list of
   `ref:role` pairs that way. Proposal, pending the user's call in iteration 4:
   - All prompt arguments become optional single tokens: `preset`, `language`, and `refs` as a
     comma-joined `12:example,/about:source`.
   - When the topic is missing, the rendered prompt tells the model to take the topic from the
     conversation or ask the user.
   - `get_authoring_context` (a tool, so no tokenizing) stays the full-fidelity path.
   - Validate required arguments in the handler (the SDK does not), and return `CodeInvalidParams`.
2. **Codex has no prompts (it 4).** That makes the prompt→tool parity drift test essential, not optional.
   Resources do work in Codex.
3. **Annotations (it 4/5).**
   - Set `DestructiveHint: false` explicitly on every tool (Anthropic requires it to be declared).
   - Draft-create tools must **not** set `IdempotentHint`: Claude Code would replay them on reconnect and
     create a duplicate draft. `update_draft_page` may set it.
   - `generate_image` needs `OpenWorldHint: true`.
   - The classification test replaces both `TestEveryRegisteredToolIsReadOnly` **and** the read-only
     assertion in `TestToolsListMatchesCatalog` (`tools_integration_test.go:35-40`).
4. **Capabilities and the server card (it 4).**
   - Declare `Prompts` / `Resources` capabilities explicitly (see 0.4).
   - `seo.MCPCapabilities` has only `Tools` / `REST` (`internal/seo/wellknown.go:139`).
   - Extend `TestServerCardMatchesInitialize` to compare capabilities as well as `serverInfo`.
   - Extend `requestGuard` logging, the time bound and error scrubbing to `prompts/get` and
     `resources/read`.
   - The "read-only" wording appears in `baseInstructions`, `SiteInfo.Server.ReadOnly`, the Init log, the
     admin "read only" label and the package doc. Update all of them in iteration 5.
5. **Server instructions length (it 4).**
   - `baseInstructions` (~640 chars) + the drafts sentence + the admin text (`maxInstructionsRunes = 4000`)
     already exceeds Claude Code's 4,096-char cut, and authoring rules will add more.
   - Put the rules in prompt/tool text, keep the server instructions short, lower the admin cap so the
     total stays ≤ 4,096, and add a test for the rendered maximum.
6. **Result size (it 4).** Keep `get_authoring_context` under 50,000 chars by default (per-reference and
   total caps), or set `_meta["anthropic/maxResultSizeChars"]`. claude.ai caps results at ≈150,000.
7. **Catalog decisions (it 5).**
   - Draft tools take a PageDraft (Markdown, forced draft), not the REST body.
   - Keep `createPage` / `updatePage` in `restOperationsNotExposed`, with the reason changed to "covered
     by draft tools with a different contract".
   - Give the draft tools an `MCPOnlyReason`.
   - `TestToolInputConstraintsMatchREST` compares only path and query parameters, so mirroring the
     operations would test nothing.
8. **Permissions (it 5).**
   - The v2 write path requires `pages:write`, but `mcp:drafts` is meant not to need it. Draft tools build
     their actor through one helper that grants `pages:write` **only together with `ForceDraft`**, and
     `taxonomy:write` only when the key holds it **and** "allow tag creation" is on. A drift test confirms
     no other tool uses that helper.
   - `actorFor` strips `pages:read` when `AllowDrafts` is off, so a key cannot `get_page` its own draft.
     Own-draft reads (`list_my_drafts`, `get_my_draft`, own-draft grounding) need a provenance-scoped
     query, not the general actor.
9. **Request body cap (it 5/7).**
   - `maxRequestBodyBytes` is 256 KiB. A JSON-escaped 200,000-byte body can exceed it, and `attach_media`
     at 8 MiB becomes ≈10.7 MiB of base64. `rejectBatches` also buffers the whole body.
   - Set the SDK cap to a hard ceiling. Put our own `MaxBytesReader` in front with a limit derived from the
     settings: ~512 KiB normally, and 4/3 × `attach_media_max_bytes` + 64 KiB when `attach_media` is
     enabled. Re-check per tool after decoding.
   - The 32-request in-flight cap bounds the memory use.
10. **Timeouts for `generate_image` (it 7).**

    | Layer | Limit |
    |---|---|
    | Tool `maxToolDuration` | 25 s (`tools.go:40`) |
    | Global `middleware.Timeout` | **30 s** (`main.go:2077`), a hard ceiling |
    | Codex `tool_timeout_sec` | **60 s** by default |
    | claude.ai / Desktop | 240 s |

    - The spec's 120 s default needs `/api/mcp` to get its own route-level timeout outside the global one.
      Raise the bound for `generate_image` only, to ≤ 180 s.
    - The client snippets must set Codex `tool_timeout_sec = 180`.
    - If an exemption is unwanted, fall back to an asynchronous pattern instead: return a job ID and poll
      with `get_image_job`.
11. **ForceDraft and scheduling (it 1/5).** The scheduler publishes any `status='draft' AND scheduled_at <=
    now`. So `ForceDraft` clears `scheduled_at`, and MCP refuses to update drafts that a human scheduled.
    The status-injection tests must also cover `scheduled_at` and `published_at`.
12. **Huma-only validation (it 1).** MCP calls the services directly, so title, status and page_type checks
    must move into the service or into `draft.Validate`. Same for `taxonomy.CreateTag`, which checks that
    the language exists but not that it is active or routable.
13. **Media upload (it 7).**
    - `media.UploadedFileFromReader` is documented as a test helper and needs a seekable reader (use
      `bytes.Reader`). Promote it, or add a production constructor.
    - Limits: 20 MB maximum; MIME sniffed from the first 512 bytes against an allowlist (no SVG); images
      re-encoded, which strips EXIF.
14. **Media candidates (it 2).** The spec assumes media FTS on filename, alt and caption, but none exists.
    Add a core `media_fts` FTS5 migration with triggers (including `media_translations` alt/caption), or
    accept LIKE with `ESCAPE`. Recommendation: FTS5, to meet the "one FTS5 query" performance requirement.
15. **Webhooks (it 1/5).** "→ webhooks via the service" needs the `module.Context` dispatcher (0.2 point 5);
    without it MCP writes fire no webhooks.
16. **Demo mode (it 5).** Saving MCP settings is blocked in demo mode, so the draft-tools toggle stays off
    there. Record that as intended, and test it.

#### 0.7 Iterations 6 and 9 — what the findings decide

- **Iteration 6 (bridge): needed.** Claude Desktop can attach a bearer only through a local stdio server,
  except for organizations in the request-headers beta. The bridge is also the only way Desktop reaches
  `http://localhost` dev servers.
- **Iteration 9 (OAuth): needed for remote connectors.** Remote connectors on claude.ai web, mobile and
  Desktop can send a static `Authorization` header only in the limited request-headers beta. Everyone else
  needs OAuth. The user confirmed on 2026-10-28 that their **Add custom connector** dialog has **no
  "Request headers" section**.
  - So remote connectors (web, mobile, Desktop-remote) can reach oCMS only through OAuth.
  - Until then, Claude Desktop uses the stdio bridge (iteration 6), and Claude Code and Codex use static
    bearer headers.
  - Iteration 9 is optional per the spec and still needs the user's go-ahead after the Part A release.
- If iteration 9 runs:
  - go-sdk gives only resource-server pieces, so oCMS builds the authorization-server endpoints.
  - Support **CIMD** (Claude's published identity) first, DCR second.
  - Accept both `https://claude.ai/api/mcp/auth_callback` and port-agnostic loopback redirects for
    Claude Code.

#### 0.8 Versions checked (2026-10-28)

go-sdk v1.8.0 (latest; still pinned), `openai-go/v3` v3.90.0, `anthropic-sdk-go/v2` v2.4.0. Re-check
each in its own iteration.

#### 0.9 Pre-existing issues found (outside Phase 5 scope; fix only where an iteration touches them)

- Admin `Translate`: no route guard, pages-only slug uniqueness, no transaction, no webhook. Fixed by
  0.2 point 1.
- `page_route_drift_test.go` asserts names only. Fixed by 0.2 point 8.
- The media LIKE search doesn't escape `%`/`_`.
- Redirect precedence is alphabetical, so a wildcard can shadow an exact rule.
- v2 slug uniqueness ignores aliases. Fixed by 0.2 point 1.

### Spec update — promotion (2026-10-28, by the spec author, not an iteration)

- **Iteration 9 (OAuth) is now required.** This supersedes §0.7's "optional per the spec and still needs
  the user's go-ahead".
- **New iterations:**
  - 8: MCP Apps draft preview;
  - 10: public demo MCP endpoint;
  - 11: MCP Registry and directories.
- **The release moved from iteration 8 to iteration 12.** Numbers 0–7 and 9 are unchanged, so every
  iteration reference in §0.1–0.9 still holds.
- **Demo mode:** §0.6 #16 (the draft-tools toggle stays off in demo mode) still holds for the settings
  UI. Iteration 10 adds the env-driven `OCMS_DEMO_MCP` override for the public demo instance.
- **Part B** is trimmed to lean built-in assist and renumbered 13–21. The former Studio iterations
  (UI, Studio images, REST generation and webhooks, server-side style profile) moved to **Part C
  (deferred)**.
- **Spec-level additions:**
  - the provenance principal covers OAuth user + client;
  - the outbound-HTTP allowlist gains `internal/oauth` (CIMD fetch);
  - new core tables `media_fts` (§0.6 #14 recommendation adopted) and `oauth_*`;
  - Quality drift tests #7–#10 (MCP Apps fallback, OAuth scopes and logging, demo gating,
    `server.json` consistency);
  - a cloud-session section in "How to Use This File".

### Spec update — ChatGPT (2026-10-29, by the spec author, not an iteration)

- Added "Verified External Facts → ChatGPT" (Developer mode, web only, public HTTPS, plan caveat for
  writes, OAuth-only auth, CIMD preferred, PKCE S256 advertised, RFC 9207 stable redirect, resource-bound
  tokens, no OIDC scopes, per-tool `securitySchemes`, `_meta["mcp/www_authenticate"]`).
- Iteration 9 gains a "ChatGPT compatibility" block. Iteration 10 and the success criteria now name
  ChatGPT web, with the plan caveat.
- Scope confirmed by the user: **MCP-driven generation from desktop and chat apps (Part A) is the goal.**
  Part B stays optional after the Part A release.

### Spec update — Release 1 restructure (2026-10-30, by the spec author, not an iteration)

This restructure follows a review of the plan by Codex.

**Product promise:** "Write with your preferred AI, using your website's context, then review and
publish in oCMS."

**Release 1 = R1.1–R1.7.** OAuth and real-client testing come first.

**New requirements:**
- idempotent creation (`request_id`);
- optimistic concurrency (`pages.revision`, `base_revision`, also protecting the admin editor);
- one-transaction saves;
- user-owned drafts with an "Allow AI edits" flag and per-app grants;
- verified / validated / reported separation;
- expiring preview links;
- revision diff;
- translation status (including stale);
- a structured issue catalog;
- connected-apps management.

**Moved to Release 2:** MCP Apps card, image generation and upload, the bridge, the public demo, the
Registry. Part B and Part C → Later.

**Supersessions:**
- **§0.6 #3:** create tools may declare `idempotentHint: true`, because `request_id` is required.
- **§0.6 #8:** "own drafts" now means drafts owned by the **user** (`author_id`) and AI-editable, not
  drafts scoped to one key or client. The provenance-scoped query becomes an ownership + `ai_editable`
  query.
- **Old Decision 4 (strip external links by default):** replaced by keep + list in review + admin
  option (`keep` | `nofollow` | `strip`).
- **"Site-only grounding" wording:** limited to retrieval and reference validation (Decisions #5,
  "What we may claim").
- **The dependency rule for OAuth** is relaxed to a library core after the R1.1 comparison and my
  approval. `fosite` is excluded.

**Client facts refreshed 2026-10-30:**
- Claude documents request headers for Pro/Max as well. Re-check my dialog in R1.1.
- ChatGPT documents read/write tools, Secure MCP Tunnel, and OAuth or no auth (no API keys).

### Spec update — Implementation contracts (2026-10-30, Codex; not an implementation iteration)

Claude's seven-iteration Release 1 structure, user ownership and deferred Release 2 remain the basis.
The active sections above now resolve these implementation gaps:

- **Git control:** cloud sessions follow the same separate commit/push approvals as local sessions;
  the earlier automatic commit/push instruction is superseded by `AGENTS.md` Git Safety.
- **R1.1 ordering:** library comparison/approval precedes implementation. The compatibility probe uses
  bounded scratch state, never a page-writing bypass before R1.2 safeguards exist. Real CMS persistence
  is proven in R1.5; scratch writes do not count as that evidence. No automatic handwritten OAuth core.
- **Authorization:** fresh account/role/grant checks also cover replays and existing sessions; human
  drafts need explicit AI access. Registered client identity and metadata display names are not proof
  that the caller is the vendor's native application.
- **Writer coverage:** revision tracking includes restore, lifecycle, image cleanup, translation changes
  and imports/generators. REST adds optional `base_revision`, required for AI-editable pages. Complete
  new snapshots coexist with visibly partial legacy history; restores retain current lifecycle/ownership.
- **Retry semantics:** updates also carry `request_id`; replay precedes CAS. Seven-day response retention
  is followed by permanent compact tombstones, not deletion that allows a later duplicate. Replay does
  not duplicate versions, mutation charges or webhook dispatch; external delivery keeps existing guarantees.
- **Translation basis:** compare source content fingerprints, enforce one language per whole component
  transactionally, and represent untracked legacy provenance explicitly. Non-text revision changes do
  not mark translations stale. Hidden siblings remain inaccessible in status/conflict results.
- **Preview policy:** default URLs need an editor session. Share links are explicit, revision-pinned,
  lifecycle/grant-revocable and excluded from logs/replayed results. Active-theme preview mode suppresses
  tracking, third-party requests, form actions and unrelated private content.
- **Draft interfaces:** create/patch schemas have distinct presence/null semantics; domain limits are
  explicit, existing taxonomy is the R1 policy, metadata edits preserve body HTML, and remote inline
  images are rejected. Provider strict-output adapters are later work, not a universal MCP schema rule.
- **Lean context:** existing bounded media search is sufficient for R1 unless benchmarks justify FTS.
- **Release evidence:** both GUI targets need actual recorded passes; blocked or untested compatibility
  remains pending. Offline fixtures, static/manual traces and real-client results are separate evidence.

**Historical-note supersessions:** §0.2's partial version snapshots/old writer allowlists, §0.6 #2–#3's
universal schema/annotation rules, §0.6 #8's old key-scoped reads/tag creation, and §0.6 #14's mandatory
media FTS yield to the active Revisions, PageDraft, Ownership, Quality and R1.4 sections. Replacing draft
updates use `destructiveHint: true`; content creates use false, independent of both being draft-only.
The earlier note that every create is idempotent does not include explicit bearer-preview issuance.

**Primary annotation reference:**
https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/
The installed go-sdk v1.8.0 `ToolAnnotations` comments corroborate additive versus replacing effects.

**Verification of this edit:** document consistency checks and manual traces of the named retry,
translation, preview, writer-compatibility and approval scenarios. No application code, database,
client connection or dependency was changed; runtime and real-account checks remain implementation work.

### Iteration R1.1A — OAuth Library Comparison (2026-10-30)

Historical pre-approval checkpoint. The explicit approval and implementation update follow in R1.1B.

**Status:** comparison checkpoint complete. **Recommend `github.com/go-oauth2/oauth2/v4` v4.6.0** for
oCMS's embedded, OAuth-only MCP authorization server. Dependency approval is pending; R1.1's server,
storage adapters and real-client compatibility checks are not implemented by this checkpoint.

The recommendation is architectural judgment based on the pinned source and measurements below.
It depends on the mandatory integration gates, not on the library defaults being sufficient.

#### Versions and Fit

| Item | go-oauth2/oauth2 | zitadel/oidc |
|---|---|---|
| Latest stable checked through Go proxy and GitHub releases | v4.6.0, 2026-09-01 | v3.51.16, 2026-10-15 |
| Module / minimum Go | `github.com/go-oauth2/oauth2/v4` / 1.21 | `github.com/zitadel/oidc/v3` / 1.26.0 |
| License | MIT | Apache-2.0 |
| Commit inspected | `e444dfa1af98b5e2d4d9ff5dbb0747ad9e82c218` | `8ae30e3cf6d7b38d7fb460c8931dde6a06e5d7bf` |
| Application integration | Standard `net/http` handlers; existing chi routes and login/MFA remain in control | chi-based OpenID Provider; existing login is pluggable, but provider/storage contracts include OIDC, signing keys and userinfo |
| Current product fit | Narrow OAuth grants and application-owned storage | Better fit if oCMS itself becomes an OpenID identity provider; broader than R1 requires |

Release sources: [go-oauth2 v4.6.0](https://github.com/go-oauth2/oauth2/releases/tag/v4.6.0),
[ZITADEL v3.51.16](https://github.com/zitadel/oidc/releases/tag/v3.51.16).

#### Conformance Matrix (Static Source Review; Adapter Work Is Not Yet Verified)

| Requirement | go-oauth2 v4.6.0 | ZITADEL v3.51.16 |
|---|---|---|
| Authorization code + PKCE S256 | Native; enable `ForcePKCE`, allow only `CodeChallengeS256`, validate verifier format | Native; public-client token exchange requires PKCE; enforce S256-only before persisting authorization requests |
| Single-use code, including concurrent redemption | Manager loads then removes a code; transaction-bound store must serialize consumption and token issuance | Auth request lookup and deletion are separate callbacks; application storage must ensure single use and transactionality |
| Refresh rotation and reuse-family revocation | Rotation configurable; atomic consumption, retained spent-token evidence and family revocation belong to our adapter | `CreateAccessAndRefreshTokens` delegates issuance/rotation to application storage; reuse-family policy remains ours |
| Opaque tokens, hashed storage | Custom generator hooks and TokenStore; raw input token can be rehydrated only in request memory | Opaque encrypted access tokens or JWTs; provider crypto/signing-key interfaces remain; refresh generation/storage are application-owned |
| RFC 8414 metadata and explicit `none` | Serve a small OAuth metadata document ourselves; configure public-client form parsing/auth method validation | Has OIDC discovery and auth-method configuration; serve curated OAuth metadata for MCP |
| RFC 9728 PRM / challenge | Use the existing go-sdk `auth.ProtectedResourceMetadataHandler` and `ResourceMetadataURL` | Same resource-server glue; not a benefit unique to the OIDC provider |
| RFC 8707 resource binding | Store trusted resource/grant data through token extensions/context and validate authorize, code exchange, refresh and resource access | Custom request/audience mapping and MCP resource validation still required |
| RFC 9207 `iss`, success and error responses | Add configured issuer to validated authorization redirect results and metadata | No automatic RFC 9207 behavior found in the inspected provider; custom metadata/response handling required |
| CIMD | Resolve HTTPS client IDs in our bounded ClientStore/metadata layer | Discovery struct has `ClientIDMetadataDocumentSupported`; no complete provider resolver found; custom client lookup/fetch policy required |
| Optional DCR | Application endpoint and client persistence | Application endpoint and client persistence; not supplied by the inspected provider router |
| No unwanted OIDC requirements | No OIDC scopes or ID tokens needed | Supported scopes are configurable and `openid` is not required by scope validation, but the stock token response creates an ID token; code-flow refresh issuance depends on `offline_access` |
| SQLite/sqlc integration | Small ClientStore/TokenStore interfaces; request-scoped transaction wrapper is required | Larger AuthStorage/OPStorage contracts plus keys/claims/session callbacks; refresh issuance callback itself is a useful transaction boundary |
| External services / CGO requirement | Embedded Go library; custom SQLite store reuses oCMS's existing driver | Also an embedded Go library, not a requirement to deploy the ZITADEL service |

Important source corrections: ZITADEL can override its advertised scopes, so describing it as simply
"forcing openid" would be inaccurate. Its CIMD discovery flag is not evidence of an implemented CIMD
authorization flow. go-oauth2's ID-JAG/JWT-bearer resource fields are unrelated to R1's code flow and
remain disabled.

Source anchors:
- [go-oauth2 server defaults](https://github.com/go-oauth2/oauth2/blob/v4.6.0/server/config.go),
  [manager lifecycle and extension hooks](https://github.com/go-oauth2/oauth2/blob/v4.6.0/manage/manager.go),
  [storage interfaces](https://github.com/go-oauth2/oauth2/blob/v4.6.0/store.go),
  [default redirect validator](https://github.com/go-oauth2/oauth2/blob/v4.6.0/manage/util.go).
- [ZITADEL token/refresh policy](https://github.com/zitadel/oidc/blob/v3.51.16/pkg/op/token.go),
  [storage contract](https://github.com/zitadel/oidc/blob/v3.51.16/pkg/op/storage.go),
  [provider discovery](https://github.com/zitadel/oidc/blob/v3.51.16/pkg/op/discovery.go),
  [discovery fields](https://github.com/zitadel/oidc/blob/v3.51.16/pkg/oidc/discovery.go).
- [MCP authorization requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization),
  [OpenAI host authentication requirements](https://developers.openai.com/plugins/build/auth).

#### Dependency and Advisory Evidence

Measured using temporary copies of the current oCMS `go.mod`/`go.sum`, Go **1.27.3 darwin/arm64**,
`GOWORK=off`, `GOTOOLCHAIN=local`. Source downloads and temporary manifests did not alter project
dependencies. Package loading and advisory analysis did not execute library/application code or tests.
Scanner: `govulncheck` v1.8.0, `https://vuln.go.dev`, reported database update
**2026-10-27 21:16:18 UTC**; comparison run 2026-10-30.

| Measurement | go-oauth2 `manage` + `server` | ZITADEL `pkg/op` |
|---|---|---|
| Non-standard-library packages loaded for those imports | 8 | 49 |
| Modules represented in those imported packages | 3 | 19 |
| Selected imported-module details | oauth2 v4.6.0, JWT v5.3.1, existing UUID v1.6.0 | Includes go-jose v4.1.4, schema, CORS, securecookie and OTel v1.47.0; existing chi/x packages reused |
| Entire selected module graph, including unimported/test/example requirements | 250 (baseline 217; +33 paths, no existing version changes) | 227 (+10 paths; doublestar v4.10.0 → v4.10.3) |
| `govulncheck -scan=package` on the named imports | Exit 0: no known vulnerabilities found | Exit 0: no known vulnerabilities found |

The small imported package set does **not** mean go-oauth2 adds only two entries to the complete
module graph; its older example/test requirements expand that graph more than ZITADEL's. These are
package/module measurements, not binary-size or latency benchmarks. Advisory checks are date-specific
and do not establish OAuth correctness. Inspect the actual module diff and rerun the scan after approval.

Executed commands (each candidate used a temporary module copy):
```sh
go list -m -json github.com/go-oauth2/oauth2/v4@latest
go list -m -json github.com/zitadel/oidc/v3@latest
go list -mod=mod -deps -json github.com/go-oauth2/oauth2/v4/manage github.com/go-oauth2/oauth2/v4/server
go list -mod=mod -deps -json github.com/zitadel/oidc/v3/pkg/op
go list -mod=mod -m -json all
govulncheck -scan=package github.com/go-oauth2/oauth2/v4/manage github.com/go-oauth2/oauth2/v4/server
govulncheck -scan=package github.com/zitadel/oidc/v3/pkg/op
```

#### Mandatory Integration Gates for the Recommended Library

1. **Constrain protocol defaults.** Only response `code`, grants `authorization_code`/`refresh_token`,
   PKCE S256, and explicitly supported client authentication methods. Disable implicit, password,
   client-credentials and JWT-bearer grants. The default redirect check uses host-suffix matching;
   replace it with registered-URI exact matching plus the RFC 8252 loopback-port exception, and validate
   before consent or any error redirect. Never return a code or error to an unvalidated redirect URI.
2. **Use our storage, not example stores.** SQLite/sqlc adapters store hashes of random 32-byte codes,
   access and refresh tokens, retaining grant/resource/family metadata and spent-refresh evidence.
   Use generator hooks; avoid the default UUID-derived credentials. Keep state request-scoped, with
   no global mutable "current client/transaction" variables.
3. **Make code exchange atomic.** Resolve/cache client metadata before entering the transaction.
   Serialize code lookup/consumption and token creation in one SQLite write transaction; invalid PKCE
   rolls back library code deletion. Buffer protocol output and emit tokens only after successful
   commit. A double redemption cannot both succeed; commit failure must not return usable credentials.
4. **Wrap refresh validation explicitly.** The manager refresh method selects the client from stored
   token data; do not assume it validates the requesting client's identity/secret or scope subset.
   Validate the presented client against the stored grant, resource, current role/scopes and token
   expiry before delegation. Restrict requested scopes to the grant and preserve its audience.
5. **Keep refresh state atomic and durable.** Rotate with single-use consumption, absolute family
   expiry and retained used-token hashes. A reused token revokes its family durably even though the
   HTTP response is `invalid_grant`—a blanket rollback-on-error must not undo that revocation.
   Do not store old token plaintext just to satisfy manager getters: hydrate only the presented token
   in request memory and invalidate previous access rows by stored family/row identifiers. Disable the
   manager's raw-old-access removal when the transaction adapter handles that invalidation itself.
6. **Keep MCP integration separate.** Reuse go-sdk's resource metadata/challenge support; implement
   bounded CIMD, RFC 9207, resource binding, consent and optional DCR around the library. Preserve the
   existing API-key verifier and its policies; reject invalid OAuth tokens without falling through to
   weaker key authentication. Reuse `SessionUser` and `grantSession` for login/MFA; fresh-consent login
   needs server-recorded authentication time, not merely an existing `mfa_verified` boolean.

**oCMS evidence:** the existing transport is API-key-only in `modules/mcpserver/transport.go` and
`identity.go`; go-sdk v1.8.0 already provides the PRM handler/challenge option. `internal/store/database.go`
uses SQLite WAL, busy timeout and immediate write transactions; `store.WithTx` provides transaction-bound
queries. `internal/handler/mfa.go:grantSession` is the sole session-grant path and currently stores
session version/MFA completion, not a dedicated authentication timestamp. These are inspected
integration points, not evidence that the proposed OAuth adapter already works.

#### Verification Still Required Before Claiming Compatibility

- Normal code → tokens → protected tool call; normal rotation and reduced-scope refresh.
- Missing/plain/malformed PKCE, unregistered redirects, wrong client/resource and scope escalation.
- Concurrent code redemption and refresh, interrupted exchange/commit failure, and refresh replay
  whose family revocation survives the error response and server restart.
- SQLite rows and captured logs contain no raw credentials; revocation/current-role changes reject
  both newly connected and existing MCP sessions; existing bearer-key behavior remains covered.
- Real Claude Desktop/ChatGPT account checks from R1.1, then the scratch read/write/revoke flow.

These runtime/fixture/client checks are **not run** at this checkpoint. Approval would authorize adding
the recommended library and its required transitive dependencies, followed by implementation of these
gates. It would not approve a commit, push, deployment or publication. No application code, `go.mod`,
`go.sum`, database or connected-client settings were changed by this comparison.


### Iteration R1.1B — Approved OAuth Core and Local Compatibility Probe (2026-10-30)

**Status:** implementation and local verification complete; R1.1's real-account
acceptance remains open. The user explicitly approved adding
`github.com/go-oauth2/oauth2/v4` **v4.6.0** and implementing its integration gates.
The working tree remains uncommitted. There was no push, deployment, wiki
publication or change to a Claude/ChatGPT/Codex connection.

Implemented:

- `internal/oauth`: code + S256-only grant engine, per-request manager/store,
  configured HTTPS issuer (development loopback exception), RFC 8414/9728
  metadata, exact resource binding and RFC 9207 `iss`. Unsupported grants,
  OIDC scopes and client authentication methods are excluded.
- SQLite/sqlc migration and adapter: 32-byte random opaque credentials stored
  only as hashes, single-use 60-second codes, transaction-bound redemption,
  rotation, absolute family expiry and retained spent-refresh hashes. Failed
  PKCE and failed commits roll back consumption; credentials are emitted
  after commit. Correct-client refresh replay commits family revocation before
  returning `invalid_grant`, even with an invalid requested scope.
- Bounded CIMD: exact host allowlist, DNS-aware SSRF guard, no proxy/redirects,
  5 seconds, 64 KiB, matching client ID, 15-minute durable cache, concurrent
  fetch coalescing and a transaction-enforced 1,000-client cap. Optional
  public-client DCR defaults off; registrations expire after ninety days.
- Fresh full password/MFA sign-in after each authorization intent, escaped
  consent display, current role checks, a session-bound one-use nonce and
  ten-minute timeout. GET parameters are held in the session; POST cannot
  replace its callback or scopes. Origin checks and nonce validation both
  apply. `/account/security` lists and revokes the account's connections.
- MCP verified user/app identity, role/grant revalidation on every request,
  current API-key owner checks, OAuth metadata challenges, per-user throttling
  and tool-level scope challenges. Existing key verification/CIDR/expiry/
  revocation behavior remains covered. `get_site_info` reports the verified
  account, auth type, app and effective grant permissions.
- Flagged, isolated scratch tools with accurate read/write annotations:
  one entry per user, 8 KiB combined title/body, 100 entries, one-hour expiry,
  no CMS writes or admin preview URL. Production/demo reject the flag; the
  draft kill switch removes the probe. The admin page labels scratch mode.
- OAuth query redaction in access logs; bounded audit events identify only
  account, grant, client and scopes. Config, Docker forwarding, English/Russian
  UI, env examples, README, login/MCP docs and corresponding local wiki pages
  are updated. Setup and nginx path-only logging are in `docs/oauth.md`.

Dependency follow-up: the full application scan found two pre-existing,
reachable HTML conversion advisories in `golang.org/x/net` v0.61.0. It was
updated to **v0.62.0** for
[GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860) and
[GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866).
`golang.org/x/sync` v0.24.0 is now direct (singleflight); JWT v5.3.1 is the
library's transitive dependency. No other existing module version changed.

#### Executed evidence

- `scripts/test-gate.sh`: full suite, uncached, passed on the final source.
- `go vet ./...`, `staticcheck ./...`, `errcheck -ignoregenerated ./...`: passed.
- `go test -race ./internal/oauth ./modules/mcpserver`: passed; concurrent
  redemption/rotation and the SDK principal/scratch/revoke cases were also
  rerun with the race detector after the final source edits.
- `FuzzAuthorizationRedirectBinding`: 21,802 executions with two workers in
  the short fuzz run; no failure. Seed cases also run in the full suite.
- Negative controls: temporarily removing `mcp:drafts` from the permissions
  inventory failed `TestMetadataIssuerAndPermissionContract`; marking the
  scratch save read-only failed the SDK annotation test. Both edits were
  restored, and the final full suite passed.
- Code/token tests include wrong/missing/malformed PKCE, exact redirect,
  client/resource/scope mismatch, code replay/expiry, concurrent code and
  refresh, reduced scopes, canceled requests, injected insert failure and a
  deferred-FK commit failure. Normal code and refresh paths pass. Reuse
  revocation remains visible after closing and reopening the SQLite database and creating a fresh service.
- CIMD fixtures cover safe TLS fetch/cache/expiry, disallowed hosts, private
  IPs/DNS, forbidden URL forms, redirects, oversized/mismatched documents and
  timeout. DCR/body/client caps, grant ownership, current role/version/expiry,
  consent fresh sign-in/MFA/CSRF/denial/replay and token/audit storage redaction
  are exercised.
- `govulncheck ./...`: exit 0, **zero affected imported code** after the update.
  Verbose output retains one module-only advisory for the unimported,
  unmaintained `x/crypto/openpgp` package (GO-2026-5932, no fixed version).
- `golangci-lint run ./...`: **not globally clean**. Five findings remain in
  unchanged files: account-layout test `offBy1`, weather renderer quick-fix,
  and unused helper results/parameters in weather, MFA handler and MFA service
  tests. The changed R1.1 code has no reported lint findings. Baseline locations
  were checked against HEAD or the existing ignored custom module; these were
  not silently counted as a passing gate.
- `CI=true make assets`, `make check-no-absolute-paths`, root and wiki
  `git diff --check`: passed. Generated SQL/templ sources were regenerated;
  unrelated SQL generator formatting changes were removed.
- Owned local preview: `http://127.0.0.1:18080`, disposable database/uploads,
  no existing site DB touched. Homepage 200; unauthenticated admin 303 to login;
  signed-in admin 200 in the browser; liveness/readiness 200; both discovery
  documents 200. The preview remains running.
- Actual HTTP (protocol 2025-11-25): sign-in → consent → code exchange → MCP
  initialize/list → verified principal → scratch save/read → token revocation
  → next request 401 with `resource_metadata`. The SDK tests also negotiate
  2026-07-28. Captured server logs contained no probe code, access/refresh
  token, verifier, state or scratch content.
- Playwright: fresh login leads back to the consent page; consent displays the
  account/app/scopes and escapes client data; Deny grants nothing; the
  connected-app card renders and its Revoke action removes the connection.
  Local screenshots: `output/playwright/r11/consent.png` and `connections.png`.
  Validation logs and a manifest are under ignored `output/r11-validation/`.

#### Real-account matrix — current acceptance status

Updated 2026-11-01. The three GUI clients passed their full recorded flows
on `f66bd7b4d5fa`; R1.1H records that evidence and reconnect scope difference.
Native follow-up `c885da90` is deployed. Both CLI OAuth lifecycles and both
bearer-key rows passed on 2026-11-01. All three GUI clients also passed fresh
site-info/search/page calls on `c885da90`; their full lifecycle baseline remains
the recorded `f66bd7b4` run.
Claude Desktop 4.4212.0, Claude web and ChatGPT web use Pro; both web clients
use Chrome 156.0.8078.80 on macOS 27.2. Account tiers were read from their UI.
All three GUI clients use streamable HTTP and the admin account. Claude Code
2.1.316 reports Enterprise; Codex CLI 0.166.0 reports ChatGPT plan type `prolite`
through read-only `account/read`. The dedicated Editor account is now exercised
by Claude Code OAuth and both CLI bearer tests. Additional GUI/editor flows are
not claimed. External Internet IPv6 acceptance passed on 2026-11-01 from an
independent Hetzner AS24940 probe, including TLS and spoofed forwarding headers
(R1.1N). No production network changes were made for this verification.

| Client | Auth | Connect/consent/list/read/scratch/revoke/reconnect | Current blocker |
|---|---|---|---|
| Claude Desktop custom connector | OAuth | Passed: native discovery/read/search/scratch save/read, revoked-call denial and fresh read after shared reconnect | None for tested flow; final reconnect grants `mcp:access` only |
| claude.ai web | OAuth | Passed: consent/discovery/read/search, live scope upgrade, scratch save/read, revoked-call denial and fresh reconnect | None for tested flow; final reconnect grants `mcp:access` only |
| ChatGPT web | OAuth | Passed: CIMD connect/consent/discovery/read/search/scratch save/read, revoked-call denial, reconnect and scratch retrieval under new grant | None for tested flow |
| Claude Code | OAuth | Passed on `c885da90`: native password/generated MFA/consent, discovery/read/search/scratch save/read, MCP 401 + refresh 400 after revocation, reconnect and current user-owned scratch read under a new grant | None for tested flow; dedicated Editor account |
| Claude Code | Bearer key | Passed on `c885da90`: five tools, deactivation denial, restoration and fresh scratch retrieval | Test key deactivated again at finish |
| Codex | OAuth | Passed on `c885da90`: native connect/consent/discovery/read/search/scratch save/read, revoked-connection denial, reconnect and same scratch read under a new grant | None for tested flow; tier metadata subsequently recorded as `prolite` |
| Codex | Bearer key | Passed on `c885da90`: five tools, deactivation denial, restoration and fresh scratch retrieval | Test key deactivated again at finish |

The owner selected `https://ocms-mcp-staging.fly.dev` and authorized the
deployment. Use `docs/oauth.md` for canonical issuer, scratch flags,
MFA/proxy setup and nginx query redaction.
The client rows and external IPv6 gate are complete; R1.1 is closed against the
recorded commits/configuration. Do not use scratch results as proof of CMS
persistence. R1.2–R1.5 still own the production authoring path and
removal of this temporary probe.


### Iteration R1.1C — Staging Plan Aligned with Claude (2026-10-31)

**Scope:** planning update, not a deployment or a new OAuth implementation.
Read Claude's latest response in the live **oCMS / AI content generation
feature** desktop conversation. The original shared conversation is a frozen
snapshot and is not evidence of the latest staging decisions. This file stays
the canonical plan; incorporate reviewed proposals here without concurrent,
independent rewrites of its historical notes.

Both proposals choose **Fly.io**, with a new app separate from `ocms-demo`,
for `https://mcp-staging.ocms.tech`. Keep the compatibility branch on staging. A new VDS is unnecessary for this
client probe. Verify production nginx during the later authorized rollout to
the production host.

#### Aligned preparation and owner-run deployment sequence

1. **Isolate and review the source.** The current work is uncommitted on
   `master`. Prepare a dedicated R1.1 branch (default
   `codex/phase5-r1.1-oauth`, retaining the repository's Codex prefix; Claude
   suggested the equivalent `phase5/r1.1-oauth`). The owner invokes the commit
   workflow and approves the exact commit message before any commit. A push
   needs its own explicit approval. Deploy only the tested commit of that
   branch, including the staging config, from a clean checkout. Report the
   existing five lint findings separately; do not relabel them a clean gate.
   This proposal supplies no Git or deployment approval.
2. **Prepare `fly.staging.toml`.** Planned app `ocms-mcp-staging`, region `fra`,
   one shared CPU with 512 MB RAM initially, one fresh app-scoped 1 GB volume
   mounted at `/app/data`. Keep DB/uploads there, `kill_timeout = 60`,
   `force_https = true`, internal port 8080, and liveness/readiness checks.
   Keep `fly.toml`, the `ocms-demo` app and `.fly/scripts/deploy.sh` unchanged;
   that script is demo-specific. The new config/commands remain preparation
   work, not files already delivered by this planning update.
3. **Keep one persistent instance.** Use `--ha=false` for every staging deploy.
   The running policy during acceptance is `auto_stop_machines = "stop"`,
   `auto_start_machines = true`, `min_machines_running = 1`, in primary region
   `fra`. Verify exactly one Machine and its volume after deployment;
   `--ha=false` prevents automatic spare creation but does not remove existing
   Machines. Independent SQLite volumes and process-local scratch entries
   cannot serve one coherent acceptance session across multiple instances.
4. **Require the intended environment and MFA.** Set:

   ```dotenv
   OCMS_ENV=staging
   OCMS_SITE_URL=https://mcp-staging.ocms.tech
   OCMS_OAUTH_ENABLED=true
   OCMS_OAUTH_CIMD_ALLOWED_HOSTS=claude.ai,chatgpt.com
   OCMS_OAUTH_ALLOW_DCR=false
   OCMS_OAUTH_ACCESS_TTL=3600
   OCMS_OAUTH_REFRESH_TTL_DAYS=30
   OCMS_MCP_SPIKE_TOOLS=true
   OCMS_AI_DISABLED=false
   OCMS_DEMO_MODE=false
   OCMS_REQUIRE_MFA=true
   OCMS_DO_SEED=false
   ```

   Seed once with a controlled first-boot override, establish non-default
   credentials before public access, then keep seeding off. Enable the MCP
   module and use a dedicated editor account with MFA. First-boot bootstrap
   instructions must cover these steps rather than expose seeded passwords.
   Content is fresh seed data plus a reviewed content-only export, never a
   production user/session/OAuth database.
5. **Owner-held secrets.** The owner sets `OCMS_SESSION_SECRET` and
   `OCMS_MFA_KEY` through Fly secrets, using distinct random values of at least
   32 bytes. They stay out of config files, repository history and transcripts.
   The agent prepares commands without secret values; the owner supplies them.
6. **Verify actual proxy behavior.** Observe a controlled non-secret request's
   original peer and relevant IP headers on the deployed Machine. oCMS trusts
   `X-Forwarded-For` only from configured peers, then chooses the first
   untrusted hop from the right; it does not consume `Fly-Client-IP`.
   Configure `OCMS_TRUSTED_PROXIES` from the verified peer/proxy chain and check
   the real external client reaches IP rate limits and auth events. Include
   spoofed forwarding-header input and a repeat check after redeploy. One
   observed peer alone is not proof that the whole forwarded chain is correct.
   Do not guess broad trust CIDRs or silently switch to another header. If
   current middleware cannot resolve it safely, a scoped, tested fix is a
   staging acceptance prerequisite.
7. **Verify Fly logs.** The nginx example in `docs/oauth.md` applies to the
   later nginx deployment; Fly uses its own proxy/TLS path. Inspect
   `fly logs` after one complete sign-in, token exchange and MCP call. No raw
   code, state, verifier, access/refresh credential or scratch body may appear.
   Keep body/header debug logging off and record the check without persisting
   the probe's secrets.
8. **Attach the hostname; owner changes DNS.** Prepare the explicit-app
   certificate commands. Obtain the actual DNS target and verification
   records from Fly's certificate setup output; the ordinary app hostname is
   a proposal, not an authoritative CNAME value. The owner applies DNS for
   `mcp-staging.ocms.tech`. Verify certificate, canonical issuer and resource
   metadata at that hostname before testing clients.
9. **Owner deploys and acceptance is recorded.** Deliver exact bootstrap,
   secrets, app/volume, domain, deploy and verification commands with the
   staging config, leaving their remote execution to the owner. The deploy
   invocation must explicitly select `fly.staging.toml`, app
   `ocms-mcp-staging` and `--ha=false`. Run the full R1.1 matrix: Claude Desktop,
   claude.ai, ChatGPT web, Claude Code and Codex; test both OAuth and bearer
   modes for the CLI clients. Record client/version/tier/date, commit/config,
   connect/consent/list/read, scratch save/read, revocation failure and
   reconnect. Re-check GUI capabilities and actual CIMD URLs/auth methods.
   Keep unsupported `private_key_jwt` and other grant methods disabled;
   diagnose a token-step failure before changing client registration policy.
   **Do not start R1.2 until all rows are recorded and required passes or an
   explicit owner scope decision resolve pending acceptance.**

#### Clarifications retained alongside Claude's proposal

- Use the standard `codex/` prefix when creating the branch unless the owner
  explicitly chooses Claude's alternative name. Branch planning does not
  authorize a commit, push or deployment.
- Bootstrap must replace known seed credentials before public exposure.
- Fly's current custom-domain docs specify a per-app certificate DNS target;
  use the returned value rather than assume `ocms-mcp-staging.fly.dev`.
- Proxy validation must cover the complete forwarded chain and spoofed input,
  not merely a guessed address list. The actual Fly peer ranges and observed
  chain remain pending until staging exists.
- Add the expired/revoked OAuth-row cleanup job in R1.7, retaining spent-refresh
  evidence for active families. This is release work, not a prerequisite to
  collecting the bounded R1.1 compatibility results.

**Verification for this plan update:** manually traced the proposed steps
through current `fly.toml`, the demo-only deploy script, the shared-binary
workflow docs, configuration env tags and `GetClientIP`. Checked the installed
Fly CLI help and current official docs for `--config`, `--app`, `--ha`,
minimum-running behavior, kill timeout, headers and certificate DNS setup.
The executable staging config, bootstrap/proxy/log checks, provisioning,
secrets, DNS and all real-account matrix rows remain unexecuted. No runtime
source was changed and earlier R1.1 runtime passes remain historical evidence;
they do not validate this remote deployment sequence.

Sources checked 2026-10-31:
[Fly configuration](https://docs.fly.io/reference/configuration),
[Fly deploy options](https://docs.fly.io/flyctl/deploy),
[Fly request headers](https://docs.fly.io/networking/request-headers),
[Fly custom domains](https://docs.fly.io/networking/custom-domain),
and [Fly volumes](https://docs.fly.io/volumes/overview).

### Iteration R1.1D — Fly Deployment Files Prepared (2026-10-31)

**Scope:** the owner requested staging configuration and exact deploy commands,
with no commit, push or deployment. Prepared these as local working-tree files:

- `fly.staging.toml`: `ocms-mcp-staging`, `fra`, one shared CPU/512 MB, one 1 GB
  volume, HTTPS, one running Machine during acceptance, routing readiness check
  plus named health checks, MFA required, OAuth/scratch on and seeding/demo/DCR
  off. Existing API-key expiry/source-CIDR and outbound HTTPS controls stay on.
- `fly.staging.bootstrap.toml`: same app/image/volume, **no public service**,
  seeding on, MFA required, OAuth/scratch off. Development-mode cookies work
  through the owner's loopback-bound private Fly tunnel. Replace seeded admin
  credentials, enroll MFA and prepare the editor before public promotion.
- `docs/mcp-staging-fly.md`: exact owner-run commands for a reviewed commit
  archive, app/volume creation, hidden secret entry, private bootstrap, domain
  setup, promotion of the same image, proxy/log verification and client checks.
  Every deploy explicitly selects its app/config, `--ha=false` and rolling
  strategy. Subsequent updates use only the public profile. Documentation is
  mirrored locally in `wiki/MCP-Staging-Fly.md` and `wiki/MCP-OAuth.md`.
- Configuration tests reuse the demo env reader and check both profiles, MFA
  key validation, persistent state, private bootstrap and required deploy flags.
  The existing demo config and deployment script were not changed.

**Direct local verification:** both profiles pass Fly's strict validator; all
14 Bash blocks pass syntax and ShellCheck. Disposable command fixtures reject
master/detached/dirty inputs before any provisioning step, and the clean-branch
fixture archives only its source and validates both profiles. This fixture uses
stubbed Git/test commands; it is not an actual commit or remote deployment.
Negative controls prove the tests reject public bootstrap routing, disabled
MFA, an omitted `--ha=false`, and an omitted routing readiness check; restored
files pass. A native binary with disposable SQLite exercised private initial
login, MFA enrollment, credential replacement, old-login rejection, MCP module
activation and restart with the public profile. The account, MFA enrollment and
module state persisted; secure-cookie behavior, health, issuer/resource metadata
and unauthenticated MCP challenge were checked. No full public OAuth/client
acceptance is claimed by this loopback test.

**Baseline checks:** the uncached full Go test gate, vet, staticcheck, errcheck,
assets build and Linux amd64 CGO-disabled build pass. Govulncheck reports zero
affected vulnerabilities, with one advisory confined to an unused required
module. Golangci-lint still reports the same five pre-existing findings in
unchanged files; this is not a clean lint gate. Whitespace and absolute-path
checks pass. Local evidence is under `output/r11-validation/fly-staging/`.

**Remaining remote evidence:** Docker is installed but its daemon is stopped,
so no container image was built locally. Fly app-name/region availability,
remote build/runtime, actual ingress peers/forwarded chain, DNS/certificate
issuance, public sign-in/logs and every real-client matrix row remain pending.
Observe and verify proxy addresses before setting `OCMS_TRUSTED_PROXIES`; do
not guess them. The runbook's source gate intentionally rejects the current
dirty `master`; branch/commit work needs the later owner-directed workflow.
No app, volume, secret or DNS record was created or changed; no Git history,
remote or deployment mutation occurred. R1.2 remains gated on real acceptance.

### Iteration R1.1E — Fly Staging Running (2026-10-31)

The owner authorized deployment and selected the existing Fly hostname,
`https://ocms-mcp-staging.fly.dev`, instead of changing `ocms.tech` DNS.
Both staging profiles and the runbook now use that canonical origin. The
earlier custom-domain plan in R1.1C/R1.1D is historical; its certificate request
is deferred and its DNS was not changed by this work.

The `ocms-mcp-staging` app runs in `ocms-tech`, region `fra`, on one shared
CPU/512 MB Machine with one encrypted 1 GB volume. Its immutable image was
built from commit `342f63e0c5666c6c811cf3cf5a256440c394cab6`. Public promotion
and later configuration updates reused that image and volume. The hostname
update changed only `OCMS_SITE_URL` in the two profiles; no runtime Go source
was changed or rebuilt. New hostname/configuration documentation remains
uncommitted pending an explicit commit instruction.

Private bootstrap was completed with admin MFA and rotated credentials;
the old default login was verified to fail. The owner retained recovery codes
and approved keeping the fresh session/MFA sealing keys in macOS login
Keychain, with values sent directly to Fly. Neither key was regenerated during
promotion. The owner skipped editor creation: this probe uses the admin role,
and manual editor-role coverage remains unverified.

MCP is enabled. Two disposable context fixtures were saved: published page
`r11-published-context-probe` and draft `r11-draft-context-probe`. The published
body was verified over HTTP; the draft returns 404 to unauthenticated callers.
The published page also rendered successfully in a browser over Fly HTTPS.

Verified on the deployed Fly hostname:

- TLS verification, liveness, readiness and the public routing check pass.
  The app still has one Machine and the original volume/image.
- OAuth discovery now advertises the Fly origin for issuer, authorization,
  token and revocation URLs. Protected-resource metadata and the unauthenticated
  MCP 401 metadata challenge use the same origin. DCR remains disabled.
- Required MFA, staging mode, demo/seeding off and guarded scratch configuration
  remain intact. Tests and strict validation passed for the hostname change;
  assets were rebuilt and all 14 runbook Bash blocks passed syntax/ShellCheck.
- One exact, nonce-marked health request was observed to identify the real
  Fly socket peer and forwarded chain. Its packet filter was compiled and
  verified before capture; no cookie or authorization header was captured and
  no raw packet was retained. Only the verified peer and app-assigned addresses
  were configured in `OCMS_TRUSTED_PROXIES`.
- Normal IPv4 requests, spoofed `X-Forwarded-For`/`X-Real-IP` input and a repeat
  after deployment all resolved to the observed client address. Temporary
  packet-capture tooling and its filter were removed.

Evidence is under `output/r11-validation/fly-staging/deployment/`.
Successful public sign-in/auth-event IP verification, a full OAuth exchange
and MCP-call log check, IPv6 client-path coverage and the real-client matrix
remain pending. Do not label this deployment a completed R1.1 acceptance or
start R1.2 without the recorded client results and required scope decisions.


### R1.1F — Claude connection blockers (2026-10-31)

The actual Claude Desktop 4.4212.0 / Pro connection opened authorization and
received `invalid_client`; discovery alone was not an acceptance pass.
Claude's public CIMD advertises JWT bearer in addition to code and refresh.
The original validator rejected the document. A captured-metadata regression
reproduced the failure, then passed after separating CIMD capability metadata
from strict DCR registration. JWT token requests remain unsupported.

The Fly Machine also received HTTP 403 from Claude metadata retrieval using
the same Go HTTP transport settings and SSRF dialer as oCMS. A local fix to
metadata validation alone cannot clear that deployed blocker. The temporary
probe binary was removed after recording its status.

An opt-in, bounded owner-reviewed public registration is now prepared via
`OCMS_OAUTH_PRE_REGISTERED_CLIENTS`, following CIMD pre-registration. Both
staging profiles pin the verified Claude client ID and callback. Exact-host,
public-client, S256, fresh MFA/login, consent and scope/revocation safeguards
remain; DCR remains off. Configuration defaults empty elsewhere. Pins are
validated at startup and use the existing fifteen-minute SQLite client cache.

This compatibility follow-up was committed as `0cb668b4` and deployed. The
subsequent CSP callback correction and staging capacity changes were committed
as `2edc6e90`; their equivalent source snapshot was already deployed. Current
acceptance results and the next local compatibility corrections follow in R1.1G.


### R1.1G — Real Claude lifecycle and ChatGPT acceptance (2026-10-31)

Claude web / Pro, Chrome 156.0.8078.80, macOS 27.2, streamable HTTP:

- Fresh password/MFA/consent, tool discovery, `get_site_info`, `get_page(id=1)`
  and `search_pages(query="R11")` passed against the deployed source equivalent
  to commit `2edc6e90`. The published fixture marker was returned.
- The `mcp:access` grant correctly denied a scratch save. A second attempt
  allowing normal scope reauthorization still produced an ordinary tool error
  in Claude; no scope-upgrade UI appeared. The result-metadata hint alone did
  not establish Claude compatibility.
- Revoking the newest grant returned 303; subsequent MCP requests returned
  401 and the refresh attempt returned 400. Claude displayed its reconnect UI.
  The owner completed a new password/MFA/consent flow; token exchange and the
  resumed `get_site_info` returned 200 under a different grant. The existing
  connection is working. No scratch or CMS data was written.
- Public MFA audit-event IP matches the verified external client reference.
  Fresh IPv4 normal/spoofed health probes also resolve to that reference.
  IPv6 AAAA is present, but this Mac's IPv6 route/connection probe could not
  reach staging. IPv6 acceptance remains unverified.

ChatGPT web exposes Add custom MCP server and advanced OAuth configuration.
The staging connector uses CIMD, the stable callback, and only `mcp:access`
and `mcp:drafts`; `pages:read` is deselected. Authorization reached oCMS but
returned `invalid_client` before login. Its public client metadata advertises
`none` and `private_key_jwt` while retaining a singular `private_key_jwt`
preference. A temporary diagnostic using oCMS's Go HTTP settings and SSRF
safe dialer fetched that metadata from the existing Fly Machine with HTTP 200.
The diagnostic was removed; the application was not restarted or deployed.

Two local corrections are prepared, not committed or deployed:

1. CIMD selects the supported public-client method `none` from the advertised
   capability array. Legacy documents retain the singular-method check. DCR
   and actual token authentication remain public-client-only; assertions fail.
2. OAuth tool requests missing a catalog scope receive HTTP 403 with an
   `insufficient_scope` challenge containing endpoint access and the required
   tool scope. Consent must still authorize a new grant. Read-only grants,
   API-key behavior, catalog gating, body limits and batch rejection remain.
   The tool wrapper retains its scope enforcement and metadata challenge.

Both regressions failed before the corrections. Focused OAuth/MCP tests,
full uncached Go tests, focused race tests and scoped lint passed afterwards.
The local source identity and direct behavior evidence are recorded in
`output/r11-validation/fly-staging/deployment/client-compatibility-manifest.json`
and `client-compatibility-behavior.md` in the same directory. Live validation
of these corrections is pending a separately approved commit/deployment.
No Git push, application deployment, seeding, secret change or CMS write was
performed during this acceptance pass. Wiki edits remain uncommitted.

Remaining: live Claude scope upgrade and scratch save/read; full ChatGPT
OAuth/read/scratch/revoke/reconnect; native Desktop calls; both CLI clients'
OAuth/bearer rows; IPv6 coverage. Editor coverage stays deferred. R1.2 remains
gated on recorded required passes or an explicit owner scope decision.

Primary references: [MCP scope challenges](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#scope-challenge-handling)
and [ChatGPT CIMD authentication](https://developers.openai.com/plugins/build/auth).


### R1.1H — Deployed GUI acceptance (2026-10-31)

The owner approved the exact compatibility commit and deployment, with no
push. Commit `f66bd7b4d5fa131cdc57b5aa157c0f18a58c9d53` was built from a clean
Git archive and deployed to `ocms-mcp-staging`. The running binary reports
that commit. The existing Machine, encrypted volume, two shared CPUs/1024 MB,
MFA requirement, disabled DCR and canonical Fly origin were preserved.
No seed, secret rotation, production deployment or Git push occurred.

- **Claude web:** the real HTTP 403 scope challenge opened the additional
  access UI. Fresh MFA/consent granted exactly `mcp:access` and `mcp:drafts`;
  scratch save/read matched. Final grant revocation produced MCP 401 and
  refresh 400. Fresh MFA/consent and token exchange succeeded, followed by
  fresh `get_site_info`, `search_pages("R11")` and `get_page(1)` calls. The
  published marker was returned under the new grant.
- **Native Claude Desktop 4.4212.0 / Pro:** a dedicated native session called
  site info, search, page read, scratch save and scratch retrieval in order.
  Native UI and server logs confirmed the exact saved/read entry. Revoking
  its shared Claude grant rejected the next native call. After the web
  reconnect, a new native site-info call succeeded under the same new grant.
- **ChatGPT web:** actual CIMD authorization, MFA/consent, tool discovery and
  all five context/scratch calls passed. Revoking its grant rejected MCP and
  refresh; reconnect issued a different grant and both site-info and the
  previously saved scratch entry were read successfully. The test used the
  Instant model; the profile menu independently identifies the account as Pro.
- Scratch grants had only `mcp:access` and `mcp:drafts`, with no `pages:read`
  and `drafts_visible: false`. Writes were explicitly approved in test
  prompts; no separate client write-approval button was observed. Scratch
  entries are disposable, isolated and one per user, so later client saves
  replaced the earlier client's entry. These are not CMS draft writes.
- Claude's final reconnect requested and received only `mcp:access`.
  Its working connection therefore reads published content; another scratch
  operation would require the already-tested scope-upgrade flow. ChatGPT's
  working connection retains `mcp:access` and `mcp:drafts`.
- Final Claude MFA was delayed by duplicated keyboard input. Inspection
  found three doubled digit pairs; a single paste of the owner's fresh code
  produced exactly six digits and passed immediately. Dummy keyboard/paste
  tests did not reproduce duplication, and the auth page has no input script.
  The duplication's source remains unproven. Server clock/replay state was
  normal. No MFA safeguards were weakened, no counters or credentials were
  reset, and no recovery code was used. The owner then tested a fresh ordinary
  staging login, confirmed manual MFA entry worked, and reached the signed-in
  dashboard. The duplication did not recur in that follow-up check.
- Sanitized Fly logs show successful token/tool requests and revocation
  failures without credential patterns or OAuth query strings. Normal and
  spoofed IPv4 forwarding probes after this deployment resolve to the
  verified external client reference. IPv6 remains unverified because the
  current Mac has no working route to the staging IPv6 address.

Baseline checks: focused OAuth/MCP tests, full uncached Go tests, focused
race tests and scoped lint passed. The clean-archive full suite initially
failed on absent ignored frontend dependencies; building the lockfile-pinned
assets resolved that prerequisite and the uncached suite then passed. Both
Fly profiles passed strict validation. Global lint's five unchanged findings
remain separately recorded; scoped lint is not a global clean result.

Evidence: `output/r11-validation/fly-staging/deployment/` contains the
deployment manifest, per-client screenshots/sanitized logs, proxy comparison
and `CLIENT-ACCEPTANCE-RESULT.md`. These ignored records are not committed.
The status documentation records the completed deployment and GUI checks.

Remaining R1.1: real Claude Code and Codex OAuth/bearer rows and IPv6 path
coverage. Editor coverage stays explicitly deferred. R1.2 remains gated on
required passes or an explicit owner scope decision; the three GUI passes
do not complete the whole matrix.


### R1.1I — Native CLI registration blockers (2026-10-31)

The GUI acceptance documentation was committed separately as `3bddee09` with
owner approval. No push or deployment followed. CLI checks still target the
running `f66bd7b4` image; the following correction is an uncommitted local
follow-up, not a new acceptance pass.

- Codex CLI **0.166.0**, signed in through ChatGPT, selected its stable
  `https://chatgpt.com/oauth/codex/client.json` CIMD and requested only
  `mcp:access mcp:drafts`. Its actual callback was IP-literal loopback.
  Staging returned `invalid_client`. The same transport as oCMS fetched that
  document from Fly with HTTP 200; its declared callbacks include both
  `127.0.0.1` and `localhost`. The original validator rejected the whole
  document. A captured-metadata code-flow regression reproduced the failure.
- Claude Code **2.1.316** reports a first-party Enterprise subscription in
  CLI auth status; this differs from the GUI's Pro tier. A registration-only
  probe identified `https://claude.ai/oauth/claude-code-client-metadata` and
  `http://localhost:<port>/callback`, with only the two intended scopes.
  No MFA or consent was completed. Its published metadata URL returns HTTP
  403 from both this Mac and Fly; the Fly probe used oCMS's bounded SSRF-safe
  Go transport and was removed without restarting or deploying the app.
- The local correction accepts declared literal `localhost` native CIMD
  callbacks with the existing loopback-port exception. Exact hostname, path
  and query checks remain; token exchange still binds the code to the exact
  callback and port. Subdomains, credentials, fragments and unregistered
  callbacks are rejected. DCR remains off and its registration validation
  retains the previous IP-literal-only HTTP policy.
- Both local staging profiles add an explicit Claude Code registration,
  derived from the installed client's observed identity/callback and the
  official callback documentation. It is separate from Claude web's existing
  registration. Registration alone grants no account access: fresh login,
  MFA, consent, S256, scopes, audience and revocation remain required.

The Mac locked during this work; browser-based acceptance is paused until
the owner unlocks it. Neither CLI's full OAuth or bearer row is a pass yet.
The local correction and its evidence must be reviewed before another
commit/deployment. Working GUI connections were not revoked or reconfigured.

Local verification: the captured Codex metadata and native pinned-client
regressions failed before their corrections. The final focused tests, full
uncached Go suite, OAuth race suite and scoped OAuth/MCP lint passed. Both
Fly profiles passed strict validation; their two registration arrays match
and DCR stays disabled. Callback-binding tests reject changed host, path,
query, credentials and fragments, and reject a different port during code
exchange. The existing HTTP `localhost` issuer rejection and DCR tests pass.
Evidence is under `output/r11-validation/fly-staging/deployment/` in the
`cli-native-*` reports; live CLI acceptance remains unverified.

Primary references: [Codex native CIMD and callbacks](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)
and [Claude Code callbacks and scopes](https://code.claude.com/docs/en/mcp).


### R1.1J — Native fix deployed; account checks pending (2026-10-31)

The owner approved the separate native fix commit and staging deployment.
Commit `c885da9071ebf911c8af71c285b67efee68f703e`, tree
`dd83b2df90172d7ace6fd8b56502f8f8d793c994`, was committed with the approved
message and deployed from a clean Git archive. Its uncached full Go suite
passed after lockfile-pinned frontend dependency preparation. The running
binary reports that exact commit. No Git push occurred.

The existing Machine, two shared CPUs/1024 MB, original encrypted volume and
other environment settings were preserved. The only environment change is
the added native Claude Code registration. DCR remains disabled, MFA remains
required, and no session/MFA sealing key was changed.

Public HTTPS verification on this image passed: liveness/readiness, canonical
issuer and the resource metadata URL advertised by the unauthenticated MCP
401 challenge. Authorization requests using the actual native client IDs,
callback forms, S256 and only `mcp:access mcp:drafts` both return 303 to fresh
login. These are HTTP registration preflights, not completed CLI OAuth flows;
no credentials or account grants were issued by those probes. Normal and
spoofed IPv4 health requests still resolve to the verified client reference.

Chrome continues to report the owner's Mac locked. The real CLI account
flows and both bearer-key rows therefore remain unverified. Browser work
requires the owner to unlock it and complete normal MFA/consent. A bounded
CLI runner is prepared with only the staging MCP server, fixed test scopes
and no command tools or other connectors. It has not yet run an account test.

The prior full GUI acceptance evidence remains tied to `f66bd7b4`; fresh GUI
calls on this new image have not been observed. No GUI grant was revoked or
connector reconfigured during this deployment. IPv6 and the explicitly
deferred editor role retain their existing dispositions. R1.1 is not complete.

New deployment and verification records are under
`output/r11-validation/fly-staging/deployment/cli-native-*`; the exact image
and source context are in `cli-native-deployment.json`. This post-deployment
status update remains uncommitted while acceptance continues.


### R1.1K — Codex CLI OAuth passed (2026-11-01)

Staging readiness and the running `c885da90` binary were rechecked after the
owner unlocked the Mac. Installed clients remain Codex CLI 0.166.0 and Claude
Code 2.1.316 on macOS 27.2. No new deployment or Git push was performed.

Codex used its direct native CIMD identity,
`https://chatgpt.com/oauth/codex/client.json`, with a loopback callback and
only `mcp:access mcp:drafts`. Actual password/MFA/consent and code exchange
succeeded. The client then called site info, search, page read, scratch save
and scratch read in order. Structured results and server audit logs agree:
OAuth auth, user 1, exactly those two permissions, `drafts_visible: false`,
the published fixture marker, and identical saved/retrieved scratch content.
The model used the configured `gpt-6-sol`; account authentication is ChatGPT,
but the CLI's subscription tier was not independently established.

Only the new Codex grant was revoked in Account Security. Subsequent native
MCP initialization received 401 and refresh received 400. The model reported
its tool unavailable; that alone was not treated as proof. Independent Fly
logs establish the actual authentication rejection. A fresh password/MFA/
consent flow issued a different grant. Fresh site-info and retrieval of the
same scratch entry both passed under that new grant, retaining only the two
intended scopes. The Codex connection is left working.

Claude Code's separate native identity reached password/MFA successfully.
Its native wait ended after about five minutes without any MFA submission
or consent reaching staging. The Mac had locked again; this is an incomplete
owner-interactive flow, not a completed compatibility test. No fresh attempt
will be opened until the owner is unlocked and ready to complete it.

An independent, credential-free probe from the existing Fly Machine forced
TCP IPv6 to the app's assigned public IPv6 address, with the canonical HTTPS
hostname and certificate verification. Normal and spoofed-header requests
returned 200; access logs resolved both to the exact IPv6 client address
observed by that probe. This source was a private Fly IPv6 address, so the
result covers the Fly-hosted IPv6 path and parser/spoofing behavior, not an
external Internet IPv6 client or the Mac's missing route. No new Machine or
other resource was created; the temporary executable was removed, and the
app was not restarted.

Evidence: `codex-oauth-lifecycle-c885-result.json`, the corresponding sanitized
CLI event/server logs and screenshots, `claude-code-oauth-initial-login-c885.json`
and `ipv6-public-probe-result.json`, under the existing ignored deployment
evidence directory. Both bearer-key rows and fresh GUI reads on `c885da90`
remain pending. No test API key has been created. R1.1 remains incomplete;
the post-acceptance status documentation is still uncommitted.

### R1.1L — Shared development authenticator (2026-11-01)

The owner requested agent-generated development TOTP codes shared by Codex and
Claude Code, and paused interactive acceptance until later. Do not open another
OAuth attempt or ask the owner for another manual code while paused.

Added the local Go helper `cmd/ocms-dev-auth` and shared agent instructions in
`docs/dev-authenticator.md`, `AGENTS.md` and `CLAUDE.md`. It stores a dedicated
account's seed and target binding in the macOS login Keychain, passes enrollment
only through private stdin, delivers codes through private temporary files and
coordinates separate agents with a file lock and time-step reservation. It
supports the canonical staging origin and IP-literal loopback development only.
No server MFA or consent safeguard is changed; no deployment is required.

RFC vectors and a disposable database enrollment/verification passed, including
server replay rejection. The actual macOS login Keychain import/generation flow
also passed with a uniquely named public test fixture, removed afterward.
**The dedicated staging account is not created or enrolled yet.** Normal initial
enrollment remains pending for the resumed session; the helper cannot generate
codes for an unknown existing personal seed. Claude Code OAuth, both bearer rows,
fresh current-image GUI reads and external IPv6 coverage remain pending. No new
commit, push or deployment is authorized by this preparation work.

### R1.1M — Shared MFA active; CLI acceptance passed (2026-11-01)

The owner resumed setup/testing and explicitly prohibited commit, push and
deployment. Created `ocms-agent@example.test` (Editor, user ID 2) with a one-off
staging maintenance command using existing store/auth/MFA services. Its password,
new seed and ten recovery codes are retained privately in the macOS login
Keychain. Normal browser login and later OAuth logins used helper-generated
codes successfully; no manual code handoff was requested. Both coding agents
share the documented helper and credential binding. The owner's account and
existing GUI grants were preserved.

After explicit consent/key approval, Claude Code 2.1.316 completed native OAuth
with exactly `mcp:access mcp:drafts`. Its five actual structured tool results
passed; revocation produced MCP 401 and refresh 400, then fresh password/MFA/
consent restored access under a different grant. The abandoned attempt that
outlasted the approval wait was also revoked. Codex and Claude Code each passed
five bearer tool calls with separate 90-minute, current-Mac-IPv4 `/32` keys,
exactly the same two scopes and `drafts_visible=false`. Both keys failed when
deactivated, passed fresh site-info/current-scratch reads after restoration,
and were deactivated again; final independent HTTP probes returned 401 for both.

Scratch storage intentionally retains only one entry per user. The bearer saves
replaced the earlier OAuth entry, so the first reconnect read of that older ID
returned `not_found`. This was test interference, not a server restart or token
failure. The latest saved entry was verified byte-for-byte under the new OAuth
grant and both restored keys. Serialize future scratch lifecycles per account.

Claude web, native Desktop and ChatGPT web each passed three fresh published
context calls on `c885da90`; full GUI lifecycle evidence remains tied to `f66bd7b4`.
Codex CLI's read-only account metadata reports `chatgpt` / `prolite`, without
persisting identity or tokens. Fresh sanitized server logs showed no credential
patterns. Temporary remote maintenance binaries were removed. The running image
was unchanged; no app restart, commit, push or deployment was performed.

External IPv6 remains unverified: this Mac failed both normal IPv6 resolution
and a strict-TLS connection forced to the assigned public IPv6 address. The
earlier successful Fly-hosted probe used a private Fly IPv6 source and is not
an external Internet substitute. An IPv6-capable external runner is still
needed, or the owner must explicitly defer that check before R1.1 closes.
Evidence: `staging-agent-enrollment.json`, per-client `*-result.json`, sanitized
server logs/screenshots and `R11-ACCEPTANCE-2026-11-01.md` in the ignored
`output/r11-validation/fly-staging/deployment/` directory.

### R1.1N — External IPv6 verified; R1.1 closed (2026-11-01)

The owner requested completion of the external IPv6 gate without a commit, push
or deployment. The production host was inspected without changes.

Used an existing independent Globalping probe in Falkenstein, Germany, on
Hetzner AS24940. An IPv6 HTTPS request to the public IP reflector established
the probe's global source address; only its SHA256 was retained locally. Reused
that exact probe for four requests to staging's canonical HTTPS hostname:
normal, forged IPv4 XFF/X-Real-IP, forged IPv6 headers, and a forged mixed XFF
chain plus X-Real-IP/Fly-Client-IP. All resolved the assigned public IPv6,
returned HTTP 200 with the expected readiness body, and reported authorized
TLS 1.3 with a matching `*.fly.dev` certificate.

Each nonce matched exactly one Fly app access-log entry. All four resolved
client addresses were global IPv6 and matched the independent source-reference
hash; every forged address was ignored. This is an external Internet result,
separate from the earlier private-source Fly probe. No application credentials,
raw client addresses or raw app logs were saved in the local evidence.

R1.1's client and environment gates are now complete. The runtime stayed on
`c885da9071eb`; R1.2 was not started. No new infrastructure, app restart, push
or deployment was needed for verification. Evidence is in `ipv6-external-verification.json`,
`ipv6-external-measurements.json` and `R11-ACCEPTANCE-2026-11-01.md` under the
existing ignored deployment-evidence directory. The shared helper and acceptance
records are separate from the deployed runtime. The existing wiki work is
excluded from this closeout.


### Iteration R1.2 — Atomic write foundations (2026-11-01)

Implemented locally on `codex/phase5-r1.2-foundations`, based on R1.1 closeout
`f92e57161790d063ae127fd2e4d70dcfa698f8da`. No new dependency, commit, push or
deployment. The existing staging runtime remains `c885da9071eb`.

- Added core revision-1 backfill, honest legacy history, AI-access flags,
  complete snapshots, per-revision verified/validated/reported provenance and
  retained idempotency records. New page operations and related state share
  one transaction; no-op saves and replays add no versions or webhooks.
- Added neutral `pagewrite` validation, CAS, translation, restore and deletion
  helpers; query adapters join legacy transactions through savepoints and reject
  autocommit handles. Converted admin/v2, scheduler, media and language changes,
  oCMS imports, PHP-Nuke/Elefant/Drupal imports, generator and rollback writers.
  The complete writer inventory and its behavioral evidence are in
  `docs/page-writes.md`.
- Admin forms carry displayed revisions, preserve unsaved conflict inputs and
  offer a new CAS on confirmed overwrite. Owner/admin AI-edit toggle and legacy
  version labels are localized in English/Russian. REST preserves ordinary
  unconditional updates but requires `base_revision` on AI-editable pages.
- `CreateWith`/`UpdateWith` support trusted `ForceDraft` and `TranslationOf`.
  Draft writes require current app identity, ownership, scopes, opt-in, lifecycle,
  request ID and transaction-bound policy callback. Explicit lifecycle nulls
  cannot bypass validation. API-key creator mapping has one actor helper.
- Wired optional module webhooks, shared featured-image checks and suspicious
  content events. Actual HTTP verification found and fixed Huma's error-envelope
  transformation so conflict codes/details reach REST clients.
- Preserved the fixed demo seed as a storage-layer exception (a pagewrite import
  would create a package cycle). It uses a single transaction and records complete
  initial versions/provenance; it never overwrites existing pages. Source imports
  retain separate alias stages, with each alias operation now revisioned.
- Five negative regression probes plus actor/dependency probes demonstrated
  failure when safeguards were removed and success after restoration. Full suite,
  race, lint/tool, HTTP and browser evidence is retained under ignored
  `output/r12-validation/`; the final source manifest identifies the tested tree.
  GolangCI retains five pre-existing findings in unchanged layout/MFA tests and
  local weather code. Govulncheck has no reachable finding; one module advisory
  is uncalled. These limits do not imply that all repository lint is green.
- Updated source documentation and local wiki pages, preserving prior wiki work.
  Wiki publication is not authorized. No new MCP authoring tools are exposed.

Before deployment: back up the real site's database and verify the migration on
its disposable copy. R1.2 remains local. Next iteration is R1.3 (draft contract,
validation issues, safe HTML and link policy); stop here for human review.

Final R1.2 verification: 59 packages, 7489 test/subtest results,
23 live HTTP checks and the confirmed-overwrite browser flow passed. Focused
race checks passed. Runtime/test source fingerprint: `98a70310f78e07fe41dba453c5ff0b2f108c506880d7ff115f5aac6304341d3d`.
The full evidence record and remaining staged-authoring scope are in
`docs/page-writes.md`. No commit, push or deployment was performed.

### Iteration R1.3 — Draft contract and safe content (2026-11-01)

Implemented locally on `codex/phase5-r1.3-draft-contract`, based on committed
R1.2 `fc25f0d29fbc56be9fd8e5dd09d6b7f407e9fde9`. The work remains uncommitted;
no dependency, migration, push or deployment was added. Staging remains on
`c885da9071eb`.

- Added `internal/ai/draft` with distinct closed create/update schemas derived
  from Huma tags, also exercised through the actual MCP schema builder. Creates
  require title, summary and Markdown; changes must be non-empty. Unknown,
  forbidden, duplicate, excessive-depth and invalid UTF-8 inputs fail closed.
  Presence wrappers preserve omission, null and explicit empty values through
  typed idempotency hashing, including nested agent-reported fields.
- Added bounded normalization, slug derivation, language/taxonomy/media reads,
  shared featured-image minimum checks and SEO warnings/description trimming.
  The normalizer never mutates input, writes pages or fetches URLs. Operational
  resolver errors remain errors. A blocking issue returns no saveable content.
  The 33-code catalog has English/Russian messages and hints, matching locales
  and documentation; reserved R1.4/R1.5 codes are explicitly distinguished.
- Added Goldmark rendering with raw HTML disabled and rejected, existing
  `media:ID` references, escaped alt/captions, available responsive variants and
  unconditional sanitization. Both Markdown and final HTML have byte caps.
  Metadata-only changes preserve authoritative stored HTML byte for byte.
  Explicit derived Markdown warns about rich-content loss and verifies media
  identity against its actual stored local URL.
- Added keep/nofollow/strip external-link policy and review results without
  remote verification. Internal links use an injected visibility callback;
  `StoreResolver` fails closed if it is absent. Shared frontend/redirect resolver
  extraction belongs to R1.4, so no second routing implementation was added.
  Host comparison covers IDNA, ports and IPv6. An observed Goldmark IPv6-host
  escaping regression was reproduced and fixed in the safe link renderer.
- The shared page sanitizer now preserves explicit allowed link relations
  instead of automatically adding nofollow. This is necessary for the reviewed
  keep policy to survive v2 saves and frontend re-sanitization. Local tests and
  actual REST/frontend requests verify all three policies, responsive images
  and exact body preservation. Existing valid nofollow relations remain valid.
- Direct failure evidence includes the XSS/remote-image corpus, invalid schema
  and reference inputs, missing/forged media, operational resolver failures and
  output expansion limits. Eight deliberate safeguard removals each failed
  their targeted checks; restored source passed. An invalid inline-image body
  stops resolution after the first blocking error. Normalizer rejection leaves
  the disposable database's page count unchanged.
- Baseline checks: `scripts/test-gate.sh` passed all 60 test packages. Race
  checks passed for draft, sanitizer, pagewrite, v2 pages and MCP. Both final
  15-second fuzz targets passed (74,012 executions total). Vet, Staticcheck and
  Errcheck passed; scoped GolangCI reported zero issues. Full GolangCI retains
  the same five pre-existing findings in unchanged layout/MFA tests and local
  weather code. Govulncheck reports zero reachable/imported-package findings
  and one uncalled module advisory. These are not a whole-repository audit.
- Built assets with the immutable npm lockfile, rebuilt/restarted the disposable
  loopback app, and passed 32 actual HTTP checks across all three link policies,
  draft privacy, metadata updates, complete revisions/provenance and image
  assets. Browser inspection verified the article, caption and responsive
  source selection. Only synthetic local fixtures were published for this
  check; their short-lived REST key was deactivated and its private file removed.
  The local app remains running at `http://127.0.0.1:18082`.
- Source docs and local wiki were updated while preserving prior wiki work.
  Evidence is retained under ignored `output/r13-validation/`, including
  `verification.json`, `behavior-verification.md` and `source-manifest.json`.
  Runtime/test source fingerprint:
  `f4f48db10a7a5a206879ecf6cbb0d97ffd8feb215a28207dfd5978de5c926d8b`.

R1.3 is complete locally. No MCP CMS-authoring tools were registered and no
real-client authoring acceptance was claimed. Next is R1.4: shared grounding
and visibility resolvers, editorial guidance, MCP resources and prompts. Stop
here for human review; commit, push and deployment remain separate actions.

### Iteration R1.4 — Shared Grounding and Authoring Context

Implemented locally on `codex/phase5-r1.4-authoring-context`, based on R1.3
`67fb3980`. No commit, push or deployment occurred. Existing staging remains
on `c885da9071eb`; this iteration did not use its accounts or credentials.

**Grounding and existing consumers**

- Extracted the existing language, alias, canonical page, taxonomy/form and
  wildcard redirect rules into neutral `internal/contentpath`. Frontend,
  language/redirect middleware, importer and draft-origin callers now use
  these primitives. Grounding uses them too, without an HTTP client or a
  second approximation of frontend routing.
- References accept page IDs, slugs, paths/aliases and normalized same-origin
  URLs. Redirect walks stop at three hops and track visited paths. Explicit
  languages, default-language rules, reserved routes, IDNA/default ports,
  encoded paths, taxonomy/form precedence and canonical ID redirects are
  covered by executed frontend/resolver parity fixtures.
- Preserved the general draft-read policy. The separate context exception
  requires `mcp:drafts`, the current user as owner, AI opt-in and an
  unscheduled draft. Missing and inaccessible references have the same
  unresolved result. No preview/share token is generated.
- Context retains every selected role and page/revision identity. Duplicate
  references retain their entries and use the first recorded snapshot once.
  Markdown conversion preserves supported existing media and captions,
  reports losses and never rewrites authoritative HTML. Follow-up page
  list/detail results expose the current revision for comparison.
- Context data caps at 46,000 characters, leaving room for shared instructions
  below the 50,000-character client bound. Excerpts/guidance have explicit
  truncation flags; reference identities/roles are never silently dropped.
  Categories, tags, languages, link candidates and existing images have
  deterministic paging. Sparse legacy media rows advance the storage offset.
  Media LIKE search escapes literal wildcards and includes translations.
- Added the bounded example-overlap helper: at least 40 five-word shingles
  and 60% candidate coverage produce a warning. Source/parent references and
  short quotations do not trigger it. R1.5 will consume this during saves;
  R1.4 does not claim factual verification or plagiarism detection.

**Editorial guidance and MCP**

- Added core editorial tables, one site-default guide, a Russian override
  and six seeded presets: blog post, page, FAQ, how-to, release notes and bot
  blog. Per-language overrides replace defaults; clearing restores fallback.
  Migration rollback/reapply was tested against a preserved existing page.
- Added `/admin/editorial` with existing templUI components and English/
  Russian strings. Saves use the authenticated admin, recheck the current
  database role, respect demo restrictions and retain submitted text on
  validation failure. Existing Fetch Metadata CSRF protects the route.
- Added four read-only tools: `get_authoring_context`,
  `get_page_draft_schema`, `get_style_guide` and `list_presets`. The default
  catalog is now 14 tools; CMS-authoring mutations remain R1.5.
- Added versioned `write_page`, `translate_page` and `seo_review` prompts.
  Optional `preset`, `language` and compact `refs` arguments are single
  tokens. The conversation supplies the topic; a multi-word brief uses the
  full context tool. Prompt instructions and tool instructions share one
  definition and versioned golden fixtures. Site content is JSON-escaped in
  one labelled data envelope, never interpolated as instruction markup.
- Registered eight resource variants/templates for style guides, presets,
  taxonomy, languages, draft schema and issue catalog. Every resource returns
  the complete equivalent tool result; all three prompts use the same
  context/instructions as their equivalent task. Added active-language
  filtering to MCP list/search and the existing REST page list.
- Explicit empty tools/prompts/resources capabilities match the server card,
  without subscriptions/list-change notifications. Admin MCP instructions
  cap at 2,400 characters, keeping complete instructions within 4,096 even
  with scratch orientation; older stored text is only clipped for rendering.
- Prompt/resource reads use current authentication/revocation, the shared
  25-second cancellation bound, scrubbed errors and one principal-aware log
  record. Arguments, content, full resource URIs and raw exception text are
  excluded from these logs. Scratch tools stay behind their existing flag
  until their planned R1.5 removal. No dependency was added.

**Executed verification and limits**

- Two direct regressions were reproduced and corrected during implementation:
  encoded router paths initially disagreed with frontend 404/alias handling;
  mixed anonymous/named SQL parameters initially emptied taxonomy paging.
  The corrected shared raw/decoded-path handling and fully named queries pass
  their focused before/after checks and the complete suite.
- All ten deliberate safeguard-removal probes failed the intended tests,
  then were restored byte-for-byte. They cover shared consumers, visibility,
  hop/context limits, prompt boundaries, tool equivalents, capabilities,
  cancellation, no outbound content fetches and fresh administrator checks.
- `scripts/test-gate.sh`: all 64 test packages passed uncached. Race checks
  passed for grounding, prompts, contentpath, editorial, MCP, handlers, v2
  pages and search service. The 15-second resolver fuzz run passed 131,040
  executions. A small-fixture context benchmark measured about 0.51 ms/op;
  this is not a large-site performance claim.
- Vet, Staticcheck and Errcheck passed. Whole-tree GolangCI reports the same
  five pre-existing findings in unchanged layout/MFA tests and local weather
  source; no new finding was introduced. Govulncheck reports zero reachable/
  imported-package vulnerabilities and one uncalled module advisory.
- Regenerated sqlc/templ, built assets from the existing lockfile, rebuilt
  the binary and restarted only the disposable loopback app. Its database
  and uploads are a separate copy; the R1.3 synthetic fixture is preserved.
  Live SDK checks verify all prompt/resource equivalents, scope/ownership,
  revocation, language filtering, revisions, existing images and read-only
  behavior. Raw curl `tools/list` verifies the default catalog too.
- The 27 HTTP checks cover readiness, discovery, published language routes,
  REST filters/revisions, draft privacy, admin login/form, guide/preset
  overrides and fallback, invalid/oversized forms and cross-site rejection.
  Actual browser interaction saved an English override and restored site
  inheritance. Synthetic fixture keys were deactivated on success/failure;
  no key value was written to chat, command arguments or repository files.
- Source docs and local wiki were updated while preserving prior wiki work;
  no wiki publication occurred. Evidence is under ignored
  `output/r14-validation/`, including `verification.json`,
  `behavior-verification.md`, source/candidate manifests and the GUI capture.
  Runtime/test source fingerprint:
  `9bc88132c813b4e063903a1df67760bd2a99f336a661bbc9bd7ffdcf1ba5c13c`.

R1.4 is complete locally. The app remains at `http://127.0.0.1:18082`, with
Editorial guidance open for inspection. No fresh Claude/ChatGPT cloud-client
acceptance or staging/production migration is claimed. Next is R1.5: real
draft creation/update/translation tools and their policy/acceptance checks.
Stop here for human review; commit, push and deployment need separate approval.


## R1.5 implementation notes — local verification (2026-11-01)

Initial local verification on `codex/phase5-r1.5-authoring-tools`, base
`1c8f71d4`, preceded commit `0afcbf618e3b` and the subsequently authorized staging
deployment. At that point staging still ran `c885da9071eb` and fresh GUI
acceptance was pending. The completed staging checks and subsequent local
consent-copy correction are recorded below. Share issuance remains deferred to
R1.6 by the owner's explicit decision.

### Implemented behavior

- Twenty-one MCP registrations: fourteen existing reads, three content mutations,
  own-draft get/list, translation status and reserved `create_preview_link`.
  The latter returns `preview_unavailable` and creates no token. Save/read URLs
  use existing session-authenticated current-draft rendering. No new preview
  renderer, publication, scheduling or deletion tool was added.
- Default-off admin draft toggle and keep/nofollow/strip policy, using existing
  templUI controls. Current user/role, exact OAuth access token or API key,
  scopes, source restrictions and module/toggle are rechecked on transaction
  queries. Policy changes during queued work are rejected or retryable.
- Only owned opted-in unscheduled drafts are editable, across apps for the same
  user. MCP `mcp:drafts` does not grant broad REST page/taxonomy write access.
- Original typed-input idempotency precedes normalization, mutable defaults and
  CAS. Retained compact receipts reconstruct the original result from immutable
  revision provenance. Replays spend no quota or new revision/side effect.
  Seven-day results/tombstones reuse the R1.2 contract. New successful content
  operations are limited to sixty per user per rolling hour across credentials.
- Normalization, assignments, CAS, translation relationships, version/provenance
  and idempotency use the shared atomic writer. Translation source fingerprints
  cover title/body/summary/SEO text; lifecycle and membership-only changes do not
  make text stale. A duplicate target language leaves no orphan. Current
  visibility conceals hidden sibling/source identities and provenance references.
- Metadata-only saves preserve rich HTML and inspect links without rewriting it.
  Explicit report/reference edits can create a revision; identical evidence is
  a no-op. Omitted reports retain prior agent reports, including legacy format;
  an explicit empty report clears them. Client metadata always describes the
  current call and is untrusted. Human/system and related membership revisions
  receive empty agent evidence rather than the translated target's claims.
- The transport wire cap is 512 KiB inside a 2 MiB SDK ceiling; Markdown and
  expanded HTML remain bounded at 200,000 bytes. Errors preserve structured
  issues/retryability and complete text fallback. A typed-nil SDK result is safe.
  The scratch implementation/state/flag and active configuration were removed.
  Prompts use contract version 1.1.0 and direct agents to the actual draft tools.

### Direct evidence and baseline checks

- The final uncached suite passes **65 test packages**. Focused race checks cover
  authoring, normalization, pagewrite, v2 pages, OAuth, middleware and MCP. After
  the final report-omission fix, authoring and the affected SDK flows were also
  rerun under the race detector and passed.
- **Thirteen deliberate negative probes** fail by assertion, with byte-exact
  restoration: early replay, own-draft opt-in, quota, source fingerprint,
  per-page evidence, admin toggle, current key/source policy, annotation class,
  structured feedback, no-content-fetch boundary, mutation adapter inventory,
  probe retirement and omitted-report preservation. Positive checks pass after
  restoration. This is regression evidence, not an independent security audit.
- Parser/link fuzzing passes **34,374 executions**. Current small-fixture
  `b.Loop` authoring benchmarks: create about **0.83 ms/op**, update **1.49 ms/op**,
  excluding network/authentication and not a large-site latency claim. The quota
  query runs; historical benchmark receipts are outside its window. Validation
  and Markdown benchmarks also pass.
- Vet, Staticcheck and Errcheck pass, with final affected-package reruns. Scoped
  GolangCI has zero findings. Full GolangCI retains the same five unchanged
  layout/MFA/weather findings; the full lint gate is not green. Govulncheck has
  zero reachable/imported-package findings and one uncalled module advisory.
- sqlc/templ regeneration, lockfile assets, binary build, absolute-path and diff
  checks pass. The full suite exposed an editorial rollback test that assumed
  its migration was newest; it now rolls back to that migration explicitly.
- The rebuilt loopback app passes **25 live MCP checks** (including raw curl)
  and **39 HTTP checks**. They cover actual CMS saves/replays, same-user app
  continuation, CAS, translations, forbidden/foreign/published access, nofollow,
  sharing refusal, prompt/resource parity, session preview and anonymous denial.
  Browser checks confirm default-off settings, a saved nofollow policy, the
  normal editor/linked translation and authenticated frontend rendering.
  All temporary loopback-only API keys are deactivated; no owner MFA or external
  app connection was used. The prior synthetic database is preserved separately.

Runtime/test tree SHA-256:
`da72b5f4141a83ddf0e0f17a138f2fda2bf800f4f39a08ac11efb11d33480ddc`.
Ignored `output/r15-validation/` contains per-file source/candidate manifests,
`verification.json`, `behavior-verification.md`, test/quality/probe logs and
local HTTP/browser artifacts. Main docs and relevant local wiki pages are
updated; unrelated wiki work is retained and nothing is published.

The subsequent staging deployment and fresh Claude Desktop/ChatGPT authoring
acceptance are recorded below. R1.6 owns isolated revision previews, explicit
share tokens and editorial review panels; R1.7 owns release work.


## R1.5 staging deployment and GUI acceptance (2026-11-01)

The owner authorized deployment and dedicated-account Claude/ChatGPT acceptance,
with no Git push. Deployed exact commit
`0afcbf618e3b1f1d6e0cb362137ae60ca66369c9` from an isolated `git archive` source
context. All 1,270 archived source blobs remained unchanged after asset building;
64 archived Go test packages passed. The deployed version reports that commit.
Fly image `[deployment ID redacted]` runs on the existing single
Frankfurt machine, with 2 shared CPUs and 1,024 MB RAM. Pre-deployment volume
snapshot `[snapshot ID redacted]` completed before deployment. Public health,
readiness, OAuth/resource metadata and the MCP server card passed; final Fly
checks remain passing.

### Real-client results

- Used the dedicated Editor `ocms-agent@example.test` (user 2), its Keychain
  password and the shared development authenticator. Every OAuth login used
  normal password, MFA and consent; no owner codes, recovery codes or MFA reset.
  The only requested permissions were `mcp:access` and `mcp:drafts`.
- Created separate **oCMS R1.5 agent acceptance** connectors in Claude and
  ChatGPT. Claude disallows another connector at an identical URL, so both tests
  use the harmless `?acceptance=r15-agent` marker on the existing MCP endpoint;
  discovery and OAuth resource binding remain canonical `/api/mcp`. Existing
  owner connections were preserved.
- Claude web proved the default-off gate (`draft_tools_disabled`), then created
  page 3, replayed the identical request without a duplicate, read provenance
  and saved an update. ChatGPT read and continued the same user's page at the
  next revision; an intentional stale update returned `revision_conflict`.
- Native Claude Desktop read ChatGPT's update and later saved the source change
  that made the Russian translation stale. It also read ChatGPT's separate draft
  after the final reconnect. These were actual native-app interactions, not SDK
  probes standing in for Desktop.
- ChatGPT created/replayed its own English draft (page 4), translated Claude's
  source into Russian (page 5), and received generic `translation_exists` for a
  distinct request/slug targeting the same language. No duplicate or orphan page
  remained. Source membership revision 4 changed only `revision`/`translations`,
  keeping the translation current; the subsequent text edit to revision 5 made
  it stale. Both clients read the recorded source basis at revision 3.
- Claude's calls to a published page and another user's draft both returned
  generic `not_found`. A publication field was rejected by JSON Schema with
  `validation_error`; no content/lifecycle write occurred. Explicit share
  creation returned `preview_unavailable` and no token, as approved for R1.5.
- Revoked each new scoped test grant separately. Actual client calls received
  HTTP 401 and refresh attempts were refused; both apps displayed reconnect UI.
  Normal MFA/consent restored access under new grants. Claude required its normal
  base-scope reconnect followed by the draft-scope upgrade. The server then
  logged successful reads under the replacement scoped grants, user 2.
- Editor-session English/Russian preview URLs rendered saved content and
  `rel="noopener noreferrer nofollow"`. Anonymous requests to all three draft
  URLs and the registered REST v2 page routes returned 404. The existing editor
  showed the AI-editable flag, saved summary and linked Russian draft.

Final database inspection: three own AI-editable drafts, pages **3/revision 5**,
**4/revision 1**, **5/revision 1**; none published or scheduled. Complete snapshot
and provenance counts match every revision. The original seven active grants
(six owner grants and the existing dedicated Claude Code grant) are preserved.
Only the two replacement scoped GUI test grants remain in addition; redundant
base grants were revoked. SQLite quick check is `ok`. Temporary Fly maintenance
executables and each private MFA code file were removed. Staging authoring stays
enabled with `nofollow`; broad unpublished-content visibility stays disabled.
Russian was enabled as a non-default staging language for translation checks.

### Observations and remaining work

- **Consent copy corrected locally (not deployed):** staging acceptance found
  that `oauth.scope.mcp:drafts` still described retired development scratch
  tools. The English catalog now says “Create and edit your own AI drafts”;
  Russian says “Создание и редактирование ваших ИИ-черновиков”. The scope key and
  authorization behavior are unchanged. A later approved deployment is needed
  for staging to display this correction. Local before/after rendering of the
  actual consent component passed in both languages with and without the draft
  scope. The i18n suite, OAuth handler tests and translation-key drift check
  passed. Evidence: `output/r15-validation/oauth-consent-wording/`.
- ChatGPT intermittently displayed “This response couldn’t load”. A reload
  recovered the initial result, and a fresh read after reconnect rendered the
  final complete result. No mutation was repeated with a new request ID to work
  around a display failure. This client-display limitation is recorded rather
  than counted as a backend success before verification.
- Relation-only source revisions intentionally carry empty validation/report
  evidence instead of copying the target translation's claims. Earlier source
  revision evidence remains in history; the current projection is empty until a
  later authored revision. This matches the current documented/tested contract
  and is a review-UI consideration for R1.6, not destruction of history.
- Generic schema `validation_error` on a forbidden publication field is the
  observed catalog behavior; do not claim a `forbidden_field` response for that
  input. No independent security audit, new IPv6 verification, production
  migration, new CLI authoring acceptance or R1.6 preview isolation is claimed.

Evidence is in ignored `output/r15-validation/staging-acceptance/`, including
`acceptance.json`, deployment/snapshot manifests, sanitized tool/request logs,
exact request fixtures, database checks, client transcripts and screenshots.
The status update is uncommitted; no push or wiki publication occurred.


## R1.6 Implementation Notes — 2026-11-01

Implemented locally on `codex/phase5-r1.6-editorial-review`, based on
`50e4d8485c83ed540e7f893f112f25bf6c1aae0c`. The owner authorized implementation
only: no commit, push or deployment. Existing dirty wiki work was preserved
and corresponding pages updated locally, with before-sync copies in the
ignored validation directory.

### Review and publishing

- Added `claims_checks` and hash-only `preview_links` in migration
  `20261101230000_editorial_review.sql`; sqlc models/queries were regenerated
  through the repository wrapper. Migration down/up preserves pages and
  earlier snapshots. Deleting a reviewer retains the acknowledgment while
  clearing its user association.
- The editor sidebar separates Verified, Validated by oCMS and Reported by
  the agent. Registered names are not vendor verification; agent text cannot
  enter the Verified/Validated panels. Known validation messages are localized
  for display without changing recorded evidence; unknown historical codes
  retain their recorded text. New labels and controls cover English/Russian.
- Claim controls record the reviewer/time against one provenance/revision;
  stale forms fail without changing content or evidence. Historical controls
  are disabled. Current claim counts, AI badges and an AI-review list filter
  support the review queue.
- History attributes each recorded revision, uses logical revision numbers
  where available, labels legacy/system uncertainty and opens bounded
  side-by-side recorded-field/body-text comparisons. The existing shared
  CAS restore path remains authoritative and creates a new revision.
- Translation review uses the same fingerprint implementation as authoring.
  Text/SEO changes make translations stale; sibling membership/publication
  alone does not. Prior missing bases remain explicitly untracked.
- Admin publish/schedule, restoration into a published/scheduled page, REST
  publication and the scheduler share current
  AI-content hard validation, including live assignments/link/media checks.
  Historical authoring provenance prevents bypass by disabling AI access.
  Warnings/info and unchecked claims remain non-blocking. CAS rejects an
  action after an intervening edit, while a fresh reviewed schedule succeeds.

### Preview and sharing decisions

- Ordinary save/read URLs now pin a recorded revision and require a current
  editor/admin session. The staff draft-slug path uses the same isolated
  renderer. No content save, replay, list or read implicitly issues a bearer
  token. The shared prompt contract is now 1.2.0, with versioned goldens.
- Explicit MCP issuance requires current policy/scopes/ownership and binds the
  issuing grant/key. The editor requires a separate disclosure acknowledgment
  and displayed-revision CAS. Only own opted-in plain unscheduled drafts can
  issue links, including to an older complete recorded snapshot.
- Tokens contain 32 random bytes; SQLite stores only SHA-256 hashes. The URL is
  returned once, never retained in receipts or subsequent reads. Default TTL
  is 24 hours, configurable down to one hour and bounded by issuer expiry.
  Caps are ten active links and sixty issuances per rolling hour per user;
  revoked links still count toward issuance. Concurrent issuance is capped
  transactionally.
- Reads recheck current account/page/credential state. Explicit revoke, page
  lifecycle/owner/AI changes, credential revoke/scope changes and role/session
  changes invalidate links. Durable triggers prevent reversal resurrecting
  disclosed links. All unavailable bearer variants use 404.
- Preview rendering bypasses page caches and frontend tracking, suppresses
  module hooks/widgets, filters menu IDs/aliases/children and replaces HTML
  theme helpers with public-only request-local versions. It clears private
  translation/structured metadata, prepares embed placeholders, sanitizes body
  HTML and strips active/external theme content. CSP blocks scripts, frames,
  forms and external resource loads. Local theme CSS/image/font assets remain
  available; only bounded version cache-busters are accepted.
- The renderer uses only the selected complete snapshot for content and
  assignments. Missing/legacy snapshots return 404; theme failure returns 503,
  with no live-content fallback. Account presentation, taxonomy names and local
  asset bytes are current; historical asset fidelity is not promised.
- Application redaction precedes logging/integration middleware. Loopback
  HTTP checks verified bearer paths were logged as `/preview/share/redacted`
  and token/key strings were absent. Proxy/APM/CDN/error-log configuration is
  documented in `docs/editorial-review.md`; the nginx example was manually
  traced, not executed against a hosting proxy. Public-host redaction remains
  a prerequisite for later public share-link acceptance.

### Local verification and boundaries

- Direct service tests cover pinning across edits, missing/legacy snapshots,
  current-role/ownership/AI/lifecycle and credential revocation, expiry,
  concurrent caps, revoked-issuance quotas and migration rollback.
- HTTP handler tests cover anonymous/session/share behavior, theme failure,
  public menu/helper isolation and recorded-snapshot denial. DOM/CSP tests
  remove active/external loads and retain local versioned assets/placeholders.
  Review tests cover provenance spoofing, per-revision checks, deleted
  reviewers, translation fingerprints, diffs and publication validation.
- The real loopback app uses an isolated copy of the synthetic R1.5 database,
  a dedicated synthetic Editor and disposable CIDR-limited key. No staging
  account, owner authenticator, Fly resource or production data was touched.
  SDK-over-HTTP plus real admin handlers proved session-only saved URLs,
  explicit old-revision anonymous rendering, English/Russian preview access,
  language/anonymous denial, claim checks, revision comparison, one-time URL
  display, revoke and credential-bound invalidation. Test key was deactivated.
- Browser verification covered English/Russian panels and localized issue
  text, actual checkbox save, source freshness and attributed history. Captures
  and command logs are in ignored `output/r16-validation/`; the final evidence
  record binds results to source hashes and records the final gate results.
- Public staging/reverse-proxy and fresh Claude Desktop/ChatGPT/CLI acceptance
  of R1.6 remain pending for a separately authorized deployment/R1.7. R1.5's
  earlier cloud authoring results are retained as historical evidence. No new
  staging acceptance, deployment, independent security audit, commit or Git
  publication is claimed.

### Final local gate

The uncached Go suite passes across 67 test packages. Race checks pass for
preview, review, authoring and MCP Server (the complete MCP race package took
286 seconds). Vet, staticcheck, errcheck, govulncheck, generated sources,
asset/application builds, absolute-path and diff checks pass. Govulncheck
reports zero affected/called vulnerabilities and one advisory only in an
uncalled required module. Full golangci-lint retains the same five findings
in unchanged account-layout/MFA/weather code; there are no new R1.6 findings.
These are baseline quality results, separate from the direct behavior checks
above. Logs and the final source manifest are in the ignored evidence folder.

Final handler checks additionally reject malformed/overflowing claim indexes
without aliasing claim zero, confirm a fresh claim save and stale conflict,
match logical history numbers to preview revisions and verify the disposable
key is inactive. A restore test rejects invalid historical content on an
already-published AI page atomically, and accepts a valid restore with a new
revision while preserving publication. Public proxy logging and fresh real
client R1.6 acceptance remain unexecuted under the no-deployment instruction.

## R1.6 Fly and web-client acceptance — 2026-11-02

The owner subsequently authorized deployment and Claude/ChatGPT acceptance,
with no push. Exact commit `ff4de3b1368cdc86095980283121c3aceed54d15` was
built from `git archive`: 1,288 archived blobs remained unchanged after the
asset build; 66 archived Go test packages passed uncached. This differs from
the earlier working-checkout gate of 67 packages. Runtime version and image
`[deployment ID redacted]` identify that deployed source. Snapshot
`[snapshot ID redacted]` completed before deployment. The existing
Frankfurt machine, encrypted volume, 2 shared CPUs/1,024 MB, MFA and disabled
DCR were preserved. Public health/readiness and all Fly checks pass.

### Direct acceptance results

- Claude web (Pro, Opus 5.5 Extra) and ChatGPT web (Instant) used the existing
  dedicated Editor, user 2, through their dedicated acceptance connectors.
  Normal login, generated MFA and consent used the shared authenticator;
  no owner code, recovery code, seed extraction or MFA reset was needed.
- Claude created/replayed page 6 revision 1. ChatGPT continued it at revision
  2, created/replayed its own page 7, and created Russian translation page 8.
  Membership-only source revision 3 kept that translation current; Claude's
  body change at revision 4 made it stale. Final revisions are 6/5, 7/3 and
  8/1, all unscheduled, AI-editable drafts with matching snapshot/provenance
  counts. A fresh ChatGPT stale-write test returned `revision_conflict`,
  current revision 5, changed fields and a non-retryable structured error.
  Its sentinel summary was absent from every stored snapshot.
- The editor showed Verified / Validated / Reported separation, attributed
  cross-client history, a recorded comparison and disabled historical claim
  controls. Checking a revision-4 claim recorded the reviewer/time without
  creating a revision. Revision 5 started unchecked while revision 4 retained
  its acknowledgment. A stale share form was rejected after the newer save.
- Neither saves, replays nor reads issued a share. Each client explicitly
  issued one link to synthetic content; later draft reads and idempotency
  receipts omitted bearer paths. Only 64-character token hashes are stored.
  Claude's revision-1 share returned the original body after later edits;
  ChatGPT's share returned only its own snapshot. Anonymous session previews,
  missing and malformed bearer links returned 404.
- Share responses had private/no-store, no-referrer, noindex/nofollow and a
  script/frame/form/external-load-blocking CSP. The tested developer theme
  retained local presentation and omitted canonical metadata, forms,
  tracking and the private Russian sibling. The old session preview also
  visibly rendered revision 1 while the current page was revision 5.
- Explicit editor revocation and controlled expiry each changed 200 to 404.
  Publishing only disposable page 7 invalidated its link; returning it to
  draft did not resurrect access. Revoking the dedicated Claude issuer
  grant invalidated its link and caused the real client to request reconnect.
  Normal base-scope reconnect followed by the additional draft-scope flow
  restored a fresh read under a new scoped grant. Only the redundant new
  base grant was revoked; the working scopes remain `mcp:access mcp:drafts`.

### Failed upstream-redaction gate

Application access logs recorded `/preview/share/redacted`; the captured
normal-request stream contained zero tested bearer tokens, query markers or
referrer markers. **Fly Proxy did not redact the path on an error.** A request
with a non-credential canary and invalid force-instance header returned 400;
its provider=`proxy` record retained the path in `meta.URL.Full`. This directly
reproduces Fly's documented COMPLIANCE-12 behavior. No real token was sent in
the error probe. Evidence is sanitized; raw log streams and tokens were not
saved in the repository.

The tested web-client and application behavior passes do not close secure
public sharing. A follow-up must remove the bearer from the proxy-visible
URL or change the ingress, then repeat the error-path verification. An nginx
configuration behind Fly Proxy cannot repair a prior edge record. Private
draft content must use session previews until this is corrected. No runtime
fix or additional deployment was made during these acceptance checks.

### Cleanup and boundaries

All four disposable share links are revoked and return 404. The three R1.6
test pages remain drafts; earlier agent pages and all six owner grants are
unchanged. The existing dedicated Claude Code and ChatGPT grants are retained;
the dedicated Claude web grant was replaced by the tested reconnect. SQLite
quick check is `ok`; temporary server helpers and private code/token delivery
files were removed. No new API test keys, secret changes or Git push occurred.

ChatGPT initially displayed “This response couldn't load”; fresh readback,
explicit share and final structured-conflict responses rendered successfully.
The native multiline input initially split a test prompt and altered Markdown;
later prompts used a single fill, and no mutation was retried with a new UUID.
Claude retained an R1.5 cached share description although the real R1.6 call
succeeded; that is a client discovery/cache observation. Relation-only source
revision 3 intentionally carried empty validation/report evidence, as in the
existing contract. Native Desktop and CLI acceptance, the full stale
publish/schedule matrix and an independent security audit were not rerun on
this build; they remain R1.7 work. No full release-readiness claim is made.

Sanitized transcripts, screenshots, deployment/source manifests, database
metadata and HTTP/log checks are in ignored
`output/r16-validation/staging-acceptance/`. These status updates are local,
uncommitted documentation; the pre-existing dirty wiki is untouched.

## R1.6 local preview transport fix — 2026-11-02

The owner authorized a local fix and verification only: no commit, push or
deployment. New links use `/preview/share#<bearer>`. A standalone nonce-only
bootstrap clears the fragment/tracking query from the current URL and submits
one form field to fixed `/preview/share/open`. The resulting captured preview
retains the strict script-blocking CSP and all per-read lifecycle checks.
No bearer cookie or stored redemption receipt is introduced. Distinct landing
and rendering paths force a fresh request when another link is opened.
ChatGPT's added `utm_source` query is ignored by the credential-free GET,
cleared before submission, and never accepted as authorization.

Legacy path-token requests fail without a redirect. A one-time migration
irreversibly revokes all previously unrevoked links, including expired ones,
so logged old credentials cannot be converted into the new transport.
Accounts, sessions, content, keys and grants are retained; repeated normal
startup does not revoke freshly issued links. Browser sharing now requires
JavaScript; non-browser consumers use the documented bounded body-only POST.

Direct local verification reproduced the original URL-shape regression before
the fix, then passed fragment issuance, MCP output, strict POST parsing,
encoded/legacy/query-only denial, pinned rendering, and migration up/repeated
startup/down behavior. A real Chrome session through an isolated local proxy
proved the GET → POST flow, token-free address bar after redemption, reopening,
ChatGPT tracking links, explicit revocation, expiry, malformed fragments and
an upstream 503 before application handling. All 24 observed request targets
and referrers excluded the synthetic bearers. The fixture used a disposable
local Editor/database, with no owner or staging credentials.

The prepared source copy passed asset generation without changing any source
blobs and the full uncached Go suite across 66 test packages. Focused race
tests passed for preview, handler, app routing and MCP; vet passed. A fresh
read-only investigation and independent candidate review found no remaining
concrete bypass or regression in this boundary. The existing unrelated MFA
`unparam` lint finding remains; no new scoped lint issue remains. Browser
fixtures and private token-delivery files were removed after verification.

At this local-only checkpoint the deployed Fly gate was still open; staging
remained on `ff4de3b1368c`. The separately authorized rollout and fresh checks
are recorded below. An old URL-issuing binary must not run concurrently
against the upgraded database. Request/response body dumps remain prohibited.

## R1.6 preview transport Fly rollout — 2026-11-02

The owner authorized deployment of exact commit
`31c55f8c9f67b1d7f07854b29dd4255a5ef6cb1f` to `ocms-mcp-staging` and fresh
Claude/ChatGPT preview-link verification, with no push. This supersedes the
undeployed status above and closes the tested staging URL-leakage gate;
it does not establish full R1.7 release readiness.

### Deployment and baseline checks

- The exact Git archive contained 1,293 source blobs, unchanged after asset
  generation. The full archived Go suite passed uncached across 66 test
  packages; these are baseline checks, separate from direct client evidence.
- Volume snapshot `[snapshot ID redacted]` completed before deployment.
  Image `[deployment ID redacted]`, digest
  `[image digest redacted]`,
  runs version `mcp-staging-31c55f8c9f67`, built at `2026-11-02T00:00:41Z`.
  Runtime version output confirmed the full requested commit.
- Startup applied `20261102010000_preview_fragment_transport.sql`. The four
  previously revoked legacy links retained their revocation records. This
  rollout did not have an active legacy link to invalidate; migration
  invalidation and repeated-startup behavior were exercised locally above.
- The single Frankfurt machine and encrypted volume, 2 shared CPUs/1,024 MB,
  canonical HTTPS origin, MFA and disabled DCR were preserved. Public
  liveness/readiness and all three Fly checks passed after rollout.

### Direct client and anonymous-browser results

- Claude web (Pro, Opus 5.5 Extra) and ChatGPT web (Instant) used the existing
  dedicated Editor, `ocms-agent@example.test`, user 2, and the dedicated
  acceptance connectors. A fresh normal staging login used the shared
  authenticator. Existing `mcp:access mcp:drafts` grants were retained; no
  owner MFA, recovery code, seed extraction or authentication reset was used.
- Both clients freshly read their own existing draft, explicitly issued
  exactly one share link, then read again. Claude selected page 6 revision 1
  while its current revision remained 5; ChatGPT selected page 7 revision 1
  while its current revision remained 3. Both returned `/preview/share#<bearer>`.
  Subsequent draft reads returned only session-preview URLs and no bearer.
  No content mutation, publication or implicit share issuance occurred.
- A separate anonymous browser first reached login when opening account
  security. The ordinary session-preview URL returned 404 there. Both actual
  client share URLs then rendered the selected original snapshot with the
  revision banner, not the latest edited body. ChatGPT's rendered link added
  `?utm_source=chatgpt.com` before the fragment; that exact link also passed.
- Browser network capture recorded 13 requests, with no bearer in request
  URLs, referrers or response Location headers. Landing GETs used fixed
  `/preview/share`, with only the benign ChatGPT tracking query when present;
  the browser cleared fragment/query before POST to `/preview/share/open`.
  The bearer appeared only in the form body. Successful redemption returned
  200 with private/no-store, no-referrer and restrictive preview CSP.
- Editor revocation of Claude's link and controlled expiry of ChatGPT's link
  each changed successful redemption from 200 to 404 in that anonymous
  browser. The capture included all four body redemptions and was not
  truncated. Revocation and expiry changed no page revision.

### Fly proxy-error evidence and limits

Eight controlled requests with invalid force-instance headers produced HTTP
400 and real Fly provider=`proxy` error records (`PU01`). They included a
non-credential legacy-path control and replays of the observed Claude landing,
ChatGPT tracking-query landing and POST redemption targets. The browser
controller did not support injecting the Fly test header, so the error probes
used a separate HTTP client with non-credential content; no live bearer was
put into an error-probe URL or body.

All 247 complete captured application/proxy records contained neither of the
two real test bearer strings nor the non-credential POST-body canary. The
capture was not truncated. These proxy errors had empty `meta.URL.Full`
fields, so they do **not** prove that Fly redacts paths or every telemetry
surface. The direct URL-safety evidence is the actual browser GET/POST trace:
new credentials never enter the HTTP request target. The earlier canary
failure on the URL-token build remains valid historical evidence. No Fly
logging policy was changed; body dumps must remain disabled because the
credential necessarily travels in the HTTPS POST body.

### Cleanup and remaining scope

Both new test links (IDs 5 and 6) are revoked. Existing draft revisions,
review/claim data and all nine active OAuth grants, including all six owner
grants, matched the pre-deployment metadata. SQLite quick check returned
`ok`. The temporary remote inspector and private MFA delivery files were
removed, the log capture stopped and its private buffers released. Raw log
streams and bearer values were not saved in the repository.

The full draft/review matrix was not repeated on this transport-only build.
Native Desktop/CLI authoring acceptance, the full stale publish/schedule
matrix, connected-app release work and an independent security audit remain
R1.7 work. The runtime commit was already approved; this rollout made no new
commit, Git push or repository edit, and preserved the pre-existing dirty wiki.

## R1.7 Implementation Notes — 2026-11-02

The local implementation checkpoint below was based on
`5ae249ea6ff40be06603e542f1434e55cb4e3ec9`, in the existing checkout. At that
checkpoint the owner had authorized no commit, push or deployment, and staging
still ran R1.6. The pre-existing dirty wiki and ignored custom-module sources
were left untouched. The later approved `df6e5d86` commit, Fly deployment and
bounded web-client checks are recorded in the
[staging record](#r17-fly-staging-and-web-client-acceptance--2026-11-02).

### Connected-app management

- Added `/account/connected-apps` for own OAuth grants and MCP-capable API
  keys, with registered name/host, scopes, created/last-used/expiry metadata,
  status and revocation. Both credential classes paginate independently at
  25 rows; SQL projections omit hashes, lookups and key prefixes. Invalid
  key permission documents are excluded consistently with authentication.
- `/admin/mcp` uses the shared panel with account attribution and explicit
  administrative revocation. The live overview uses bounded metadata queries,
  rather than materializing the legacy complete key list. Personal account
  management remains owner-only even when the account is an administrator.
- The shared service rechecks the actor's current account role and session
  version inside a transaction. Forms are body-only, bounded and reject
  duplicate/query selectors. Normal session/CSRF and demo-mode restrictions
  apply. Revocation is repeatable, preserves the first grant revocation time,
  deactivates keys and records a security event. It creates no credentials,
  changes no scopes and deletes no content/review data.
- Existing grant/key lifecycle triggers invalidate shares; the next MCP
  request rechecks revocation. OAuth validation now updates last-used metadata
  at most once per minute; token exchange keeps its existing timestamp touch.
  Registered names/hosts remain explicitly unverified and API keys have no
  asserted client host. The management page works with the issuer disabled.
- Added English/Russian labels and account navigation. Same-origin referrers
  preserve the validated language-switch return path without disclosing that
  path cross-origin. Reserved alias routes and the scheduler/webhook startup
  drift checks include the new entry points. The old security-page revoke
  route remains compatible. MCP settings no longer call sharing a future
  R1.6 feature; they retain the explicit-request requirement.

### OAuth retention

- `core:oauth_cleanup` is registered before scheduler startup, every 15
  minutes, even with OAuth disabled. Scheduler overrides/manual triggering
  are supported. No retention environment knob or security-policy weakening
  was introduced; [the policy](../../docs/connected-apps.md#retention-policy)
  documents the constants and recovery boundary.
- One five-second transaction deletes at most 250 records in each class:
  codes expired for 24 hours; tokens/previews in grant families expired or
  first revoked for 30 days; empty eligible grants; unused client metadata
  expired for 24 hours. Child records are drained explicitly before family
  deletion, bounding cascades to at most 1,250 deletions per run.
- No token of a still-active family is pruned, including spent refresh hashes
  and expired access rows. Recent terminal families retain evidence. A large
  retired family drains over repeated runs. Any statement or commit failure
  rolls back the whole batch and reports no committed deletions.
- Migration `20261102020000_oauth_retention.sql` adds query/dependency indexes
  only. Down/up preserves active credentials and content; already completed
  retention deletions are not reversed. The existing registration-cap cleanup
  may still evict unused expired client metadata earlier.

### Direct local verification

Service/HTTP tests pass for own/admin isolation, stale role/session denial,
foreign/non-MCP target refusal, pagination through older keys, safe display
projection, escaped app names, duplicate/query selector denial, CSRF, repeated
revocation and demo-mode refusal. Current OAuth access succeeds before revoke
and fails afterward; linked previews are durably invalidated.

Retention tests pass at exact 24-hour/30-day boundaries, across repeated runs,
with oversized token/preview/code/grant/client batches, cancellation, statement
failure and deferred commit failure. An active family's expired access/spent
refresh rows survive cleanup; replay still revokes its current credential.
Concurrent cleanup and normal refresh also preserve replay detection. The
index migration's down/up path retains a usable credential.

A real loopback app used a new isolated database and synthetic Editor/admin
accounts. Its English/Russian connected-app views, metadata, account
attribution, revocation and language switching passed in a separate browser.
The Editor could not enter the administrator overview. Local MCP
`get_site_info` returned 200 under both credentials using the negotiated
`2025-11-25` protocol; after GUI revocation each next request returned 401.
Both synthetic grants are revoked, the key inactive and SQLite quick check
`ok`. Server logs contained none of the private password/key/access strings.
The browser session was signed out/closed, its server stopped and the private
credential-delivery file removed. No owner or staging credential was used.

The bundled `docs/skills/ocms-author/SKILL.md` validates structurally. Its
normal save, retained retry, stale revision, foreign draft and explicit-share
instructions were manually traced against the actual schemas/services and
existing SDK tests; this is not an independent agent/client forward test.
The launch script and article outline are prepared files, not a recorded demo,
published article or authorization for an external action.

### Baseline checks and measurements

The full current-checkout uncached Go suite passes across 69 test packages,
including installed ignored custom modules. Focused race checks pass for
OAuth, connected apps, handler, scheduler and MCP management paths. Core
golangci-lint and `go vet ./...` pass; asset generation, application build,
sqlc/templ regeneration, skill validation, absolute-path and diff checks pass.
These are local results, not an archived release-build or independent audit.
Three earlier core test-helper lint issues were corrected without changing
runtime MFA behavior. Ignored custom sources were not edited to clear lint.

Small-fixture measurements on darwin/arm64, Apple M3 Ultra, Go 1.27.3, ten
iterations per benchmark (not production load tests or release SLOs):

| Operation | Time per operation |
|---|---|
| Connected-app first page, 100 grants and 100 keys | 0.724 ms |
| Retention, 250 tokens plus one retired grant | 1.042 ms |
| Draft create | 0.827 ms |
| Draft update | 1.291 ms |
| Authoring context | 0.729 ms |
| Draft validation | 0.165 ms |
| Markdown pipeline | 0.560 ms |

### Open release gates

- `govulncheck ./...` reports zero affected/called vulnerabilities and zero in
  imported packages; one advisory remains only in an uncalled required module.
  This is a dependency analysis result, not a complete security audit.
- `npm audit` reports seven moderate package entries rooted in the
  [minimatch advisory](https://github.com/advisories/GHSA-g3hv-75wg-m249),
  through Klaro's ESLint/parser dependency tree; no high/critical entry.
  The asset script copies Klaro's prebuilt browser JS/CSS, not those Node lint
  dependencies; copied JS bytes match that distribution. No forced Klaro
  downgrade or incompatible minimatch-major override was applied. A safe
  dependency fix or an explicit documented release disposition is still needed.
- Full-workspace golangci-lint retains two findings in the ignored installed
  weather module. Core lint passes. The installed NilAway was built for Go
  1.26 and could not load Go 1.27 source; a private rebuild of the same pinned
  tool runs but reports broader model/test-fixture diagnostics. No clean
  whole-project nil-safety check is claimed; those diagnostics need disposition.
- At this local checkpoint, fresh native/CLI authoring acceptance was pending.
  Later web and native/CLI passes are recorded below and in the per-client
  matrix. Native-only Desktop reconnect, the full two-target multilingual
  demo with existing taxonomy/media, the complete publish/schedule failure
  matrix, independent security audit, human publication and demo recording
  remain open. Wiki synchronization and release preparation are not complete.

Sanitized local results, screenshots, source fingerprints and detailed
verification are kept under ignored `output/r17-validation/`. Raw credentials
are excluded. This local checkpoint performed no commit, Git push, deployment,
site publication or external message. Subsequent authorized staging work is
recorded below. R1.7 remains open for release closure.

## R1.7 Fly staging and web-client acceptance — 2026-11-02

The owner separately approved the implementation commit and its deployment to
`https://ocms-mcp-staging.fly.dev`, with no Git push. The exact reviewed commit
was archived for the build; ignored modules, credentials and dirty wiki work
were excluded. Asset generation and all 68 uncached archived Go test packages
passed, with the archived source blobs unchanged after asset generation.

- Commit: `df6e5d8687eabaeca4f978f33ad6fa3da86aadeb`.
- Runtime: `mcp-staging-df6e5d8687ea`, built `2026-11-02T04:30:42Z`.
- Image: `[deployment ID redacted]`, digest
  `[image digest redacted]`.
- Pre-deployment volume snapshot: `[snapshot ID redacted]`.
- The existing Frankfurt machine, two shared CPUs, 1 GB RAM and original
  volume were preserved. Startup installed all six retention indexes.
  Public HTTPS health/readiness, issuer/resource metadata and anonymous MCP
  denial passed; all three final Fly checks were passing.

### Tested clients and settings

Claude web (Pro; UI model Opus 5.5 Extra) and ChatGPT web (UI model Instant)
used the existing dedicated acceptance connectors in Chrome on macOS. Exact
browser/web build versions and ChatGPT's account tier were not recorded.
These are web-client results, not native Claude Desktop or CLI authoring
passes. Client-reported protocol names remain unverified metadata.

The acting account was `ocms-agent@example.test`, Editor user 2. Normal fresh
password/MFA/consent used the shared Keychain authenticator without owner code
entry. Both restored grants have exactly `mcp:access mcp:drafts` and a 30-day
refresh-family lifetime. MCP draft authoring was enabled, ordinary draft
visibility remained off, external links used `nofollow`, and English/Russian
were active. MFA, OAuth, sanitization and existing API-key restrictions stayed
enabled; seeding, demo mode and DCR stayed off.

### Direct web-client results

- Both clients read site/schema/context, resolved published reference page 1
  at revision 1, created an English draft, replayed the identical create
  without a duplicate, and created/read a linked Russian translation.
- ChatGPT continued Claude's draft with a summary-only update. A stale base
  revision returned `revision_conflict`; an identical successful update replay
  returned the same revision. The original body survived and its Russian
  translation became stale after the source summary changed. Linking a
  translation had advanced source membership revisions without making an
  unchanged source fingerprint stale.
- The editor showed recorded OAuth principal/app, separate validated/reported
  information and translation freshness. No publication or scheduling occurred.
  Final synthetic fixtures were:

  | Fixture | Page | Language | Final revision |
  |---|---|---|---|
  | Claude source, continued in ChatGPT | 9 | en | 3 |
  | ChatGPT source | 10 | en | 2 |
  | Claude translation | 11 | ru | 1 |
  | ChatGPT translation | 12 | ru | 1 |

- Ordinary revision previews denied anonymous access. Each client issued one
  explicitly requested share. Claude's page 9/revision 2 snapshot retained its
  original summary after the current draft reached revision 3; ChatGPT's
  page 10/revision 2 link also rendered with its tracking query intact.
- English/Russian Account → Connected apps displayed the dedicated account's
  metadata and preserved the language-switch return path. The Editor was
  forbidden from `/admin/mcp`. Revoking each own web grant produced the native
  client reconnect gate and invalidated that grant's share link (404).
- Fresh reconnects restored both clients with the intended account/scopes.
  Claude read page 9/revision 3; ChatGPT read pages 10/revision 2 and 9/revision 3,
  including the cross-client summary. New caller grant identities matched
  active database rows and successful MCP events. Historical save provenance
  retained the original grant identity, separately from current authorization.

The first scope-upgrade attempt expired while the Mac was locked; fresh
sign-ins completed after it was unlocked. ChatGPT's paused revocation-check
call resumed after the planned reconnect and was initially described by the
client as unexpected access. The explicit new-identity/readback checks above
confirmed the new grant; this was not evidence that the revoked grant worked.

### Retention, logs and preservation

A temporary staging maintenance helper invoked the deployed source's normal
`oauth.Cleanup` twice. The first batch removed nine expired authorization codes;
the immediate second batch removed nothing. The fingerprint of 32 active-family
token rows was unchanged. An automatic scheduler batch at `04:45:00Z` removed
one additional expired code. No token, preview, grant or client row was removed
by these batches; aged-family draining, rollback, batching limits and refresh
replay/concurrency remain local-test coverage, not live replay experiments.

An exact-value check of a captured Fly log buffer found neither test share
bearer nor the dedicated password. The final 100-record scan found no
credential patterns, and current client grant hashes matched successful MCP
events. Captures were bounded; raw logs were not retained and no new
upstream-error probe was run. This does not extend the earlier R1.6 proxy-error
coverage or establish proxy redaction.

Final database checks preserved all six owner grants and six older dedicated
pages, confirmed five successful content-write receipts with no persisted
stale-write marker, and returned SQLite quick check `ok`. All four new pages
remain unscheduled drafts. Both test shares remain revoked. The temporary
Claude base grant was revoked; both restored web grants and the pre-existing
Claude Code grant remain active. Temporary Fly helpers and private MFA-code
files were removed. Closing one expired browser MFA window could not be
confirmed after a controller timeout; it is not a pending consent for either
restored connection.

Sanitized records and screenshots are in ignored
`output/r17-validation/staging-acceptance/`, including `acceptance-summary.json`,
`deployment.json`, `final-preservation.json`, `retention-staging.json`,
`scheduled-retention.json` and the client readback/log records. No credentials,
share tokens or machine-specific paths enter these status notes. The rollout
made no Git push, publication or release. At this checkpoint the native/CLI
matrix was unfinished; the follow-up below records those later results and
the Desktop reconnect caveat. The complete multilingual demo and remaining
release gates stay open.

## R1.7 native Desktop and CLI acceptance — 2026-11-02

The owner authorized native Claude Desktop, Claude Code and Codex acceptance
on the existing `ocms-mcp-staging` deployment, including CLI OAuth and bearer
modes, with no commit, push or deployment. The live binary reported
`mcp-staging-df6e5d8687ea`, commit
`df6e5d8687eabaeca4f978f33ad6fa3da86aadeb`, built
`2026-11-02T04:30:42Z`. The image is the one recorded in the web rollout above;
no application rebuild or deployment occurred during this pass.

### Clients, identity and scope

- Native Claude Desktop **4.4212.0**, Pro, visible model **Opus 5.5 Extra**;
  the dedicated acceptance connector shared with Claude web.
- Claude Code **2.1.316**, observed model `claude-opus-5-5`, and Codex CLI
  **0.166.0**. CLI subscription tiers and Codex's model were not freshly
  recorded; older R1.1 tier observations are not new measurements.
- Dedicated account `ocms-agent@example.test`, Editor user **2**, with the
  shared Keychain password and generated MFA codes. Normal fresh sign-in and
  consent were preserved. OAuth reconnects used 30-day refresh families and
  exactly `mcp:access mcp:drafts`; no broader permission was granted.
- Each CLI used an isolated staging configuration with other connectors and
  command/file/web tools disabled. Its bearer mode used a separate 90-minute
  Keychain-held test key, restricted to the Mac's public IPv4 `/32` and the
  same two permissions. Both keys were deactivated after testing.
- Draft authoring stayed enabled, ordinary draft visibility off and external
  links `nofollow`. Only English/Russian were active; no existing categories,
  tags or media were available. No settings, roles or owner connections changed.

### Direct native and CLI results

| Client/mode | English page | Russian translation | Final revisions (EN / RU) | Bounded result |
|---|---|---|---|---|
| Native Claude Desktop / OAuth | 13 | 14 | 4 / 1 | Authoring/readback passed; reconnect needed Chrome |
| Codex / OAuth | 15 | 16 | 3 / 1 | Passed |
| Claude Code / OAuth | 17 | 18 | 3 / 1 | Passed |
| Codex / bearer | 19 | 20 | 3 / 1 | Passed |
| Claude Code / bearer | 21 | 22 | 3 / 1 | Passed |

- All five paths read site/schema/context, created an English draft, repeated
  identical create/update requests, read their drafts and linked one Russian
  translation. Retries returned identical successful payloads and revisions;
  the API does not expose a `replayed` flag. Stale updates returned structured
  `revision_conflict` and did not persist the intentional marker. CLI checks
  compared actual requests/results, matching text fallbacks and preserved
  body/agent reports after summary-only edits, not model verdicts alone.
- Translation membership advanced the source from revision 2 to 3 without
  changing its text fingerprint; the new translation stayed current. Membership
  revisions have their own provenance. Historical reports/claims remained
  visible in revision review; they were not inherited from the target draft.
- Codex OAuth continued Desktop's page 13 with a summary-only update to
  revision 4, including an identical replay. Native Desktop and Claude Code
  read that revision; Claude Code's stale revision-3 update was rejected.
  Russian page 14 became stale. Native Desktop and Claude Code received generic
  `not_found` when requesting published/foreign page 1 through `get_my_draft`.
- The editor showed the native draft's body/SEO fields, writer identity,
  separated verified/validated/reported blocks, historical unacknowledged
  claim and translation freshness. No claim acknowledgment, restore, publish
  or schedule action was executed in this follow-up.
- Desktop explicitly issued one share pinned to page 13/revision 2. It
  rendered in the browser and in cookie-free HTTP checks (200), retaining its
  earlier summary after the source reached revision 4. Anonymous ordinary
  previews returned 404. Revoking its issuing grant made the old share return
  404; it remained revoked after reconnect.
- Revocation rejected both CLIs' OAuth/bearer startup requests with MCP 401;
  OAuth refresh attempts also returned 400. No model-level tool ran after the
  failed handshake, so missing tools alone were not counted as refusal proof.
  Both CLI OAuth reconnects issued new grants and successfully read Desktop's
  page 13/revision 4. Independent revoked-bearer probes also returned 401.

### Desktop reconnect caveat

Native Connect/Reconnect did not open an observable sign-in in the controlled
native/Chrome surfaces. Chrome web settings and Connect in the same shared
chat launched the normal OAuth flow. Password, shared MFA and consent restored
the connector, first at `mcp:access`, then through the normal `mcp:drafts`
scope upgrade. The final fresh native `get_site_info` and `get_my_draft` calls
confirmed user 2, exactly both scopes and page 13/revision 4. Database grant
metadata and successful MCP events corroborated the new caller; saved revision
provenance still identified the earlier Codex writer. The temporary access-only
grant was revoked.

The scope upgrade explains the draft read after the earlier access-only
site-info response; it is not a scope-enforcement bypass. The cause of the
native reconnect behavior was not established. Native authoring/readback
passed with this Chrome workaround; **native-only reconnect remains
unverified**, not an unconditional OAuth UX pass.

### Logs, preservation and remaining gates

Six overlapping Fly log slices of up to 100 records had no credential-pattern
matches. An exact-value scan of the current raw buffer found neither the two
test bearer keys nor the share token; retained text evidence also contained
none. Raw logs were not retained. These bounded captures do not cover every
historic request, and no new proxy-error experiment was run or proxy-redaction
claim established. A consumed single-use Codex callback code appeared in one
intermediate accessibility tool snapshot; callback redaction was corrected.
No reusable credential or MFA code was exposed or retained in evidence.

Final database checks preserved all **six owner grants**, all **ten older
dedicated-account pages** and site settings. All **ten new pages** remain
unscheduled drafts, every test share is revoked, no intentional stale-write
marker persisted and SQLite quick check returned `ok`. Both test bearer keys
are inactive; the restored two-scope OAuth connections remain active.
Temporary Fly helpers, private MFA delivery files and nine temporary Chrome
tabs were removed. The native acceptance chat remains available. Pre-existing
seed/demo source and dirty wiki work were preserved.

Sanitized evidence is in ignored
`output/r17-validation/native-cli-acceptance/`: `acceptance-summary.json`,
`cli-assertions.json`, per-run tool events/summaries, `final-preservation.json`,
`credential-redaction-check.json`, preview checks, native screenshots and
accessibility records. These records, credentials and machine-specific paths
are excluded from the documentation commit.

The two-target multilingual demo and successful existing taxonomy/media cases
remain unexecuted because staging had only English/Russian and no such
fixtures. Native-only Desktop reconnect, the full publish/schedule failure
matrix, independent audit, unresolved quality/dependency dispositions, human
publication, recording, wiki synchronization and release preparation remain
open. This bounded pass does not complete R1.7 or authorize Release 2.

## R1.7 local quality follow-up — 2026-11-02

This follow-up starts from core HEAD `1ac771a2a379842b69959da10b345aa7e8340e72`
and includes uncommitted local quality changes. The deployed staging binary
remains `df6e5d86`. No commit, push, deployment or live-client test was made.

### Verified local changes and checks

- Fixed the two weather lint findings in its owning site repository, then
  copied those files to the ignored installed module. The rendering change
  writes the same precipitation text directly; the test helper stops returning
  an unused database handle. The site changes need their own later review.
- Full-workspace golangci-lint 2.14.0 reports **zero issues**. `go test ./...`
  passes across **69 test packages**, including installed custom modules;
  `go vet ./...` passes. Go and its compiler both report **1.27.3**.
- Rebuilt and installed NilAway `v0.0.0-20260918162853-acb8859b9031` using
  Go 1.27.3, retaining a private backup of the previous executable. The local
  quality wrapper now uses the module's first-party package prefix, as
  [recommended upstream](https://github.com/uber-go/nilaway).
- Disposable fixtures verified a guarded normal case and detection of an
  actual nil dereference. A separate wrapper fixture verified successful
  execution, nonzero overall status on analyzer failure and continued test
  execution after that failure. No broad diagnostic suppression was added.

### Initial gates before findings repairs

- **NilAway:** the real quality wrapper still exits **1**. The diagnostic
  inventory contains 142 entries, or 92 distinct position/message pairs,
  including test variants. JSON diagnostic output returned status 0 despite
  findings; that status is not a clean analyzer result. Some reports involve
  custom return contracts, callbacks and test helpers, but classification of
  the full inventory is unfinished. No whole-project nil-safety pass is claimed.
- **Go dependencies:** fresh govulncheck 1.8.0 reports zero called or imported
  package vulnerabilities. [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)
  remains in a required module's unimported OpenPGP package. This does not
  establish source security or remove the independent audit requirement.
- **npm dependencies:** fresh audit still exits **1**, with seven moderate
  package entries and no high/critical entries, rooted in
  [GHSA-g3hv-75wg-m249](https://github.com/advisories/GHSA-g3hv-75wg-m249).
  The chain runs through Klaro's ESLint/parser dependencies; shipped Klaro
  browser JS/CSS exactly match its prebuilt distribution, and the asset
  pipeline copies those files without running that lint chain. The installed
  minimatch 3.1.5 lacks the advisory's described worklist implementation; a
  bounded check did not reproduce that mechanism. This is exposure evidence,
  not proof of general ReDoS safety or an upstream advisory withdrawal.
  No compatible patched legacy version was available in the registry check.
  No forced Klaro downgrade or incompatible minimatch override was applied.
  A safe fix or reviewed release disposition remains pending.
- **Independent audit:** Codex Security refused to create a scan because the
  `wiki` submodule is dirty. Its Desktop workflow prohibits silently starting
  a replacement after a failed launch. A core-only source snapshot is prepared;
  starting an audit on that new target is awaiting the owner's instruction.
  No scan ID, independent source-review result or security approval exists.

The dependency manifests and existing wiki changes are preserved. Sanitized
logs, analyzer inventories and fixture results are in ignored
`output/r17-validation/release-hardening/`. These are baseline quality and
targeted wrapper checks, not completion of R1.7 or release authorization.

### Findings closure follow-up — 2026-11-02

The NilAway and independent-audit status above records the initial baseline;
this follow-up supersedes those two pending entries.

- The core security audit is complete and its follow-up was verified.
- Resolved the first-party NilAway inventory across runtime code, tests and the
  installed weather module. The original 92 distinct reports included one in
  a third-party Go example under `node_modules`; the wrapper excludes that
  directory explicitly. First-party tests remain in scope, with no package or
  diagnostic suppression added for them.
- NilAway now exits **0**, with no diagnostics on macOS or Linux. The actual
  quality wrapper exits **0** in an isolated Linux copy: golangci-lint 2.14.0
  reports **zero issues**, and all **69 test packages** pass. `go vet ./...`
  also passes. Both Go and its compiler are 1.27.3.
- Added regression coverage for missing import data, absent mutation results
  and transaction rollback, null replay receipts, malformed consent URLs and
  unavailable dashboard settings. Existing translation, media, OAuth and MCP
  tests pass through the affected consumers.
- A fixture using the real NilAway executable verifies that a first-party nil
  dereference still makes the wrapper exit **1**, while Go tests continue.
  The guarded normal case passes, including with a broken third-party example
  present under the excluded directory. Only lint is stubbed in this fixture;
  the full quality run uses the real linter.
- Fixed a weather test setup race by waiting for startup warm-up before seeding
  fixtures. Its stale-cache regression passes 20 repetitions. Both this change
  and the weather form guard are in the owning site repository and installed
  copy; those site changes still require their own commit review.

Evidence is in ignored `output/r17-validation/nilaway-fixes/`. The core tests
and analyzers ran without network access, credentials or unrelated host files.
Dependency manifests and wiki edits are unchanged. The dependency dispositions
and other release items above are unchanged; this closes the reported code
findings and NilAway gate, not R1.7 as a whole. No commit, push, deployment or
live-client acceptance was performed during these repairs.


### R1.7 full multilingual acceptance follow-up — 2026-11-02

Before the alt-text rollout, [acceptance](../../docs/launch/r17-acceptance.md)
recorded the deployed
`39d82c94` image, fresh bounded web OAuth/share/revocation checks and full
EN/RU/DE draft fields, existing taxonomy/media and linked translations in
native Claude Desktop, ChatGPT and both CLI OAuth/bearer modes. The selected
component is pages 27/43/44, revisions 5/3/2. ChatGPT's update/replay/conflict,
Codex's exact source restoration, Claude Code's localized target captions
and native final readback passed. Both translations are current. A revision
4 test claim acknowledgment did not carry into revision 5.

The image path exposed silent punctuation-dependent inline alt loss on
staging. The local sanitizer fix retains escaped text, with failing-before
and passing-after supplied/library fallback and Markdown round-trip tests.
All 68 candidate test packages, golangci-lint and first-party NilAway pass.
The full publication/lifecycle selection (15 test roots) also passed on the
clean deployed commit; the record distinguishes HTTP, guarded-helper and
scheduler coverage from pending human staging publication.

All 18 new drafts remain private/unscheduled. The original 26 page identity,
revision and lifecycle fingerprints and ten owner grants are unchanged.
Only the revision 4 test
claim is checked; the current revision needs fresh review. Both temporary
90-minute bearer keys are inactive and subsequent initialization returned
401. Temporary remote fixture/inspection helpers were removed. Pre-existing
wiki edits were backed up before reconciling transport and release status.

Evidence: ignored `output/r17-validation/release-closure/`, including
`verification.json`, `cli-matrix.json`, `preservation-checks.json`, sanitized
GUI/CLI readbacks and the 40-second `r17-draft-workflow-partial.mp4`. The
video assembles actual screenshots; it is not continuous screen capture and
does not show publication. The original core snapshot is the test base;
the three modified Go files match the candidate's recorded SHA-256 hashes.
The author skill's media-readback instruction was manually traced against
these stored results: punctuation cases fail, while the plain-alt Codex
bearer fixtures pass. It does not treat an empty issues array as proof of
accessibility. Existing body resave/review is required after rollout.

No commit, push or further application deployment occurred in this run. The
[release decision record](../../docs/launch/release-decisions.md) proposes
scoped advisory dispositions and a documented Desktop Chrome handoff; none
is an accepted waiver. At that handoff, alt-fix rollout/readback, human
publication and the recording's final shot still required their
corresponding authorization. The subsequent alt-text rollout is below.

### R1.7 alt-text staging verification — 2026-11-02

After explicit authorization, `16ac71611e2e82e8a5789f0bf13145ef844301ef`
was deployed from a clean Git archive to `ocms-mcp-staging`. The host binary
reported build time `2026-11-02T19:40:19Z`; its image digest is
`[image digest redacted]`.
The existing Machine/volume were retained, a volume snapshot preceded the
rollout and all Fly/public health checks passed. MFA stayed required and
seeding disabled.

Claude web saved EN 27 once, revision 5 → 6. ChatGPT web saved RU 43 once,
3 → 4, and DE 44 once, 2 → 3. Both clients then freshly read all three
drafts under dedicated Editor user 2 and exactly `mcp:access mcp:drafts`.
The shared authenticator refreshed the expired staff browser session.
Exact EN/RU/DE inline alt text, punctuation and captions survived stored
HTML, derived Markdown and signed-in revision previews. Only inline alt
changed in the bodies; metadata and agent reports were preserved. Issues
and conversion warnings were empty. See the
[focused verification](../../docs/launch/r17-acceptance.md#alt-text-staging-verification).

The English body fingerprint changed, so both translations now report
`stale`; their existing source bases were preserved. All three are still
unscheduled drafts and need fresh human claim/content and translation
review. Read-only database checks preserved 44 pages, the published count,
all other page identity/revision/lifecycle fingerprints, ten owner grants
and preview links, with SQLite quick-check success. Both prior temporary
keys remain inactive and inspection files were removed. Wiki was untouched.

Evidence: ignored `output/r17-validation/alt-text-staging/`, including
deployment identity, before/after drafts, exact body comparisons, both
clients' final readbacks/screenshots, rendered previews and preservation
checks. Native Desktop/CLI modes, web revocation/reconnect and proxy-error
probes were not rerun; their earlier build-specific evidence remains above.
No new Git commit, push, publication or scheduling occurred in this rollout.
The advisory and Desktop workaround decisions, fresh editorial review,
human publication and final demo segment remain open; R1.7 is not closed.

### R1.7 local dependency disposition — 2026-11-02

The owner accepted GO-2026-5932 as not applicable to the reviewed local build:
retain `golang.org/x/crypto v0.58.0` for Argon2/bcrypt, with no OpenPGP
imports. Existing vulnerability checks and the module-only warning remain;
revalidate after import, dependency or build-target changes and before release.
The [decision record](../../docs/launch/release-decisions.md#dependency-advisory-dispositions)
binds this acceptance to the tested candidate. Klaro/minimatch is fixed
locally by the six-file import from master `a325de44`; staging rollout remains
pending. No commit, push or deployment occurred for this documentation update.

### R1.7 Klaro staging verification — 2026-11-02

After separate approval, `035ac1ddf9b3a9abb6e4289d8545ec246cea1ac3` was
deployed from a clean Git archive to `ocms-mcp-staging`, built at
`2026-11-02T22:25:49Z`, image
`[image digest redacted]`.
The existing Machine/volume were retained and recovery snapshot
`[snapshot ID redacted]` preceded rollout. All Fly/public health checks
and canonical OAuth discovery passed; MFA stayed required and seeding off.

Klaro JS/CSS and the new BSD license returned 200 with exact committed bytes.
The rendered script retained matching SRI, defer, nonce, anonymous crossorigin
and cache busting. A temporary Russian consent fixture passed notice reject,
required-service lock, optional opt-in/out persistence and footer reopen.
Both synthetic services and the separate cookie name were test settings;
the original disabled privacy row was restored exactly, and the temporary
SQLite verifier was removed. All 44 page rows, the published count of one,
ten owner grants and module settings matched pre/post-fixture fingerprints;
SQLite quick_check passed.

Claude web and ChatGPT web freshly read schema/context and EN 27/revision 6,
RU 43/revision 4 and DE 44/revision 3 as OAuth Editor user 2 with exactly
`mcp:access mcp:drafts`. Alt text/captions and empty warnings/issues were
preserved; RU/DE remained stale. Normal dedicated-account password/shared MFA
and the signed-in pinned preview passed. After final restoration/restart,
both clients freshly read site info and page 27 again; the final log buffer
independently corroborated those two successful calls per client.

The controlled proxy-spoof pair retained the same resolved client. A bounded
100-record log slice had no checked credential patterns; raw logs were not
saved. No new upstream-error/share probe ran. Linux/amd64 CGO-disabled package
analysis with Go 1.27.3 / govulncheck 1.9.0 returned zero imported-package
vulnerabilities, retaining the module-only OpenPGP warning. This is source
analysis for the staging target, not a deployed-binary scan or wider waiver.

Evidence: ignored `output/r17-validation/klaro-staging/`. The
[acceptance follow-up](../../docs/launch/r17-acceptance.md#klarominimatch-staging-verification--2026-11-02)
records exact identity, restoration and limits. Earlier native/CLI authoring,
revocation/reconnect, sharing and full Go tests were not rerun in this focused
web/read-only check. No content edits, publication, scheduling, new commit
or Git push occurred; wiki was unchanged. Desktop reconnect disposition,
human review/publication, the final demo shot and launch drafting remain open.

### R1.7 merged release staging verification — 2026-11-04

After explicit approval, `ba0444341691b567b3e128f59e7e4cb323125df8`
(tree `fd409ccfd133de7c8e5c0e5c4b0e8b3598f2eb20`) was archived, built and
deployed to `ocms-mcp-staging`. The host reported build time
`2026-11-04T17:17:32Z`, image
`[image digest redacted]`.
Snapshot `[snapshot ID redacted]` preceded prerequisites and rollout.
The existing Machine/volume were retained; all Fly/public health checks,
canonical OAuth discovery and anonymous MCP refusal passed.
Klaro JS/CSS/license delivery returned 200 and matched reviewed vendor bytes;
interactive consent and rendered SRI retain their earlier-build scope.

Preparation set the current Mac IPv4 `/32` API allowlist privately and
disabled Developer, Embed and Migrator modules, retaining the Developer
theme. The owner separately approved `OCMS_REQUIRE_FORM_CAPTCHA=false`
only for this staging app with zero public forms. No CAPTCHA keys were
configured; MFA stayed required, seeding/DCR off, and existing security
secret digests matched. The tracked Fly config still requires CAPTCHA;
the approved exception was an explicit deployment override. Startup applied
core search migration `20261023120000` and MCP migration 3.

Claude web and ChatGPT web used dedicated Editor user 2 with exactly
`mcp:access mcp:drafts`. Both passed fresh schema/context, translation and
demo readbacks; idempotent create/update replay; stale-revision refusal;
disabled-share-policy refusal; anonymous share rendering; and own-grant
revoke/refusal/normal password/shared-MFA/consent reconnect. Fresh reads
and backend events verified both final two-scope grants. The temporary
Claude access-only grant was revoked without disrupting its final grant.

Synthetic pages 45/46 ended as private, unscheduled revision-3 drafts.
Claude's revision-2 share stayed byte-identical after its draft advanced,
then explicit revocation returned 404 for both token and cookie access.
ChatGPT's share rendered revision 2; parent-grant revocation returned 404
for both, including after reconnect. Its separate post-update pin check
was not repeated. All three disposable shares are revoked and the original
enabled share policy was restored. Refresh tools plus a fresh ChatGPT chat
removed stale sharing wording without global permission changes.

All original 44 pages, 101 versions and 99 provenance rows matched hashes;
demo pages 27/43/44 stayed at revisions 6/4/3 with stale RU/DE bases intact.
Owner user/TOTP/recovery data and API-key tables matched their baselines.
OAuth runtime fingerprints changed alongside refresh activity; no byte-level
preservation claim is made for them. Database quick/FK checks passed.

Token transport stayed in redemption POST bodies. Neither test token
appeared in the captured 100-record Fly buffer; no fresh controlled
proxy-error probe ran. Temporary remote utilities and private token/cookie
and MFA-code files were removed. Native Desktop/CLI modes, new multilingual
creation and Klaro consent/SRI were not rerun on this SHA. This web pass
does not close native-only reconnect, editorial/stale-translation review,
human publication, the final demo segment or launch drafting.

Evidence: ignored `output/r17-validation/ba044434-staging-20261104/`,
especially `acceptance-summary.json`, `verification.txt`, client captures,
final readbacks and preservation receipts. The
[acceptance record](../../docs/launch/r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
retains the client-specific alt punctuation and preview-check limits.
No commit, push, publication or scheduling change occurred in this rollout;
wiki and `.claude/shared` remained untouched.
