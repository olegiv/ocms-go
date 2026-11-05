# R1 release decisions

Updated 2026-11-05. The OpenPGP disposition below is owner-approved for the
reviewed build, with the staging-target recheck recorded below; other
decisions retain their stated status. This record does not authorize
commit, push, deployment or publication. The
[acceptance record](r17-acceptance.md) binds results to the tested build.

## Latest staging verification

`be0e9cc3` is the latest verified deployment on `ocms-mcp-staging`
(2026-11-04). Normal dedicated Editor password/shared-MFA login and
translation review passed. RU 43/revision 6 and DE 44/revision 5 are
`current` against unchanged EN 27/revision 7, verified in the editor and
MCP; all remain unscheduled drafts. The
[rollout record](r17-acceptance.md#staging-translation-review-verification-on-be0e9cc3--2026-11-04)
includes exact image, preservation and cleanup evidence. This focused
rollout did not repeat client reconnect, dependency, sharing or proxy-error
checks; earlier results keep their tested builds.

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
are passed. Final owner editorial approval, human publication, the final
demo segment and article drafting remain open.

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
editor action resolved that state: RU 43/6 and DE 44/5 are now `current`
against EN 27/7. Old pinned snapshots remain unchanged. These focused
checks do not establish a whole-site accessibility pass or authorize
publication.

## Dependency advisory dispositions

| Advisory | Actual exposure and disposition | Decision status |
|---|---|---|
| GO-2026-5932 / `golang.org/x/crypto/openpgp` | Keep `golang.org/x/crypto v0.58.0` for Argon2 password hashing and bcrypt imports. oCMS does not import OpenPGP; the tested build has no vulnerable imported packages or reachable symbols. OpenPGP imports are outside this disposition. | Owner-approved not applicable; revalidated on `ba044434` for Linux/amd64, CGO disabled; future revalidation conditions remain |
| GHSA-g3hv-75wg-m249 / transitive `minimatch` | `035ac1dd` imports master fix `a325de44`: vendor the existing Klaro 0.7.22 browser JS/CSS and BSD license, removing its unused Babel/ESLint/minimatch npm chain. No major override or advisory suppression. | Resolved; `ba044434` npm audit reports zero findings and reviewed vendor bytes match |

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

The selected staging component is now EN 27/revision 7, RU 43/revision 6
and DE 44/revision 5. The authorized content review saved corrections and
claim acknowledgments at revisions 7/5/4. The `be0e9cc3` translation-review
action then made RU/DE current against English revision 7 without changing
article fields; previous acknowledgments remain in their original
revisions. Final owner editorial approval is still required. Re-read the
current revisions and their review state before any lifecycle action.
All remain unscheduled drafts.
Only the human editor performs the final Publish action after separate
publication approval. Capture the published page and translation state to
finish the demo. The current screenshot walkthrough ends before that action
and is labelled partial.

The production outline is prepared; creating or publishing a real article
on that production site needs its separate authoring request. No production
post, release tag, remote push or release announcement is authorized here.
