# R1 release decisions

Updated 2026-11-04. The OpenPGP disposition below is owner-approved for the
reviewed build, with the staging-target recheck recorded below; other
decisions retain their stated status. This record does not authorize
commit, push, deployment or publication. The
[acceptance record](r17-acceptance.md) binds results to the tested build.

## Latest staging verification

`ba044434` is deployed and healthy on `ocms-mcp-staging`. Fresh Claude web
and ChatGPT web OAuth/MFA reconnect, draft replay/conflict, share-policy and
anonymous preview/revocation checks passed. Original content and demo
drafts 27/43/44 at revisions 6/4/3 were preserved; all test shares are
revoked. See the
[upgrade record](r17-acceptance.md#staging-upgrade-to-ba044434--2026-11-04)
for exact identity and client-specific limits. Native Desktop/CLI modes,
Klaro consent/SRI and dependency scans were not rerun during this rollout;
earlier evidence and advisory dispositions retain their recorded scope.

The owner explicitly approved `OCMS_REQUIRE_FORM_CAPTCHA=false` only for
this staging app with zero public forms. MFA remains required. No CAPTCHA
keys were configured and the tracked profile's secure default was not
changed; future redeploys need the explicit approved exception or a real
verifier. This is not a production CAPTCHA exception or a publication
approval. The existing native-only reconnect and human release gates below
remain open.

## Inline-image accessibility

Resolved on staging `16ac7161` after the separately authorized deployment
and resaves. Claude web saved EN 27/revision 6; ChatGPT web saved RU
43/revision 4 and DE 44/revision 3. Both clients freshly read all three
drafts: exact localized descriptions survived in HTML and Markdown, and
signed-in revision previews retained their rendered alt values. Captions
and other content metadata were preserved. See the
[verification record](r17-acceptance.md#alt-text-staging-verification).

Fresh editorial review is still required at these new revisions. RU/DE are
now `stale` after the English body changed; their source bases were left
intact. Old pinned snapshots remain unchanged. This focused check does not
establish a whole-site accessibility pass or authorize publication.

## Dependency advisory dispositions

| Advisory | Actual exposure and disposition | Decision status |
|---|---|---|
| GO-2026-5932 / `golang.org/x/crypto/openpgp` | Keep `golang.org/x/crypto v0.58.0` for Argon2 password hashing and bcrypt imports. oCMS does not import OpenPGP; the reviewed build has no affected imported packages. OpenPGP imports are outside this disposition. | Accepted by owner on 2026-11-02: not applicable to the reviewed local build; revalidation required |
| GHSA-g3hv-75wg-m249 / transitive `minimatch` | Deployed commit `035ac1dd` imports master fix `a325de44`: vendor the existing Klaro 0.7.22 browser JS/CSS and BSD license, removing its unused Babel/ESLint/minimatch npm chain. No major override or advisory suppression. | Resolved in `035ac1dd`; focused staging verification passed |

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

The advisory has no known fixed OpenPGP version. It remains visible in
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

Native Claude Desktop 4.4212.0 authored and freshly read the multilingual
demo. Earlier revocation correctly blocked access, but native Connect /
Reconnect did not open an observable sign-in. Normal Chrome web settings
and the shared chat's Connect flow completed password/MFA/consent; subsequent
native calls confirmed the new account/scopes and content. The cause is
unestablished and native-only reconnect is not a passed row.

Proposed release position: support the documented Chrome handoff and state
this client limitation explicitly. If native-only reconnect is mandatory,
keep release closure blocked on a reproducible client-side resolution and
fresh native verification. Do not remove MFA or expand scopes to bypass it.

## Human publication and launch material

The alt fix is deployed and verified. Review the selected staging component,
currently EN 27/revision 6, RU 43/revision 4 and DE 44/revision 3, including
fresh claim acknowledgments and stale translation review. Re-read current
revisions before any lifecycle action. All remain unscheduled drafts.
Only the human editor performs the final Publish action after separate
publication approval. Capture the published page and translation state to
finish the demo. The current screenshot walkthrough ends before that action
and is labelled partial.

The production outline is prepared; creating or publishing a real article
on that production site needs its separate authoring request. No production
post, release tag, remote push or release announcement is authorized here.
