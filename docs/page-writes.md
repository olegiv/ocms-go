# Atomic page writes and revisions

R1.2 provides the storage and editor foundations for AI authoring. R1.5 now
consumes this boundary through [gated MCP draft tools](draft-authoring.md),
using shared validation and authoring context.

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

`TestPageWritersUseTransactionQueries` also checks tracking order across
`internal`, `modules`, `custom`, `cmd` and `scripts`. Its structural scan follows transaction
and query aliases and page IDs through the supported writer patterns. Tracking
in another transaction, on another page, or only in a conditional branch cannot
authorize a later write. Inserts must register their returned page with
`Created` before related writes or a successful return; `Insert` callbacks
receive an already registered page. Deferred/asynchronous writes and unsupported
mutation method values are refused by the scan.

Compound helpers use `RequireTracked` (or `RequireTrackedEntity` for mixed
translation kinds) to check their caller's precondition. These checks return
`ErrPageNotTracked` instead of taking a late snapshot. Import completion asserts
that every reported page is already tracked. This is a source drift check plus
runtime helper preconditions, not a replacement for the behavioral writer tests:
it covers the inventoried query methods and supported Go source shapes, rather
than arbitrary interprocedural data flow or custom SQL.

JSON/ZIP imports apply configured suspicious-HTML blocking to selected page
bodies before writes, and sanitize each body at persistence when enabled.
The editor and importer share the same storage normalization helper. Import
revisions, snapshots and provenance follow the normalized write; a refusal
or later transaction failure leaves no imported revision or removal notice.
Admin overwrite and optional `BaseRevisions` conflict checks are unchanged.

## REST compatibility and editor conflicts

REST v2 page responses include `revision`. `PUT /api/v2/pages/{id}` accepts
`base_revision`:

| Page and input | Result |
|---|---|
| Ordinary page, revision omitted | Legacy unconditional update retained |
| AI-editable page, revision omitted | HTTP 409, `revision_required` |
| Any page, stale revision supplied | HTTP 409, `revision_conflict` |
| Matching revision | Save atomically; a no-op retains the revision |

The account that acts for a REST or agent write is checked inside the
transaction through `v2.RequireEditor`: a deleted account is 401, an account
that lost the editor or admin role is 403, and a failed account read is a
server error, never a 401 (`TestPageWritesTellCredentialFailuresFromStorageFaults`).

Conflict details contain `current_revision` and changed field names, never
intervening content. The normal authorization check runs before these details.
The existing `{ "error": { "code", "message", "details" } }` envelope is
preserved through Huma's schema-link transformer.

Admin edit, restore, translation creation, publish and scheduling actions submit
the displayed revision. A stale editor retains its unsaved inputs and offers
**View latest** or **Confirm overwrite**. Confirmation submits a new CAS against
the revision displayed on the conflict form. Another intervening edit conflicts
again. If a temporary read error prevents loading the conflict form, the editor
keeps the submission on a recovery screen. **Retry with these edits** submits
the same values and revision for another check.

The guarded admin create and update helpers require a `pageFormWrite` context.
They reject a missing editor and recheck the account's current editor/admin
role inside the write transaction. Updates always require the displayed base
revision. Runtime callers use `formWrite` to include relationship changes and
publication validation in that same transaction; the call-site drift test
continues to enforce this even though the context argument is required.

The owner or an administrator can toggle **Allow AI edits**. That toggle
also advances the page revision when its value changes. A save that leaves it
as it was does not touch `page_ai_access`, so `set_by` and `set_at` keep naming
whoever last changed the setting. That user cannot be deleted while the row
names them (`ON DELETE RESTRICT`): the admin user list, single or bulk, refuses
with "This user still owns pages, media or other content", and the Migrator's
rollback keeps the user, exactly as for pages, versions, media, API keys,
webhooks and scheduled tasks. `TestEveryRestrictingUserForeignKeyIsCountedAsOwnedContent`
derives every restricting reference to `users` from the migrated schema and
requires `CountUserOwnedContent` to count it.

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
and the existing featured-image minimum. The PageDraft schema, reference
validation, normalization and issue catalog are in the
[draft contract](draft-contract.md).

## History, restores and provenance

Migration `20261101150000_page_write_foundations.sql` gives existing pages a
complete revision-1 snapshot of their current state. Older title/body-only
versions remain visibly **legacy**, with no invented revision or snapshot.
All new revisions have one unique provenance row. Authenticated principal
fields are stored separately from server validation JSON and agent report JSON.
Human and system edits start with empty evidence; they do not inherit claims.
Registered OAuth client IDs identify registrations, not verified vendors.

REST and MCP adapters derive provenance and preview issuer bindings through
`v2.Actor.Principal(transport)`. The adapter supplies the transport; the
credential selects the source and its IDs. API-key identity takes precedence,
as in write authorization and `ActingUserID`; OAuth fields are carried only
for an OAuth actor. The conversion does not authenticate an actor or replace
the existing permission and credential rechecks. A drift test keeps this
mapping centralized across v2, authoring and MCP adapters.

