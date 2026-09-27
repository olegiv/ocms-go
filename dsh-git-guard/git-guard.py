#!/usr/bin/env python3
"""Conservative PreToolUse tripwire for Git mutations (not a shell sandbox).

Protected or unrecognized Git invocations ask for approval. Other commands emit
no decision, preserving the harness permission policy. Invalid payloads exit 2.
"""

import json
import os
import shlex
import sys


READ_ONLY = {
    "status", "diff", "log", "show", "shortlog", "blame", "annotate", "grep",
    "rev-parse", "rev-list", "ls-files", "ls-tree", "ls-remote", "cat-file",
    "describe", "name-rev", "merge-base", "check-ref-format", "check-ignore",
    "check-attr", "diff-files", "diff-index", "diff-tree", "for-each-ref",
    "show-ref", "count-objects", "version", "help",
}
GLOBAL_FLAGS = {
    "--no-pager", "--paginate", "-p", "-P", "--bare", "--no-replace-objects",
    "--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs",
    "--icase-pathspecs", "--no-optional-locks", "--no-lazy-fetch",
}
GLOBAL_VALUES = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env"}
SEPARATORS = ";&|()`\n"


def git_requires_approval(args):
    """Parse Git global options before classifying the subcommand."""
    index = 0
    while index < len(args) and args[index].startswith("-"):
        option = args[index]
        if option in {"--version", "--help", "-h"}:
            return False
        if option in GLOBAL_VALUES:
            index += 2
        elif option in GLOBAL_FLAGS or any(
            option.startswith(flag + "=") for flag in GLOBAL_VALUES if flag.startswith("--")
        ) or (option.startswith(("-C", "-c")) and len(option) > 2):
            index += 1
        else:
            # Unknown options/aliases must not silently bypass the tripwire.
            return True
    if index >= len(args):
        # A shell substitution can interrupt the global options before the
        # subcommand becomes visible. Incomplete invocations must ask too.
        return bool(args)
    subcommand, args = args[index], args[index + 1:]
    if subcommand in READ_ONLY:
        return False
    if subcommand == "branch":
        for arg in args:
            if arg == "--":
                break
            if "$" in arg:
                return True  # An expanded variable may supply mutation flags.
            # Git accepts unambiguous prefixes of long options (e.g. --del).
            # Conservatively ask for ambiguous prefixes too instead of trying
            # to reproduce Git's version-dependent option table.
            long_option = arg.partition("=")[0]
            if (arg.startswith("--") and any(
                option.startswith(long_option)
                for option in ("--delete", "--move", "--copy", "--force")
            )) or (
                arg.startswith("-") and not arg.startswith("--")
                and any(flag in arg[1:] for flag in "dDmMcCf")
            ):
                return True
        return False
    if subcommand == "remote":
        # -v/--verbose may precede the subcommand. Everything except inspection
        # requires approval, including the built-in rm alias and future verbs.
        args = [arg for arg in args if arg not in {"-v", "--verbose"}]
        return bool(args) and args[0] not in {"show", "get-url"}
    return True


def requires_approval(command, depth=0):
    # shlex handles quoting, escaped newlines and compound shell commands without
    # executing anything. Nested command strings (e.g. sh -c) are checked too.
    if depth > 8:
        return True
    lexer = shlex.shlex(command.replace("\\\n", ""), posix=True, punctuation_chars=SEPARATORS)
    lexer.whitespace = " \t\r"
    lexer.whitespace_split = True
    # shlex treats '#' inside an unquoted word as a comment, unlike the shell
    # (e.g. git -C repo#one push). Conservative matching also scans comments.
    lexer.commenters = ""
    try:
        tokens = list(lexer)
    except ValueError:
        return True  # Unparseable shell input cannot be classified safely.
    for index, token in enumerate(tokens):
        if os.path.basename(token).casefold() == "git":
            args = []
            for arg in tokens[index + 1:]:
                if arg and all(char in SEPARATORS for char in arg):
                    if "`" in arg or "(" in arg:
                        return True  # Substitution may produce a command/flag.
                    break
                args.append(arg)
            if git_requires_approval(args):
                return True
        elif "git" in token and any(char in token for char in " \t\n;|&()`"):
            if requires_approval(token, depth + 1):
                return True
    return False


def main():
    try:
        payload = json.load(sys.stdin)
        if not isinstance(payload, dict) or not isinstance(payload.get("tool_input"), dict):
            raise ValueError("expected an object containing tool_input")
        command = payload["tool_input"].get("command")
        if not isinstance(command, str):
            raise ValueError("tool_input.command must be a string")
    except (ValueError, TypeError, RecursionError) as exc:
        sys.stderr.write(f"git-guard: invalid hook payload: {exc}\n")
        return 2

    if requires_approval(command):
        json.dump({"hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "ask",
            "permissionDecisionReason": (
                "This Git command may mutate history or repository state; "
                "it requires explicit human approval."
            ),
        }}, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
