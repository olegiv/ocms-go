# R1 release decisions

Prepared 2026-11-02. These are proposed dispositions, not accepted waivers
or authorization to commit, push, deploy or publish. The
[acceptance record](r17-acceptance.md) binds results to the tested build.

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

| Advisory | Actual exposure and proposal | Decision status |
|---|---|---|
| GO-2026-5932 / `golang.org/x/crypto/openpgp` | Required module contains an unmaintained OpenPGP package; oCMS does not import it. Keep the needed non-OpenPGP crypto dependency and prohibit adding OpenPGP imports. Accept this unimported-module finding only for the reviewed tree. | Proposed; owner decision pending |
| GHSA-g3hv-75wg-m249 / transitive `minimatch` | Installed 3.1.5 is reported under the advisory's broad `<11.0.1` range. The installed dependency is in Klaro's Babel/ESLint chain, which oCMS does not invoke when copying Klaro's prebuilt assets. Accept only this unused tooling path for the reviewed lockfile, pending a compatible upstream resolution. | Proposed; owner decision pending |

The Go advisory lists no known fixed OpenPGP version and directs users away
from the unmaintained package. A called/imported-symbol scan returned no
reachable vulnerabilities; that does not erase the required-module finding.
See the [Go vulnerability record](https://pkg.go.dev/vuln/GO-2026-5932).

The minimatch advisory describes quadratic worklist amplification in 10.2.6
at optimization level 2 or greater, while its published affected range also
includes 3.1.5. That installed implementation has none of the described
preprocessing/worklist symbols, and the bounded reproduction did not show
that mechanism. This is exposure evidence, not an upstream withdrawal or
proof that every ReDoS pattern is safe. See the
[GitHub advisory](https://github.com/advisories/GHSA-g3hv-75wg-m249).

The existing npm audit reports seven moderate affected package entries in
the same chain, with no high/critical entry. Registry metadata checked at
`2026-11-02T16:09:45Z` has minimatch 11.0.4 as latest and 3.1.5 as the latest
3.x release; Klaro's `latest` tag points to 0.7.21, older than installed
0.7.22. Neither a forced major minimatch override nor a Klaro downgrade is a
verified compatible repair. Klaro has no install lifecycle script; this
project copies its shipped JS/CSS, whose hashes match the bundled assets,
and does not run its Babel/ESLint tasks. npm's dependency classification
alone is not used to claim absence of runtime exposure. Primary metadata:
[minimatch registry](https://registry.npmjs.org/minimatch),
[Klaro registry](https://registry.npmjs.org/klaro).

Invalidate either proposed disposition when imports, the lockfile, Klaro
build/lint use, install scripts or shipped artifacts change, or an upstream
advisory/fix changes the exposure analysis. Run dependency scans against the
candidate tree before a release tag. No broad all-dependency waiver is
proposed, and no package manifest was changed in this acceptance run.

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
