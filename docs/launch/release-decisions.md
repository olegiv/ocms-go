# R1 release decisions

Updated 2026-11-09. The OpenPGP disposition below is owner-approved for the
reviewed build, with the staging-target recheck recorded below; other
decisions retain their stated status. This record does not authorize
commit, push, deployment or publication. The
[acceptance record](r17-acceptance.md) binds results to the tested build.

## Latest staging verification

`b58b7b51` is the latest verified deployment on `ocms-mcp-staging`
(2026-11-08). The [staging rollout](r17-acceptance.md#staging-rollout-of-b58b7b51)
covers readiness, proxy trust, OAuth/MFA, CSP, language switching and
preview-link issuance and revocation. The
[November 7 rollout](r17-acceptance.md#staging-rollout--2026-11-07)
records the earlier checks on `9799b3e6`.

On `6c611040` (2026-11-05), an ordinary status-only editor save preserved
the source HTML byte for byte and kept both translations Current. See the
[editor rollout](r17-acceptance.md#editor-preservation-staging-verification--2026-11-05).
Dedicated Editor login and translation review passed on the earlier
`be0e9cc3` build. Following explicit owner approval and manual
publication on 2026-11-05, final revisions are EN 27/9, RU 43/7 and DE 44/6,
all Published, with both translations Current. The
[publication record](r17-acceptance.md#human-staging-publication-and-final-demo--2026-11-05)
includes the English restore workaround and public readbacks. The earlier
[rollout record](r17-acceptance.md#staging-translation-review-verification-on-be0e9cc3--2026-11-04)
includes exact image, preservation and cleanup evidence. Earlier client reconnect,
dependency and sharing results remain tied to their tested builds.

On the preceding `ba044434` build, fresh Claude web and ChatGPT web
OAuth/MFA reconnect, draft replay/conflict, share-policy and
anonymous preview/revocation checks passed. Original content and demo
drafts 27/43/44 at revisions 6/4/3 were preserved; all test shares are
revoked. See the
[upgrade record](r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
for exact identity and client-specific limits. The subsequent
[final native/CLI follow-up](r17-acceptance.md#final-native-and-cli-verification--2026-11-04)
passed Desktop OAuth and both CLIs' OAuth/bearer authoring and credential
lifecycle checks on the same binary. Dependency revalidation found zero
vulnerable imported Go packages/reachable symbols and zero npm findings;
the approved OpenPGP module-only warning remains. Interactive Klaro
consent/SRI and the earlier full EN/RU/DE matrix retain their build scope.

The owner explicitly approved `OCMS_REQUIRE_FORM_CAPTCHA=false` only for
this staging app with zero public forms. MFA remains required. No CAPTCHA
keys were configured and the tracked profile's secure default was not
changed; future redeploys need the explicit approved exception or a real
verifier. This is not a production CAPTCHA exception or a publication
approval. Native-initiated Desktop reconnect and staging translation review
are passed. Owner approval, manual staging publication and the final demo
walkthrough are complete. The editor preservation fix passed staging
verification on `6c611040`.

## Inline-image accessibility

Resolved on staging `16ac7161` after the separately authorized deployment
and resaves. Claude web saved EN 27/revision 6; ChatGPT web saved RU
43/revision 4 and DE 44/revision 3. Both clients freshly read all three
drafts: exact localized descriptions survived in HTML and Markdown, and
signed-in revision previews retained their rendered alt values. Captions
and other content metadata were preserved. See the
[verification record](r17-acceptance.md#alt-text-staging-verification).

Those resaves made RU/DE `stale` after the English body changed; their
source bases were left intact. The later content review and `be0e9cc3`
editor action resolved that state at revisions RU 43/6 and DE 44/5
against EN 27/7. The later manual publication retained Current status at
final revisions 9/7/6. Old pinned snapshots remain unchanged. These focused
checks do not establish a whole-site accessibility pass or authorize
publication.

## Dependency advisory dispositions

| Advisory | Actual exposure and disposition | Decision status |
|---|---|---|
| GO-2026-5932 / `golang.org/x/crypto/openpgp` | Keep `golang.org/x/crypto v0.58.0` for Argon2 password hashing and bcrypt imports. oCMS does not import OpenPGP; the tested build has no vulnerable imported packages or reachable symbols. OpenPGP imports are outside this disposition. | Owner-approved not applicable; revalidated on `ba044434` for Linux/amd64, CGO disabled; future revalidation conditions remain |
| GHSA-g3hv-75wg-m249 / transitive `minimatch` | `035ac1dd` imports master fix `a325de44`: vendor the existing Klaro 0.7.22 browser JS/CSS and BSD license, removing its unused Babel/ESLint/minimatch npm chain. No major override or advisory suppression. | Resolved; `ba044434` npm audit reports zero findings and reviewed vendor bytes match |
| CVE-2026-122132 / `github.com/go-chi/chi/v5` | The advisory applies to `v5.5.0`. oCMS does not register `RedirectSlashes`, and that middleware is absent from the checked Linux/amd64 binary. | Owner-approved pin at `v5.5.0` pending a fixed release. See [Chi open redirect](#chi-open-redirect-cve-2026-122132). |

### Chi open redirect (CVE-2026-122132)

Mend flagged `github.com/go-chi/chi/v5 v5.5.0` for an open redirect in
`RedirectSlashes`. A request path with an encoded tab can cause the
middleware to send a redirect that a browser resolves to an external host.
The Go vulnerability database did not list this CVE for Chi on 2026-11-09,
so the clean `govulncheck` result did not cover it. See the
[CVE advisory](https://www.vulncheck.com/advisories/go-chi-chi-5.2.2-through-5.5.0-open-redirect-via-redirectslashes-middleware).

The server does not register `RedirectSlashes`. `go version -m` confirmed
Chi `v5.5.0` in the Linux/amd64 build with CGO disabled, and `go tool nm`
found no `RedirectSlashes` symbol. The module version is affected, but the
vulnerable middleware is unreachable in the checked build. As of
2026-11-09, `v5.5.0` was the latest stable release and the
[upstream fix](https://github.com/go-chi/chi/pull/1238) was still open. Keep
the pin until a fixed release is available, then update Chi and recheck
the release build. Reassess sooner if the server starts using this
middleware.

### Historical OpenPGP and minimatch checks

The owner approved retaining the required crypto module and recording this
advisory as not applicable to the reviewed local build. The scope is the
Linux ARM64 R1.7 candidate based on `fad98d1a`, with the six-file Klaro import
from master `a325de44` and unchanged Go dependencies. The fresh Go 1.27.3 /
govulncheck 1.9.0 package scan returned zero imported-package vulnerabilities
and retained the module-only warning. oCMS imports `x/crypto/argon2` in
`internal/auth/password.go` and `x/crypto/bcrypt` in
`internal/transfer/importer.go`, with no OpenPGP imports in the tested build.

The subsequent `035ac1dd` rollout rechecked source packages for the staging
target, Linux/amd64 with CGO disabled, using Go 1.27.3 / govulncheck 1.9.0.
It again reported zero imported-package vulnerabilities and retained the
module-only warning. This is source package analysis, not a scan of the
deployed binary or a broader waiver. See the
[staging follow-up](r17-acceptance.md#klarominimatch-staging-verification--2026-11-02).

The final `ba044434` revalidation used Go 1.27.3 / govulncheck 1.9.0
source/symbol analysis on its exact clean archive for Linux/amd64 with CGO
disabled. There were zero vulnerable imported packages or reachable symbols;
GO-2026-5932 remained module-only. npm audit including dev dependencies
reported zero findings, and Klaro JS/CSS/license bytes matched `035ac1dd`.
No new exception, runtime dependency change, binary scan or container OS
scan was part of this follow-up. See the
[final record](r17-acceptance.md#final-native-and-cli-verification--2026-11-04).

OpenPGP remains unsafe and unmaintained. Its advisory remains visible in
dependency reports; no scanner exclusion or CI bypass is part of this
decision. Keep the existing local, CI and release vulnerability checks.
Revalidate the disposition when imports, dependency versions, build tags
or target platforms change, and against the release candidate before a tag.
Any proposed OpenPGP use needs a new assessment. See the
[Go vulnerability record](https://pkg.go.dev/vuln/GO-2026-5932).

The R1.7 baseline audit reported seven moderate entries in Klaro's unused
tooling chain. Applying the six-file master fix removes 136 package entries,
including Klaro's npm package, ESLint and minimatch, without changing any
retained package version or integrity value. The dependency removal
supersedes the proposed minimatch waiver. The
[advisory](https://github.com/advisories/GHSA-g3hv-75wg-m249) itself remains.

The vendored browser files preserve the existing asset URLs, consent API,
SRI and cache busting. The BSD license is included in the generated assets
and embedded binary. Exact upstream hashes, source integrity and the manual
update process are recorded in the
[vendor provenance](../../web/static/vendor/klaro/README.md). npm audit no
longer tracks this browser library; future Klaro updates and advisories need
explicit review.

The earlier `035ac1dd` rollout passed asset/SRI/license delivery, the
temporary Russian consent fixture and focused Claude/ChatGPT read-only
checks. Original privacy settings were restored exactly;
privacy consent remains disabled as before the fixture. The dependency-fix
rollout is complete; the remaining release gates are below. Revalidate this asset
assessment when vendor bytes, the lockfile, copy/build paths or relevant
upstream advisories change.
The accepted OpenPGP disposition remains limited to the reviewed build
and the revalidation conditions above. Run dependency scans against the
candidate before a release tag. No broad dependency waiver is accepted.

## Native Desktop reconnect

**Passed on `ba044434` with Desktop 4.16336.0 (2026-11-04).** Native Connect
opened Chrome's normal **Finish connecting a connector?** confirmation,
identifying `claude-desktop-chat` and a Desktop callback. Normal password,
shared MFA and base/draft consent restored the intended Editor and exactly
`mcp:access mcp:drafts`. Fresh native reads passed after the temporary
access-only grant was revoked. No web-settings workaround or oCMS fix was
needed; the browser is the normal external OAuth handoff.

Native-initiated reconnect had already passed on `035ac1dd` with Desktop
4.4212.0 on 2026-11-03. Earlier notes retaining the pending/workaround label
were stale. The cause of the original failed attempt is unestablished;
it is not a current release blocker. Both build-specific records are
retained in the [acceptance record](r17-acceptance.md#final-native-and-cli-verification--2026-11-04).

## Human publication and launch material

Completed on 2026-11-05. The owner approved EN 27/7, RU 43/6 and DE 44/5
and personally confirmed publication and the English version restore.
Final published revisions are EN 27/9, RU 43/7 and DE 44/6, with both
translations Current. English revision 9 restores the exact approved
revision 7 HTML after TinyMCE normalization during publication falsely
made translations stale. Localized wording, alt text and content metadata
were preserved. The
[publication record](r17-acceptance.md#human-staging-publication-and-final-demo--2026-11-05)
records the sequence, public/editor/MCP readbacks and cleanup.

The final 31-second video is a screenshot walkthrough of actual published
staging views, not continuous footage of the human clicks. The original
40-second draft walkthrough remains historical. Together with the recorded
acceptance they cover the final demo segment without claiming a continuous
end-to-end film.

The article is saved as an unscheduled production draft. The API
readback verified the article, metadata, taxonomy and inline workflow
illustration. The public URL and both anonymous API routes returned HTTP 404.
The draft was absent from published listings, RSS and Atom. Browser preview
awaits staff sign-in. The
[local HTML](article-draft.html) and [metadata](article-draft.json) record
the source. Production publication awaits editorial approval.

## Editor publication follow-up

The HTML preservation fix passed on staging `6c611040`. The normal editor
form published EN 57 from revision 4 to 5, kept all 644 stored HTML bytes,
and retained both translation bases. The public page returned HTTP 200
with its image alt and caption. Returning the source to Draft at revision
6 restored anonymous HTTP 404 for the fixture. The real browser baseline
failure and fixed edit/retry/conflict cases are recorded in the
[local verification](r17-acceptance.md#local-editor-body-preservation--2026-11-05).
The server still applies the normal body policy before preserving bytes
that differ only by form newline conversion. Freshness, publication
validation and revision checks still apply. See the
[staging verification](r17-acceptance.md#editor-preservation-staging-verification--2026-11-05).

## Release candidate checks

The clean `6c611040` archive matched the source used for the staging image.
`make assets` ran with Node 24, Sass 1.105.1 and templ 0.3.1070. The
generated-source comparison found zero changes outside `web/static/dist`.
Go vet and 68 release-archive test packages passed. Application lint and
first-party NilAway reported zero issues. npm audit reported zero findings.

Go 1.27.3 and govulncheck 1.9.0 revalidated the exact archive on
Linux/amd64, Linux/arm64 and Darwin/arm64 with CGO disabled. Each
source/symbol scan retained GO-2026-5932 at module scope
only, with zero vulnerable imported packages or reachable symbols. The
release binary's import graph excluded `golang.org/x/crypto/openpgp` on
all three targets. The approved scoped disposition and revalidation
conditions above apply. Logs and hashes are under ignored
`output/r17-validation/release-stop-20261105/`.

Release version, integration and tag decisions await the owner's sign-off.
