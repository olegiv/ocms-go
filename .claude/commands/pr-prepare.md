---
allowed-tools: ""
description: "Run a fresh code review on the branch diff before opening a PR"
---

Run a fresh code review on the branch diff before opening a PR. This is the local-side counterpart of Codex's PR review — it runs with no prior conversation context so it catches the kind of blast-radius issues the implementing session is primed to miss (fix invariants that don't generalize, helper-contract changes that aren't followed through at every call site, spec/runtime mismatches).

**When to use:** before running `gh pr create` on any non-trivial branch. Typos, README tweaks, and single-line fixes do not need this gate.

## Step 1: Establish the diff range

1. Confirm the current branch is not `master`/`main`.
2. Confirm the branch has commits ahead of `origin/master`:
   ```bash
   git log --oneline origin/master..HEAD | head -20
   ```
3. Produce the diff for the reviewer agent:
   ```bash
   git diff origin/master...HEAD --stat
   git diff origin/master...HEAD
   ```

## Step 2: Invoke the code-reviewer agent

Use the Task tool to launch `pr-review-toolkit:code-reviewer` with these instructions:

- **Focus**: the diff from `origin/master...HEAD` on the current branch.
- **Blast-radius emphasis**: for every helper whose signature, return shape, or error kinds changed, identify all callers in the repo and verify each caller handles the new contract. For every pattern-level fix (e.g., "add Security to write ops"), verify the invariant generalizes to every applicable location (e.g., read ops that also gate on auth).
- **Silent-failure emphasis**: any `err.Error()` passed to an API response, any `catch/if err != nil { return nil, v2.NewError(v2.ErrInternal, ...) }` wrapping that collapses finer-grained error kinds, any swallowed error in a loop.
- **Ask for P1 / P2 / P3 severity on each finding** and a recommended fix.

## Step 3: If silent-failure patterns are flagged, run a second focused pass

Launch `pr-review-toolkit:silent-failure-hunter` on the same diff. This agent specifically hunts for error-swallowing patterns — complementary to the general reviewer.

## Step 4: Surface findings to the user

For each finding:
- Quote the reviewer's summary
- Include file:line reference
- Show the proposed fix
- Ask the user: "Fix now, defer to a follow-up, or accept as-is?"

**Do NOT run `gh pr create` yet.**

## Step 5: Resolve or explicitly accept

- If the user says "fix now": implement the fix, re-run the full Go test suite, and return to Step 2 on the updated diff.
- If the user says "defer" or "accept": note each accepted finding in the PR description so the reviewer's context survives.

## Step 6: Hand off to `gh pr create`

Only once every finding is either fixed or explicitly accepted do you proceed to create the PR. Use the usual `gh pr create` flow with a Summary + Test plan in the body.

## Why this command exists

Codex reviews every PR from the GitHub side. Without `/pr-prepare`, Codex is the only fresh-context reviewer and any bug it catches has already shipped into PR history. With `/pr-prepare`, the same class of review happens locally first — Codex becomes a backstop instead of the primary signal.

Concrete instance from PR #127 that motivated this command: Codex caught three P2 findings that were direct consequences of my own audit-fix commit. I didn't walk the blast radius (every caller of a helper whose error contract I had just changed; every op that gates on auth, not just writes). A fresh-context reviewer pass on the diff would have surfaced all three before push.

## Related

- `.claude/commands/commit-prepare.md` — per-commit message review (lightweight, no AI review)
- `.claude/commands/commit-do.md` — execute commits
- `.claude/commands/code-quality.md` — static analysis pass
- `internal/api/v2/drift_test.go` — mechanical invariants that complement this process-level gate
