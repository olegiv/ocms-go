Review changes and prepare a commit message:

## Step 1: Code Quality Checks

Run `/code-quality` command first.

**If any warnings or errors are found:**
1. List all warnings/errors clearly
2. Ask the user: "Code quality issues found. Do you want to proceed with commit anyway?"
3. Wait for user confirmation before continuing
4. If user declines, stop and suggest fixes

**If no warnings:** Continue to Step 2.

## Step 2: Review Changes

1. Run `git status` to see all changed files
2. Run `git diff` to see the changes
3. Run `git log -5 --oneline` to see recent commit style

## Step 3: Prepare Commit Message

Analyze all changes and draft a commit message following these rules:
- Subject line format: `Brief description`
- Subject line max 50 characters
- Use imperative mood ("Add feature" not "Added feature")
- No period at end of subject
- Blank line between subject and body
- Body lines wrapped at 72 characters
- Explain *what* and *why*, not *how*
- Never include "Bump module version" in commit messages
- Never add AI attribution footers

## Step 4: Present for Approval

Present the draft commit message to the user for approval.

**IMPORTANT:** Do NOT create the commit yet - just prepare the message.
