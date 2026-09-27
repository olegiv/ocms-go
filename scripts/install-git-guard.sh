#!/usr/bin/env bash
# Installs the DSH git-approval gate for this machine:
#   - copies dsh-git-guard/hooks.json and dsh-git-guard/git-guard.py into the
#     harness home (~/.dsh, or $DSH_HOME), where the mounted hook plugin reads
#     them at startup;
#   - appends the global git-safety rule to the harness-home AGENTS.md
#     (marker-guarded, so re-running is safe);
#   - prints the one remaining manual step (bundle installation).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DSH_HOME_DIR="${DSH_HOME:-$HOME/.dsh}"
BUNDLE_DIR="$REPO_ROOT/dsh-git-guard"

mkdir -p "$DSH_HOME_DIR"

echo "→ copying hooks.json -> $DSH_HOME_DIR/hooks.json"
cp "$BUNDLE_DIR/hooks.json" "$DSH_HOME_DIR/hooks.json"

echo "→ copying git-guard.py -> $DSH_HOME_DIR/git-guard.py"
cp "$BUNDLE_DIR/git-guard.py" "$DSH_HOME_DIR/git-guard.py"
chmod +x "$DSH_HOME_DIR/git-guard.py"

GLOBAL_RULES="$DSH_HOME_DIR/AGENTS.md"
MARKER_BEGIN='<!-- dsh-git-guard: begin -->'

BLOCK=$(cat <<'EOF'
<!-- dsh-git-guard: begin -->
## Git Safety (Hard Rule)

Never run any git-history or remote mutation — `commit`, `tag`, `push`,
`reset`, `rebase`, `revert`, `merge`, `cherry-pick`, `am`, force-push, or
`remote add/set-url/remove` and branch/tag deletion — without a separate,
explicit "yes" from the human in the current conversation for that exact
action. Approving a larger task (for example "cut the release") is not that
"yes".

- Local edits stay uncommitted working-tree changes until the human asks to
  commit them.
- When the human explicitly asks to commit ("commit these changes"), that
  request approves the commit — and only that commit. Use `--no-verify` only
  for a commit the human explicitly requested.
- Never push, tag, or otherwise touch the remote without a separate explicit
  go-ahead for that exact push/tag.
<!-- dsh-git-guard: end -->
EOF
)

if [[ -f "$GLOBAL_RULES" ]] && grep -qF "$MARKER_BEGIN" "$GLOBAL_RULES"; then
  echo "→ $GLOBAL_RULES already has the git-safety block; leaving it unchanged"
else
  if [[ -f "$GLOBAL_RULES" ]]; then
    printf '\n' >> "$GLOBAL_RULES"
  fi
  printf '%s\n' "$BLOCK" >> "$GLOBAL_RULES"
  echo "→ appended the git-safety rule to $GLOBAL_RULES"
fi

cat <<EOF

Done. One step remains — install the bundle so the harness mounts the hook:

  a) Web GUI → sidebar "Plugins" page → Install bundle → $BUNDLE_DIR
  b) or ask your agent:
       "install the dsh-git-guard bundle from $BUNDLE_DIR"

New sessions then require your approval for git history/remote mutations.
See docs/dsh-onboarding.md for details.
EOF
