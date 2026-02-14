#!/usr/bin/env bash
# Install the claude alias into the user's shell rc file.
set -euo pipefail

ALIAS_FILE="$(pwd)/.claude/claude-alias.sh"
SOURCE_LINE="source \"${ALIAS_FILE}\""

shell_name="$(basename "${SHELL:-zsh}")"
case "$shell_name" in
  bash) RC_FILE="$HOME/.bashrc" ;;
  zsh)  RC_FILE="$HOME/.zshrc" ;;
  *)    RC_FILE="$HOME/.zshrc" ;;
esac

if grep -qF "$SOURCE_LINE" "$RC_FILE" 2>/dev/null; then
  echo "Already present in $RC_FILE -- nothing to do."
else
  printf '\n%s\n' "$SOURCE_LINE" >> "$RC_FILE"
  echo "Added to $RC_FILE. Open a new shell to activate."
fi
