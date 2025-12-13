Create a git commit with the prepared commit message:

1. Verify there are changes to commit by running `git status`
2. If no commit message was prepared in the conversation, remind the user to run `/prepare-commit` first
3. Add all changed files to staging: `git add .`
4. Create the commit using the message prepared by `/prepare-commit`
5. Use HEREDOC format to ensure proper formatting:
   ```
   git commit -m "$(cat <<'EOF'
   Subject line

   Body paragraph explaining what and why.

   - Bullet point if needed
   - Another point
   EOF
   )"
   ```
6. After successful commit, run `git status` to confirm
7. Do NOT ask the user if they want to push the changes
8. Do NOT push automatically – wait for explicit confirmation