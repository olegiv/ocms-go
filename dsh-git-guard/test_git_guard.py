"""Regression checks; all installations use temporary harness homes."""

import json
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parent.parent
BUNDLE = ROOT / "dsh-git-guard"
LEGACY_COMMAND = "python3 ${CLAUDE_PLUGIN_ROOT}/git-guard.py"
SPEC = importlib.util.spec_from_file_location("guard_installer", ROOT / "scripts/install-git-guard.py")
INSTALLER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(INSTALLER)


class GitGuardTests(unittest.TestCase):
    def run_guard(self, command):
        return subprocess.run(
            [sys.executable, str(BUNDLE / "git-guard.py")],
            input=json.dumps({"tool_input": {"command": command}}),
            text=True, capture_output=True, check=True,
        ).stdout

    def test_unmatched_commands_defer_to_normal_permissions(self):
        for command in ("rm example.txt", "curl https://example.com", "git status",
                        "git diff", "git branch --list", "git branch develop",
                        "git remote -v", "git remote get-url origin",
                        "git -C 'a repo' status", "git --no-pager log -1",
                        "git branch --list topic-d", "git status\ngit diff"):
            with self.subTest(command=command):
                self.assertEqual(self.run_guard(command), "")

    def test_protected_commands_ask(self):
        for command in ("git branch --delete topic", "git branch -d topic",
                        "git branch -D topic", "git branch -rd origin/topic",
                        "git branch --delete --force topic", "git commit -m test",
                        "git push", "git tag v1", "git remote remove origin",
                        "git branch -r -d origin/topic", "git branch --force --delete topic",
                        "git branch --quiet -D topic", "git branch topic --delete",
                        "git branch -f release HEAD~1", "git branch --force release HEAD~1",
                        "git branch --del topic", "git branch -r --dele origin/topic",
                        'git branch "$flags" topic', "git branch $(echo -d) topic",
                        "git branch `echo -d` topic",
                        "git remote rm origin", "git remote -v rm origin",
                        "git -C 'a repo' push", "git -C../repo branch -r -d origin/topic",
                        "git --git-dir=repo.git --work-tree . reset --hard",
                        "git -c 'user.name=Some Name' commit -m test",
                        "git --no-pager remote rm origin", "'/usr/bin/git' push",
                        "/usr/bin/GIT push",
                        "git status\ngit push", "git status && git remote rm origin",
                        "sh -c 'git push'", 'echo "$(git push)"',
                        "git\\\n push", "git custom-alias", "git --unknown push",
                        "git -C repo#one push", "git -C $(pwd) push",
                        "git -C$(pwd) push", "echo `git push`", 'echo "`git push`"'):
            with self.subTest(command=command):
                output = json.loads(self.run_guard(command))
                self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_protected_long_option_prefixes_ask(self):
        # Git's long-option abbreviation rules apply to every protected branch
        # operation, not just the particular --del spelling from the review.
        for option in ("--delete", "--move", "--copy", "--force"):
            for end in range(3, len(option) + 1):
                command = "git branch --quiet " + option[:end] + " topic"
                with self.subTest(command=command):
                    output = json.loads(self.run_guard(command))
                    self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_repository_commit_wrappers_ask(self):
        commands = (
            "make commit-do-local", "make commit-do",
            "make -C 'a repo' --jobs=2 commit-do-local",
            "gmake --file=Makefile commit-do-local", "/usr/bin/make commit-do-local",
            "make test commit-do-local", 'make "$target"',
            "./scripts/codex-commands commit-do-local",
            "./scripts/codex-commands commit-do",
            "bash scripts/codex-commands commit-do-local",
            "./scripts/codex/commit-do.sh", "bash scripts/codex/commit-do.sh",
            "'/a repo/scripts/codex/commit-do.sh'",
            "./scripts/codex/proxy-claude-command.sh /commit-do",
            "./scripts/codex/proxy-claude-command.sh commit-do",
            "./scripts/codex/proxy-claude-command.sh 2>/dev/null commit-do",
            "./scripts/codex/proxy-claude-command.sh > out.log commit-do 2>&1",
            "./scripts/codex/proxy-claude-command.sh code-quality commit-do",
            "git status && make commit-do-local",
            "make test\n./scripts/codex/commit-do.sh",
            "sh -c 'make commit-do-local'",
            "bash -c './scripts/codex-commands commit-do-local'",
            'echo "$(make commit-do-local)"',
            'echo "`./scripts/codex/commit-do.sh`"',
        )
        for command in commands:
            with self.subTest(command=command):
                output = json.loads(self.run_guard(command))
                self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_protected_commands_with_redirections_ask(self):
        for command in (
            "bash -s <<< 'git push'", "bash -s<<<'git push'",
            'bash<<<"git push"', "sh <<EOF\ngit push\nEOF",
            "bash -s <<< 'make commit-do-local'",
            "bash -s <<< '/usr/lib/git-core/git-push origin main'",
            "git>output push", "git 2>errors push",
            "git -C repo>output push", "git branch>output -D topic",
            "git branch 2>&1 --delete topic", "git >output branch -D topic",
            "git push>output", "git push &>output",
            "bash <(printf '%s' 'git push')",
        ):
            with self.subTest(command=command):
                output = json.loads(self.run_guard(command))
                self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_dashed_git_executables_ask(self):
        for command in (
            "git-commit -m x", "/usr/lib/git-core/git-push origin main",
            "'/a repo/git-core/git-commit' -m x", "/usr/lib/git-core/GIT-PUSH",
            "git-branch -r -d origin/topic", "git-branch --del topic",
            "git-remote rm origin", "git-update-ref refs/heads/topic HEAD",
            "sh -c 'git-push origin main'", "git-push>output",
        ):
            with self.subTest(command=command):
                output = json.loads(self.run_guard(command))
                self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_read_only_redirections_and_dashed_git_defer(self):
        for command in (
            "git status>output", "git diff 2>errors", "git branch --list>output",
            "cat<input", "cat <<EOF\nordinary text\nEOF",
            "bash -s <<< 'git status'", "sh -c 'git-log -1'",
            "git-status", "/usr/lib/git-core/git-log -1", "git-diff>output",
            "git-branch --list", "git-remote get-url origin",
        ):
            with self.subTest(command=command):
                self.assertEqual(self.run_guard(command), "")

    def test_non_commit_repository_wrappers_defer(self):
        for command in (
            "make test", "make commit-prepare", "make commit-prepare-local",
            "make code-quality-local", "./scripts/codex-commands commit-prepare-local",
            "./scripts/codex/commit-prepare.sh",
            "./scripts/codex/proxy-claude-command.sh /commit-prepare",
            "sh -c 'make test'", "make test; echo commit-do-local",
        ):
            with self.subTest(command=command):
                self.assertEqual(self.run_guard(command), "")

    def test_malformed_payloads_fail_closed(self):
        for payload in ("{", "null", "[]", "{}", '{"tool_input":null}',
                        '{"tool_input":{"command":42}}', "[" * 1500 + "]" * 1500):
            with self.subTest(payload=payload):
                result = subprocess.run(
                    [sys.executable, str(BUNDLE / "git-guard.py")],
                    input=payload, text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")

    def test_install_rolls_back_each_replacement_failure(self):
        for existing in (False, True):
            for failure_at in (1, 2, 3):
                with self.subTest(existing=existing, failure_at=failure_at), \
                        tempfile.TemporaryDirectory() as directory:
                    home = Path(directory)
                    originals = {"hooks.json": b'{"hooks":{}}',
                                 "AGENTS.md": b"Keep this.\n", "git-guard.py": b"old script\n"}
                    if existing:
                        for name, content in originals.items():
                            (home / name).write_bytes(content)
                            (home / name).chmod(0o640)
                    replace = os.replace
                    calls = 0

                    def fail_once(source, target):
                        nonlocal calls
                        calls += 1
                        if calls == failure_at:
                            raise OSError("injected publication failure")
                        return replace(source, target)

                    with mock.patch.object(INSTALLER.os, "replace", side_effect=fail_once):
                        with self.assertRaisesRegex(OSError, "injected"):
                            INSTALLER.install(BUNDLE, home, "new rules")
                    if existing:
                        for name, content in originals.items():
                            self.assertEqual((home / name).read_bytes(), content)
                            self.assertEqual((home / name).stat().st_mode & 0o777, 0o640)
                    else:
                        self.assertEqual(list(home.iterdir()), [])

    def test_staging_failure_leaves_installation_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            (home / "hooks.json").write_text("{}")
            with mock.patch.object(INSTALLER.os, "fsync", side_effect=OSError("disk full")):
                with self.assertRaisesRegex(OSError, "disk full"):
                    INSTALLER.install(BUNDLE, home, "rules")
            self.assertEqual((home / "hooks.json").read_text(), "{}")
            self.assertEqual(sorted(path.name for path in home.iterdir()), ["hooks.json"])

    def test_failed_rollback_retains_recovery_files(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            (home / "git-guard.py").write_text("previous guard")
            replace = os.replace
            calls = 0

            def persistent_failure(source, target):
                nonlocal calls
                calls += 1
                if calls >= 2:
                    raise OSError("persistent disk failure")
                return replace(source, target)

            with mock.patch.object(INSTALLER.os, "replace", side_effect=persistent_failure):
                with self.assertRaisesRegex(OSError, "recovery files retained"):
                    INSTALLER.install(BUNDLE, home, "rules")
            backups = list(home.glob(".git-guard-*/git-guard.py.backup"))
            self.assertEqual(len(backups), 1)
            self.assertEqual(backups[0].read_text(), "previous guard")
            self.assertFalse((home / "hooks.json").exists())

    def test_read_only_rules_and_symlinks_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            rules = home / "AGENTS.md"
            rules.write_text("keep")
            rules.chmod(0o400)
            if not os.access(rules, os.W_OK):
                self.assertNotEqual(self.install(home, check=False).returncode, 0)
                self.assertEqual(rules.read_text(), "keep")
                self.assertFalse((home / "hooks.json").exists())
                self.assertFalse((home / "git-guard.py").exists())
            rules.chmod(0o600)
            (home / "hooks.json").symlink_to(rules)
            self.assertNotEqual(self.install(home, check=False).returncode, 0)
            self.assertTrue((home / "hooks.json").is_symlink())
            self.assertEqual(rules.read_text(), "keep")

    def install(self, home, check=True):
        return subprocess.run(
            ["bash", str(ROOT / "scripts/install-git-guard.sh")],
            env={**os.environ, "DSH_HOME": str(home)},
            text=True, capture_output=True, check=check,
        )

    def test_install_preserves_configuration_and_refreshes_rules(self):
        with tempfile.TemporaryDirectory(prefix="git guard ") as directory:
            home = Path(directory)
            unrelated = {"type": "command", "command": "echo unrelated"}
            config = {
                "customSetting": True,
                "hooks": {
                    "PostToolUse": [{"matcher": "*", "hooks": [unrelated]}],
                    "PreToolUse": [{"matcher": "bash", "hooks": [
                        unrelated, {"type": "command", "command": LEGACY_COMMAND}
                    ]}],
                },
            }
            (home / "hooks.json").write_text(json.dumps(config))
            prefix, suffix = "# Existing rules\n\n", "\n\nKeep these rules.\n"
            (home / "AGENTS.md").write_text(
                prefix + "<!-- dsh-git-guard: begin -->\nSTALE RULE\n"
                "<!-- dsh-git-guard: end -->" + suffix
            )
            self.install(home)
            installed = json.loads((home / "hooks.json").read_text())
            self.assertTrue(installed["customSetting"])
            self.assertEqual(installed["hooks"]["PostToolUse"], config["hooks"]["PostToolUse"])
            entries = installed["hooks"]["PreToolUse"]
            self.assertEqual(len(entries), 1)
            self.assertEqual(entries[0]["hooks"][0], unrelated)
            self.assertEqual(len(entries[0]["hooks"]), 2)
            rules = (home / "AGENTS.md").read_text()
            self.assertTrue(rules.startswith(prefix))
            self.assertTrue(rules.endswith(suffix))
            self.assertNotIn("STALE RULE", rules)
            self.assertIn("## Git Safety (Hard Rule)", rules)
            snapshot = [(home / name).read_bytes() for name in ("hooks.json", "AGENTS.md")]
            self.install(home)
            self.assertEqual(snapshot, [(home / name).read_bytes()
                                        for name in ("hooks.json", "AGENTS.md")])

            # Execute the configured command through a shell with a spaced plugin root.
            result = subprocess.run(
                ["sh", "-c", entries[0]["hooks"][1]["command"]],
                env={**os.environ, "CLAUDE_PLUGIN_ROOT": str(home)},
                input=json.dumps({"tool_input": {"command": "git branch --delete topic"}}),
                text=True, capture_output=True, check=True,
            )
            self.assertEqual(json.loads(result.stdout)["hookSpecificOutput"]["permissionDecision"], "ask")

    def test_new_install_and_append_to_existing_hooks(self):
        for existing in (None, {"hooks": {"PreToolUse": [
                {"matcher": "bash", "hooks": [{"type": "command", "command": "echo keep"}]}
        ]}}):
            with self.subTest(existing=existing), tempfile.TemporaryDirectory() as directory:
                home = Path(directory)
                if existing is not None:
                    (home / "hooks.json").write_text(json.dumps(existing))
                (home / "AGENTS.md").write_text("Keep this.\n")
                self.install(home)
                entries = json.loads((home / "hooks.json").read_text())["hooks"]["PreToolUse"]
                self.assertEqual(len(entries), 1 if existing is None else 2)
                if existing is not None:
                    self.assertEqual(entries[0], existing["hooks"]["PreToolUse"][0])
                self.assertTrue((home / "AGENTS.md").read_text().startswith("Keep this.\n"))

    def test_concurrent_installations_do_not_duplicate_the_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            processes = [subprocess.Popen(
                ["bash", str(ROOT / "scripts/install-git-guard.sh")],
                env={**os.environ, "DSH_HOME": str(home)},
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            ) for _ in range(4)]
            for process in processes:
                stdout, stderr = process.communicate(timeout=15)
                self.assertEqual(process.returncode, 0, stdout + stderr)
            config = json.loads((home / "hooks.json").read_text())
            self.assertEqual(len(config["hooks"]["PreToolUse"]), 1)
            self.assertEqual((home / "AGENTS.md").read_text().count(
                "<!-- dsh-git-guard: begin -->"), 1)

    def test_invalid_existing_documents_are_not_overwritten(self):
        for hooks, rules in (("{bad json", "keep"),
                             ('{"hooks":{"PreToolUse":{}}}', "keep"),
                             ('{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":{}}]}}', "keep"),
                             ('{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[null]}]}}', "keep"),
                             ("{}", "<!-- dsh-git-guard: begin -->\nstale")):
            with self.subTest(hooks=hooks, rules=rules), tempfile.TemporaryDirectory() as directory:
                home = Path(directory)
                (home / "hooks.json").write_text(hooks)
                (home / "AGENTS.md").write_text(rules)
                result = self.install(home, check=False)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual((home / "hooks.json").read_text(), hooks)
                self.assertEqual((home / "AGENTS.md").read_text(), rules)


if __name__ == "__main__":
    unittest.main()
