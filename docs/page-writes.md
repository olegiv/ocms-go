# Atomic page writes and revisions

R1.2 provides the storage and editor foundations for AI authoring. It does not
add MCP page-writing tools: those remain scheduled for R1.5, after draft
validation and authoring context are implemented.

## Write boundary

`internal/pagewrite.InTx` saves content, taxonomy, aliases, translation links,
AI-access flags, revision, complete version and provenance in one SQLite
transaction. The v2 service also saves an optional idempotency result there.
`NewTx` joins an existing `*sql.Tx` and rejects autocommit queries. Its owner
must call `Finish` before commit. Legacy query-based import and rollback code
uses `InQueriesTx`, which creates a savepoint when joining an outer transaction.
A failed entity is rolled back even if a bulk caller continues.

Track each existing page before changing it; register inserts with `Created`.
`Finish` compares complete state, including relationships. It advances CAS once
per changed page, records the committed snapshot and provenance, and skips
content no-ops. New pages start at revision 1. Webhooks, audit events and cache
invalidation run after commit. Replays do not repeat mutation side effects.

## REST compatibility and editor conflicts

REST v2 page responses include `revision`. `PUT /api/v2/pages/{id}` accepts
`base_revision`:

| Page and input | Result |
|---|---|
| Ordinary page, revision omitted | Legacy unconditional update retained |
| AI-editable page, revision omitted | HTTP 409, `revision_required` |
| Any page, stale revision supplied | HTTP 409, `revision_conflict` |
| Matching revision | Save atomically; a no-op retains the revision |

Conflict details contain `current_revision` and changed field names, never
intervening content. The normal authorization check runs before these details.
The existing `{ "error": { "code", "message", "details" } }` envelope is
preserved through Huma's schema-link transformer.

Admin edit, restore, translation creation, publish and scheduling actions submit
the displayed revision. A stale editor retains its unsaved inputs and offers
**View latest** or **Confirm overwrite**. Confirmation submits a new CAS against
the revision displayed on the conflict form; another intervening edit conflicts
again. The owner or an administrator can toggle **Allow AI edits**. That toggle
also advances the page revision when its value changes.

## Draft service contract

Trusted adapters may call the v2 service with `WriteOptions{ForceDraft: true}`.
Such calls require an app credential, `mcp:access`, `mcp:drafts`, a UUID request
ID and a transaction-bound authorization callback. Current account, role,
credential, ownership, opt-in and lifecycle are checked inside the transaction;
the callback supplies module and source-policy checks. A missing callback fails
closed. API keys act for their creator; OAuth acts for the consenting user.

Agent updates accept only that user's AI-editable plain drafts. An admin app
has no exception for another user's drafts. Explicit status, `published_at`
or `scheduled_at` input, including explicit JSON null, is rejected. Successful
agent saves force draft status and clear both lifecycle timestamps. Shared
validation covers title, enums, routes/aliases, active language, sanitized HTML
and the existing featured-image minimum. Later iterations add the PageDraft
schema, reference validation, normalization and issue catalog.

## History, restores and provenance

Migration `20261101150000_page_write_foundations.sql` gives existing pages a
complete revision-1 snapshot of their current state. Older title/body-only
versions remain visibly **legacy**, with no invented revision or snapshot.
All new revisions have one unique provenance row. Authenticated principal
fields are stored separately from server validation JSON and agent report JSON.
Human and system edits start with empty evidence; they do not inherit claims.
Registered OAuth client IDs identify registrations, not verified vendors.

Restoring a complete version recovers recorded content, taxonomy, aliases and
media assignments. It retains current author, AI access, publication/scheduling
and translation membership. A legacy restore changes only title and body.
Restore uses a new revision, checks references and route availability, and rolls
back if a recorded dependency is unavailable. Language changes also track
affected translation siblings. Linking components refuses duplicate languages
without disclosing a conflicting sibling ID.

Before applying the migration to any real site, take a consistent database
backup and verify the upgrade on a disposable copy. Development verification
uses disposable migrated databases; no production or staging database was
migrated as part of this local R1.2 work.

## Idempotency

The key is `(user_id, operation, request_id)`. Canonical typed input is hashed
before mutable defaults; object property order is irrelevant, while omitted
and null inputs remain distinct. Target, base revision and evidence participate
in the hash. Fresh authorization and entity visibility precede replay; replay
precedes current-content validation and CAS so a lost response can be retried.

Successful non-secret results are retained for seven days. The scheduler
compacts expired payloads; request/hash/entity tombstones remain permanently.
Page deletion compacts its payloads in the deletion transaction. A reused key
with different input returns `idempotency_key_reused`; an expired or deleted
result returns `request_expired` and never performs another mutation. Deleted
users do not remove tombstones. Preview bearer tokens and credentials cannot
be stored in replay results. SQLite busy/locked failures return
`write_retryable`; retry using the same request ID.

## Writer inventory and direct verification

The inventory includes relationship and foreign-key effects, not just content
updates. `TestPageWritersUseTransactionQueries` follows transaction query
receivers and closures across `internal`, `modules` and `custom`, and scans
literal SQL mutations. It supplements these behavioral fixtures; it is not a
general proof of dynamically constructed SQL or arbitrary external modules.

