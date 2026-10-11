---
description: "Create the commit prepared by /commit-prepare"
---

Create a git commit with the commit message prepared by `/commit-prepare`:

1. Verify there are changes to commit by running `git status --short`.
2. If no commit message was prepared in this conversation, stop and remind the
   user to run `/commit-prepare` first.
3. Confirm the test gate from `/commit-prepare` is still valid: the tree it
   tested (`git write-tree` on the prepared content) must equal the tree you
   are about to commit. If files changed since, stop and ask the user to rerun
   `/commit-prepare`.
4. Stage exactly the files that the prepared message describes, by name:
   `git add -- <file>...`. Never run `git add .` or `git add -A`: untracked
   files such as `.audit/`, `.claude/settings.local.json`, local databases or
   build output must never enter a commit.
5. Create the commit using the prepared message. Use HEREDOC format to ensure
   proper formatting:
   ```
   git commit -m "$(cat <<'EOF'
   Subject line

   Body paragraph explaining what and why.

   - Bullet point if needed
   - Another point
   EOF
   )"
   ```
   Do not pass `--no-verify`; a hook that refuses the commit is reported to
   the user, not bypassed.
6. After a successful commit, run `git status --short` and `git log --oneline -1`
   to confirm.
7. Do NOT ask the user if they want to push the changes.
8. Do NOT push automatically; wait for explicit confirmation.
