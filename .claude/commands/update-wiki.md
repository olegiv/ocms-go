Publish local wiki changes to GitHub.

The wiki lives in the `wiki/` submodule (git@github.com:olegiv/ocms-go.wiki.git).

Execute the following steps:

1. Show what changed in the wiki submodule:
   ```bash
   cd wiki && git status && git diff --stat
   ```

2. If there are changes, stage and commit them:
   ```bash
   cd wiki && git add -A && git commit -m "Update wiki pages"
   ```
   Use a descriptive commit message based on the actual changes.

3. Push wiki changes to GitHub:
   ```bash
   cd wiki && git push origin master
   ```

4. Update the submodule pointer in the main repo:
   ```bash
   cd /Users/olegiv/Desktop/Projects/Go/ocms-go.core
   git add wiki
   ```

5. Report what was published:
   - List changed/added/deleted files
   - Confirm push succeeded
   - Remind that the submodule pointer change is staged but not committed

If there are no changes in the wiki submodule, report that the wiki is already up to date.
