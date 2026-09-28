#!/usr/bin/env python3
"""Stage and install the guard under an exclusive lock, rolling back on errors."""

import fcntl
import json
import os
from pathlib import Path
import shutil
import stat
import sys
import tempfile


def prepare(bundle, home, block):
    config_path, rules_path = home / "hooks.json", home / "AGENTS.md"
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
        if not isinstance(entry, dict) or not isinstance(entry.get("hooks"), list):
            raise ValueError("PreToolUse entries must contain a hooks array")
        if not all(isinstance(hook, dict) for hook in entry["hooks"]):
            raise ValueError("PreToolUse hooks must be objects")
        if entry.get("matcher") != guard["matcher"]:
            continue
        for hook in entry["hooks"]:
            if hook.get("type") == "command" and hook.get("command") in (
                legacy_command, guard_hook["command"]):
                hook["command"] = guard_hook["command"]
                found = True
    if not found:
        entries.append(guard)

    rules = rules_path.read_text() if rules_path.exists() else ""
    begin, end = "<!-- dsh-git-guard: begin -->", "<!-- dsh-git-guard: end -->"
    if begin in rules or end in rules:
        if rules.count(begin) != 1 or rules.count(end) != 1:
            raise ValueError("AGENTS.md must have exactly one complete guard block")
        start, stop = rules.index(begin), rules.index(end)
        if stop < start:
            raise ValueError("AGENTS.md guard markers are out of order")
        rules = rules[:start] + block + rules[stop + len(end):]
    else:
        rules += ("\n" if rules else "") + block + "\n"

    # Publish the executable first and the hook configuration last. Even if the
    # process is killed between replacements, a hook never points at a missing
    # script. Every new file and every rollback copy is staged before publishing.
    return {
        "git-guard.py": (bundle / "git-guard.py").read_bytes(),
        "AGENTS.md": rules.encode(),
        "hooks.json": (json.dumps(config, indent=2) + "\n").encode(),
    }


def install(bundle, home, block):
    # Lock the directory inode: no stale lock files or unlocked unlink window.
    lock = os.open(home, os.O_RDONLY)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX)
        for name in ("git-guard.py", "AGENTS.md", "hooks.json"):
            path = home / name
            if path.is_symlink() or (path.exists() and not path.is_file()):
                raise ValueError(f"refusing to replace non-regular file: {path}")
            if path.exists() and not os.access(path, os.W_OK):
                raise PermissionError(f"file is not writable: {path}")
        outputs = prepare(bundle, home, block)
        staging = Path(tempfile.mkdtemp(prefix=".git-guard-", dir=home))
        retain_backups = False
        try:
            originals = {}
            for name, content in outputs.items():
                target = home / name
                backup = staging / (name + ".backup")
                if target.exists():
                    shutil.copy2(target, backup)
                    originals[name] = backup
                    mode = stat.S_IMODE(target.stat().st_mode)
                else:
                    originals[name] = None
                    mode = 0o600
                if name == "git-guard.py":
                    mode |= stat.S_IXUSR
                staged = staging / name
                with staged.open("wb") as output:
                    output.write(content)
                    output.flush()
                    os.fsync(output.fileno())
                staged.chmod(mode)
            replaced = []
            try:
                for name in outputs:
                    os.replace(staging / name, home / name)
                    replaced.append(name)
            except OSError as failure:
                rollback_errors = []
                for name in reversed(replaced):
                    try:
                        if originals[name] is None:
                            (home / name).unlink()
                        else:
                            os.replace(originals[name], home / name)
                    except OSError as exc:
                        rollback_errors.append(f"{name}: {exc}")
                if rollback_errors:
                    retain_backups = True
                    raise OSError(
                        f"installation failed ({failure}); rollback incomplete: "
                        f"{'; '.join(rollback_errors)}; recovery files retained in {staging}"
                    ) from failure
                raise
        finally:
            if not retain_backups:
                shutil.rmtree(staging)
    finally:
        os.close(lock)


def main():
    try:
        bundle, home = map(Path, sys.argv[1:3])
        install(bundle, home, sys.argv[3])
    except (ValueError, TypeError, AttributeError, OSError) as exc:
        sys.exit(f"git-guard: cannot update installation: {exc}")


if __name__ == "__main__":
    main()