| Writer | Boundary and revision coverage | Direct checks |
|---|---|---|
| v2 create/update, trusted agent options | Shared transaction; initial taxonomy, flag, translation, history, provenance, retry result | `internal/api/v2/pages/write_test.go`: lost responses, duplicates, ownership/role/key/policy changes, rollback, lifecycle fields, compatibility, no-op and webhook count |
| Admin create/edit, dates, taxonomy, aliases, AI toggle | `createPageGuarded` / `writePageGuarded` callbacks use `Tx.Q`; displayed CAS | Actual local HTTP saves and AI toggle; two-browser conflict and confirmed overwrite; existing handler tests |
| Admin translate | Source CAS, target insert, guarded namespace and component link in one transaction | Shared translation fixtures; local HTTP acceptance; existing route tests |
| Admin publish/schedule | Displayed CAS plus existing lifecycle predicates | `TestTogglePublishAppliesOnlyToTheStatusItRead`, local HTTP; form scheduling goes through the same editor save |
| Admin restore | Shared transaction; complete or legacy content; current policy retained | `TestRestoreCompleteAndLegacyVersionsPreservesCurrentPolicy`, local HTTP |
| Admin and REST delete/bulk delete | Shared deletion, survivor component tracking, replay compaction | Existing delete/bulk fixtures; `TestExpiredAndDeletedRequestsNeverWriteAgain` |
| Taxonomy delete/bulk delete, REST and rollback | Track assignments, unlink, delete term, finish once per affected page | `TestTaxonomyDeletionRevisionsAssignmentsAtomically`: tag/category normal and provenance-failure rollback; existing taxonomy/bulk tests |
| Scheduled publish | Fresh transactional page, narrow lifecycle update and system provenance | `internal/scheduler/webhooks_test.go`: publication revision/snapshot/provenance and event counts |
| Media deletion and FK image clearing | Existing media transaction joins shared tracking before deletion | `internal/service/media_test.go`: both image fields cleared in one revision; cancellation/failure retains rows and files |
| Language rename | Existing language transaction tracks all affected pages/components | `internal/handler/languages_test.go`: propagation, revision and snapshot; deletion still refuses referenced languages |
| oCMS JSON/ZIP import | One enclosing transaction; existing pages/components captured before mutation; finish before commit | `TestImportOverwriteRequiresCapturedAIRevision`, complete import/language/rollback suite |
| PHP-Nuke import | Shared insert with initial taxonomy for stories, static pages and encyclopedia entries | Source importer tests verify complete revision 1 for each kind; association failure leaves no page |
| Elefant import | Shared post/page insert; initial tags/publication; separate alias operations are revisioned | Safe legacy-alias fixture verifies revision 2, complete snapshot and provenance; import/rollback suite |
| Drupal import | Shared insert/taxonomy; each later alias operation is revisioned | Redundant-alias fixture verifies revision 2; multilingual route/menu tests; failure/rollback suite |
| Developer generator and cleanup | Shared insert with initial taxonomy/publication; target/link atomic; tracked deletion/media cleanup | Normal and multilingual generation verify all complete revisions; cleanup failure retains files, variants and tracking |
| Migrator rollback | Savepoint adapters for page, alias, translation, term and media deletion | `TestJoinedWriteFailureDoesNotLeakIntoOuterCommit` and existing migration dependency/rollback fixtures |
| Fixed demo seed | Storage-layer exception to avoid a package cycle: one startup transaction, complete initial versions and provenance; skips existing pages | `TestSeedDemo`, `TestSeedDemoSnapshotFailureRollsBackDatabase`; no updates to existing pages |

No production caller uses unconditional `PublishPage`; importers/generators set
publication fields on the initial insert. The publication drift test now
forbids that query everywhere. Direct store queries remain available for
fixtures/generated storage, but runtime page mutations must use this boundary.

## Local validation record

Evidence is retained locally under `output/r12-validation/` (ignored by Git).
The source manifest binds the checked runtime files and fixture binary to the
working tree based on `f92e57161790d063ae127fd2e4d70dcfa698f8da`.

Checks include the full offline Go suite, focused race tests, vet, Staticcheck,
Errcheck, Govulncheck, generated assets and a rebuilt loopback-only fixture.
Regression probes temporarily removed the writer boundary, provenance row,
evidence separation, a Russian message and publication guard. Each probe
failed at its intended check; restoring the source passed. Actor-mapping and
domain-to-handler dependency probes also passed their failure/restoration checks.

The whole-checkout GolangCI run retains five pre-existing findings in account
layout/MFA tests and the local weather module; no new finding is accepted.
Govulncheck reports no reachable vulnerability, with one uncalled module
advisory. This is local verification, not staging acceptance of AI authoring.
New MCP content tools and their real-client acceptance remain R1.5 work.


Final local results on 2026-11-01: **59 test packages passed**, including
7489 passing test/subtest results, with no failures. Focused race checks
passed for pagewrite, idempotency and v2 pages, including two distinct apps
racing on one user's draft. Vet, Staticcheck and Errcheck passed. Scoped v2
page lint has zero findings; whole-checkout GolangCI has the five unchanged
baseline findings above. Assets, build, absolute-path check and diff whitespace
checks passed. The rebuilt binary passed **23 live HTTP checks**; the two-editor
browser flow refused a stale save, preserved input and committed the confirmed
overwrite as exactly one new complete revision. All temporary fixture API keys
are inactive. The loopback fixture remains running.

Tested source SHA-256: `98a70310f78e07fe41dba453c5ff0b2f108c506880d7ff115f5aac6304341d3d`.
The matching manifest and machine-readable results are
`output/r12-validation/source-manifest.json` and
`output/r12-validation/verification.json`. These local artifacts are ignored by
Git and contain no test bearer credentials. No staging deployment or new
real-client authoring claim is included in this result.
