Review changes and prepare a commit message:

1. Run `git status` to see all changed files
2. Run `git diff` to see the changes
3. Run `git log -5 --oneline` to see recent commit style
4. Analyze all changes and draft a commit message following these rules:
   - Subject line format: `Brief description`
   - Subject line max 50 characters
   - Use imperative mood ("Add feature" not "Added feature")
   - No period at end of subject
   - Blank line between subject and body
   - Body lines wrapped at 72 characters
   - Explain *what* and *why*, not *how*
   - Never include "Bump module version" in commit messages
   - Never add AI attribution footers
5. Present the draft commit message to the user for approval
6. Do NOT create the commit yet - just prepare the message