New provenance must name a known source: `human`, `api`, `oauth`, `system`
or `migration`. Human, API-key and OAuth writes require a positive user ID.
API provenance also requires a positive key ID; OAuth requires both grant and
client IDs. Credential fields cannot be mixed or attached to human, system
or migration provenance. System and migration writes may retain an initiating
user ID, or omit it. Empty sources and negative IDs are refused.

`provenance.Record` rejects inconsistent principals before storage, and the
page transaction rolls back. Database triggers enforce the same rules on
inserts and identity changes made through direct SQL. The migration preserves
existing history and claim checks without rewriting old attribution. Deleting
an editor who owns no content still clears their historical user reference
through `ON DELETE SET NULL`; credential IDs and evidence remain intact. This
consistency check does not replace authentication or credential rechecks.

History is not kept forever for AI drafts. The `core:ai_revision_cleanup`
scheduler job keeps the newest 20 revisions of each unpublished, unscheduled
page with AI access, plus any older revision still pinned by an active share
link, still the latest authoring or translation evidence, or recorded as a
reference by an AI draft's current evidence. A removed revision's version,
provenance and claim acknowledgments go together, and its retained replay
receipts become tombstones (`request_expired`). Other pages keep every
revision. Every column that stores a revision number is inventoried by
`TestEveryRevisionReferenceIsAccountedForByRetention`; a new one must be
handled by `pagewrite.PruneAIDraftRevisions` first. See
[Draft authoring](draft-authoring.md#revision-retention).

Restoring a complete version recovers recorded content, taxonomy, aliases and
media assignments. It retains current author, AI access, publication/scheduling
and translation membership. A legacy restore changes only title and body.
Restore checks saved references and route availability before recording a new
revision. If a saved tag, category, image or language is missing, the editor
identifies the missing reference and asks you to choose another version.
Language changes also track affected translation siblings. Linking components
refuses duplicate languages without disclosing a conflicting sibling ID.

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
updates. Three source scans supplement the behavioral fixtures below. None is a
general proof of dynamically constructed SQL or arbitrary external modules.

- `TestPageWritersUseTransactionQueries` (`internal/pagewrite/writer_drift_test.go`)
  follows transaction query receivers and closures across `internal`,
  `modules`, `custom`, `cmd` and `scripts`. It checks the transaction boundary
  and tracking order for known page-mutating queries. It also flags literal
  SQL mutations of page tables outside the language rename. The behavioral
  fixtures below cover committed revisions and provenance.
  The separate `TestPagePublicationUsesLifecycleGuards` check rejects
  unconditional `PublishPage` calls through `work.Q`.
- `TestPagePublicationUsesLifecycleGuards` (`cmd/ocms/publish_page_drift_test.go`)
  is the test that forbids the unconditional `PublishPage` query, in every git-tracked
  non-test Go file (outside a git checkout, every such file outside the directories the shared repository walker skips).
- `TestEveryPublicationPathIsInventoriedAndValidated`
  (`internal/pagewrite/publication_drift_test.go`) lists every runtime function
  that calls `CreatePage`, `UpdatePage`, `PublishUnpublishedPage`,
  `PublishScheduledPage`, `PublishPage` or `pagewrite.Insert`, and requires each
  to reach `review.ValidatePublication` or carry a written reason it cannot
  publish an AI draft. An unlisted function, a stale entry or a deleted call
  fails it. `TestPageFormWritersAlwaysCarryTheFormWrite` and
  `TestRESTPageMutationsOnlyRunInsideTheValidatingWrite` pin the two indirect
  routes: the admin writers always receive `formWrite`, and the REST `create`
  and `update` run only inside `Service.write`.

Publication is validated by the admin editor save (`formWrite`), the publish
toggle, a restore, the scheduler, the REST and agent write, and an import that
overwrites a page. Insert-only paths (importers, generators, the demo seed)
cannot publish an AI draft because a new page has no AI access or authoring
history.

| Writer | Boundary and revision coverage | Direct checks |
|---|---|---|
| v2 create/update, trusted agent options | Shared transaction; initial taxonomy, flag, translation, history, provenance, retry result | `internal/api/v2/pages/write_test.go`: lost responses, duplicates, ownership/role/key/policy changes, rollback, lifecycle fields, compatibility, no-op and webhook count |
| `/create-page` command and `scripts/codex/create-page` client | REST v2 create for local and remote sites. The client requires a private API key file and a confirmed HTTPS origin or local HTTP loopback origin. | `TestPageCreationToolsUseInventoriedBoundary`; client tests cover transport, authorization, successful and partial batch results |
| Admin create/edit, dates, taxonomy, aliases, AI toggle | `createPageGuarded` / `writePageGuarded` callbacks use `Tx.Q`; displayed CAS; `set_by` rewritten only when the AI setting changes | Actual local HTTP saves and AI toggle; two-browser conflict and confirmed overwrite; `TestUnchangedSaveKeepsAIAccessAttribution`; existing handler tests |
| Admin translate | Source CAS, target insert, guarded namespace and component link in one transaction | Shared translation fixtures; local HTTP acceptance; existing route tests |
| Admin publish/schedule | Displayed CAS plus existing lifecycle predicates | `TestTogglePublishAppliesOnlyToTheStatusItRead`, local HTTP; form scheduling goes through the same editor save |
| Admin restore | Shared transaction; complete or legacy content; current policy retained | `TestRestoreCompleteAndLegacyVersionsPreservesCurrentPolicy`, local HTTP |
| Admin and REST delete/bulk delete | Shared deletion, survivor component tracking, replay compaction | Existing delete/bulk fixtures; `TestExpiredAndDeletedRequestsNeverWriteAgain` |
| Taxonomy delete/bulk delete, REST and rollback | Track assignments, unlink, delete term, finish once per affected page | `TestTaxonomyDeletionRevisionsAssignmentsAtomically`: tag/category normal and provenance-failure rollback; existing taxonomy/bulk tests |
| Scheduled publish | Fresh transactional page, narrow lifecycle update and system provenance | `internal/scheduler/webhooks_test.go`: publication revision/snapshot/provenance and event counts |
| Media deletion and FK image clearing | Existing media transaction joins shared tracking before deletion; `MediaService.Delete` returns the pages it revisioned and the admin handler (single and bulk) and REST service run the after-commit page side effects: cache invalidation and the `page.updated` webhook, once per changed page | `internal/service/media_test.go`: both image fields cleared in one revision, changed pages returned with a file-cleanup error, none on rollback; `TestMediaDeleteRunsPageSideEffects` in `internal/handler` and `internal/api/v2/media`; `TestMediaDeleteCallersKeepTheChangedPages`, `TestChangedPagesListsOnlyPagesWithANewRevision` |
| Language rename | Existing language transaction tracks all affected pages/components | `internal/handler/languages_test.go`: propagation, revision and snapshot; deletion still refuses referenced languages |
| oCMS JSON/ZIP import | One enclosing transaction; existing pages/components captured before mutation (an overwrite is tracked as a system capture unless the caller supplies `BaseRevisions`, so an AI-editable page can be overwritten and gets the import's own revision and provenance); an overwritten page that ends published or scheduled is validated; finish before commit | `TestImportOverwritesAIEditablePage`, `TestImportOverwriteWithoutPrincipalIsRecordedAsImport`, `TestImportOverwriteHonorsAnExplicitBaseRevision`, `TestImportOverwriteValidatesPublication`, complete import/language/rollback suite |
| PHP-Nuke import | Shared insert with initial taxonomy for stories, static pages and encyclopedia entries | Source importer tests verify complete revision 1 for each kind; association failure leaves no page |
| Elefant import | Shared post/page insert; initial tags/publication; separate alias operations are revisioned | Safe legacy-alias fixture verifies revision 2, complete snapshot and provenance; import/rollback suite |
| Drupal import | Shared insert/taxonomy; each later alias operation is revisioned | Redundant-alias fixture verifies revision 2; multilingual route/menu tests; failure/rollback suite |
| Developer generator and cleanup | Shared insert with initial taxonomy/publication; target/link atomic; tracked deletion/media cleanup | Normal and multilingual generation verify all complete revisions; cleanup failure retains files, variants and tracking |
| Migrator rollback | Savepoint adapters for page, alias, translation, term and media deletion | `TestJoinedWriteFailureDoesNotLeakIntoOuterCommit` and existing migration dependency/rollback fixtures |
| Fixed demo seed | Storage-layer exception to avoid a package cycle: one startup transaction, complete initial versions and provenance; skips existing pages | `TestSeedDemo`, `TestSeedDemoSnapshotFailureRollsBackDatabase`; no updates to existing pages |

No production caller uses unconditional `PublishPage`; importers/generators set
publication fields on the initial insert. `TestPagePublicationUsesLifecycleGuards`
forbids that query in every git-tracked non-test Go file. Direct store queries remain available for
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
See the [R1.7 acceptance record](launch/r17-acceptance.md) for later real-client checks.


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

## R1.5 trusted adapter extension

The authoring adapter hashes original typed input before mutable defaults and
checks retained requests before normalization or CAS. A trusted mutation callback
rechecks credential/policy, user quota, references and translation fingerprints
inside the shared transaction. These callbacks are not request fields. Compact
agent receipts omit body HTML and resolve their original validation from the
immutable saved revision. Per-page evidence prevents translation membership
updates from copying target claims onto source/sibling revisions. Explicit
report/reference edits participate in CAS; identical evidence is a no-op.

Replay storage accepts only the approved page response type, including its
compact agent form. The same field definitions back REST responses and replay
receipts, preserving their JSON format. Arbitrary response objects, including
share links or wrappers with extra fields, cannot become replay receipts.
