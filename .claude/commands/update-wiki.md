---
description: "Publish local wiki changes to GitHub after approval of the commit and the push"
---

Publish local wiki changes to GitHub.

The wiki lives in the `wiki/` submodule (git@github.com:olegiv/ocms-go.wiki.git).
Committing and pushing the wiki are outward-facing actions: each one needs the
user's explicit approval in this conversation, given after they have seen what
it will publish.

Execute the following steps:

1. Show what changed in the wiki submodule:
   ```bash
   cd wiki && git status --short && git diff --stat
   ```
   If there are no changes, report that the wiki is already up to date and stop.

2. Draft a descriptive commit message based on the actual changes. Present the
   list of files that will be staged (modified, added, and deleted) together
   with the message, then ask: "Should I proceed with this wiki commit?" Wait
   for the user's approval. Remove any file the user does not want published
   from the list.

3. After approval, stage exactly the approved files by name and commit:
   ```bash
   cd wiki && git add -- <file>... && git commit -F <message-file>
   ```
   Write the message to a temporary file with the Write tool so no text is
   interpolated by the shell. Never run `git add -A` or `git add .`.

4. Ask separately: "Should I push the wiki commit to origin master?" Wait for
   the user's approval, then push:
   ```bash
   cd wiki && git push origin master
   ```
   Never pass `--no-verify` or `--force`. A refused push is reported to the
   user, not worked around.

5. Update the submodule pointer in the main repo:
   ```bash
   cd "$(git -C wiki rev-parse --show-superproject-working-tree)"
   git add wiki
   ```

6. Report what was published:
   - List changed/added/deleted files
   - Confirm push succeeded
   - Remind that the submodule pointer change is staged but not committed
