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

python3 - "$BUNDLE_DIR" "$DSH_HOME_DIR" "$BLOCK" <<'PY'
import json
from pathlib import Path
import sys

bundle, home = map(Path, sys.argv[1:3])
block = sys.argv[3]
config_path = home / "hooks.json"
rules_path = home / "AGENTS.md"

try:
    config = json.loads(config_path.read_text()) if config_path.exists() else {}
    source = json.loads((bundle / "hooks.json").read_text())
    guard = source["hooks"]["PreToolUse"][0]
    guard_hook = guard["hooks"][0]
    legacy_command = "python3 ${CLAUDE_PLUGIN_ROOT}/git-guard.py"
    hooks = config.setdefault("hooks", {})
    entries = hooks.setdefault("PreToolUse", [])
    if not isinstance(entries, list):
        raise ValueError("PreToolUse must be an array")
    found = False
    for entry in entries:
        if entry.get("matcher") != guard["matcher"]:
            continue
        for hook in entry.get("hooks", []):
            if hook.get("type") == "command" and hook.get("command") in (
                legacy_command, guard_hook["command"]
            ):
                hook["command"] = guard_hook["command"]
                found = True
    if not found:
        entries.append(guard)

    rules = rules_path.read_text() if rules_path.exists() else ""
    begin = "<!-- dsh-git-guard: begin -->"
    end = "<!-- dsh-git-guard: end -->"
    if begin in rules or end in rules:
        if rules.count(begin) != 1 or rules.count(end) != 1:
            raise ValueError("AGENTS.md must have exactly one complete guard block")
        start, stop = rules.index(begin), rules.index(end)
        if stop < start:
            raise ValueError("AGENTS.md guard markers are out of order")
        rules = rules[:start] + block + rules[stop + len(end):]
    else:
        rules += ("\n" if rules else "") + block + "\n"
except (ValueError, TypeError, AttributeError, OSError) as exc:
    sys.exit(f"git-guard: cannot update installation: {exc}")

# Validate both documents before replacing either; preserve unrelated content.
config_path.write_text(json.dumps(config, indent=2) + "\n")
rules_path.write_text(rules)
PY

echo "→ merged hooks and refreshed the git-safety rule in $DSH_HOME_DIR"
echo "→ copying git-guard.py -> $DSH_HOME_DIR/git-guard.py"
cp "$BUNDLE_DIR/git-guard.py" "$DSH_HOME_DIR/git-guard.py"
chmod +x "$DSH_HOME_DIR/git-guard.py"

cat <<EOF

Done. One step remains — install the bundle so the harness mounts the hook:

  a) Web GUI → sidebar "Plugins" page → Install bundle → $BUNDLE_DIR
  b) or ask your agent:
       "install the dsh-git-guard bundle from $BUNDLE_DIR"

New sessions then require your approval for git history/remote mutations.
See docs/dsh-onboarding.md for details.
EOF
