#!/usr/bin/env bash
# Installs the DSH git-approval gate for this machine:
#   - merges dsh-git-guard/hooks.json and copies dsh-git-guard/git-guard.py into the
#     harness home (~/.dsh, or $DSH_HOME), where the mounted hook plugin reads
#     them at startup;
#   - refreshes the marked global git-safety rule in the harness-home AGENTS.md;
#   - prints the one remaining manual step (bundle installation).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DSH_HOME_DIR="${DSH_HOME:-$HOME/.dsh}"
BUNDLE_DIR="$REPO_ROOT/dsh-git-guard"

mkdir -p "$DSH_HOME_DIR"

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

python3 "$REPO_ROOT/scripts/install-git-guard.py" "$BUNDLE_DIR" "$DSH_HOME_DIR" "$BLOCK"
echo "→ installed guard, merged hooks, and refreshed the git-safety rule in $DSH_HOME_DIR"

cat <<EOF

Done. One step remains — install the bundle so the harness mounts the hook:

  a) Web GUI → sidebar "Plugins" page → Install bundle → $BUNDLE_DIR
  b) or ask your agent:
       "install the dsh-git-guard bundle from $BUNDLE_DIR"

New sessions then require your approval for git history/remote mutations.
See docs/dsh-onboarding.md for details.
EOF
