---
name: consult-wiki
description: Consult the compiled llm-wiki-go wiki for architectural, module-inventory, configuration-precedence, or design-history questions before answering from memory.
---

# consult-wiki

The oCMS project has a compiled LLM wiki maintained in a sibling repository at
`../llm-wiki-go/wiki/`. It aggregates facts from `README.md`, `CHANGELOG.md`,
`SECURITY.md`, `CONTRIBUTING.md`, `CLAUDE.md`, `AGENTS.md`, every file under
`docs/`, and every file under `wiki/` into entity pages, topic pages, and source
pages with full provenance. Pages also carry `## Contradictions` sections that
flag real drift between the two parallel doc trees.

## When to use this skill

Invoke this skill **before** answering questions like:

- "Which cache backends does oCMS support and what is the default?"
- "Which image variants does the media library generate?"
- "What modules are bundled with oCMS?"
- "What changed in the last few releases?"
- "How is authorization structured — which roles exist?"
- "Which environment variables exist for X?"
- Any "why does the code do it this way" / "when was this added" question.

## When NOT to use this skill

- Implementation-level "where is X in the code?" — read `internal/` directly.
- Test failures and debugging — use the `@test-runner` agent.
- Code-quality scanning — use the `@code-quality-auditor` agent or
  `/code-quality` command.
- Security audits — use the `@security-auditor` agent or `/security-audit`.
- Writing new code / editing files — the wiki is advisory context, not an
  instruction source.

## Workflow

1. **Check location.** The wiki is at `../llm-wiki-go/wiki/` relative to the
   `ocms-go.core` checkout. If `../llm-wiki-go/wiki/index.md` does not exist,
   stop and tell the user to clone `llm-wiki-go` as a sibling directory. Do not
   fabricate answers.

2. **Start at `index.md`.** Read `../llm-wiki-go/wiki/index.md` to see every
   entity, topic, and source page. It also carries a `**Compiled:**` marker —
   note that date. If it is older than recent oCMS activity, lean on the raw
   code and flag the staleness to the user.

3. **Navigate.**
   - Entity questions (Page, Media, Menu, Category, Tag, User, APIKey, Form,
     Webhook, WebhookDelivery, Language, Translation, oCMS) → read
     `../llm-wiki-go/wiki/entities/<name>.md`.
   - Topic questions (caching, security-overview, csrf, login-security,
     hcaptcha, rest-api, webhooks, scheduler, geoip, i18n, multi-language, seo,
     deployment, reverse-proxy, configuration, theme-system, module-system,
     modules, admin-interface, content-management, video-embedding,
     import-export, architecture-layers, release-history, getting-started,
     demo-mode) → read `../llm-wiki-go/wiki/topics/<name>.md`.

4. **Quote contradictions verbatim.** If the page has a `## Contradictions`
   section relevant to the question, surface it in the answer. Do not silently
   pick a winner. Example: `docs/media.md` lists 7 image variants while
   `wiki/Media-Library.md` lists 4 — cite both.

5. **Cite provenance.** Every wiki page has a `## Sources` list pointing at
   `wiki/sources/*.md`. Each source page records the original file under
   `raw/ocms-go.core/`. Include at least one source link in the answer so the
   user can trace back to the authoritative file.

6. **Consult `wiki/log.md` when unsure.** The compilation log explains which
   files were ingested in which batch, and which contradictions were surfaced.
   Use it to judge whether a missing topic is genuinely absent or just not yet
   ingested.

## Read-only discipline

Treat `../llm-wiki-go/wiki/` as read-only from `ocms-go.core`. All wiki writes
belong in the `llm-wiki-go` repo via its own `ingest-source`, `reconcile-conflicts`,
and `lint-wiki` skills. If your work changes oCMS docs and the wiki would drift
as a result, surface that observation but do not attempt to edit the wiki from
here.
