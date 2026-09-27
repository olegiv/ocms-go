#!/usr/bin/env python3
"""PreToolUse guard: require approval for git history/remote mutations.

Reads the harness hook payload from stdin (JSON with tool_input.command) and
emits a Claude Code hookSpecificOutput decision:
  - "ask"    for git commands that mutate history or the remote
  - no decision otherwise, preserving the normal permission flow
Unexpected input fails CLOSED with exit code 2, which the hook protocol treats
as a blocking decision with stderr as the reason.
"""

import json
import re
import sys

MUTATING = re.compile(
    r"(?:^|[;&|()\s]|\&\&)\s*"
    r"(?:env\s+(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*)?"
    r"(?:command\s+)?"
    r"git\s+"
    r"(?:-c\s+\S+\s+)*"
    r"(commit|tag|push|reset|rebase|revert|merge|cherry-pick|am|"
    r"fetch|pull|clean\s+-[a-z]*f|checkout\s+--|"
    r"branch\s+(?:--delete\b|-[a-z]*d)|"
    r"remote\s+(?:add|set-url|remove|rename)|stash\s+(?:drop|pop|clear)|"
    r"filter-branch|reflog\s+(?:delete|expire)|notes\s+(?:add|remove)|"
    r"submodule\s+(?:add|update|deinit))",
    re.IGNORECASE,
)


def main() -> int:
    try:
        payload = json.load(sys.stdin)
    except Exception as exc:  # fail closed on unreadable input
        sys.stderr.write(f"git-guard: cannot parse hook payload: {exc}\n")
        return 2

    command = ""
    tool_input = payload.get("tool_input")
    if isinstance(tool_input, dict):
        command = tool_input.get("command") or ""

    if MUTATING.search(command):
        decision = {
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "ask",
                "permissionDecisionReason": (
                    "This git command mutates commit history or the remote; "
                    "it requires explicit human approval."
                ),
            }
        }
    else:
        return 0

    json.dump(decision, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
