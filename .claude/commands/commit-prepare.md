---
description: "Test the intended tree and prepare a commit draft"
argument-hint: "[quality|q] [-- FILE ...]"
---

Use `$ARGUMENTS` to select the candidate and optional quality check. Accept
only an exact `quality` or `q` as the first argument for quality checks. Without
either, use the normal test gate. Reject unknown options such as `--quiet`.
Use the staged tree unless the caller names individual repository-relative
paths after `--`. Pass those paths as separate, quoted arguments.

Review the intended diff and verify that the checkout and gate are trusted.
Run the matching form of the guarded helper from the repository root.

```bash
./scripts/codex/commit-prepare.sh
./scripts/codex/commit-prepare.sh quality
./scripts/codex/commit-prepare.sh -- 'path/to/file'
```

The helper builds and tests a fresh snapshot without changing the real index.
A gate or quality failure blocks preparation. Report the failure and stop.
On success, present the tested tree, file list and exact saved draft for
review. The helper saves a generic local draft. For a draft based on the
tested diff, use `./scripts/codex-commands commit-prepare` from a terminal
with the same arguments.

For the next step, direct the user to `/commit-do`. The terminal helper
requires the intended files to be staged and asks for approval of the
matching tree and draft.
