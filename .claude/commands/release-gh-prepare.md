---
allowed-tools: ""
description: "Cut an ocms-go release: update CHANGELOG, commit, push, and push a v* tag to trigger the release workflow. Mandatory user approval of the version and tag push."
---

# release-gh-prepare

Prepare a new `ocms-go` release. Write release notes from commits since the
last tag, let the user pick the version, write the approved notes into
`CHANGELOG.md`, commit, push, and finally create and push a `v*` git tag.
Pushing the tag triggers `.github/workflows/release.yml`, which builds assets,
compiles release binaries, uploads archives, and creates the GitHub Release.

Optional argument: a version override like `0.21.0` or `v0.21.0`. If provided,
the version-selection question is skipped and the argument is used directly
after validation, while the content, commit, push, and tag gates still apply.

## Hard preconditions - abort if any fails

Before any edit or git action:

1. The current branch is `master`. If it is `main`, `dev`, a feature branch, or
   a detached HEAD, abort with a message explaining that releases come from
   `master` only.
2. `git status --porcelain` is empty.
3. The current branch is in sync with `origin/master`.
4. `CHANGELOG.md` exists at the repository root.
5. There is at least one commit on `master` since the last release tag.
6. The repository has an `origin` remote pointing to github.com.
7. `.github/workflows/release.yml` exists and is the tag-triggered release
   workflow.
8. The target tag does not already exist locally or on `origin`.

If any precondition fails, report which one and stop. Do not prompt for
recovery and do not attempt partial progress.

**Note on `[Unreleased]`:** This command does not require the `[Unreleased]`
section in `CHANGELOG.md` to be populated. It drafts release notes directly
from commit history. An empty `[Unreleased]` stub is valid between releases.

## Step 1 - read state

Capture:

- Latest released version: from the top compare-link row
  (`[X.Y.Z]: ...compare/vA.B.C...vX.Y.Z`) at the bottom of `CHANGELOG.md`, or
  from `git describe --tags --abbrev=0` as a fallback.
- Commits since the last tag: `git log <last-tag>..HEAD --oneline`. Follow up
  with `git show --stat <sha>` for ambiguous commits.
- GitHub owner/repo from the `origin` remote.
- Today's date in `YYYY-MM-DD` with `date +%Y-%m-%d`.

Categorize commits:

- **User-facing:** features, behavior changes, bugfixes, security fixes,
  dependency changes that affect the shipped binary, operator-facing docs.
- **Internal:** wiki submodule bumps, shared Claude submodule bumps,
  docs-index churn, slash-command symlinks, merge commits with no substantive
  delta of their own.

## Step 2 - write release notes

Produce a Keep-a-Changelog-style draft organized into `### Added`,
`### Changed`, `### Fixed`, `### Security`, `### Removed`, and
`### Breaking Changes` as warranted. Group with `#### Component` subheadings
where it matches the style of prior releases.

Rules:

- Focus on what changed and why it matters to users/operators.
- Reference security audit finding IDs when a commit closes one.
- Mention new config/env vars by name.
- Omit internal churn.
- Each code-level security fix should note if it ships with a drift test when
  the commit body or audit report says so.

## Step 3 - propose version

Offer 2-3 version options based on the draft:

- Major bump if there are breaking changes.
- Minor bump if there is new user-facing surface.
- Patch bump if the draft is only fixes, security, or small behavior changes.

Present the options and let the user pick, override with a version string, or
cancel. Never skip this step unless the user passed a version argument.

## Step 4 - infer release title

From the drafted notes, take the first bullet under `### Added`, or the first
bullet overall if there is no Added section. Strip formatting and trim to about
50 characters. Fallback: `vX.Y.Z`.

Let the user override the title in Step 5. The tag-triggered release workflow
uses the tag name as the GitHub Release title unless the workflow is changed.

## Step 5 - mandatory content-approval gate

Present:

- **Proposed version:** `vX.Y.Z` (previous: `vA.B.C`).
- **Proposed title:** `vX.Y.Z - <subtitle>`.
- **Release notes draft:** the exact notes block for `CHANGELOG.md`.
- **Release trigger:** pushing tag `vX.Y.Z` will run `.github/workflows/release.yml`
  and publish the release archives.
- **Ask:** "Accept, adjust the title, request edits to the notes, or cancel?"

Wait for the user. Never assume approval from silence.

## Step 6 - update CHANGELOG.md

Two edits to one file:

1. Insert a new section directly below the existing `[Unreleased]` stub:

   ```markdown
   ## [X.Y.Z] - YYYY-MM-DD

   <approved release notes>

   ```

2. Update the compare-link block at the bottom:
   - Rewrite `[Unreleased]: ...compare/vA.B.C...HEAD` to
     `[Unreleased]: ...compare/vX.Y.Z...HEAD`.
   - Insert `[X.Y.Z]: ...compare/vA.B.C...vX.Y.Z` directly below it.

Do not rename or delete `[Unreleased]`.

## Step 7 - commit gate

Show `git diff --stat CHANGELOG.md` and the first 30 plus last 10 lines of
`git diff CHANGELOG.md`.

Draft commit message:

```text
Cut vX.Y.Z

Add the X.Y.Z release notes to CHANGELOG and update the compare-link
block. The published binaries are built by the tag-triggered release
workflow.
```

Ask: "Should I proceed with this commit?" Wait for explicit approval.

When committing, use `git commit --no-verify` per the user-explicit-commit
convention.

## Step 8 - push master gate

After the commit succeeds, ask: "Should I push to origin/master?" Wait for
explicit approval. Do not push silently.

## Step 9 - tag gate and release trigger

After `origin/master` contains the CHANGELOG commit, capture the full SHA:

```bash
FULL_SHA=$(git rev-parse HEAD)
```

Verify again that the tag does not exist:

```bash
git tag -l vX.Y.Z
git ls-remote --tags origin refs/tags/vX.Y.Z
```

Ask: "Should I create and push tag vX.Y.Z to trigger the release workflow?"
Wait for explicit approval. This tag push is the release publication trigger
for `ocms-go`.

Then:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z" "$FULL_SHA"
git push origin vX.Y.Z
```

Do not create a GitHub Release manually. Do not use the GitHub CLI release
command. The tag push is the handoff to `.github/workflows/release.yml`.

## Step 10 - verify and report

Run:

```bash
git ls-remote --tags origin refs/tags/vX.Y.Z
gh run list --workflow release.yml --limit 5
```

If the run is visible, report its status and URL. Also report:

- CHANGELOG commit SHA on `master`.
- Pushed tag name.
- That the release workflow will publish `ocms-linux-amd64.tar.gz`,
  `ocms-darwin-arm64.tar.gz`, and `checksums.txt`.

## Error recovery

If the tag push fails after the CHANGELOG commit is pushed, do not reset or
force-push. Report the exact error and the commit SHA. Suggest retrying:

```bash
git push origin vX.Y.Z
```

If a local tag was created but not pushed and the user wants to cancel, delete
only the local tag:

```bash
git tag -d vX.Y.Z
```

## What this command does not do

- Change `.claude/shared` or any shared command template.
- Create a manual GitHub Release.
- Use the GitHub CLI release command.
- Publish without explicit approval to push the `v*` tag.
- Rename or delete the `[Unreleased]` stub.
- Update Makefile, package, or module version fields.
- Run tests, linters, or vulnerability scans; those belong in CI or separate
  commands.
