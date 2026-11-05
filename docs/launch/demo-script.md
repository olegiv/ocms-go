# Multilingual authoring demo

Status: the full EN/RU/DE draft preparation ran on `39d82c94`; see the
[acceptance record](r17-acceptance.md). A partial screenshot walkthrough
captures real draft/review/preview states; it is not a continuous recording
of completed publication. The inline alt-text repair was verified on
`16ac7161`; Claude web and ChatGPT web verified the resaved EN 27/revision
6, RU 43/revision 4 and DE 44/revision 3 in HTML and Markdown, with matching
rendered preview alt text. Those resaves made RU/DE stale. The subsequent
content review and `be0e9cc3` staging rollout (2026-11-04) resolved that
state: RU 43/6 and DE 44/5 are `current` against unchanged EN 27/7, verified
in the editor and MCP after normal dedicated password/shared-MFA login.
All remained unscheduled drafts at that checkpoint; see the
[review record](r17-acceptance.md#staging-translation-review-verification-on-be0e9cc3--2026-11-04).
The owner subsequently approved and manually published the component on
2026-11-05: EN 27/9, RU 43/7 and DE 44/6 are Published, both translations
Current. The English revision restores approved revision 7 HTML after an
editor normalization failure at revision 8. See the
[publication record](r17-acceptance.md#human-staging-publication-and-final-demo--2026-11-05).
No new client reconnect or sharing test ran in this focused publication pass.
The existing walkthrough predates this repair and retains its historical
limitations. Native-initiated Desktop reconnect passed on `ba044434`,
through normal browser confirmation, shared MFA/consent and Desktop callback;
see the [final native/CLI record](r17-acceptance.md#final-native-and-cli-verification--2026-11-04).
Preserve owner connections.

## Completed final segment

`output/r17-validation/publication-20261105/r17-publication-final.mp4`
contains a 31-second screenshot walkthrough of the actual verified final
views: published listing, English article, Russian, German and the editor's
Published/Current status. It is not continuous footage of Publish clicks.
Its six scene sources/hashes are in `demo-artifact.json`; full decode and
visual contact-sheet review passed. The earlier 40-second draft walkthrough
retains its historical limits. Do not present the two as one uninterrupted
session or claim every client was retested on `be0e9cc3`.

The subsequent ordinary editor save on `6c611040` published a disposable
fixture with byte-identical HTML and both translations Current. See the
[staging verification](r17-acceptance.md#editor-preservation-staging-verification--2026-11-05).

## Before recording

Record the exact reviewed build/image, client version and tier, browser/OS,
site settings and date. Obtain separate deployment authorization first.
Confirm active English, Russian and a third target language, existing source
pages, taxonomy and a local featured image. Enable draft authoring and grant
only `mcp:access mcp:drafts`; ordinary saves must not issue public shares.
Use the [author workflow](../skills/ocms-author/SKILL.md) and
[client checklist](../desktop-authoring.md).

## Recording sequence

1. Show Account → Connected apps, the app's scopes and registration metadata.
   Keep credentials, authenticator codes and share fragments out of capture.
2. In Claude Desktop, provide a brief and selected visible references. This
   staging run used published page 1/revision 1 and the dedicated user's
   opted-in draft 23/revision 4, both with the source role. Do not imply both
   are published or treat the empty probe as substantive factual evidence.
   Ask for an English draft with title, summary, body, SEO, existing taxonomy
   and an existing image. Read context/schema before saving; record the saved
   page ID, revision, issues, editor URL and session-preview URL.
3. Continue that same user's draft in ChatGPT. Re-read it, request a precise
   edit and save with the current revision. Repeat in Claude Code and Codex
   for the CLI rows; do not count earlier-build OAuth tests as authoring passes.
4. Create two linked translation drafts from the recorded source revision.
   Show language membership and freshness in the editor. Edit source text;
   show the translation becoming stale while the saved old preview stays pinned.
5. Show Verified / Validated / Reported separation, acknowledge a synthetic
   claim and compare recorded revisions. A newer revision must start with
   fresh review state.
6. Open the ordinary preview anonymously: expect denial. If the owner
   explicitly requests a disposable public share, issue it once, open the
   entire fragment URL in a separate anonymous browser and check the pinned
   snapshot. Revoke it and show 404 on the next redemption. Exclude the token
   from the recording and inspect fixed request targets plus proxy error logs.
7. Demonstrate a stale update conflict and an uncertain-response retry with
   the original request UUID. Confirm no duplicate content operation occurred.
8. Have the human editor review and publish the chosen synthetic article
   manually only after explicit publication approval. Show fresh revision CAS
   and translation review. Do not authorize publication merely by this script.
9. Revoke a dedicated disposable connection in Connected apps, verify the next
   MCP request fails, then reconnect through normal password/MFA/consent if
   that reconnect has been authorized. Never revoke owner connections.

## Evidence and cleanup

Record each client separately, including unavailable prompts/resources or
blocked account features. Keep failures and pending rows explicit. Revoke all
test shares and temporary keys, remove private code/token delivery files and
retain synthetic content unless its cleanup was requested. Record the
publication result and distinguish live footage from screenshot walkthroughs.
No commit, Git push,
deployment, public post or external message is authorized by this document.
