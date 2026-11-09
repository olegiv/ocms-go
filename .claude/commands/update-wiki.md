---
description: "Publish local wiki changes to GitHub after approval of the commit and the push"
---

Publish local changes from the `wiki/` submodule to GitHub. Run these steps
from the core repository root. A wiki commit, a wiki push, and a core
commit each require separate explicit approval in the current conversation.

1. Inspect `git -C wiki status --short`. For uncommitted changes, show the
   exact files and proposed message, run the repository's snapshot gate on
   the candidate, and ask for approval. Once approved, stage only those files
   by name, verify the staged tree matches the tested tree, and use
   `git -C wiki commit -F <message-file>`. If the wiki
   already has the approved local commit, continue to publication review.

2. Record the exact wiki commit with `git -C wiki rev-parse HEAD` and read
   the destination with `git -C wiki ls-remote --exit-code origin refs/heads/master`.
   Check that the published commit is an ancestor of the candidate using
   `git -C wiki merge-base --is-ancestor <published-commit> <candidate>`.
   Stop if the remote revision cannot be determined, is missing locally,
   or has diverged. If the remote already has the candidate, skip the push.
   Otherwise show the outgoing commit list and changed files for the range,
   run the repository's publication gate, and ask for separate approval to
   push that candidate to `origin`'s `refs/heads/master`.

3. Once approved, recheck the candidate, working tree and remote tip. If any
   changed, stop and refresh the review. Set `wiki_commit` to the full
   approved commit ID and push that exact commit.

   ```bash
   git -C wiki push origin "${wiki_commit}:refs/heads/master"
   ```

   Use a normal fast-forward push with hooks enabled. Report a refusal and
   stop. Re-read `refs/heads/master` with `git ls-remote` and verify it equals
   the candidate before proceeding.

4. With the published wiki commit checked out and the wiki worktree clean,
   stage the core gitlink using `git add -- wiki`. Check that the staged
   gitlink equals the verified published commit. Run the snapshot gate on
   the core candidate, show the staged change and proposed message, and ask
   for separate approval before committing it. Report the published wiki
   commit and the state of the core gitlink.
