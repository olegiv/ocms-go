"""Regression checks; all installations use temporary harness homes."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
BUNDLE = ROOT / "dsh-git-guard"
LEGACY_COMMAND = "python3 ${CLAUDE_PLUGIN_ROOT}/git-guard.py"


class GitGuardTests(unittest.TestCase):
    def run_guard(self, command):
        return subprocess.run(
            [sys.executable, str(BUNDLE / "git-guard.py")],
            input=json.dumps({"tool_input": {"command": command}}),
            text=True, capture_output=True, check=True,
        ).stdout

    def test_unmatched_commands_defer_to_normal_permissions(self):
        for command in ("rm example.txt", "curl https://example.com", "git status",
                        "git diff", "git branch --list", "git branch develop"):
            with self.subTest(command=command):
                self.assertEqual(self.run_guard(command), "")

    def test_protected_commands_ask(self):
        for command in ("git branch --delete topic", "git branch -d topic",
                        "git branch -D topic", "git branch -rd origin/topic",
                        "git branch --delete --force topic", "git commit -m test",
                        "git push", "git tag v1", "git remote remove origin"):
            with self.subTest(command=command):
                output = json.loads(self.run_guard(command))
                self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "ask")

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

    def test_invalid_existing_documents_are_not_overwritten(self):
        for hooks, rules in (("{bad json", "keep"),
                             ('{"hooks":{"PreToolUse":{}}}', "keep"),
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
